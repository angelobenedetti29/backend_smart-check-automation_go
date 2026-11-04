# Smart-Check Automation — Backend (Go)

## Contexto del proyecto
Sistema de supervisión inteligente para hornos túnel (Fermar S.A.).
Backend en Go que expone una API REST consumida por un frontend Next.js.
Los datos son enviados por nodos Raspberry Pi via HTTP.

## Stack
- Lenguaje: Go 1.25
- Base de datos: PostgreSQL 16 (Supabase)
- Infraestructura: Docker + Docker Compose (`db`, `app`, `predictor-svc`)
- Predictor: sidecar Python/FastAPI + XGBoost (`cmd/predictor_svc`, puerto `8090`)
- Frontend: Next.js (React) — consumidor de esta API
- Autenticación (usuarios): Google OAuth 2.0 + credenciales locales (bcrypt) → JWT HttpOnly cookie
- Autenticación (Raspberry Pi → API): secret por nodo vía header `Authorization: Bearer <secret>`; el nodo pide el alta, un Supervisor/Admin la aprueba y el backend guarda solo `SHA-256(secret)` (`secret_hash`). Ver `docs/device-registration-contract.md`

## Convenciones del equipo
- Nombres de structs: PascalCase
- Nombres de variables y funciones: camelCase
- Comentario breve por cada función exportada
- Un archivo por componente/entidad
- Patrón: Handler → Service → Repository → PostgreSQL

## Convención de commits

Formato obligatorio:

```
<tipo>(SCA-XXX): <descripción en imperativo, minúscula, sin punto final>
```

| Tipo | Cuándo usarlo |
|---|---|
| `feat` | Nueva funcionalidad |
| `fix` | Corrección de un bug |
| `refactor` | Cambio de código que no agrega feature ni corrige bug |
| `test` | Agregar o modificar tests |
| `docs` | Cambios en documentación |
| `chore` | Tareas de mantenimiento (deps, configs, CI) |
| `style` | Formato, linting, sin cambio de lógica |
| `perf` | Mejora de rendimiento |

El identificador `SCA-XXX` es obligatorio — permite la vinculación automática con Jira.

## Ramas

```
main        ← versión estable y aprobada (solo merge desde staging)
staging     ← versión lista para pruebas finales antes de producción
develop     ← línea principal de integración
feature/    ← nueva funcionalidad (se crea desde develop)
bugfix/     ← corrección de bug (se crea desde develop)
```

Nombrado: `feature/SCA-042-clasificacion-imagenes`, `bugfix/SCA-067-validacion-jwt`

**Prohibido** hacer commit directo a `main`, `staging` o `develop`.

## Estructura de carpetas

