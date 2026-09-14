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
- Autenticación (Raspberry Pi → API): API Key via header `X-API-Key`

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
│   │   ├── dispositivo/                             # POST /api/v1/dispositivos/ping, GET|POST /api/v1/dispositivos, GET /api/v1/dispositivos/metricas
│   │   ├── horno/                                   # GET /api/v1/horno, POST /api/v1/horno/temperatura
│   │   ├── lote/                                    # POST /api/v1/lotes, POST /api/v1/lotes/inicio
│   │   ├── lote_productivo/                         # GET /api/v1/lotes-productivos
│   │   ├── parametros_producto/                     # Handler GET/POST/PUT /api/v1/parametros-producto
│   │   ├── sse/                                     # Handler SSE genérico
│   │   └── user/                                    # GET|POST /api/v1/admin/usuarios, PATCH .../usuarios/{id}
│   ├── domain/
│   │   ├── alerta/                                  # Modelo Alerta
│   │   ├── consigna/                                # Modelo Consigna (auditoría) + ConsignaManualRequest
│   │   ├── dispositivo/                             # Modelo Dispositivo + MetricaDispositivo + EstadoDispositivo
│   │   ├── horno/                                   # Modelo Horno
│   │   ├── lote/                                    # Modelo Lote + interfaces Repository/Service
│   │   ├── lote_productivo/                         # Modelo LoteProductivo + PaginatedResult
│   │   ├── parametros_producto/                     # Modelo ParametroProducto
│   │   └── user/                                    # Entidad User, constantes de roles, errores centinela
│   ├── provider/
│   │   ├── database/                                # NewPostgresPool, repos en memoria, StateStore
│   │   ├── google/                                  # Implementa user.GoogleVerifier vía google/api/idtoken
│   │   ├── oven_controller/                         # Cliente simulado del controlador del horno (PLC) — ver "Driver del controlador físico del horno"
│   │   │   ├── modbus/                              # Cliente Modbus TCP genérico, sin dependencias externas (agnóstico de marca)
│   │   │   └── novus/                                # Driver NOVUS N1500 (temperatura) + variador (velocidad) vía Modbus TCP — implementado, NO conectado en main.go
│   │   └── yolo_client/                             # Cliente HTTP para servicio YOLO de inspección visual
│   ├── repository/
│   │   ├── postgres_repo.go                         # PostgreSQL — CREATE lote
│   │   ├── lote_productivo_repo.go                  # PostgreSQL — GET lotes (JOIN productos)
│   │   ├── parametros_producto_repo.go              # PostgreSQL — GET/CREATE/UPDATE parametros_producto
│   │   ├── dispositivo_repo.go                      # PostgreSQL — Dispositivos y métricas
│   │   ├── consigna_repo.go                         # PostgreSQL — Historial de consignas
│   │   └── user_postgres_repository.go              # PostgreSQL — CRUD usuarios
│   └── service/
│       ├── auth/                                    # LoginWithGoogle, LoginWithCredentials, generateJWT
│       ├── consigna/                                # Despacho automático/manual de consigna
│       ├── dispositivo/                             # Lógica de ping, reaper y SSE
│       ├── horno/                                   # Lógica de umbrales térmicos y alertas
│       ├── lote_productivo/                         # Lógica de paginación
│       ├── parametros_producto/                     # Alta/consulta/actualización de parámetros
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
| POST | /api/v1/lotes | X-API-Key | — |
| POST | /api/v1/lotes/inicio | X-API-Key | — |
| GET | /api/v1/lotes-productivos | JWT cookie | cualquier rol |
| GET/POST/PUT | /api/v1/parametros-producto | JWT cookie | Supervisor, Admin |
| POST | /api/v1/dispositivos/ping | X-API-Key | — |
| GET | /api/v1/dispositivos | JWT cookie | cualquier rol |
| POST | /api/v1/dispositivos | JWT cookie | Operario, Supervisor, Admin |
| GET | /api/v1/dispositivos/metricas | JWT cookie | cualquier rol |
| GET | /api/v1/dispositivos/events | JWT cookie | cualquier rol |
| POST | /api/v1/horno/consigna | JWT cookie | Operario, Supervisor, Admin |
| GET | /api/v1/horno/consigna/historial | JWT cookie | cualquier rol |
| GET | /api/v1/horno/events | JWT cookie | cualquier rol |

