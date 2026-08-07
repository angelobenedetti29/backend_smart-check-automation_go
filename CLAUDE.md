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
│   │   ├── auth/
│   │   │   ├── auth_handler.go                      # POST /api/v1/auth/login|google|logout
│   │   │   ├── auth_handler_test.go
│   │   │   ├── middleware.go                        # JWTMiddleware + RequireRole (RBAC)
│   │   │   └── middleware_test.go
│   │   ├── horno/                                   # GET /api/v1/horno, POST /api/v1/horno/temperatura
│   │   ├── lote/                                    # POST /api/v1/lotes
│   │   ├── lote_productivo/                         # GET /api/v1/lotes-productivos
│   │   └── user/
│   │       └── user_handler.go                      # GET|POST /api/v1/admin/usuarios, PATCH .../usuarios/{id}
│   ├── domain/
│   │   ├── alerta/                                  # Modelo Alerta
│   │   ├── horno/                                   # Modelo Horno
│   │   ├── lote/                                    # Modelo Lote + interfaces Repository/Service
│   │   ├── lote_productivo/                         # Modelo LoteProductivo + PaginatedResult
│   │   └── user/
│   │       └── user.go                              # Entidad User, constantes de roles, errores centinela
│   ├── provider/
│   │   ├── database/                                # NewPostgresPool + repos en memoria (horno/alerta)
│   │   ├── google/
│   │   │   └── oauth_provider.go                    # Implementa user.GoogleVerifier vía google/api/idtoken
│   │   └── yolo_client/                             # Cliente HTTP para servicio YOLO de inspección visual
│   ├── repository/
│   │   ├── postgres_repo.go                         # PostgreSQL — CREATE lote
│   │   ├── lote_productivo_repo.go                  # PostgreSQL — GET lotes (JOIN productos)
│   │   └── user_postgres_repository.go              # PostgreSQL — CRUD usuarios
│   └── service/
│       ├── auth/
│       │   ├── auth_service.go                      # LoginWithGoogle, LoginWithCredentials, generateJWT
│       │   └── auth_service_test.go
│       ├── horno/                                   # Lógica de umbrales térmicos y alertas
│       ├── lote_productivo/                         # Lógica de paginación
│       └── user/
│           ├── user_service.go                      # ListUsers, CreateUser, UpdateUser (con bcrypt)
│           └── user_service_test.go
├── pkg/response/response.go                         # Envelope JSON estándar {success, message, data, errors}
├── database/schema.sql                              # DDL: productos + lotes_productivos + usuarios + seeds
├── Dockerfile                                       # Multi-stage build: golang:1.25-alpine → alpine
├── docker-compose.yml
├── .env.example                                     # Plantilla de variables (copiar como .env, NO commitear .env)
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
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    producto_id UUID NOT NULL REFERENCES productos(id),
    turno VARCHAR(10) NOT NULL CHECK (turno IN ('mañana', 'tarde', 'noche')),
    -- ... (ver schema.sql completo)
);

-- Usuarios corporativos autorizados (autenticación + RBAC)
CREATE TABLE usuarios (
    id            UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    email         VARCHAR(254) NOT NULL UNIQUE,
    nombre        VARCHAR(150) NOT NULL,
    rol           VARCHAR(20)  NOT NULL CHECK (rol IN ('Administrador', 'Supervisor', 'Operario')),
    password_hash VARCHAR(255),          -- nullable: si solo usa Google OAuth
    activo        BOOLEAN      NOT NULL DEFAULT true,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT now()
);
```

**Seed de desarrollo:** `admin@fermar.com.ar`, `supervisor@fermar.com.ar`, `operario@fermar.com.ar`
Contraseña seed: `password123` — **cambiar antes de producción**.

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
| GET | /api/v1/lotes-productivos | JWT cookie | cualquier rol |

## Decisiones de arquitectura

- **Repos en memoria vs repos reales**: `internal/provider/database/` tiene repos en memoria usados por horno/alerta (aún sin persistencia real). Los repos reales contra PostgreSQL están en `internal/repository/`.
- **JWT en cookie HttpOnly**: `session_token` — HttpOnly+Secure+SameSite=Strict. El frontend no accede al token desde JS.
- **RBAC en middleware**: `RequireRole([]string{...})` se encadena después de `JWTMiddleware`. Los roles son `Administrador > Supervisor > Operario`.
- **Bcrypt para contraseñas locales**: `DefaultCost`. Los usuarios Google-only tienen `password_hash = NULL`.
- **Errores centinela en domain/user**: usar `errors.Is()` en servicios y handlers para mapear a códigos HTTP correctos sin filtrar información.
- **Docker como infraestructura del MVP**: el proyecto corre con `docker compose up --build`.

## Variables de entorno

| Variable | Descripción |
|---|---|
| `DATABASE_URL` | Conexión a PostgreSQL (Aiven o Docker local) |
| `JWT_SECRET` | Clave de firma del JWT. Mínimo 32 chars. `openssl rand -hex 32` |
| `GOOGLE_CLIENT_ID` | Client ID de Google Cloud Console |
| `API_KEY_SECRET` | Clave para Raspberry Pi (`X-API-Key` header) |
| `PORT` | Puerto HTTP (default 8080) |
| `TEST_DATABASE_URL` | Solo para tests de integración. Nunca apuntar a producción. |

Ver `.env.example` para la plantilla completa.
