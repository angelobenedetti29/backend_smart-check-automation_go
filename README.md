# Smart-Check Automation — Backend

Backend REST en Go para el sistema de supervisión inteligente de hornos túnel de
Fermar S.A. Registra y consulta lotes productivos, monitorea temperaturas de
hornos y recibe telemetría de nodos Raspberry Pi vía HTTP. Incluye un control
predictivo (MPC) por lote que propone setpoints de temperatura y velocidad de
cinta, y un sidecar Python que sirve las predicciones XGBoost.

## Stack

| Capa | Tecnología |
|---|---|
| Lenguaje | Go 1.25 |
| Base de datos | PostgreSQL 16 (Aiven en producción) |
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
database/schema.sql             # DDL idempotente: tablas, constraints, índices, seed
docker-compose.yml              # db + app + predictor-svc
```

El backend aplica `database/schema.sql` embebido en cada arranque
(`database/schema.go`); el contenedor `db` también lo ejecuta al inicializar un
volumen vacío.

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
  `POST /api/v1/horno/consigna` (se despacha y audita igual). Si
  `CONTROL_ACTUADOR=pi`, el backend además encola la consigna como decisión ya
  aprobada (`manual_override`) para que la Raspberry la levante por
  `GET /api/v1/lotes/{id}/consigna-propuesta` y la aplique sin el límite de Δ
  por ciclo.
- **SUPERVISION:** el loop del MPC publica propuestas sin autorizar. Un operario
  (o la propia Pi) las aprueba con `POST /api/v1/lotes/{id}/aplicar-consigna`
  (alias `/aprobar-consigna`); la Pi las aplica y confirma con
  `POST /api/v1/lotes/{id}/consigna/ack`.
- **AUTOMATICO:** el loop publica propuestas ya autorizadas (`autorizada=true`)
  y la Pi las aplica.

**La Pi ENTRADA es la única que escribe el PLC real**, con su propio
`control.sensores`/`control.actuadores` (`modbus_tcp`/`modbus_rtu`). El backend
no abre conexiones Modbus ni escribe ningún PLC: usa un stub interno
determinista (`SimulatedOvenController`, ~40 ms de latencia, siempre acepta)
para auditar y actualizar el estado del horno.

`CONTROL_ACTUADOR` acepta `pi` (default) o `backend`:

- `pi`: la Pi aplica las propuestas del MPC; el backend publica, audita y encola
  el override manual para la Pi.
- `backend`: sólo laboratorio; el backend aplica contra el mismo stub interno,
  nunca contra un PLC real.

El loop emite `control.propuesta` por SSE; el evento incluye `autorizada`. La
propuesta serializa `temp1`, `velocidad`, `z` y `prediccion`. Variables y puesta
en marcha en [`MANUAL_INSTALACION.md`](MANUAL_INSTALACION.md).

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
- **`/api/v1/productos`:** `GET` acepta JWT o Bearer de dispositivo; las
  escrituras (`POST`/`PUT`/`DELETE`) exigen rol Supervisor o Admin.
- **Salud:** `GET /health` y `GET /healthz` verifican conectividad con
  PostgreSQL.

## Documentación

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
