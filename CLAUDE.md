# Smart-Check Automation — Backend (Go)

## Contexto del proyecto
Sistema de supervisión inteligente para hornos túnel (Fermar S.A.).
Backend en Go que expone una API REST consumida por un frontend Next.js.
Los datos son enviados por nodos Raspberry Pi via HTTP.

## Stack
- Lenguaje: Go 1.25
- Base de datos: PostgreSQL 16 (Aiven Cloud)
- Infraestructura: Docker + Docker Compose
- Frontend: Next.js (React) — consumidor de esta API
- Autenticación (usuarios): Google OAuth 2.0 + credenciales locales (bcrypt) → JWT HttpOnly cookie
- Autenticación (Raspberry Pi → API): secret por nodo vía header `Authorization: Bearer <secret>` (enrolamiento con identidad Ed25519)

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
├── internal/
│   ├── controller/
│   │   ├── auth/                                    # POST /api/v1/auth/login|google|logout
│   │   ├── consigna/                                # POST /api/v1/horno/consigna, GET /api/v1/horno/consigna/historial
│   │   ├── dispositivo/                             # POST /api/v1/dispositivos/ping, GET|PUT /api/v1/dispositivos, GET /api/v1/dispositivos/metricas
│   │   ├── devicetoken/                             # Middleware BeforeMux: auth Bearer de nodos Raspberry Pi
│   │   ├── dualauth/                                # Auth dual (Bearer de dispositivo o cookie JWT)
│   │   ├── horno/                                   # GET /api/v1/horno, POST /api/v1/horno/temperatura
│   │   ├── lote_sector/                             # GET /api/v1/sectores|/dispositivos/sector, POST /api/v1/lotes/inicio|{id}/eventos|{id}/cierre, GET /api/v1/lotes|/lotes/abierto
│   │   ├── parametros_producto/                     # Handler GET/POST/PUT /api/v1/parametros-producto
│   │   ├── producto/                                # GET /api/v1/productos
│   │   ├── registro/                                # POST|GET /api/v1/registration-requests (+ approve/reject)
│   │   ├── sse/                                     # Handler SSE genérico
│   │   └── user/                                    # GET|POST /api/v1/admin/usuarios, PATCH .../usuarios/{id}
│   ├── domain/
│   │   ├── alerta/                                  # Modelo Alerta
│   │   ├── consigna/                                # Modelo Consigna (auditoría) + ConsignaManualRequest
│   │   ├── dispositivo/                             # Modelo Dispositivo + MetricaDispositivo + EstadoDispositivo + DeviceRead
│   │   ├── horno/                                   # Modelo Horno
│   │   ├── lote_sector/                             # Modelo Lote + Evento + interfaces Repository/Service
│   │   ├── parametros_producto/                     # Modelo ParametroProducto
│   │   ├── producto/                                # Modelo Producto (catálogo maestro)
│   │   ├── registro/                                # Modelo de solicitudes de enrolamiento
│   │   ├── sector/                                  # Modelo Sector + contrato Repository
│   │   └── user/                                    # Entidad User, constantes de roles, errores centinela
│   ├── provider/
│   │   ├── database/                                # NewPostgresPool, repos en memoria, StateStore
│   │   ├── google/                                  # Implementa user.GoogleVerifier vía google/api/idtoken
│   │   ├── oven_controller/                         # Cliente simulado del controlador del horno (PLC)
│   │   └── yolo_client/                             # Cliente HTTP para servicio YOLO de inspección visual
│   ├── repository/
│   │   ├── lote_sector_repo.go                      # PostgreSQL — ciclo de lote por sector
│   │   ├── sector_repo.go                           # PostgreSQL — sectores y relación con dispositivos
│   │   ├── producto_repo.go                         # PostgreSQL — catálogo de productos
│   │   ├── parametros_producto_repo.go              # PostgreSQL — GET/CREATE/UPDATE parametros_producto
│   │   ├── dispositivo_repo.go                      # PostgreSQL — Dispositivos y métricas
│   │   ├── device_auth_repo.go                      # PostgreSQL — credenciales de nodos (secret_hash)
│   │   ├── registro_repo.go                         # PostgreSQL — solicitudes de enrolamiento
│   │   ├── consigna_repo.go                         # PostgreSQL — Historial de consignas
│   │   └── user_postgres_repository.go              # PostgreSQL — CRUD usuarios
│   └── service/
│       ├── auth/                                    # LoginWithGoogle, LoginWithCredentials, generateJWT
│       ├── consigna/                                # Despacho automático/manual de consigna
│       ├── dispositivo/                             # Lógica de ping, reaper y SSE
│       ├── horno/                                   # Lógica de umbrales térmicos y alertas
│       ├── lote_sector/                             # Ciclo de lote por sector (apertura, eventos, cierre, historial)
│       ├── parametros_producto/                     # Alta/consulta/actualización de parámetros
│       ├── producto/                                # Catálogo de productos
│       ├── registro/                                # Enrolamiento de dispositivos
│       └── user/                                    # ListUsers, CreateUser, UpdateUser
├── pkg/response/response.go                         # Envelope JSON estándar {success, message, data, errors}
├── database/schema.sql                              # DDL: productos + lotes_productivos + parametros_producto + dispositivos + metricas_dispositivo + historial_consignas + usuarios
├── Dockerfile                                       # Multi-stage build: golang:1.25-alpine → alpine
├── docker-compose.yml
├── .env.example                                     # Plantilla de variables
└── go.mod / go.sum
```

## Schema de base de datos (fuente de verdad: database/schema.sql)

- **productos**: Catálogo maestro.
- **lotes_productivos**: Registros transaccionales de horneadas.
- **parametros_producto**: Umbrales ideales y setpoints de cocción.
- **dispositivos** & **metricas_dispositivo**: Nodos Raspberry Pi y telemetría (CPU/RAM/Temp).
- **historial_consignas**: Auditoría de consignas enviadas al horno (automáticas/manuales).
- **usuarios**: Autenticación corporativa (Email, Rol, Password Hash).

Seed de desarrollo: `admin@fermar.com.ar`, `supervisor@fermar.com.ar`, `operario@fermar.com.ar`. Contraseña seed: `password123`.

## Endpoints implementados

| Método | Ruta | Auth | Rol mínimo |
|---|---|---|---|
| GET | /health | — | — |
| POST | /api/v1/auth/login | — | — |
| POST | /api/v1/auth/google | — | — |
| POST | /api/v1/auth/logout | — | — |
| GET | /api/v1/admin/usuarios | JWT cookie | Administrador |
| POST | /api/v1/admin/usuarios | JWT cookie | Administrador |
| PATCH | /api/v1/admin/usuarios/{id} | JWT cookie | Administrador |
| GET | /api/v1/horno | JWT cookie | cualquier rol |
| POST | /api/v1/horno/temperatura | JWT cookie | Supervisor, Admin |
| GET | /api/v1/productos | Bearer dispositivo o JWT cookie | — |
| GET | /api/v1/dispositivos/sector | Bearer dispositivo | — |
| GET | /api/v1/sectores | JWT cookie | cualquier rol |
| POST | /api/v1/lotes/inicio | Bearer dispositivo | — |
| GET | /api/v1/lotes/abierto | Bearer dispositivo o JWT cookie | — |
| POST | /api/v1/lotes/{id}/eventos | Bearer dispositivo | — |
| POST | /api/v1/lotes/{id}/cierre | Bearer dispositivo | — |
| GET | /api/v1/lotes | Bearer dispositivo o JWT cookie | — |
| GET | /api/v1/lotes/events | JWT cookie | cualquier rol |
| GET/POST/PUT | /api/v1/parametros-producto | JWT cookie | Supervisor, Admin (escrituras) |
| POST | /api/v1/dispositivos/ping | Bearer dispositivo | — |
| GET | /api/v1/dispositivos | JWT cookie | cualquier rol |
| PUT | /api/v1/dispositivos | JWT cookie | Supervisor, Admin |
| GET | /api/v1/dispositivos/metricas | JWT cookie | cualquier rol |
| GET | /api/v1/dispositivos/events | JWT cookie | cualquier rol |
| POST | /api/v1/horno/consigna | JWT cookie | Operario, Supervisor, Admin |
| GET | /api/v1/horno/consigna/historial | JWT cookie | cualquier rol |
| GET | /api/v1/horno/events | JWT cookie | cualquier rol |
| POST/GET | /api/v1/registration-requests | — (POST/GET por id) · JWT Supervisor/Admin (listado y approve/reject) | — |

Nota: `POST /api/v1/lotes/inicio` ya **no dispara consigna automática**; solo abre/persiste el lote abierto del sector. La consigna automática (SCA-142) dejó de despacharse desde ese endpoint.

## Decisiones de arquitectura tomadas

- **JWT en cookie HttpOnly**: `session_token` — HttpOnly+Secure+SameSite=Strict.
- **RBAC en middleware**: `RequireRole([]string{...})` se encadena después de `JWTMiddleware`. Roles: `Administrador > Supervisor > Operario`.
- **Bcrypt para contraseñas locales**: `DefaultCost`. Usuarios Google-only tienen `password_hash = NULL`.
- **Estado de dispositivos en caché en memoria + historial en PostgreSQL**: `MemoryDispositivoStateStore` para lecturas rápidas; historial append-only en `metricas_dispositivo`.
- **Detección de offline con reaper en background**: `StartReaper` corre cada `DISPOSITIVO_REAPER_INTERVAL`.
- **Mecanismo de consigna compartido**: `ConsignaService` gestiona tanto consignas automáticas (SCA-142) como manuales (SCA-320).

## Variables de entorno

| Variable | Descripción |
|---|---|
| `DATABASE_URL` | Conexión a PostgreSQL (Aiven o Docker local) |
| `JWT_SECRET` | Clave de firma del JWT. Mínimo 32 chars. |
| `GOOGLE_CLIENT_ID` | Client ID de Google Cloud Console |
| `LOTE_CIERRE_MIN_INACTIVIDAD_SEGUNDOS` | Inactividad mínima (segundos) para aceptar un cierre de lote (default 0 = no enforce) |
| `PORT` | Puerto HTTP (default 8080) |
| `DISPOSITIVO_REAPER_INTERVAL` | Intervalo del reaper (default 5s) |
| `DISPOSITIVO_OFFLINE_THRESHOLD` | Umbral de inactividad (default 25s) |
| `TEST_DATABASE_URL` | Opcional — activa tests de integración con DB real |
