# Manual de instalación — Backend SmartCheck Automation + sidecar predictor

Este manual cubre instalación, configuración, ejecución y tests del backend Go
y del sidecar predictor XGBoost.

---

## 1. Componentes y arquitectura

| Componente | Qué es | Puerto por defecto |
|---|---|---|
| `app` | API REST en Go (`cmd/server/main.go`) | `8080` (`PORT`) |
| `db` | PostgreSQL 16 (contenedor local o Supabase) | `5432` |
| `predictor-svc` | Sidecar HTTP en Python/FastAPI que sirve predicciones XGBoost o placeholder | `8090` |

---

## 2. Requisitos

| Requisito | Versión / nota | ¿Cuándo hace falta? |
|---|---|---|
| Go | 1.25 (`go.mod`: `go 1.25.0`; Dockerfile: `golang:1.25-alpine`) | Solo para ejecutar o compilar sin Docker |
| Docker + Docker Compose | Docker Compose v2 | Opción recomendada; no requiere Go, PostgreSQL ni Python locales |
| PostgreSQL | 16 | Solo si no usás el contenedor `db` (por ejemplo, Supabase o una instancia local) |
| Python | 3.12 (Dockerfile del predictor: `python:3.12-slim`) | Solo para correr o entrenar el sidecar predictor |
| Git | Cualquier versión reciente | Para clonar el repositorio |
| `psql` | Cliente de PostgreSQL | Para aplicar el schema manualmente en Supabase o en una base local |

Con Docker Compose alcanza con Docker: la API, PostgreSQL y el predictor corren
en contenedores. Go, PostgreSQL y Python locales solo se necesitan si elegís el
flujo sin Docker.

---

## 3. Variables de entorno

### 3.1 Crear el `.env`

Copiar la plantilla:

```bash
cp .env.example .env
```

Completar los valores reales en `.env`.

### 3.2 Variables del backend

| Variable | ¿Obligatoria para arrancar? | Default si falta | Uso |
|---|---|---|---|
| `DATABASE_URL` | Sí | — (el servidor aborta) | Cadena de conexión a PostgreSQL |
| `JWT_SECRET` | Sí | — (el servidor aborta) | Firma del JWT de sesión; usar mínimo 32 caracteres |
| `GOOGLE_CLIENT_ID` | Sí | — (el servidor aborta) | Client ID de Google OAuth 2.0 |
| `FRONTEND_ORIGINS` | No | `http://localhost:3000,http://127.0.0.1:3000` | Orígenes permitidos por CORS, separados por coma |
| `PORT` | No | `8080` | Puerto HTTP del backend |
| `PREDICTOR_URL` | No | `http://localhost:8090` | URL base del sidecar predictor |
| `PREDICTOR_AUTH_TOKEN` | No | vacío | Si se define, el backend envía `Authorization: Bearer <token>` en sus requests al sidecar (`/predict`, `/health`, `/admin/modelos`) |
| `MPC_CONFIANZA_MIN` | No | `0.5` | Confianza mínima promedio del detector para proponer cambios |
| `MPC_PRECALENTAMIENTO_TIMEOUT_SEGUNDOS` | No | `1800` | Timeout de precalentamiento en modo automático |
| `MPC_PREHEAT_POLL_SEGUNDOS` | No | `15` | Cadencia de lectura durante el precalentamiento |
| `MPC_PERMITIR_PLACEHOLDER` | No | `false` | Permite operar en automático con el predictor placeholder; solo demo/tesis |
| `DISPOSITIVO_REAPER_INTERVAL` | No | `5s` | Intervalo del reaper que detecta dispositivos offline |
| `DISPOSITIVO_OFFLINE_THRESHOLD` | No | `90s` | Inactividad para considerar offline un dispositivo |
| `TELEMETRIA_MONITOR_INTERVALO` | No | `15s` | Cadencia del monitor de telemetría de lotes abiertos |
| `TELEMETRIA_DESACTUALIZADA_SEGUNDOS` | No | `30s` | Antigüedad máxima de la última muestra antes de alertar telemetría desactualizada |
| `DISPOSITIVO_RENAME_PUSH_TIMEOUT` | No | `10s` | Timeout del push de renombre a la Raspberry |
| `LOTE_CIERRE_MIN_INACTIVIDAD_SEGUNDOS` | No | `0` (no enforce) | Inactividad mínima para aceptar un cierre de lote |
| `NOTIFICACIONES_EMAIL_PROVIDER` | No | `resend` | Proveedor de email: `resend` o `gmail`/`gmail_api` |
| `RESEND_API_KEY` | No | vacío (email deshabilitado) | API key de Resend para enviar alertas por email |
| `GMAIL_CLIENT_ID` | No | vacío | OAuth Client ID (Web application con redirect URI de OAuth Playground) con Gmail API habilitada |
| `GMAIL_CLIENT_SECRET` | No | vacío | OAuth Client Secret del mismo cliente |
| `GMAIL_REFRESH_TOKEN` | No | vacío | Refresh token OAuth con scope `gmail.send` |
| `NOTIFICACIONES_EMAIL_FROM` | No | vacío (email deshabilitado) | Remitente; con Gmail debe ser la cuenta autenticada |
| `TELEGRAM_BOT_TOKEN` | No | vacío (Telegram deshabilitado) | Token del bot de Telegram (BotFather) |
| `NOTIFICACIONES_PANEL_URL` | No | vacío | URL base del panel; agrega el enlace `/alertas` al mensaje |
| `NOTIFICACIONES_INTERVALO` | No | `15s` | Cadencia del worker del outbox |
| `NOTIFICACIONES_MAX_INTENTOS` | No | `5` | Intentos máximos por notificación antes de marcarla `FALLIDO` |
| `TEST_DATABASE_URL` | No | vacío | Activa los tests de integración contra una PostgreSQL aislada |

