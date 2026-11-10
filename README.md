# SmartCheck Automation — Backend

Backend REST en Go para el sistema de supervisión inteligente de hornos túnel de
Fermar S.A. Registra y consulta lotes productivos, monitorea temperaturas de
hornos y recibe telemetría de nodos Raspberry Pi vía HTTP. Incluye un control
predictivo (MPC) por lote que propone setpoints de temperatura y velocidad de
cinta, y un sidecar Python que sirve las predicciones XGBoost.

## Stack

| Capa | Tecnología |
|---|---|
| Lenguaje | Go 1.25 |
| Base de datos | PostgreSQL 16 (Supabase en producción) |
| Driver DB | pgx/v5 |
| Infraestructura local | Docker + Docker Compose (`db`, `app`, `predictor-svc`) |
| Predictor | Sidecar Python/FastAPI + XGBoost (`cmd/predictor_svc`, puerto `8090`) |
| Autenticación (usuarios) | Google OAuth 2.0 + credenciales locales (bcrypt) → JWT en cookie HttpOnly `session_token` |
| Autenticación (nodos Raspberry Pi) | secret por nodo vía header `Authorization: Bearer <secret>` |

## Arquitectura

El proyecto sigue el patrón por capas: **Handler → Service → Repository → PostgreSQL**

```
cmd/server/main.go              # Entry point, wiring de dependencias y rutas
cmd/predictor_svc/              # Sidecar FastAPI que sirve el predictor XGBoost
internal/
  controller/                   # Handlers HTTP (capa de presentación)
  service/                      # Lógica de negocio (incluye control MPC y optimizador)
  domain/                       # Modelos y contratos (interfaces)
  repository/                   # Repositorios reales contra PostgreSQL
  provider/database/            # Conexión al pool de PostgreSQL
database/schema.sql             # Esquema canónico de instalación limpia: tablas, constraints, índices, seed
database/migrations/            # Migraciones incrementales para bases existentes (schema_migrations)
docker-compose.yml              # db + app + predictor-svc
```

El backend aplica `database/schema.sql` embebido en cada arranque
(`database/schema.go`) y luego las migraciones pendientes de
`database/migrations/` (registradas en `schema_migrations`). El contenedor `db`
también ejecuta `schema.sql` al inicializar un volumen vacío. Para cambios
estructurales, agregá `database/migrations/NNNN_descripcion.sql`; no edites
`schema.sql` para bases existentes. `go run ./cmd/migrate` aplica lo mismo sin
levantar el servidor.

## Servicios de Docker Compose

`docker compose up --build` levanta **tres** contenedores:

| Servicio | Qué es | Puerto |
|---|---|---|
| `db` | PostgreSQL 16 | `5432` |
| `app` | API Go | `${PORT}` (default `8080`) |
| `predictor-svc` | Sidecar predictor XGBoost | `8090` |

El servicio `app` usa `PREDICTOR_URL=http://predictor-svc:8090`. Fuera de
Compose, el default del código es `http://localhost:8090`. Si el predictor no
responde, el MPC opera degradado: la decisión se audita como `skipped` y el
horno conserva el último setpoint aplicado.

## Control del horno (SCA-400)

Cada lote abierto puede operar en tres modos:

- **MANUAL:** no corre loop de MPC. La consigna se envía a mano por
  `POST /api/v1/horno/consigna`, que exige un lote abierto en modo MANUAL y la
  deja **encolada** (no aplicada) como decisión ya aprobada (`manual_override`)
  para que la Raspberry la levante por
  `GET /api/v1/lotes/{id}/consigna-propuesta` y la aplique sin el límite de Δ
  por ciclo. `exitosa=true` en `historial_consignas` significa encolada, no
  aplicada en el PLC.
- **SUPERVISION:** el loop del MPC publica propuestas sin autorizar. Un operario
  (o la propia Pi) las aprueba con `POST /api/v1/lotes/{id}/aplicar-consigna`
  (alias `/aprobar-consigna`); la Pi las aplica y confirma con
  `POST /api/v1/lotes/{id}/consigna/ack`.
- **AUTOMATICO:** el loop publica propuestas ya autorizadas (`autorizada=true`)
  y la Pi las aplica.

**La Pi ENTRADA es la única que escribe el PLC real**, con su propio
`control.sensores`/`control.actuadores` (`modbus_tcp`/`modbus_rtu`). El backend
no abre conexiones Modbus ni escribe ningún PLC, y tampoco mantiene un horno en
memoria: sólo encola/propone y audita. El estado real del horno es la telemetría
de la Pi (`metricas_horno`), que `GET /api/v1/horno` proyecta.

El loop emite `control.propuesta` por SSE; el evento incluye `autorizada`. La
propuesta serializa `temp1`, `velocidad`, `z` y `prediccion`. Variables y puesta
en marcha en [`MANUAL_INSTALACION.md`](MANUAL_INSTALACION.md).

## Alertas operativas

Las alertas se persisten en `alertas` y quedan en estado **ACTIVA** hasta que un
operario las reconoce (ACK); no hay auto-resolve. Tipos actuales:

- `TEMPERATURA_HORNO` — T1 promedio de las últimas 3 muestras fuera del
  objetivo ± `tolerancia_temp_c` (default 2 °C). La evalúa el loop MPC en
  ADAPTANDO y, para lotes `MANUAL`, el monitor de telemetría.
- `TELEMETRIA_DESACTUALIZADA` — el lote abierto no tiene muestras y lleva
  abierto más de `TELEMETRIA_DESACTUALIZADA_SEGUNDOS`, o su última muestra es
  más vieja que ese umbral (default 30 s). El monitor corre cada
  `TELEMETRIA_MONITOR_INTERVALO` (default 15 s).
