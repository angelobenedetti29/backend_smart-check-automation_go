# Deploy — Smart-Check Automation Backend

## Infraestructura actual

| Componente | Servicio | URL / Referencia |
|---|---|---|
| Backend | Render (Docker, rama `staging`) | https://backend-smart-check-automation-go.onrender.com |
| Base de datos | Supabase (PostgreSQL 16, región São Paulo) | Session pooler, puerto 5432 |

El backend corre en un contenedor Docker buildeado desde el `Dockerfile` del repo.
La base de datos tiene aplicado el schema de `database/schema.sql` con el seed inicial de productos.

---

## Variables de entorno configuradas en Render

| Variable | Descripción |
|---|---|
| `DATABASE_URL` | Connection string al Session pooler de Supabase (puerto 5432, sslmode=require) |
| `API_KEY_SECRET` | Clave secreta para autenticar requests de las Raspberry Pi |
| `PORT` | `8080` |

---

## Endpoints disponibles



### GET /api/v1/lotes-productivos

Retorna los lotes productivos registrados, paginados. Soporta `?page=1&pageSize=10`.

```bash
curl https://backend-smart-check-automation-go.onrender.com/api/v1/lotes-productivos
```

```json
{
  "success": true,
  "message": "Lotes productivos obtenidos exitosamente",
  "data": [
    {
      "id": "b4ae1a81-6d47-4f7c-bcaf-fc8ae710118f",
      "productoId": "a1b2c3d4-5678-90ab-cdef-1234567890ab",
      "productoNombre": "Tostada Integral",
      "turno": "tarde",
      "inicioAt": "2026-06-25T14:00:00Z",
      "finAt": "2026-06-25T18:00:00Z",
      "totalUnidades": 200,
      "correctos": 180,
      "quemados": 20,
      "crudas": null,
      "correctosKg": 90,
      "quemadosKg": 10,
      "crudosKg": null,
      "tempHorno1": 215,
      "tempCombHorno1": 310,
      "tempHorno2": 212.5,
      "tempCombHorno2": 308,
      "velocidadCinta": 0.9,
      "createdAt": "2026-06-25T14:00:01Z",
      "updatedAt": "2026-06-25T18:00:05Z"
    }
  ],
  "total": 1,
  "page": 1,
  "pageSize": 10
}
```

---

### POST /api/v1/lotes

Registra un nuevo lote productivo. Requiere el header `X-API-Key` con el valor configurado en `API_KEY_SECRET`.

Los campos del body van en **camelCase**. `correctos + quemados + crudas` (si se envía) no puede superar `totalUnidades`.

El único `productoId` disponible en el seed actual es `a1b2c3d4-5678-90ab-cdef-1234567890ab` ("Tostada Integral").

```bash
curl -X POST https://backend-smart-check-automation-go.onrender.com/api/v1/lotes \
  -H "Content-Type: application/json" \
  -H "X-API-Key: <API_KEY_SECRET>" \
  -d '{
    "productoId": "a1b2c3d4-5678-90ab-cdef-1234567890ab",
    "turno": "tarde",
    "inicioAt": "2026-06-25T14:00:00Z",
    "finAt": "2026-06-25T18:00:00Z",
    "totalUnidades": 200,
    "correctos": 180,
    "quemados": 20,
    "crudas": null,
    "correctosKg": 90.00,
    "quemadosKg": 10.00,
    "crudosKg": null,
    "tempHorno1": 215.00,
    "tempCombHorno1": 310.00,
    "tempHorno2": 212.50,
    "tempCombHorno2": 308.00,
    "velocidadCinta": 0.90
  }'
```

```json
{
  "success": true,
  "message": "Lote creado exitosamente",
  "data": {
    "id": "b4ae1a81-6d47-4f7c-bcaf-fc8ae710118f",
    "producto_id": "a1b2c3d4-5678-90ab-cdef-1234567890ab",
    "turno": "tarde",
    "inicio_at": "2026-06-25T14:00:00Z",
    "fin_at": "2026-06-25T18:00:00Z",
    "total_unidades": 200,
    "correctos": 180,
    "quemados": 20,
    "crudas": null,
    "correctos_kg": 90,
    "quemados_kg": 10,
    "crudos_kg": null,
    "temp_horno_1": 215,
    "temp_comb_horno_1": 310,
    "temp_horno_2": 212.5,
    "temp_comb_horno_2": 308,
    "velocidad_cinta": 0.9
  }
}
```