```
backend_smart-check-automation_go/
├── cmd/server/main.go                               # Entry point: wiring de dependencias y setup de rutas
├── cmd/predictor_svc/                               # Sidecar FastAPI: predictor XGBoost + entrenamiento
├── internal/
│   ├── controller/
│   │   ├── auth/                                    # POST /api/v1/auth/login|google|logout
│   │   ├── consigna/                                # POST /api/v1/horno/consigna, GET /api/v1/horno/consigna/historial
│   │   ├── control/                                 # Start/stop/tuning/modo, consigna-propuesta, aplicar-consigna, ack, estado, SSE control.propuesta
│   │   ├── decisiones_control/                      # GET /api/v1/lotes/{id}/decisiones
│   │   ├── dispositivo/                             # POST /api/v1/dispositivos/ping, GET|PUT /api/v1/dispositivos, GET /api/v1/dispositivos/metricas
│   │   ├── devicetoken/                             # Middleware BeforeMux: auth Bearer de nodos Raspberry Pi
│   │   ├── dualauth/                                # Auth dual (Bearer de dispositivo o cookie JWT)
│   │   ├── horno/                                   # GET /api/v1/horno, POST /api/v1/horno/temperatura
│   │   ├── lote_sector/                             # GET /api/v1/dispositivos/sector, POST /api/v1/lotes/inicio|{id}/eventos|{id}/cierre, GET /api/v1/lotes|/lotes/abierto
│   │   ├── metricashorno/                           # POST|GET /api/v1/lotes/{id}/metricas-horno
│   │   ├── producto/                                # GET/POST /api/v1/productos, PUT|DELETE /api/v1/productos/{id}
│   │   ├── registro/                                # POST|GET /api/v1/registration-requests (+ approve/reject)
│   │   ├── sector/                                  # POST /api/v1/sectores, PUT|DELETE /api/v1/sectores/{id}
│   │   ├── sse/                                     # Handler SSE genérico
│   │   └── user/                                    # GET|POST /api/v1/admin/usuarios, PATCH .../usuarios/{id}
│   ├── domain/
│   │   ├── alerta/                                  # Modelo Alerta
│   │   ├── consigna/                                # Modelo Consigna (auditoría) + ConsignaManualRequest
│   │   ├── decisiones_control/                      # Audit del MPC (decisiones, predicción, aprobación)
│   │   ├── dispositivo/                             # Modelo Dispositivo + MetricaDispositivo + EstadoDispositivo + DeviceRead
│   │   ├── horno/                                   # Modelo Horno
│   │   ├── lote_sector/                             # Modelo Lote + Evento + modo/ventanas + interfaces Repository/Service
│   │   ├── metricashorno/                           # Telemetría cruda del horno (metricas_horno)
│   │   ├── producto/                                # Modelo Producto (catálogo maestro + parámetros de control)
│   │   ├── registro/                                # Modelo de solicitudes de enrolamiento
│   │   ├── sector/                                  # Modelo Sector + contrato Repository
│   │   └── user/                                    # Entidad User, constantes de roles, errores centinela
│   ├── provider/
│   │   ├── database/                                # NewPostgresPool, repos en memoria, StateStore
│   │   ├── google/                                  # Implementa user.GoogleVerifier vía google/api/idtoken
│   │   ├── oven_controller/                         # Stub interno del controlador del horno (SimulatedOvenController); no abre Modbus ni escribe un PLC
│   ├── repository/
│   │   ├── lote_sector_repo.go                      # PostgreSQL — ciclo de lote por sector
│   │   ├── sector_repo.go                           # PostgreSQL — sectores y relación con dispositivos
│   │   ├── producto_repo.go                         # PostgreSQL — catálogo de productos y sus parámetros de control
│   │   ├── dispositivo_repo.go                      # PostgreSQL — Dispositivos y métricas
│   │   ├── device_auth_repo.go                      # PostgreSQL — credenciales de nodos (secret_hash)
│   │   ├── registro_repo.go                         # PostgreSQL — solicitudes de enrolamiento
│   │   ├── consigna_repo.go                         # PostgreSQL — Historial de consignas
│   │   ├── decisiones_control_repo.go               # PostgreSQL — audit del MPC
│   │   ├── metricas_horno_repo.go                   # PostgreSQL — telemetría cruda del horno
│   │   └── user_postgres_repository.go              # PostgreSQL — CRUD usuarios
│   └── service/
│       ├── auth/                                    # LoginWithGoogle, LoginWithCredentials, generateJWT
│       ├── consigna/                                # Despacho de consigna (manual SCA-320 y MPC SCA-400)
│       ├── control/                                 # Loop MPC por lote: precalentamiento, propuesta, aprobación, ack
│       ├── dispositivo/                             # Lógica de ping, reaper y SSE
│       ├── horno/                                   # Lógica de umbrales térmicos y alertas
│       ├── lote_sector/                             # Ciclo de lote por sector (apertura, eventos, cierre, historial)
│       ├── optimizador/                             # MPC horizonte 1: grilla, Z, límites y pesos
│       ├── predictor/                               # Cliente HTTP al sidecar XGBoost
│       ├── producto/                                # Catálogo de productos y sus parámetros de control
│       ├── registro/                                # Enrolamiento de dispositivos
│       ├── sector/                                  # CRUD de sectores (slug de id, borrado bloqueado con lotes)
│       └── user/                                    # ListUsers, CreateUser, UpdateUser
├── pkg/response/response.go                         # Envelope JSON estándar {success, message, data, errors}
├── database/schema.sql                              # DDL idempotente: productos, lotes, sectores, dispositivos, métricas, consignas, decisiones_control, alertas, usuarios, registro
├── Dockerfile                                       # Multi-stage build: golang:1.25-alpine → alpine
├── docker-compose.yml
├── .env.example                                     # Plantilla de variables
└── go.mod / go.sum
```

## Schema de base de datos (fuente de verdad: database/schema.sql)

