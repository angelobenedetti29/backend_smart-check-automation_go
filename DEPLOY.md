# Deploy — SmartCheck Automation Backend

## Infraestructura actual

| Componente | Servicio | URL / Referencia |
|---|---|---|
| Backend (staging) | Render (Docker, rama `staging`) | `smartcheck-backend-staging` |
| Backend (producción) | Render (Docker, rama `main`) | `smartcheck-backend-prod` |
| Base de datos | Supabase PostgreSQL 16 | Session pooler, puerto 5432 |
| Predictor XGBoost | Sidecar externo (no definido en `render.yaml`) | Debe ser alcanzable por `PREDICTOR_URL` |

Los servicios de Render están definidos en [`render.yaml`](render.yaml). El
backend corre en un contenedor Docker buildeado desde el `Dockerfile` del repo.
La base de datos tiene aplicado el schema de `database/schema.sql` con el seed
inicial de productos.

`render.yaml` define **solo los dos servicios web del backend**
(`smartcheck-backend-staging` y `smartcheck-backend-prod`). No crea ni despliega
el sidecar predictor: si `PREDICTOR_URL` no apunta a un sidecar alcanzable, el
MPC opera degradado y las decisiones se auditan como `skipped`.

---

## Variables de entorno configuradas en Render

| Variable | Descripción |
|---|---|
| `DATABASE_URL` | Connection string a Supabase PostgreSQL 16 (puerto 5432, `sslmode=require`) |
| `JWT_SECRET` | Clave de firma del JWT (mínimo 32 caracteres) |
| `GOOGLE_CLIENT_ID` | Client ID de Google Cloud Console |
| `FRONTEND_ORIGINS` | Orígenes del panel permitidos por CORS, separados por coma |
| `PREDICTOR_URL` | URL base del sidecar predictor; en Render debe ser una URL alcanzable desde el servicio |
| `PREDICTOR_AUTH_TOKEN` | Opcional; si se define, el backend envía `Authorization: Bearer <token>` al sidecar |
| `MPC_CONFIANZA_MIN` | Confianza mínima promedio del detector para proponer cambios (default `0.5`) |
| `MPC_PRECALENTAMIENTO_TIMEOUT_SEGUNDOS` | Timeout de precalentamiento en AUTOMATICO (default `1800`) |
| `MPC_PREHEAT_POLL_SEGUNDOS` | Cadencia de lectura durante el precalentamiento (default `15`) |
| `MPC_PERMITIR_PLACEHOLDER` | Permite operar en AUTOMATICO con el predictor placeholder; solo demo/tesis (default `false`) |
| `TELEMETRIA_MONITOR_INTERVALO` | Cadencia del monitor de telemetría de lotes abiertos (default `15s`) |
| `TELEMETRIA_DESACTUALIZADA_SEGUNDOS` | Antigüedad máxima de la última muestra antes de alertar telemetría desactualizada (default `30s`) |
| `NOTIFICACIONES_EMAIL_PROVIDER` | Proveedor de email: `resend` (default) o `gmail`/`gmail_api` |
| `RESEND_API_KEY` | API key de Resend; si falta, el canal email queda deshabilitado |
| `GMAIL_CLIENT_ID` | OAuth Client ID con Gmail API habilitada (solo proveedor Gmail) |
| `GMAIL_CLIENT_SECRET` | OAuth Client Secret del mismo cliente |
| `GMAIL_REFRESH_TOKEN` | Refresh token con scope `gmail.send` |
| `NOTIFICACIONES_EMAIL_FROM` | Remitente; si falta, el canal email queda deshabilitado (con Gmail, la misma cuenta autenticada) |
| `TELEGRAM_BOT_TOKEN` | Token del bot de Telegram; si falta, el canal Telegram queda deshabilitado |
| `NOTIFICACIONES_PANEL_URL` | URL base del panel; agrega el enlace `/alertas` al mensaje (opcional) |
| `NOTIFICACIONES_INTERVALO` | Cadencia del worker del outbox de notificaciones (default `15s`) |
| `NOTIFICACIONES_MAX_INTENTOS` | Intentos máximos por notificación antes de marcarla `FALLIDO` (default `5`) |
| `PORT` | `8080` |

Claves alineadas con [`render.yaml`](render.yaml) para el backend:
`DATABASE_URL`, `JWT_SECRET`, `GOOGLE_CLIENT_ID`, `FRONTEND_ORIGINS`,
`PREDICTOR_URL`, `PREDICTOR_AUTH_TOKEN`, `NOTIFICACIONES_EMAIL_PROVIDER`,
`RESEND_API_KEY`, `NOTIFICACIONES_EMAIL_FROM`, `GMAIL_CLIENT_ID`,
`GMAIL_CLIENT_SECRET`, `GMAIL_REFRESH_TOKEN`, `TELEGRAM_BOT_TOKEN`,
`NOTIFICACIONES_PANEL_URL`, `TELEMETRIA_MONITOR_INTERVALO`,
`TELEMETRIA_DESACTUALIZADA_SEGUNDOS` y `PORT`. Las `MPC_*`,
`NOTIFICACIONES_INTERVALO` y `NOTIFICACIONES_MAX_INTENTOS` no están declaradas
en `render.yaml`; si no se configuran aplican los defaults del código. Cada canal
de notificación queda deshabilitado si su configuración está incompleta: el
backend arranca igual y solo loguea el canal inactivo. El backend no se conecta
al PLC ni lee variables de driver Modbus.

**La Pi ENTRADA es la única que escribe el PLC real** con su propio
`control.sensores`/`actuadores` (`modbus_tcp`/`modbus_rtu`). El backend no
mantiene un horno en memoria ni aplica consignas: sólo encola la consigna
manual y publica propuestas del MPC; la Pi las levanta por pull. El estado real
del horno es la telemetría (`metricas_horno`), expuesta por
`GET /api/v1/horno`.

