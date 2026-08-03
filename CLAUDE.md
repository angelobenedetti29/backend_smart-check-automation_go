# Smart-Check Automation — Backend (Go)

## Contexto del proyecto
Sistema de supervisión inteligente para hornos túnel (Fermar S.A.).
Backend en Go que expone una API REST consumida por un frontend Next.js.
Los datos son enviados por nodos Raspberry Pi via HTTP.

## Stack
- Lenguaje: Go 1.25
- Base de datos: PostgreSQL 16
- Infraestructura: Docker + Docker Compose
- Frontend: Next.js (React) — consumidor de esta API
- Autenticación (Raspberry Pi → API): API Key via header `X-API-Key`
- Autenticación (usuarios → frontend): Google OAuth 2.0 (pendiente)

## Convenciones del equipo
- Nombres de structs: PascalCase
- Nombres de variables y funciones: camelCase
- Comentario breve por cada función
- Un archivo por componente/entidad
- Patrón: Handler → Service → Repository → PostgreSQL

## Convención de commits

Formato obligatorio:

```
<tipo>(SMA19-XXX): <descripción en imperativo, minúscula, sin punto final>
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

El identificador `SMA19-XXX` es obligatorio — permite la vinculación automática con Jira.

## Ramas

```
main        ← versión estable y aprobada (solo merge desde staging)
staging     ← versión lista para pruebas finales antes de producción
develop     ← línea principal de integración
feature/    ← nueva funcionalidad (se crea desde develop)
bugfix/     ← corrección de bug (se crea desde develop)
```

Nombrado: `feature/SMA19-042-clasificacion-imagenes`, `bugfix/SMA19-067-validacion-jwt`

**Prohibido** hacer commit directo a `main`, `staging` o `develop`.

## Estructura de carpetas actual
```
test_backend_go/
├── cmd/server/main.go                          # Entry point, wiring de dependencias
├── internal/
│   ├── controller/
│   │   ├── dispositivo/                        # Handler POST /api/v1/dispositivos/ping, GET /api/v1/dispositivos, GET /api/v1/dispositivos/metricas
│   │   ├── horno/                              # Handler GET /api/v1/horno, POST /api/v1/horno/temperatura
│   │   ├── lote/                               # Handler POST /api/v1/lotes
│   │   ├── lote_productivo/                    # Handler GET /api/v1/lotes-productivos
│   │   └── parametros_producto/                # Handler GET/POST/PUT /api/v1/parametros-producto
│   ├── domain/
│   │   ├── alerta/                             # Modelo Alerta
│   │   ├── dispositivo/                        # Modelo Dispositivo + MetricaDispositivo + EstadoDispositivo + PingRequest + validaciones + Repository/StateStore/Service interfaces
│   │   ├── horno/                              # Modelo Horno
│   │   ├── lote/                               # Modelo Lote + LoteRequest + validaciones + Repository/Service interfaces
│   │   ├── lote_productivo/                    # Modelo LoteProductivo + PaginatedResult + Repository/Service interfaces
│   │   └── parametros_producto/                # Modelo ParametroProducto + Request + validaciones + Repository/Service interfaces
│   ├── provider/
│   │   ├── database/
│   │   │   ├── postgres.go                     # NewPostgresPool — conexión pgxpool
│   │   │   ├── postgres_repo.go                # Repo en memoria para Horno y Alerta (no toca DB)
│   │   │   ├── lote_productivo_repo.go         # Repo en memoria para lotes (solo para tests unitarios)
│   │   │   └── dispositivo_state_store.go      # StateStore en memoria (estado actual online/offline, sin tocar DB)
│   │   └── yolo_client/client.go               # Cliente simulado para inspección visual YOLO
│   ├── repository/
│   │   ├── postgres_repo.go                    # Repo real PostgreSQL — CREATE lote
│   │   ├── lote_productivo_repo.go             # Repo real PostgreSQL — GET lotes (JOIN con productos)
│   │   ├── parametros_producto_repo.go         # Repo real PostgreSQL — GET/CREATE/UPDATE parametros_producto (JOIN con productos)
│   │   └── dispositivo_repo.go                 # Repo real PostgreSQL — INSERT metricas, GET catálogo de dispositivos, historial paginado
│   └── service/
│       ├── dispositivo/                        # Lógica de ping, estado online/offline, reaper, emisión SSE
│       ├── horno/                              # Lógica de umbrales térmicos y alertas
│       ├── lote_productivo/                    # Lógica de paginación (límites, defaults)
│       └── parametros_producto/                # Alta/consulta/actualización de parámetros por producto
├── pkg/response/response.go                    # Envelope JSON estándar {success, message, data, errors}
├── database/schema.sql                         # DDL: tablas productos + lotes_productivos + parametros_producto + dispositivos + metricas_dispositivo, constraints, seed
├── Dockerfile                                  # Multi-stage build: golang:1.25-alpine → alpine:3.19
├── docker-compose.yml                          # Servicios: db (PostgreSQL 16) + app (Go)
├── .env                                        # Variables de entorno locales (NO commitear)
├── .env.example                                # Plantilla de variables de entorno
└── go.mod / go.sum
```

## Schema de base de datos (fuente de verdad: database/schema.sql)

```sql
-- Catálogo de productos
CREATE TABLE productos (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    nombre     VARCHAR(100) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Registro de lotes productivos
CREATE TABLE lotes_productivos (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    producto_id       UUID NOT NULL REFERENCES productos(id),
    turno             VARCHAR(10) NOT NULL CHECK (turno IN ('mañana', 'tarde', 'noche')),
    inicio_at         TIMESTAMPTZ NOT NULL,
    fin_at            TIMESTAMPTZ NOT NULL,
    total_unidades    INTEGER NOT NULL,
    correctos         INTEGER NOT NULL,
    quemados          INTEGER NOT NULL,
    crudas            INTEGER,
    correctos_kg      NUMERIC(10,2) NOT NULL,
    quemados_kg       NUMERIC(10,2) NOT NULL,
    crudos_kg         NUMERIC(10,2),
    temp_horno_1      NUMERIC(6,2),
    temp_comb_horno_1 NUMERIC(6,2),
    temp_horno_2      NUMERIC(6,2),
    temp_comb_horno_2 NUMERIC(6,2),
    velocidad_cinta   NUMERIC(6,2),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Seed: único producto disponible actualmente
-- id: a1b2c3d4-5678-90ab-cdef-1234567890ab → "Tostada Integral"

-- Parámetros y umbrales de control ideales de horneado por producto (ABM del Supervisor)
CREATE TABLE parametros_producto (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    producto_id             UUID NOT NULL UNIQUE REFERENCES productos(id),
    peso_referencia_kg      NUMERIC(10,3) NOT NULL CHECK (peso_referencia_kg > 0),
    tolerancia_peso_pct     NUMERIC(5,2)  NOT NULL CHECK (tolerancia_peso_pct >= 0),
    dimension_base_cm       NUMERIC(6,2)  NOT NULL CHECK (dimension_base_cm > 0),
    tolerancia_dimension_cm NUMERIC(6,2)  NOT NULL CHECK (tolerancia_dimension_cm >= 0),
    temp_min                NUMERIC(6,2)  NOT NULL,
    temp_max                NUMERIC(6,2)  NOT NULL CHECK (temp_max > temp_min),
    velocidad_cinta_min     NUMERIC(6,2)  NOT NULL,
    velocidad_cinta_max     NUMERIC(6,2)  NOT NULL CHECK (velocidad_cinta_max > velocidad_cinta_min),
    activo                  BOOLEAN NOT NULL DEFAULT true,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Seed: valores por defecto para "Tostada Integral" (temp_min=160, temp_max=180, etc.)

-- Catálogo de dispositivos Raspberry Pi
CREATE TABLE dispositivos (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    nombre     VARCHAR(100) NOT NULL,
    ubicacion  VARCHAR(100),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Historial append-only de telemetría por dispositivo
CREATE TABLE metricas_dispositivo (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dispositivo_id        UUID NOT NULL REFERENCES dispositivos(id) ON DELETE CASCADE,
    cpu_pct               NUMERIC(5,2)  NOT NULL CHECK (cpu_pct BETWEEN 0 AND 100),
    mem_ram_disponible_mb NUMERIC(10,2) NOT NULL CHECK (mem_ram_disponible_mb >= 0),
    temp_chip             NUMERIC(6,2)  NOT NULL,
    received_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_metricas_dispositivo_disp_received
    ON metricas_dispositivo (dispositivo_id, received_at DESC);
-- Seed: "Raspberry Pi Horno 1" → id: b1c2d3e4-5678-90ab-cdef-1234567890ab
```

**Importante:** `lotes_productivos` NO tiene columna `producto_nombre`.
El nombre se obtiene via JOIN con `productos` en el GET. Usar ese UUID en los POSTs de prueba.
`crudas` y `crudos_kg` son nullable. La validación exige `correctos + quemados + crudas <= total_unidades`.

`parametros_producto.producto_id` es `UNIQUE`: hay un único set de parámetros vigente por producto (1:1). No tiene columna `producto_nombre` — igual que `lotes_productivos`, se resuelve con JOIN. El "M" del ABM (PUT) hace `UPDATE` sobre esa fila; no se versiona historial de cambios.

## Decisiones de arquitectura tomadas

- **Repos en memoria vs repos reales**: `internal/provider/database/` tiene repos en memoria usados por los tests unitarios de horno. Los repos reales contra PostgreSQL están en `internal/repository/` y son los que usa `main.go` en producción.
- **`producto_nombre` en el GET**: se resuelve con JOIN (`lotes_productivos lp JOIN productos pr ON lp.producto_id = pr.id`), no está desnormalizado en la tabla de lotes.
- **Docker como infraestructura del MVP**: el proyecto corre íntegramente con `docker compose up --build`. No se requiere Go ni PostgreSQL instalados localmente.
- **Horno usa repo en memoria**: el dominio de horno/alerta aún no tiene persistencia real en PostgreSQL. El repo es simulado con datos seed en memoria.
- **`parametros_producto` sin autenticación (por ahora)**: GET/POST/PUT quedan abiertos porque el login de usuarios (Google OAuth) todavía no existe; no se reusa `X-API-Key` (pensada para Raspberry Pi) para este caso de uso. Hay un TODO en el handler para restringir por rol Supervisor cuando OAuth esté implementado.
- **PUT de `parametros_producto` identifica por body, no por path param**: un solo endpoint (`/api/v1/parametros-producto`) despacha GET/POST/PUT por `r.Method` dentro del handler, igual que el resto de las rutas del proyecto (sin wildcards de ruteo). El `productoId` va en el JSON del body, no en la URL.
- **Estado de dispositivos en caché en memoria + historial en PostgreSQL**: el estado actual (última métrica + `last_seen`) vive en `MemoryDispositivoStateStore` (map + RWMutex) para lecturas rápidas sin tocar DB; el historial append-only se persiste en `metricas_dispositivo` de forma fire-and-forget (el estado online no depende de que la DB esté disponible). Al arrancar, el store se hidrata con `GetDispositivosConUltimaMetrica` (DISTINCT ON): recupera el catálogo + la última métrica por dispositivo de la DB y calcula el estado según antigüedad — así un dispositivo vivo antes de un restart vuelve `online`, y uno caído conserva su última métrica/last_seen.
- **Detección de offline con reaper en background**: una goroutine (`StartReaper`) corre cada `DISPOSITIVO_REAPER_INTERVAL` (default 5s) y marca como `offline` a los dispositivos cuyo `last_seen` sea ≥ `DISPOSITIVO_OFFLINE_THRESHOLD` (default 25s). Se detiene limpiamente con `rootCancel()` en el graceful shutdown.
- **Dos brokers SSE separados**: un broker dedicado (`dispositivoSSEBroker`) para telemetría de dispositivos evita ruido cruzado con los eventos de lotes. El `SSEHandler` es genérico (solo subscribe al broker), por eso se reutiliza la misma clase en dos rutas distintas.
- **Eventos SSE de dispositivos**: `dispositivo.metric` se emite en cada ping (cada ~10s) con la última métrica; `dispositivo.state` se emite solo ante una transición online↔offline (detectada en el ping para offline→online y en el reaper para online→offline).
- **`POST /api/v1/dispositivos/ping` autenticado con `X-API-Key`**: reusa el `API_KEY_SECRET` compartido (mismo patrón que `/api/v1/lotes`). Los GET de consulta quedan abiertos porque el login de usuarios (Google OAuth) todavía no existe.

## Endpoints implementados

| Método | Ruta | Auth | Estado |
|---|---|---|---|
| GET | /health | — | ✅ implementado |
| GET | /api/v1/horno | — | ✅ implementado (repo en memoria) |
| POST | /api/v1/horno/temperatura | — | ✅ implementado (repo en memoria) |
| POST | /api/v1/lotes | X-API-Key | ✅ implementado (PostgreSQL real) |
| GET | /api/v1/lotes-productivos | — | ✅ implementado (PostgreSQL real) |
| GET | /api/v1/parametros-producto | — | ✅ implementado (PostgreSQL real) — lista todos los sets de parámetros |
| POST | /api/v1/parametros-producto | — | ✅ implementado (PostgreSQL real) — alta, 409 si el producto ya tiene parámetros, 422 si el producto no existe |
| PUT | /api/v1/parametros-producto | — | ✅ implementado (PostgreSQL real) — modificación por `productoId` en el body, 404 si no existe |
| POST | /api/v1/dispositivos/ping | X-API-Key | ✅ implementado (PostgreSQL real + caché en memoria) — recibe CPU/RAM/temp cada ~10s, actualiza estado online/offline y emite SSE |
| GET | /api/v1/dispositivos | — | ✅ implementado (caché en memoria) — estado actual online/offline de todos los dispositivos |
| GET | /api/v1/dispositivos/metricas | — | ✅ implementado (PostgreSQL real) — historial paginado por `dispositivoId` (query param) |
| GET | /api/v1/dispositivos/events | — | ✅ implementado — SSE de telemetría: `dispositivo.metric` (cada ping) y `dispositivo.state` (transiciones) |

## Estado actual de tareas

### Completadas
- SCA-60: Persistencia de lotes productivos (POST /api/v1/lotes → PostgreSQL)
- SCA-61: Consulta de lotes productivos (GET /api/v1/lotes-productivos → PostgreSQL con paginación)
- SCA-83: Service GetAll con paginación (defaults: page=1, pageSize=10, max=100)
- SCA-84: Handler HTTP GET /lotes-productivos
- Infraestructura Docker completa (Dockerfile multi-stage + docker-compose con healthcheck)
- SCA-115: Modelo de datos + migración de `parametros_producto` (peso, tolerancias, rangos de temperatura y velocidad de cinta por producto)
- SCA-115: CRUD de parámetros por producto (GET/POST/PUT /api/v1/parametros-producto) con validaciones estrictas en servidor
- SCA-172: Endpoint POST /api/v1/dispositivos/ping (X-API-Key) — recibe CPU/RAM/temp cada ~10s, persiste historial, calcula estado online/offline con reaper en background y emite SSE (`dispositivo.metric` + `dispositivo.state`)

### Pendientes
- SCA-86: Tests para lote_productivo (handler + service)
- Persistencia real de Horno y Alerta en PostgreSQL (actualmente en memoria)
- Autenticación Google OAuth 2.0 para el frontend (y, con eso, restringir `parametros_producto` a rol Supervisor)

## Variables de entorno

| Variable | Descripción |
|---|---|
| `DATABASE_URL` | Conexión a PostgreSQL. En Docker la define el compose internamente. |
| `API_KEY_SECRET` | Clave que deben enviar las Raspberry Pi en header `X-API-Key` |
| `PORT` | Puerto HTTP (default 8080) |
| `DISPOSITIVO_REAPER_INTERVAL` | Intervalo del reaper que detecta dispositivos offline (default 5s) |
| `DISPOSITIVO_OFFLINE_THRESHOLD` | Umbral de inactividad para considerar un dispositivo offline (default 25s) |
| `POSTGRES_USER/PASSWORD/DB` | Solo usadas por Docker Compose para crear el contenedor de DB |
| `TEST_DATABASE_URL` | Opcional — activa tests de integración con DB real |