- **productos**: Catálogo maestro **y** parámetros de control embebidos (peso, dimensión, temperatura, velocidad de cinta, setpoints opcionales y Δmáx del MPC). La antigua tabla `parametros_producto` fue unificada y eliminada; `temp_setpoint`/`velocidad_cinta_setpoint` son nullable y el loop del MPC (SCA-400) los usa como receta de arranque cuando el lote no tiene setpoints propios.
- **lotes_productivos**: Registros transaccionales de horneadas (con `sector_id`, `estado`, `modo`, ventanas de control, tolerancias, umbrales, pesos objetivo y setpoints aplicados).
- **sectores**: Sectores de producción (`id` legible tipo slug, `nombre`). Un dispositivo pertenece a un sector (`dispositivos.sector_id`, nullable); la columna legacy `dispositivos.ubicacion` fue eliminada y sus valores migrados a sectores al arrancar (migración idempotente; si dos dispositivos del mismo tipo comparten ubicación solo el primero queda asignado, y nunca viola `uq_dispositivos_sector_tipo`). El `DROP COLUMN` es irreversible: un rollback al binario anterior requiere restaurar la columna. El deploy de frontend y backend debe ser coordinado (el decoder es estricto: `ubicacion` vs `sectorId`).
- **dispositivos** & **metricas_dispositivo**: Nodos Raspberry Pi y telemetría (CPU/RAM/Temp).
- **metricas_horno**: Telemetría cruda del horno reportada por la Pi ENTRADA (T1, combustión, humedad, ambiente, velocidad y setpoints observados). Es la fuente de la ventana de evaluación del MPC.
- **decisiones_control**: Audit del MPC. Cada fila registra la observación, la predicción XGBoost, la propuesta, el resultado (`aplicado_por`), el motivo de skip y la aprobación (`aprobada_por`/`aprobada_at`) cuando el modo es SUPERVISION.
- **historial_consignas**: Auditoría de las consignas despachadas al horno. `origen='MANUAL'` corresponde a SCA-320 y `origen='AUTOMATICO'` al despacho del MPC (SCA-400) cuando el actuador es `backend`; con actuador `pi`, el MPC sólo publica la propuesta y la aplicación se confirma por `/consigna/ack`. El trigger automático de SCA-142 desde `/lotes/inicio` ya no existe.
- **registration_requests**: Solicitudes de enrolamiento de nodos (`PENDING`/`APPROVED`/`REJECTED`/`EXPIRED`) con `request_id` y secret de un solo uso.
- **eventos_lote**: Detecciones individuales reportadas por los nodos, deduplicadas por `evento_id`, que alimentan los conteos del lote.
- **device_lifecycle_audit**: Traza de auditoría del ciclo de vida de dispositivos (approve/reject del alta, baja lógica) con actor y estados anterior/nuevo.
- **usuarios**: Autenticación corporativa (Email, Rol, Password Hash).

Seed de desarrollo: `admin@fermar.com.ar`, `supervisor@fermar.com.ar`, `operario@fermar.com.ar`. Contraseña seed: `password123`.

## Endpoints implementados

