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
│   │   ├── horno/                              # Handler GET /api/v1/horno, POST /api/v1/horno/temperatura
│   │   ├── lote/                               # Handler POST /api/v1/lotes
│   │   └── lote_productivo/                    # Handler GET /api/v1/lotes-productivos
│   ├── domain/
│   │   ├── alerta/                             # Modelo Alerta
│   │   ├── horno/                              # Modelo Horno
│   │   ├── lote/                               # Modelo Lote + LoteRequest + validaciones + Repository/Service interfaces
│   │   └── lote_productivo/                    # Modelo LoteProductivo + PaginatedResult + Repository/Service interfaces
│   ├── provider/
│   │   ├── database/
│   │   │   ├── postgres.go                     # NewPostgresPool — conexión pgxpool
│   │   │   ├── postgres_repo.go                # Repo en memoria para Horno y Alerta (no toca DB)
│   │   │   └── lote_productivo_repo.go         # Repo en memoria para lotes (solo para tests unitarios)
│   │   └── yolo_client/client.go               # Cliente simulado para inspección visual YOLO
│   ├── repository/
│   │   ├── postgres_repo.go                    # Repo real PostgreSQL — CREATE lote
│   │   └── lote_productivo_repo.go             # Repo real PostgreSQL — GET lotes (JOIN con productos)
│   └── service/
│       ├── horno/                              # Lógica de umbrales térmicos y alertas
│       └── lote_productivo/                    # Lógica de paginación (límites, defaults)
├── pkg/response/response.go                    # Envelope JSON estándar {success, message, data, errors}
├── database/schema.sql                         # DDL: tablas productos + lotes_productivos, constraints, seed
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
    correctos_kg      NUMERIC(10,2) NOT NULL,
    quemados_kg       NUMERIC(10,2) NOT NULL,
    temp_horno_1      NUMERIC(6,2),
    temp_comb_horno_1 NUMERIC(6,2),
    temp_horno_2      NUMERIC(6,2),
    temp_comb_horno_2 NUMERIC(6,2),
    velocidad_horno   NUMERIC(6,2),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Seed: único producto disponible actualmente
-- id: a1b2c3d4-5678-90ab-cdef-1234567890ab → "Tostada Integral"
```

**Importante:** `lotes_productivos` NO tiene columna `producto_nombre`.
El nombre se obtiene via JOIN con `productos` en el GET. Usar ese UUID en los POSTs de prueba.

## Decisiones de arquitectura tomadas

- **Repos en memoria vs repos reales**: `internal/provider/database/` tiene repos en memoria usados por los tests unitarios de horno. Los repos reales contra PostgreSQL están en `internal/repository/` y son los que usa `main.go` en producción.
- **`producto_nombre` en el GET**: se resuelve con JOIN (`lotes_productivos lp JOIN productos pr ON lp.producto_id = pr.id`), no está desnormalizado en la tabla de lotes.
- **Docker como infraestructura del MVP**: el proyecto corre íntegramente con `docker compose up --build`. No se requiere Go ni PostgreSQL instalados localmente.
- **Horno usa repo en memoria**: el dominio de horno/alerta aún no tiene persistencia real en PostgreSQL. El repo es simulado con datos seed en memoria.

## Endpoints implementados

| Método | Ruta | Auth | Estado |
|---|---|---|---|
| GET | /health | — | ✅ implementado |
| GET | /api/v1/horno | — | ✅ implementado (repo en memoria) |
| POST | /api/v1/horno/temperatura | — | ✅ implementado (repo en memoria) |
| POST | /api/v1/lotes | X-API-Key | ✅ implementado (PostgreSQL real) |
| GET | /api/v1/lotes-productivos | — | ✅ implementado (PostgreSQL real) |

## Estado actual de tareas

### Completadas
- SCA-60: Persistencia de lotes productivos (POST /api/v1/lotes → PostgreSQL)
- SCA-61: Consulta de lotes productivos (GET /api/v1/lotes-productivos → PostgreSQL con paginación)
- SCA-83: Service GetAll con paginación (defaults: page=1, pageSize=10, max=100)
- SCA-84: Handler HTTP GET /lotes-productivos
- Infraestructura Docker completa (Dockerfile multi-stage + docker-compose con healthcheck)

### Pendientes
- SCA-86: Tests para lote_productivo (handler + service)
- Persistencia real de Horno y Alerta en PostgreSQL (actualmente en memoria)
- Autenticación Google OAuth 2.0 para el frontend

## Variables de entorno

| Variable | Descripción |
|---|---|
| `DATABASE_URL` | Conexión a PostgreSQL. En Docker la define el compose internamente. |
| `API_KEY_SECRET` | Clave que deben enviar las Raspberry Pi en header `X-API-Key` |
| `PORT` | Puerto HTTP (default 8080) |
| `POSTGRES_USER/PASSWORD/DB` | Solo usadas por Docker Compose para crear el contenedor de DB |
| `TEST_DATABASE_URL` | Opcional — activa tests de integración con DB real |