`JWT_SECRET`, `GOOGLE_CLIENT_ID` y `DATABASE_URL` son validados al inicio. Si
falta alguno, el proceso termina con un mensaje de error y no escucha en el
puerto.

En Docker Compose, `PORT` debe estar definido en `.env` porque el mapeo de
puertos usa `${PORT}:${PORT}`; el default `8080` aplica al binario cuando la
variable no está seteada.

### 3.3 Variables locales de Docker Compose

El servicio `db` se inicializa con estas variables y las usa también en su
healthcheck. Son requeridas para `docker compose up`:

| Variable | Uso |
|---|---|
| `POSTGRES_USER` | Usuario de PostgreSQL del contenedor |
| `POSTGRES_PASSWORD` | Contraseña del usuario |
| `POSTGRES_DB` | Nombre de la base |

No se usan en Render/Supabase.

### 3.4 `DATABASE_URL` según el contexto

| Contexto | Host correcto | Ejemplo |
|---|---|---|
| Contenedor `app` de Compose | `db` | `postgres://smartcheck:smartcheck123@db:5432/smart_check?sslmode=disable` |
| `go run` en el host | `localhost` | `postgres://smartcheck:smartcheck123@localhost:5432/smart_check?sslmode=disable` |
| Supabase PostgreSQL 16 | host de Supabase | `postgres://usuario:password@host:5432/db?sslmode=require` |

Dentro del contenedor `app`, `localhost` apunta al propio contenedor, no a la
base. Si vas a alternar entre Compose y `go run` en el host, ajustá
`DATABASE_URL` o exportá el valor por comando.

Para Supabase usar el **session pooler** en el puerto `5432`; `render.yaml`
explícitamente indica no usar un pooler de transacciones.

### 3.5 Notificaciones de alertas (opcional)

Cuando se persiste una alerta **WARNING** o **CRITICAL**, el backend encola una
notificación por cada usuario activo y canal habilitado. Los emails se envían
con **HTML estilizado** (header por severidad, mensaje, detalles y botón al
panel) y **fallback de texto plano** para clientes que no renderizan HTML. El
envío lo hace un worker con un outbox persistente (`notificaciones_outbox`):
reintenta con backoff exponencial (2, 4, 8… minutos, techo 60) hasta
`NOTIFICACIONES_MAX_INTENTOS`. El unique `(alerta_id, usuario_id, canal)`
garantiza una sola notificación por alerta, usuario y canal.

Cada canal queda deshabilitado si falta su configuración; el backend arranca
igual y sólo loguea el canal inactivo. Para habilitarlos:

- **Email — elegir proveedor con `NOTIFICACIONES_EMAIL_PROVIDER`:**
  - **Resend (`resend`, default):** crear una cuenta en <https://resend.com>,
    verificar el dominio del remitente (DNS) y generar una API key. Configurar
    `RESEND_API_KEY` y `NOTIFICACIONES_EMAIL_FROM` (por ejemplo
    `alertas@fermar.com.ar`). Limitación de test mode: sin dominio verificado,
    Resend sólo permite enviar a la dirección de la cuenta; para producción hay
    que verificar el dominio.
  - **Gmail API (`gmail` o `gmail_api`):** no requiere dominio propio; envía por
    HTTPS con la cuenta de Gmail autenticada. Pasos:
    1. En [Google Cloud Console](https://console.cloud.google.com/) crear (o
       reutilizar) un proyecto y habilitar **Gmail API** en *APIs & Services →
       Library*.
    2. Configurar la *OAuth consent screen*: **External** (con la app en
       *Testing* y el usuario agregado como *test user*) o **Internal** si es
       una cuenta Google Workspace. Agregar el scope
       `https://www.googleapis.com/auth/gmail.send`.
    3. En *Credentials* crear un **OAuth Client ID** de tipo **Web application**,
       agregar `https://developers.google.com/oauthplayground` como
       **Authorized redirect URI** y anotar `Client ID` y `Client Secret`.
       El Playground con credenciales propias no acepta un cliente Desktop.
    4. Obtener el `refresh_token` con el
       [OAuth 2.0 Playground](https://developers.google.com/oauthplayground/)
       usando las credenciales propias: engranaje → *Use your own OAuth
       credentials*, autorizar el scope `gmail.send` con `access_type=offline`
       y `prompt=consent`, y canjear el código por tokens.
    5. Configurar `GMAIL_CLIENT_ID`, `GMAIL_CLIENT_SECRET`,
       `GMAIL_REFRESH_TOKEN`, `NOTIFICACIONES_EMAIL_FROM` (la misma cuenta de
       Gmail autenticada) y `NOTIFICACIONES_EMAIL_PROVIDER=gmail`.
    - **Advertencia:** con la app External en *Testing*, el refresh token vence
      a los 7 días; para que persista hay que publicar la app o usar una cuenta
      Workspace con app Internal.
    - **Límites de Gmail (aprox.):** 500 emails/día en cuentas gratuitas y
      2.000/día en Workspace. Alcanzan para alertas operativas normales.
    - **Render free:** funciona porque la Gmail API es HTTPS; el SMTP de Gmail
      (puerto 587) no está permitido en el plan free.
- **Telegram:** crear un bot con `@BotFather` (`/newbot`) y copiar el token en
  `TELEGRAM_BOT_TOKEN`. Cada usuario debe iniciar el bot (`/start`) y cargar su
  chat ID desde el panel admin. Para obtenerlo, consultar
  `https://api.telegram.org/bot<TOKEN>/getUpdates` después de que el usuario
  escriba al bot y tomar `message.chat.id` (los grupos usan IDs negativos). El
  chat ID se valida con `^-?[0-9]{5,20}$`.
- **Enlace al panel:** `NOTIFICACIONES_PANEL_URL` agrega `<URL>/alertas` al
  cuerpo del mensaje. Opcional.

Los endpoints de administración (`GET/PATCH /api/v1/admin/notificaciones` y
`POST /api/v1/admin/notificaciones/test`) exigen rol Administrador y permiten
editar preferencias por usuario y enviar una prueba directa, sin pasar por el
outbox.

#### Alertas que se notifican

Sólo las alertas `WARNING` y `CRITICAL` se notifican (las `INFO` no). Cada una
llega a todos los usuarios activos por los canales habilitados, con el mismo
formato en Telegram (texto con emojis) y en email (HTML con fallback de texto):

| Tipo | Severidad | Se genera cuando |
|---|---|---|
| `TEMPERATURA_HORNO` | WARNING | T1 se aleja del objetivo más que `tolerancia_temp_c` (2 °C por defecto) |
| `PRECALENTAMIENTO_TIMEOUT` | CRITICAL | El horno no llega a temperatura en `MPC_PRECALENTAMIENTO_TIMEOUT_SEGUNDOS` |
| `PORCENTAJE_QUEMADOS_ALTO` | WARNING | % de quemados de la ventana supera el umbral (5 % por defecto) |
| `PORCENTAJE_CRUDOS_ALTO` | WARNING | % de crudos de la ventana supera el umbral (5 % por defecto) |
| `DISPOSITIVO_OFFLINE` | WARNING | Un dispositivo supera `DISPOSITIVO_OFFLINE_THRESHOLD` sin ping |
| `TELEMETRIA_DESACTUALIZADA` | WARNING | El lote abierto supera `TELEMETRIA_DESACTUALIZADA_SEGUNDOS` sin muestra |

#### Probar las notificaciones

Pasos para verificar ambos canales en un entorno desplegado (usar el rol
Administrador):

1. **Variables de entorno:** cargar las de §3.5 en el servicio (en Render, en
   *Environment*; el `.env` sólo lo lee la máquina local). Al arrancar no deben
   aparecer los avisos `Canal EMAIL deshabilitado` / `Canal TELEGRAM
   deshabilitado`.
2. **Login:** `POST /api/v1/auth/login` (la cookie `session_token` queda en el
   cliente).
3. **Canales:** `GET /api/v1/admin/notificaciones` debe devolver
   `canales.email.configurado` y `canales.telegram.configurado` en `true`, y
   lista cada usuario con su `usuario_id` y `telegram_chat_id`.
4. **Destinatario:** el email se envía a `usuarios.email`. Si hace falta uno
   propio, crear un usuario con `POST /api/v1/admin/usuarios` (el email no se
   puede editar con `PATCH`).
5. **Chat ID de Telegram:** el usuario inicia el bot (`/start`), se obtiene su
   chat ID (ver arriba) y se carga con
   `PATCH /api/v1/admin/notificaciones/{usuario_id}` y
   `{"telegram_chat_id": "123456789"}`.
6. **Prueba genérica:** `POST /api/v1/admin/notificaciones/test` con
   `{"canal": "EMAIL"|"TELEGRAM", "usuario_id": "..."}`.
7. **Un mensaje por tipo de alerta:** el mismo endpoint con el campo `tipo`
   (ver la tabla anterior y `docs/api-endpoints.md` #40). El mensaje lleva
   `[PRUEBA]` y no crea alertas reales.
8. **Flujo real (outbox + worker):** provocar una alerta real, por ejemplo
   `DISPOSITIVO_OFFLINE` (un dispositivo hace ping y se apaga; a los ~90 s se
   crea la alerta) y revisar el estado:

   ```sql
   SELECT canal, destino, estado, intentos, ultimo_error, created_at
   FROM notificaciones_outbox ORDER BY created_at DESC LIMIT 20;
   ```

   `ENVIADO` = entregada; `PENDIENTE` con `ultimo_error` = reintentando;
   `FALLIDO` = agotó `NOTIFICACIONES_MAX_INTENTOS`.

Una colección de Postman con los pasos 2, 3 y 7 está en
[`docs/postman/`](docs/postman/SmartCheck-Notificaciones.postman_collection.json):
se importa, se completa `miEmail` en las variables de la colección y se ejecuta
en orden.

Errores frecuentes: `409 canal_no_configurado` (falta una variable de entorno),
`400 sin_destino` (el usuario no tiene email o chat ID), `502` (el proveedor
rechazó el envío; el motivo está en el log, línea `[NOTIFICACIONES] Prueba
fallida`), y `400 Formato JSON inválido` al enviar `tipo` (el despliegue sigue
con una versión anterior del backend). Con Gmail en modo *Testing* de Google
Cloud, el refresh token vence a los 7 días (`invalid_grant`): hay que
regenerarlo.

#### Reenvío manual de una alerta

Para encolar una alerta existente a destinatarios concretos (por ejemplo, una
`PORCENTAJE_QUEMADOS_ALTO` que no se notificó porque el canal estaba
deshabilitado):

```bash
# Última alerta ACTIVA PORCENTAJE_QUEMADOS_ALTO
go run ./cmd/notificar_alerta -emails "a@x.com,b@y.com"

# Alerta puntual por UUID
go run ./cmd/notificar_alerta -alerta-id <uuid> -emails "a@x.com" \
  -panel-url "https://panel.example"
```

El comando no envía directo: inserta filas `EMAIL` en `notificaciones_outbox`
y el worker del backend las entrega con el proveedor configurado. Es
idempotente por `(alerta_id, usuario_id, canal)`, así que repetirlo no duplica
envíos.

### 3.6 Alertas operativas

El backend genera alertas `WARNING` (o `CRITICAL` en el timeout de
precalentamiento) y las deja en estado `ACTIVA` hasta que un operario las
reconoce (ACK); no hay auto-resolve.

| Tipo | Disparador |
|---|---|
| `TEMPERATURA_HORNO` | T1 promedio (últimas 3 muestras) fuera del objetivo ± `tolerancia_temp_c` (default 2 °C). Lo evalúa el loop MPC en ADAPTANDO y, para lotes `MANUAL`, el monitor de telemetría. |
| `TELEMETRIA_DESACTUALIZADA` | La última muestra del lote abierto supera `TELEMETRIA_DESACTUALIZADA_SEGUNDOS` (default 30 s), o el lote abierto no tiene muestras y lleva abierto más que ese umbral. |
| `DISPOSITIVO_OFFLINE` | El reaper detecta la transición online→offline de una Raspberry. |
| `PRECALENTAMIENTO_TIMEOUT` | El horno no alcanza la temperatura de receta antes de `MPC_PRECALENTAMIENTO_TIMEOUT_SEGUNDOS`. |
| `PORCENTAJE_QUEMADOS_ALTO` / `PORCENTAJE_CRUDOS_ALTO` | El % observado en la ventana del lote supera `umbral_aceptacion_quemado`/`crudo` (default 5 %). |

Las alertas de lote viajan con `lote_id` y las de dispositivo con
`dispositivo_id`; `horno_id` queda vacío. El monitor de telemetría corre cada
`TELEMETRIA_MONITOR_INTERVALO` (default 15 s).

---

## 4. Instalación con Docker Compose (recomendada)

### 4.1 Pasos

```bash
cp .env.example .env
# Editar .env con DATABASE_URL, JWT_SECRET, GOOGLE_CLIENT_ID,
# FRONTEND_ORIGINS y POSTGRES_USER / POSTGRES_PASSWORD / POSTGRES_DB.

docker compose up --build
```

Valores mínimos de ejemplo para un entorno local:

```env
POSTGRES_USER=smartcheck
POSTGRES_PASSWORD=smartcheck123
POSTGRES_DB=smart_check

DATABASE_URL=postgres://smartcheck:smartcheck123@db:5432/smart_check?sslmode=disable
JWT_SECRET=una-clave-de-firma-de-al-menos-32-caracteres
GOOGLE_CLIENT_ID=tu-client-id.apps.googleusercontent.com
FRONTEND_ORIGINS=http://localhost:3000
PORT=8080
```

> `JWT_SECRET` se genera, por ejemplo, con `openssl rand -hex 32` (comentario de
> `.env.example`). No reutilizar el mismo secret en desarrollo y producción.

### 4.2 Qué levanta Compose

| Servicio | Imagen / build | Puertos | Volumen | Notas |
|---|---|---|---|---|
| `db` | `postgres:16-alpine` | `5432:5432` | `postgres_data` | Monta `database/schema.sql` en `/docker-entrypoint-initdb.d/schema.sql`; healthcheck con `pg_isready` cada 5 s |
| `app` | build del `Dockerfile` del repo | `${PORT}:${PORT}` | — | `env_file: .env`; sobrescribe `PREDICTOR_URL=http://predictor-svc:8090`; espera `db` healthy y `predictor-svc` iniciado |
| `predictor-svc` | build de `cmd/predictor_svc/Dockerfile` | `8090:8090` | `predictor_models` | Persiste los modelos entrenados en `/app/predictor_svc/models_xgb` |

La primera ejecución descarga imágenes y compila; las siguientes son más
rápidas. El `db` crea las tablas y el seed la primera vez que se inicializa el
volumen. En cada arranque, el backend aplica `database/schema.sql` y las
migraciones pendientes de `database/migrations/`; cada versión queda registrada
en `schema_migrations`. Para un cambio estructural, agregá una migración nueva.

### 4.3 Logs esperados

```text
db-1             | database system is ready to accept connections
app-1            | Pool de conexiones a PostgreSQL inicializado y verificado exitosamente.
app-1            | Schema de base de datos aplicado/verificado correctamente.
app-1            | Server running on http://localhost:8080
```

El contenedor `predictor-svc` muestra el arranque de uvicorn escuchando en el
puerto `8090`.

### 4.4 Comandos útiles

```bash
# Detener los contenedores (conserva datos y modelos)
docker compose down

# Detener y borrar volúmenes: base de datos y modelos entrenados
docker compose down -v

# Ver logs del backend en tiempo real
docker compose logs -f app

# Ver logs del predictor
docker compose logs -f predictor-svc

# Levantar solo PostgreSQL (para desarrollo local con go run)
docker compose up db
```

> `docker compose down -v` es destructivo: elimina `postgres_data` y
> `predictor_models`. No usarlo si necesitás conservar lotes, usuarios o
> modelos entrenados.

---

## 5. Ejecución sin Docker

Requisitos: Go 1.25 y acceso a una PostgreSQL 16 (local, contenedor o Supabase).

### 5.1 Base de datos

Opción A — solo el contenedor de PostgreSQL:

```bash
docker compose up db
```

Esto requiere `POSTGRES_USER`, `POSTGRES_PASSWORD` y `POSTGRES_DB` en `.env`, y
expone PostgreSQL en `localhost:5432`. En este flujo, `DATABASE_URL` debe usar
`localhost` como host.

Opción B — PostgreSQL local o Supabase: crear la base y aplicar el schema y las
migraciones:

```bash
go run ./cmd/migrate
```

El backend también aplica el schema y las migraciones al arrancar, pero
ejecutarlo manualmente sirve para provisionar la base antes del primer deploy o
para verificar permisos. Para aplicar sólo las migraciones pendientes usá
`go run ./cmd/migrate` (equivale a `schema.sql` + `database/migrations/`).

### 5.2 Backend

Desde la raíz del repositorio:

```bash
go run cmd/server/main.go
```

`main.go` carga `.env` con `godotenv.Load()`, por lo que hay que ejecutar el
comando desde la raíz del repo para que tome ese archivo. Si las variables ya
están exportadas en el entorno, también funcionan sin `.env`.

### 5.3 Tests unitarios

```bash
go test ./... -v
```

Los tests unitarios no requieren base de datos. Los tests de integración se
saltan si `TEST_DATABASE_URL` no está configurada (ver sección 8).

---

## 6. Sidecar predictor

El predictor es un servicio FastAPI que el backend Go consulta por HTTP. No
accede a PostgreSQL: el entrenamiento con datos históricos sí usa
`DATABASE_URL`, pero se ejecuta como script batch fuera del contenedor.

### 6.1 Ejecución local

Desde la raíz del repositorio, con Python 3.12:

```bash
pip install -r cmd/predictor_svc/requirements.txt
uvicorn cmd.predictor_svc.main:app --reload --port 8090
```

El comando debe ejecutarse desde la raíz para que `cmd.predictor_svc` se
resuelva como paquete.

### 6.2 Ejecución con Docker

El contexto de build debe ser la raíz del repositorio, porque el Dockerfile
copia `cmd/predictor_svc/`:

```bash
docker build -f cmd/predictor_svc/Dockerfile -t predictor-svc .
docker run -p 8090:8090 predictor-svc
```

En Compose, los modelos se persisten en el volumen `predictor_models`. Un
`docker run` suelto no persiste los modelos salvo que se monte un volumen sobre
`/app/predictor_svc/models_xgb`.

### 6.3 Endpoints

| Método | Ruta | Descripción |
|---|---|---|
| `GET` | `/health` | Estado del sidecar; incluye `status`, `xgboost_disponible`, `modelos_entrenados`, `directorio_modelos` y `generado_en` |
| `POST` | `/predict` | Devuelve `pctCrudos`, `pctOk`, `pctQuemados`, `modelVersion` y `esPlaceholder` |
| `POST` | `/admin/recargar/{producto_id}` | Invalida la cache del producto; el próximo `/predict` recarga el modelo |
| `GET` | `/admin/modelos` | Lista los IDs de producto con modelo XGBoost entrenado |

Ejemplos:

```bash
curl http://localhost:8090/health

curl -X POST http://localhost:8090/predict \
  -H 'Content-Type: application/json' \
  -d '{
    "producto_id": "a1b2c3d4-5678-90ab-cdef-1234567890ab",
    "temp_horno_1": 195.0,
    "velocidad_cinta": 0.22,
    "temp_comb_1": 800.0,
    "humedad_horno": 12.0
  }'

curl -X POST http://localhost:8090/admin/recargar/a1b2c3d4-5678-90ab-cdef-1234567890ab
curl http://localhost:8090/admin/modelos
```

El request acepta los nombres en `snake_case` que usa el backend Go
(`producto_id`, `temp_horno_1`, `velocidad_cinta`, `temp_comb_1`,
`humedad_horno`) y también sus alias `camelCase`. La respuesta se serializa en
`camelCase`:

```json
{
  "pctCrudos": 2.1,
  "pctOk": 95.4,
  "pctQuemados": 2.5,
  "modelVersion": "xgb_v3",
  "esPlaceholder": false
}
```

### 6.4 Entrenamiento con dataset sintético

El repositorio incluye un dataset de demo con 6 productos:

```bash
python -m cmd.predictor_svc.train \
  --dataset cmd/predictor_svc/data/sintetico_6_productos.csv
```

El script entrena un modelo por producto si hay al menos 30 muestras (default
`--min-muestras`) y guarda los artefactos en:

```text
cmd/predictor_svc/models_xgb/producto_{uuid}_v{n}.json
```

El sidecar carga el modelo más reciente en el próximo `/predict`. En Docker, ese
directorio está montado sobre el volumen `predictor_models`.

Para entrenar con datos históricos reales, el script puede leer la base:

```bash
DATABASE_URL='postgres://usuario:password@host:5432/db?sslmode=require' \
  python -m cmd.predictor_svc.train

# Un solo producto:
DATABASE_URL='...' python -m cmd.predictor_svc.train --producto <uuid>
```

También existe el flujo de exportación por ventanas (`export_dataset.py`),
documentado en `cmd/predictor_svc/README.md`; no es necesario para una
instalación básica.

### 6.5 Placeholder vs. XGBoost

- **Placeholder** (default al inicio): modelo determinístico calibrado con los
  rangos del producto. No aprende. Permite que el MPC tenga algo con qué
  optimizar antes de juntar histórico. La respuesta trae
  `esPlaceholder: true`.
- **XGBoost entrenado**: se usa cuando existe un artefacto
  `producto_{uuid}_v{n}.json`. La respuesta trae `esPlaceholder: false` y
  `modelVersion` con la versión del modelo.

`MPC_PERMITIR_PLACEHOLDER` es una variable **del backend**, no del sidecar. Con
`false` (default y recomendado en producción), el loop automático se bloquea
con `modelo_no_entrenado` si solo hay placeholder. Con `true` permite operar en
automático para demos/tesis.

### 6.6 Integración con el backend

| Contexto | `PREDICTOR_URL` |
|---|---|
| Docker Compose | `http://predictor-svc:8090` (lo fija `docker-compose.yml`) |
| Backend en el host, predictor local | `http://localhost:8090` (default del código) |
| Predictor remoto | URL alcanzable desde el backend |

Si `PREDICTOR_AUTH_TOKEN` está definido, el backend agrega
`Authorization: Bearer <token>` en sus requests al sidecar (`/predict`,
`/health` y `/admin/modelos`). Si el sidecar no responde, el backend sigue
funcionando en modo degradado: la decisión se audita como `skipped`.

---

## 7. Verificación de la instalación

| Chequeo | Comando | Resultado esperado |
|---|---|---|
| Salud del backend | `curl http://localhost:8080/health` | `{"status":"healthy"}` |
| Salud para Render/healthcheck | `curl http://localhost:8080/healthz` | `{"status":"healthy"}` |
| Raíz | `curl http://localhost:8080/` | `SmartCheck Automation Backend running.` |
| Salud del predictor | `curl http://localhost:8090/health` | JSON con `"status":"ok"` |
| Productos con JWT | `curl -b "session_token=<JWT>" http://localhost:8080/api/v1/productos` | Listado de productos del seed |
| Logs de DB | `docker compose logs db` | `database system is ready to accept connections` |
| Logs del backend | `docker compose logs app` | Pool inicializado, schema aplicado, servidor escuchando |

Si PostgreSQL no responde, `/health` y `/healthz` devuelven HTTP `503` con:

```json
{"status":"unhealthy","reason":"database unreachable"}
```

Para obtener un JWT con un usuario del seed de desarrollo:

```bash
curl -i -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@fermar.com.ar","password":"password123"}'
```

La respuesta setea la cookie HttpOnly `session_token`. Luego:

```bash
curl -b "session_token=<JWT>" http://localhost:8080/api/v1/productos
```

> Los usuarios `admin@fermar.com.ar`, `supervisor@fermar.com.ar` y
> `operario@fermar.com.ar` con contraseña `password123` son un seed de
> desarrollo. Cambiarlos o desactivarlos en producción.

`GET /api/v1/productos` también acepta `Authorization: Bearer <secret>` de un
dispositivo registrado, según el auth dual del backend.

Puertos a revisar si algo no responde:

| Puerto | Servicio |
|---|---|
| `5432` | PostgreSQL (`db`) |
| `8080` | Backend Go (`app`) |
| `8090` | Sidecar predictor (`predictor-svc`) |

---

## 8. Tests

### 8.1 Backend Go

```bash
# Tests unitarios (no requieren base de datos)
go test ./... -v
```

El workflow de CI (`.github/workflows/ci.yml`) ejecuta además:

```bash
go vet ./...
go test ./... -v -race -timeout 60s
go build -o /dev/null ./cmd/server
```

### 8.2 Tests de integración

Los tests de integración usan `TEST_DATABASE_URL`. Si la variable no está
definida, se saltan explícitamente con el mensaje
`TEST_DATABASE_URL no configurada — se requiere PostgreSQL aislado`.

```bash
export TEST_DATABASE_URL='postgres://smartcheck:smartcheck123@localhost:5432/smart_check_test?sslmode=disable'
go test ./... -v
```

Los tests crean y eliminan bases temporales (`sca_test_*` y
`sca_repo_test_*`), por lo que el usuario de `TEST_DATABASE_URL` necesita
permiso para crear bases. La base indicada en `TEST_DATABASE_URL` debe existir
antes de correr los tests.
**No apuntar `TEST_DATABASE_URL` a la base de producción.**

### 8.3 Tests del predictor

Los tests del sidecar usan `pytest` y están en `cmd/predictor_svc/tests/`.
`pytest` **no** figura en `cmd/predictor_svc/requirements.txt`; instalarlo
aparte:

```bash
pip install pytest
python -m pytest cmd/predictor_svc/tests
```

`cmd/predictor_svc/pytest.ini` configura `--import-mode=importlib` para evitar
la colisión del paquete `cmd/` con el módulo estándar `cmd` de Python.

Los tests cubren predicción, features, exportación de dataset y entrenamiento
(`test_predict.py`, `test_features.py`, `test_export_dataset.py`,
`test_train_dataset.py`). No hay una suite E2E de pytest declarada en el
repositorio.

---

## 9. Troubleshooting

| Síntoma | Causa probable | Solución |
|---|---|---|
| `Error fatal al inicializar pool de PostgreSQL` | `DATABASE_URL` incorrecta o DB caída | Revisar host, puerto, usuario, contraseña y `sslmode`. En Compose el host es `db`; en el host es `localhost` |
| `database system is ready...` no aparece | El contenedor `db` no arrancó | `docker compose ps` y `docker compose logs db`; verificar `POSTGRES_*` en `.env` |
| El backend no arranca y menciona `GOOGLE_CLIENT_ID y JWT_SECRET son requeridos` | Falta una de esas variables | Completarlas en `.env` o en el entorno; ejecutar desde la raíz para que `godotenv` lea `.env` |
| `DATABASE_URL no configurada` | Falta la variable | Definirla en `.env` o exportarla |
| El schema no está aplicado | El volumen ya existía y el initdb no vuelve a correr | El backend aplica schema + migraciones en cada arranque; verificar el log `Schema de base de datos aplicado/verificado correctamente.` o ejecutar `go run ./cmd/migrate`. Si una migración falla, revisar `schema_migrations` y el error del runner |
| Errores de CORS en el panel | `FRONTEND_ORIGINS` no coincide exactamente con el origen | Incluir esquema, host y puerto exactos, separados por coma. Un `OPTIONS` de un origen no permitido responde `403`; una escritura de gestión responde `403 origin_not_allowed` |
| El predictor no responde | `PREDICTOR_URL` incorrecta o contenedor caído | En Compose debe ser `http://predictor-svc:8090`; fuera de Compose, `http://localhost:8090`. Verificar `curl http://localhost:8090/health` y `docker compose logs predictor-svc` |
| El MPC no propone cambios y audita `skipped` | Predictor caído o inalcanzable | Es el modo degradado esperado; restablecer el sidecar. El horno sigue con el último setpoint |
| `modelo_no_entrenado` en automático | Solo hay placeholder y `MPC_PERMITIR_PLACEHOLDER=false` | Entrenar un modelo XGBoost o, solo para demo/tesis, poner `MPC_PERMITIR_PLACEHOLDER=true` |
| `bind: address already in use` en 8080 | Otro proceso usa el puerto | Liberar el puerto o cambiar `PORT` en `.env` (Compose mapea `${PORT}:${PORT}`) |
| Conflicto en 5432 | Otra PostgreSQL local ocupa el puerto | Detener ese proceso o ajustar el mapeo del servicio `db` en `docker-compose.yml` |
| Conflicto en 8090 | Otro proceso usa el puerto del predictor | Liberar `8090`; Compose y el contrato del backend lo fijan |
| Cambios de schema no aparecen tras actualizar el repo | La migración no se aplicó o falló | Verificar el log `Schema de base de datos aplicado/verificado correctamente.` y la tabla `schema_migrations`; correr `go run ./cmd/migrate`. Si una migración falla, el backend no arranca |
| `go: go.mod requires go >= 1.25.0` | Toolchain de Go anterior | Instalar Go 1.25 o usar el build Docker (`golang:1.25-alpine`) |
| La consigna no llega al PLC real | El backend no escribe el PLC por diseño | Verificar que la Pi ENTRADA esté aplicando las propuestas del MPC (`consigna-propuesta` + `consigna/ack`); el backend sólo publica y audita |

---

## 10. Seguridad y operación

- **Secretos**: mantener `JWT_SECRET`, `GOOGLE_CLIENT_ID`, `DATABASE_URL`,
  `PREDICTOR_AUTH_TOKEN` y las credenciales de PostgreSQL en `.env` local o en
  los secrets de Render/GitHub. `.env` está en `.gitignore`; no commitearlo.
- **JWT**: usar un secret aleatorio de al menos 32 caracteres
  (`openssl rand -hex 32`) y distinto por entorno.
- **CORS**: listar orígenes explícitos en `FRONTEND_ORIGINS`. El backend refleja
  el origen permitido y habilita credenciales; no usar `*` con cookies.
- **Base de tests**: `TEST_DATABASE_URL` debe apuntar a una PostgreSQL aislada.
  Los tests crean y eliminan bases temporales y no deben correr contra
  producción.
- **Usuarios seed**: el schema crea `admin@fermar.com.ar`,
  `supervisor@fermar.com.ar` y `operario@fermar.com.ar` con contraseña
  `password123`. Cambiar las contraseñas o desactivar los usuarios en producción
  (`UPDATE usuarios SET activo=false ...`).
- **Supabase**: usar `sslmode=require` y el session pooler en `5432`.
- **Dispositivos**: el backend guarda solo `SHA-256(secret)`; el secret en texto
  plano de una solicitud de registro se entrega una sola vez y los
  `request_id` se redactan en los logs de acceso.
- **Volúmenes**: `docker compose down -v` borra la base y los modelos
  entrenados. Tratarlo como un reset destructivo.
- **Predictor**: en producción dejar `MPC_PERMITIR_PLACEHOLDER=false`; el
  placeholder es solo para demo/tesis.