| Método | Ruta | Auth | Rol mínimo |
|---|---|---|---|
| GET | /health | — | — |
| GET | /healthz | — | — |
| POST | /api/v1/auth/login | — | — |
| POST | /api/v1/auth/google | — | — |
| POST | /api/v1/auth/logout | — | — |
| GET | /api/v1/admin/usuarios | JWT cookie | Administrador |
| POST | /api/v1/admin/usuarios | JWT cookie | Administrador |
| PATCH | /api/v1/admin/usuarios/{id} | JWT cookie | Administrador |
| GET | /api/v1/horno | JWT cookie | cualquier rol |
| POST | /api/v1/horno/temperatura | JWT cookie | Supervisor, Admin |
| GET | /api/v1/productos | Bearer dispositivo o JWT cookie | — |
| POST | /api/v1/productos | JWT cookie | Supervisor, Admin |
| PUT | /api/v1/productos/{id} | JWT cookie | Supervisor, Admin |
| DELETE | /api/v1/productos/{id} | JWT cookie | Supervisor, Admin |
| GET | /api/v1/dispositivos/sector | Bearer dispositivo | — |
| GET | /api/v1/sectores | JWT cookie | cualquier rol |
| POST | /api/v1/sectores | JWT cookie | Supervisor, Admin |
| PUT | /api/v1/sectores/{id} | JWT cookie | Supervisor, Admin |
| DELETE | /api/v1/sectores/{id} | JWT cookie | Supervisor, Admin |
| POST | /api/v1/lotes/inicio | Bearer dispositivo | — |
| GET | /api/v1/lotes/abierto | Bearer dispositivo o JWT cookie | — |
| POST | /api/v1/lotes/{id}/eventos | Bearer dispositivo | — |
| POST | /api/v1/lotes/{id}/cierre | Bearer dispositivo | — |
| GET | /api/v1/lotes | Bearer dispositivo o JWT cookie | — |
| GET | /api/v1/lotes/events | JWT cookie | cualquier rol |
| POST | /api/v1/dispositivos/ping | Bearer dispositivo | — |
| GET | /api/v1/dispositivos | JWT cookie | cualquier rol |
| PUT | /api/v1/dispositivos | JWT cookie | Supervisor, Admin |
| DELETE | /api/v1/dispositivos | JWT cookie | Supervisor, Admin |
| PUT | /api/v1/dispositivos/nombre | Bearer dispositivo | — |
| PUT | /api/v1/dispositivos/{id}/nombre | JWT cookie | Supervisor, Admin |
| GET | /api/v1/dispositivos/metricas | JWT cookie | cualquier rol |
| GET | /api/v1/dispositivos/events | JWT cookie | cualquier rol |
| POST | /api/v1/horno/consigna | JWT cookie | Operario, Supervisor, Admin |
| GET | /api/v1/horno/consigna/historial | JWT cookie | cualquier rol |
| GET | /api/v1/horno/events | JWT cookie | cualquier rol |
| GET | /api/v1/lotes/{id}/control/estado | JWT cookie | cualquier rol |
| PATCH | /api/v1/lotes/{id}/modo | JWT cookie | Supervisor, Admin |
| PUT | /api/v1/lotes/{id}/modo | Bearer dispositivo (o JWT) | — (dispositivo de su sector) |
| POST | /api/v1/lotes/{id}/control/start | JWT cookie | Supervisor, Admin |
| POST | /api/v1/lotes/{id}/control/stop | JWT cookie | Supervisor, Admin |
| PATCH | /api/v1/lotes/{id}/control/parametros | JWT cookie | Supervisor, Admin |
| GET | /api/v1/lotes/{id}/consigna-propuesta | Bearer dispositivo o JWT cookie | — |
| POST | /api/v1/lotes/{id}/aplicar-consigna | Bearer dispositivo o JWT cookie | Operario, Supervisor, Admin |
| POST | /api/v1/lotes/{id}/aprobar-consigna | Bearer dispositivo o JWT cookie | Operario, Supervisor, Admin |
| POST | /api/v1/lotes/{id}/consigna/ack | Bearer dispositivo | — |
| GET | /api/v1/lotes/{id}/decisiones | JWT cookie | cualquier rol |
| GET | /api/v1/lotes/{id}/confianza-clases | JWT cookie | cualquier rol |
| POST | /api/v1/lotes/{id}/metricas-horno | Bearer dispositivo | — |
| GET | /api/v1/lotes/{id}/metricas-horno | Bearer dispositivo o JWT cookie | — |
| GET | /api/v1/lotes/{id}/control/events | JWT cookie | cualquier rol |
| POST/GET | /api/v1/registration-requests | — (POST/GET por id) · JWT Supervisor/Admin (listado y approve/reject) | — |

Nota: `POST /api/v1/lotes/inicio` ya **no dispara la consigna automática de SCA-142**;
solo abre/persiste el lote abierto del sector. Si el lote nace en modo no manual,
arranca el loop del MPC (SCA-400), que es quien propone/aplica consignas.

## Decisiones de arquitectura tomadas

