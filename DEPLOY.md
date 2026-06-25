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
  "data": {
    "items": [
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
        "correctosKg": 90,
        "quemadosKg": 10,
        "tempHorno1": 215,
        "tempCombHorno1": 310,
        "tempHorno2": 212.5,
        "tempCombHorno2": 308,
        "velocidadHorno": 0.9
      }
    ],
    "total": 1,
    "page": 1,
    "pageSize": 10,
    "totalPages": 1
  }
}
```

---

### POST /api/v1/lotes

Registra un nuevo lote productivo. Requiere el header `X-API-Key` con el valor configurado en `API_KEY_SECRET`.

Los campos del body van en **camelCase**. `correctos + quemados` debe ser igual a `totalUnidades`.

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
    "correctosKg": 90.00,
    "quemadosKg": 10.00,
    "tempHorno1": 215.00,
    "tempCombHorno1": 310.00,
    "tempHorno2": 212.50,
    "tempCombHorno2": 308.00,
    "velocidadHorno": 0.90
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
    "correctos_kg": 90,
    "quemados_kg": 10,
    "temp_horno_1": 215,
    "temp_comb_horno_1": 310,
    "temp_horno_2": 212.5,
    "temp_comb_horno_2": 308,
    "velocidad_horno": 0.9
  }
}
```

---

## Base de datos

Schema aplicado: `database/schema.sql`

Tablas:
- `productos` — catálogo de productos panificados
- `lotes_productivos` — registro transaccional de lotes horneados

Seed cargado:

| id | nombre |
|---|---|
| `a1b2c3d4-5678-90ab-cdef-1234567890ab` | Tostada Integral |