El detalle de todas las variables (incluidas las de telemetría de dispositivos)
está en [`MANUAL_INSTALACION.md`](MANUAL_INSTALACION.md) §3.

---

## Endpoints disponibles

La referencia completa y actualizada de la API —con auth, rol mínimo,
request/response y códigos— está en [`docs/api-endpoints.md`](docs/api-endpoints.md).
El flujo legado que antes figuraba acá (`POST /api/v1/lotes` y el listado global
de lotes) fue retirado: `POST /api/v1/lotes` responde **405** y el ciclo actual
se inicia con `POST /api/v1/lotes/inicio`.

El control MPC (SCA-400) expone, entre otros:
`GET /api/v1/lotes/{id}/control/estado`,
`PATCH /api/v1/lotes/{id}/modo`,
`POST /api/v1/lotes/{id}/control/start|stop`,
`PATCH /api/v1/lotes/{id}/control/parametros`,
`GET /api/v1/lotes/{id}/consigna-propuesta`,
`POST /api/v1/lotes/{id}/aplicar-consigna` y
`POST /api/v1/lotes/{id}/consigna/ack`.

---

## Base de datos

Schema aplicado: `database/schema.sql` + `database/migrations/` (el backend
aplica ambos en cada arranque; las versiones quedan en `schema_migrations`).

Tablas principales:

- `productos` — catálogo maestro **y** parámetros de control embebidos (peso,
  dimensión, temperatura, velocidad de cinta, setpoints opcionales y Δmáx del
  MPC). La antigua tabla `parametros_producto` fue unificada y eliminada.
- `lotes_productivos` — registro transaccional de lotes, con sector, estado,
  modo de operación, ventanas, tolerancias, umbrales y setpoints aplicados.
- `sectores` — sectores de producción y relación con dispositivos.
- `dispositivos` y `metricas_dispositivo` — nodos Raspberry Pi y telemetría.
- `metricas_horno` — telemetría cruda del horno reportada por la Pi ENTRADA.
- `eventos_lote` — detecciones individuales de la salida que alimentan los conteos.
- `historial_consignas` — auditoría de consignas despachadas al horno.
- `decisiones_control` — auditoría del MPC (propuestas, skips y aprobaciones).
- `alertas` — alertas operativas (temperatura, telemetría desactualizada,
  dispositivo offline, precalentamiento y calidad).
- `usuarios_notificaciones` — preferencias de notificación por usuario (email,
  Telegram y chat ID); la ausencia de fila equivale a ambos canales habilitados.
- `notificaciones_outbox` — outbox persistente de notificaciones por alerta,
  usuario y canal, con reintentos y backoff.
- `usuarios` — autenticación corporativa (email, rol, password hash).
- `registration_requests` y `device_lifecycle_audit` — enrolamiento y auditoría
  del ciclo de vida de dispositivos.

Seed cargado (6 productos en `database/schema.sql`):

| id | nombre |
|---|---|
| `a1b2c3d4-5678-90ab-cdef-1234567890ab` | Tostada Integral |
| `b2c3d4e5-6789-01ab-cdef-234567890abc` | Pan Lactal |
| `c3d4e5f6-789a-12bc-def3-34567890abcd` | Pan Francés |
| `d4e5f6a7-89ab-23cd-ef34-4567890abcde` | Pan de Salvado |
| `e5f6a7b8-9abc-34de-f456-567890abcdef` | Medialunas |
| `f6a7b8c9-abcd-45ef-5678-67890abcdef1` | Pan Dulce |

Cada producto del seed trae sus rangos de temperatura y velocidad de cinta. Los
setpoints puntuales (`temp_setpoint`/`velocidad_cinta_setpoint`) son nullable y
no vienen sembrados.

---

## Flujo de migraciones y deploy

`.github/workflows/migrate.yml` se ejecuta con push a `staging` o `main`:

1. Resuelve el GitHub Environment `staging` o `production` según la rama.
2. Instala Go y ejecuta `go run ./cmd/migrate` con el secret `DATABASE_URL` del
   environment. El comando aplica `schema.sql` y las migraciones pendientes.
3. Dispara el deploy de Render con `POST $RENDER_DEPLOY_HOOK` (secret
   `RENDER_DEPLOY_HOOK`).

Cada environment de GitHub necesita sus propios secrets `DATABASE_URL` y
`RENDER_DEPLOY_HOOK`. `render.yaml` tiene `autoDeploy: false`: Render **no**
deploya al hacer push; el deploy lo dispara el workflow después de aplicar las
migraciones.

Para evolucionar el esquema, agregá `database/migrations/NNNN_descripcion.sql`.
El runner las aplica en orden y registra cada versión en `schema_migrations`;
no hace falta tocar `schema.sql` para bases existentes.

---

## Reglas de despliegue

- No commitear `.env` ni credenciales.
- Configurar `FRONTEND_ORIGINS` con los orígenes reales del panel, separados por
  coma; no usar `*` con cookies.
- Mantener `JWT_SECRET` distinto entre staging y producción.
- Verificar `/healthz` después del deploy: Render lo usa como health check.
- Si se usa el predictor en producción, desplegarlo como servicio aparte y
  apuntar `PREDICTOR_URL` a una URL alcanzable desde Render. Sin predictor, el
  MPC funciona en modo degradado.
- Para reenviar una alerta existente a destinatarios concretos, ejecutar
  `go run ./cmd/notificar_alerta -emails "a@x.com,b@y.com"` (o `-alerta-id <uuid>`).
  El comando encola filas `EMAIL` en `notificaciones_outbox`; no envía directo:
  el worker del backend las entrega con el proveedor configurado.