- **JWT en cookie HttpOnly**: `session_token` — HttpOnly+Secure+SameSite=Strict.
- **RBAC en middleware**: `RequireRoleFromDB(...)` se encadena después de `JWTMiddleware` y re-lee rol/activo desde la DB en cada request. Roles: `Administrador > Supervisor > Operario`.
- **Bcrypt para contraseñas locales**: `DefaultCost`. Usuarios Google-only tienen `password_hash = NULL`.
- **Estado de dispositivos en caché en memoria + historial en PostgreSQL**: `MemoryDispositivoStateStore` para lecturas rápidas; historial append-only en `metricas_dispositivo`.
- **Detección de offline con reaper en background**: `StartReaper` corre cada `DISPOSITIVO_REAPER_INTERVAL`.
- **Consigna manual y MPC**: `ConsignaService.Dispatch` es el camino único; `DispatchManual` (SCA-320) usa `origen=MANUAL` y el loop del MPC (SCA-400) usa `origen=AUTOMATICO`. El trigger automático de SCA-142 desde `/lotes/inicio` fue retirado.
- **Modos de operación por lote**: `MANUAL` no corre loop; `SUPERVISION` publica propuestas que requieren aprobación; `AUTOMATICO` publica propuestas ya autorizadas. El modo se persiste en `lotes_productivos.modo` y se cambia con `PATCH` (panel) o `PUT` (Pi) `/api/v1/lotes/{id}/modo`.
- **Actuador configurable**: para el loop MPC, `CONTROL_ACTUADOR=pi` (default) hace que la Pi ENTRADA aplique la consigna haciendo pull; `backend` (solo laboratorio) despacha contra el stub interno del backend, nunca contra un PLC real. La consigna manual siempre pasa por `ConsignaService.dispatch` y llama al stub; el actuador sólo decide si además se encola el override.
- **PLC del horno**: el backend no abre conexiones Modbus ni escribe ningún PLC. La Pi ENTRADA es la única escritora del PLC real, con su propio `control.sensores`/`actuadores` (`modbus_tcp`/`modbus_rtu`). El backend usa `SimulatedOvenController` (stub determinista, ~40 ms de latencia, siempre acepta) para auditar y registrar el comando de consigna.
- **Estado del horno**: `GET /api/v1/horno` es una proyección de solo lectura de `metricas_horno` (último sample del lote abierto) + lote + alertas. Estados: `SIN_LOTE`, `SIN_TELEMETRIA`, `SIN_DATOS`, `ACTIVO`, `DESACTUALIZADO` (>30 s) y `CONTROL_MANUAL` (alerta `CONSIGNA_ENLACE` activa). Sin telemetría los campos van `null`. El horno en memoria (`horno.Repository`/`Ensure`) sólo registra comandos/auditoría y sirve al endpoint legacy `POST /api/v1/horno/temperatura`; no es fuente de estado.
- **Override manual encolado**: en modo `MANUAL`, con actuador `pi` y lote abierto, `POST /api/v1/horno/consigna` deja una decisión ya aprobada con `muestras=0`. La Pi la obtiene por `consigna-propuesta` con `manual_override=true` y la aplica sin el clamp de Δ por ciclo; luego confirma por `/consigna/ack`.
- **SSE de control**: el loop emite `control.propuesta` con `autorizada` (true en AUTOMATICO, false en SUPERVISION hasta la aprobación). La `Propuesta` serializa `temp1`, `velocidad`, `z` y `prediccion`.
- **Predictor sidecar**: cliente HTTP con timeout de 2s; si el sidecar falla, el optimizador no propone y la decisión se audita como `skipped`. Los artefactos entrenados son JSON (`save_raw(raw_format="json")`) por producto en `models_xgb/`.
- **Aprobación y ACK**: en SUPERVISION la aprobación se persiste en `decisiones_control.aprobada_por`; el ACK de la Pi marca `aplicado_por` y los setpoints efectivamente aplicados.

## Variables de entorno

> Detalle, defaults y despliegue en [`MANUAL_INSTALACION.md`](MANUAL_INSTALACION.md) §3.

| Variable | Descripción |
|---|---|
| `DATABASE_URL` | Conexión a PostgreSQL (Supabase PostgreSQL 16 o Docker local) |
| `FRONTEND_ORIGINS` | Orígenes permitidos por CORS para el panel, separados por coma (default `http://localhost:3000,http://127.0.0.1:3000`) |
| `JWT_SECRET` | Clave de firma del JWT. Mínimo 32 chars. |
| `GOOGLE_CLIENT_ID` | Client ID de Google Cloud Console |
| `LOTE_CIERRE_MIN_INACTIVIDAD_SEGUNDOS` | Inactividad mínima (segundos) para aceptar un cierre de lote (default 0 = no enforce) |
| `PORT` | Puerto HTTP (default 8080) |
| `DISPOSITIVO_REAPER_INTERVAL` | Intervalo del reaper (default 5s) |
| `DISPOSITIVO_OFFLINE_THRESHOLD` | Umbral de inactividad (default 90s) |
| `PREDICTOR_URL` | URL base del sidecar predictor (default `http://localhost:8090`) |
| `PREDICTOR_AUTH_TOKEN` | Opcional — Bearer para autenticar contra el sidecar |
| `CONTROL_ACTUADOR` | `pi` (default; la Pi aplica en el PLC) o `backend` (solo laboratorio; aplica contra el stub interno, no contra un PLC) |
| `MPC_CONFIANZA_MIN` | Confianza mínima promedio para proponer (default `0.5`) |
| `MPC_PRECALENTAMIENTO_TIMEOUT_SEGUNDOS` | Timeout de precalentamiento (default `1800`) |
| `MPC_PREHEAT_POLL_SEGUNDOS` | Cadencia durante precalentamiento (default `15`) |
| `MPC_PERMITIR_PLACEHOLDER` | Permite AUTOMATICO con placeholder (default `false`) |
| `TEST_DATABASE_URL` | Opcional — activa tests de integración con DB real |
