# Deploy — Smart-Check Automation Backend

## Infraestructura actual

| Componente | Servicio | URL / Referencia |
|---|---|---|
| Backend (staging) | Render (Docker, rama `staging`) | `smartcheck-backend-staging` |
| Backend (producción) | Render (Docker, rama `main`) | `smartcheck-backend-prod` |
| Base de datos | Aiven PostgreSQL 16 | Session pooler, puerto 5432 |

Los servicios de Render están definidos en [`render.yaml`](render.yaml). El backend corre en
un contenedor Docker buildeado desde el `Dockerfile` del repo.
La base de datos tiene aplicado el schema de `database/schema.sql` con el seed inicial de productos.

---

## Variables de entorno configuradas en Render

| Variable | Descripción |
|---|---|
| `DATABASE_URL` | Connection string a Aiven PostgreSQL 16 (puerto 5432, sslmode=require) |
| `JWT_SECRET` | Clave de firma del JWT (mínimo 32 caracteres) |
| `GOOGLE_CLIENT_ID` | Client ID de Google Cloud Console |
| `FRONTEND_ORIGINS` | Orígenes del panel permitidos por CORS, separados por coma |
| `PORT` | `8080` |

Claves alineadas con [`render.yaml`](render.yaml): `DATABASE_URL`, `JWT_SECRET`,
`GOOGLE_CLIENT_ID` y `PORT`. `FRONTEND_ORIGINS` se agrega además para el CORS del panel.

---

## Endpoints disponibles

La referencia completa y actualizada de la API —con auth, rol mínimo, request/response y
códigos— está en [`docs/api-endpoints.md`](docs/api-endpoints.md). El flujo legado que antes
figuraba acá (`POST /api/v1/lotes` y el listado global de lotes) fue retirado:
`POST /api/v1/lotes` responde **405** y el ciclo actual se inicia con
`POST /api/v1/lotes/inicio`.

---

## Base de datos

Schema aplicado: `database/schema.sql`

Tablas:
- `productos` — catálogo de productos panificados
- `lotes_productivos` — registro transaccional de lotes horneados
- `parametros_producto` — umbrales de control ideales de horneado por producto (peso, dimensión, temperatura, velocidad de cinta)

Seed cargado (6 productos en `database/schema.sql`):

| id | nombre |
|---|---|
| `a1b2c3d4-5678-90ab-cdef-1234567890ab` | Tostada Integral |
| `b2c3d4e5-6789-01ab-cdef-234567890abc` | Pan Lactal |
| `c3d4e5f6-789a-12bc-def3-34567890abcd` | Pan Francés |
| `d4e5f6a7-89ab-23cd-ef34-4567890abcde` | Pan de Salvado |
| `e5f6a7b8-9abc-34de-f456-567890abcdef` | Medialunas |
| `f6a7b8c9-abcd-45ef-5678-67890abcdef1` | Pan Dulce |

`parametros_producto` viene sembrado con un set de valores por defecto para los 6 productos
(`tempMin`/`tempMax`, dimensiones, velocidades — ver `database/schema.sql`).