## Decisiones de arquitectura tomadas

- **JWT en cookie HttpOnly**: `session_token` — HttpOnly+Secure+SameSite=Strict.
- **RBAC en middleware**: `RequireRole([]string{...})` se encadena después de `JWTMiddleware`. Roles: `Administrador > Supervisor > Operario`.
- **Bcrypt para contraseñas locales**: `DefaultCost`. Usuarios Google-only tienen `password_hash = NULL`.
- **Estado de dispositivos en caché en memoria + historial en PostgreSQL**: `MemoryDispositivoStateStore` para lecturas rápidas; historial append-only en `metricas_dispositivo`.
- **Detección de offline con reaper en background**: `StartReaper` corre cada `DISPOSITIVO_REAPER_INTERVAL`.
- **Mecanismo de consigna compartido**: `ConsignaService` gestiona tanto consignas automáticas (SCA-142) como manuales (SCA-320).

## Driver del controlador físico del horno (SCA-142)

`ConsignaService` depende de la interfaz `consigna.OvenController` (`SendSetpoint(hornoID, temperatura, velocidad) DispatchResult`), no de una implementación concreta — el modelo de horno/PLC es un punto de extensión intercambiable, no un valor fijo.

- **`oven_controller.OvenControllerClient`** (`internal/provider/oven_controller/client.go`): simulador in-memory (latencia + ~5% de fallo aleatorio). Es el que está **wireado hoy en `cmd/server/main.go`** — el único que corre en dev/CI.
- **`novus.N1500Client`** (`internal/provider/oven_controller/novus/n1500_client.go`): driver real para hornos con indicador/controlador NOVUS N1500 (temperatura) + variador de frecuencia (velocidad de cinta), vía Modbus TCP — típicamente contra un gateway RS-485↔Ethernet, ya que el N1500 nativamente solo habla Modbus RTU por RS-485. **Está implementado y testeado, pero NO conectado en `main.go`** — no hay hardware real contra el cual confirmar direcciones de registro/config, así que cablearlo hoy significaría apuntar a datos inventados.
- **`modbus.Client`** (`internal/provider/oven_controller/modbus/client.go`): cliente Modbus TCP genérico (framing MBAP, sin dependencias externas), agnóstico de marca — lo usa `novus.N1500Client` pero cualquier otro driver de horno puede reutilizarlo.

**Para cambiar de modelo de horno** (NOVUS u otro): escribir un paquete hermano de `novus/` que implemente `SendSetpoint` con la misma firma, e inyectarlo en lugar de `oven_controller.NewOvenControllerClient(...)` en `cmd/server/main.go` (una sola línea) — ni `ConsignaService` ni el resto del sistema necesitan cambios.

**Antes de conectar `novus.N1500Client` a hardware real** falta confirmar contra la "Tabela de Registradores para Comunicação Serial" del firmware instalado: las direcciones de registro (`SetpointRegister`, `PVRegister`) y la escala (`Decimals`) en `novus.DeviceConfig` son placeholders de ejemplo, no valores verificados.

## Variables de entorno

| Variable | Descripción |
|---|---|
| `DATABASE_URL` | Conexión a PostgreSQL (Aiven o Docker local) |
| `JWT_SECRET` | Clave de firma del JWT. Mínimo 32 chars. |
| `GOOGLE_CLIENT_ID` | Client ID de Google Cloud Console |
| `API_KEY_SECRET` | Clave para Raspberry Pi (`X-API-Key` header) |
| `PORT` | Puerto HTTP (default 8080) |
| `DISPOSITIVO_REAPER_INTERVAL` | Intervalo del reaper (default 5s) |
| `DISPOSITIVO_OFFLINE_THRESHOLD` | Umbral de inactividad (default 25s) |
| `TEST_DATABASE_URL` | Opcional — activa tests de integración con DB real |