---

### GET /api/v1/parametros-producto

Lista todos los sets de parámetros y umbrales de control configurados por producto (panel de configuración del Supervisor). No requiere `X-API-Key`.

```bash
curl https://backend-smart-check-automation-go.onrender.com/api/v1/parametros-producto
```

```json
{
  "success": true,
  "message": "Parámetros por producto obtenidos exitosamente",
  "data": [
    {
      "id": "de91d67e-a9e1-4b69-a08a-46a965a4b728",
      "productoId": "a1b2c3d4-5678-90ab-cdef-1234567890ab",
      "productoNombre": "Tostada Integral",
      "pesoReferenciaKg": 0.03,
      "toleranciaPesoPct": 10,
      "dimensionBaseCm": 8,
      "toleranciaDimensionCm": 0.5,
      "tempMin": 160,
      "tempMax": 180,
      "velocidadCintaMin": 0.1,
      "velocidadCintaMax": 0.3,
      "activo": true,
      "createdAt": "2026-07-23T23:10:05Z",
      "updatedAt": "2026-07-23T23:10:05Z"
    }
  ]
}
```

---

### POST /api/v1/parametros-producto

Alta de parámetros para un producto que todavía no tiene un set cargado. Sin autenticación por ahora (OAuth de usuarios pendiente).

```bash
curl -X POST https://backend-smart-check-automation-go.onrender.com/api/v1/parametros-producto \
  -H "Content-Type: application/json" \
  -d '{
    "productoId": "a1b2c3d4-5678-90ab-cdef-1234567890ab",
    "pesoReferenciaKg": 0.03,
    "toleranciaPesoPct": 10,
    "dimensionBaseCm": 8,
    "toleranciaDimensionCm": 0.5,
    "tempMin": 160,
    "tempMax": 180,
    "velocidadCintaMin": 0.1,
    "velocidadCintaMax": 0.3
  }'
```

Devuelve `409` si el producto ya tiene parámetros cargados, `422` si `productoId` no existe en el catálogo o si algún rango/valor numérico es inválido (ver reglas en el `README.md`).

---

### PUT /api/v1/parametros-producto

Modifica los rangos de un producto existente. El `productoId` va en el body (no hay path param); identifica el registro a actualizar y sus nuevas reglas se aplican de inmediato a los próximos lotes de ese producto.

```bash
curl -X PUT https://backend-smart-check-automation-go.onrender.com/api/v1/parametros-producto \
  -H "Content-Type: application/json" \
  -d '{
    "productoId": "a1b2c3d4-5678-90ab-cdef-1234567890ab",
    "pesoReferenciaKg": 0.03,
    "toleranciaPesoPct": 10,
    "dimensionBaseCm": 8,
    "toleranciaDimensionCm": 0.5,
    "tempMin": 160,
    "tempMax": 180,
    "velocidadCintaMin": 0.1,
    "velocidadCintaMax": 0.3
  }'
```

Devuelve `404` si no existe un set de parámetros para ese `productoId`.

---

## Base de datos

Schema aplicado: `database/schema.sql`

Tablas:
- `productos` — catálogo de productos panificados
- `lotes_productivos` — registro transaccional de lotes horneados
- `parametros_producto` — umbrales de control ideales de horneado por producto (peso, dimensión, temperatura, velocidad de cinta)

Seed cargado:

| id | nombre |
|---|---|
| `a1b2c3d4-5678-90ab-cdef-1234567890ab` | Tostada Integral |

`parametros_producto` viene sembrado con un set de valores por defecto para ese mismo producto (`tempMin=160`, `tempMax=180`, etc. — ver `database/schema.sql`).