- `DISPOSITIVO_OFFLINE` — el reaper detecta la transición online→offline de una
  Raspberry (`DISPOSITIVO_REAPER_INTERVAL` / `DISPOSITIVO_OFFLINE_THRESHOLD`).
- `PRECALENTAMIENTO_TIMEOUT` — el horno no alcanza la receta antes de
  `MPC_PRECALENTAMIENTO_TIMEOUT_SEGUNDOS`.
- `PORCENTAJE_QUEMADOS_ALTO` / `PORCENTAJE_CRUDOS_ALTO` — el porcentaje
  observado en la ventana del lote supera su umbral de aceptación (default
  `5 %`).

Las alertas de lote llevan `lote_id` y las de dispositivo `dispositivo_id`;
`horno_id` queda vacío.

## Notificaciones de alertas

Las alertas **WARNING** y **CRITICAL** generan una notificación por cada usuario
activo y canal habilitado (email vía Resend o Gmail API, Telegram vía Bot API).
Los emails llevan HTML estilizado (tabla, header por severidad, detalles y CTA)
con fallback de texto plano. Se persisten en un outbox
(`notificaciones_outbox`) y un worker las reintenta con backoff hasta
`NOTIFICACIONES_MAX_INTENTOS`; el unique `(alerta_id, usuario_id, canal)`
garantiza una sola notificación por alerta y usuario. Para reenviar una alerta
existente a destinatarios concretos sin esperar el flujo automático:

```bash
go run ./cmd/notificar_alerta -emails "a@x.com,b@y.com"
go run ./cmd/notificar_alerta -alerta-id <uuid> -emails "a@x.com" \
  -panel-url "https://panel.example"
```

El comando sólo encola filas `EMAIL`; el worker del backend las envía.

El proveedor de email se elige con `NOTIFICACIONES_EMAIL_PROVIDER`
(`resend` por defecto, o `gmail`/`gmail_api`; Gmail usa HTTPS, sin dominio
propio y con la cuenta autenticada como remitente). Cada canal se habilita sólo
si su configuración está completa; con variables vacías el backend arranca igual
y ese canal queda deshabilitado. El alta y las pruebas se administran desde
`/api/v1/admin/notificaciones` (sólo Administrador). Guía de Resend, Gmail API y
Telegram en
[`MANUAL_INSTALACION.md`](MANUAL_INSTALACION.md#35-notificaciones-de-alertas-opcional).

## Endpoints (resumen)

La referencia completa y actualizada —con auth, rol mínimo, request/response y
códigos— está en [`docs/api-endpoints.md`](docs/api-endpoints.md). Los puntos de
mayor impacto:

- **Auth del panel/usuarios:** JWT en cookie HttpOnly `session_token` (Google
  OAuth 2.0 o credenciales locales). No existe un header de API key compartido.
- **Auth de dispositivos (Raspberry Pi):** cada nodo aprobado usa
  `Authorization: Bearer <secret>`; el alta se hace vía
  `POST /api/v1/registration-requests`.
- **Ciclo de lote por sector:** se inicia con `POST /api/v1/lotes/inicio`
  (device-only) y el historial se consulta con `GET /api/v1/lotes` (device o
  JWT). El alta legada `POST /api/v1/lotes` responde **405**.
- **Consigna manual:** `POST /api/v1/horno/consigna` (JWT;
  Operario/Supervisor/Admin). El historial por lote se consulta en
  `GET /api/v1/horno/consigna/historial?loteId=...`.
- **Control MPC:** `GET /api/v1/lotes/{id}/control/estado`,
  `PATCH /api/v1/lotes/{id}/modo`, `POST /api/v1/lotes/{id}/control/start|stop`,
  `PATCH /api/v1/lotes/{id}/control/parametros`,
  `GET /api/v1/lotes/{id}/consigna-propuesta`,
  `POST /api/v1/lotes/{id}/aplicar-consigna`,
  `POST /api/v1/lotes/{id}/consigna/ack` y `GET /api/v1/lotes/{id}/decisiones`.
- **Notificaciones:** `GET|PATCH /api/v1/admin/notificaciones[...]` y
  `POST /api/v1/admin/notificaciones/test` (Administrador). Ver
  [`docs/api-endpoints.md`](docs/api-endpoints.md).
- **`/api/v1/productos`:** `GET` acepta JWT o Bearer de dispositivo; las
  escrituras (`POST`/`PUT`/`DELETE`) exigen rol Supervisor o Admin.
- **Salud:** `GET /health` y `GET /healthz` verifican conectividad con
  PostgreSQL.

## Documentación

- [`docs/impacto-economico-mermas.md`](docs/impacto-economico-mermas.md) — costos
  unitarios en ARS, KPI de mermas, datos incompletos y criterio de valorización.

- [`MANUAL_INSTALACION.md`](MANUAL_INSTALACION.md) — instalación, variables de
  entorno, ejecución, tests y despliegue.
- [`docs/api-endpoints.md`](docs/api-endpoints.md) — contrato detallado de los
  endpoints.
- [`CLAUDE.md`](CLAUDE.md) — convenciones, estructura, estado y decisiones de
  arquitectura del proyecto.
- [`docs/consigna-horno.md`](docs/consigna-horno.md) — diseño de consigna
  manual y control MPC (incluye notas históricas de SCA-142).
- [`docs/backend-go-lotes-sector.md`](docs/backend-go-lotes-sector.md) —
  contrato de lotes por sector.
- [`docs/device-registration-contract.md`](docs/device-registration-contract.md)
  — registro y autenticación de dispositivos.
- [`cmd/predictor_svc/README.md`](cmd/predictor_svc/README.md) — sidecar
  predictor XGBoost.
