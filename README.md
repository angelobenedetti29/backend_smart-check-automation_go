# Smart-Check Automation — Backend

Backend REST en Go para el sistema de supervisión inteligente de hornos túnel de Fermar S.A.
Registra y consulta lotes productivos, monitorea temperaturas de hornos y recibe datos de nodos Raspberry Pi via HTTP.

## Stack

| Capa | Tecnología |
|---|---|
| Lenguaje | Go 1.25 |
| Base de datos | PostgreSQL 16 |
| Driver DB | pgx/v5 |
| Infraestructura local | Docker + Docker Compose |
| Autenticación | API Key (header `X-API-Key`) |

## Arquitectura

El proyecto sigue el patrón por capas: **Handler → Service → Repository → PostgreSQL**

```
cmd/server/main.go              # Entry point, wiring de dependencias
internal/
  controller/                   # Handlers HTTP (capa de presentación)
  service/                      # Lógica de negocio
  domain/                       # Modelos y contratos (interfaces)
  repository/                   # Repositorios reales contra PostgreSQL
  provider/database/            # Conexión al pool de PostgreSQL
database/schema.sql             # DDL: tablas, constraints, índices, seed
```

---

## Endpoints

### GET /health

Verifica conectividad con PostgreSQL.

```
200 OK    → {"status":"healthy"}
503       → {"status":"unhealthy","reason":"database unreachable"}
```

---

### GET /api/v1/lotes-productivos

Devuelve lotes productivos paginados, ordenados por `inicio_at` descendente.

**Query params:**
| Param | Default | Máximo |
|---|---|---|
| `page` | 1 | — |
| `pageSize` | 10 | 100 |

**Respuesta 200:**
```json
{
  "success": true,
  "message": "Lotes productivos obtenidos exitosamente",
  "data": [{ ... }],
  "total": 42,
  "page": 1,
  "pageSize": 10
}
```

---

### POST /api/v1/lotes

Registra un lote productivo enviado por la Raspberry Pi. Requiere `X-API-Key`.

**Headers requeridos:**
| Header | Valor |
|---|---|
| `Content-Type` | `application/json` |
| `X-API-Key` | Valor de `API_KEY_SECRET` en `.env` |

**Body:**
```json
{
  "productoId": "a1b2c3d4-5678-90ab-cdef-1234567890ab",
  "productoNombre": "Tostada Integral",
  "turno": "tarde",
  "inicioAt": "2026-06-12T14:00:00Z",
  "finAt": "2026-06-12T18:00:00Z",
  "totalUnidades": 500,
  "correctos": 480,
  "quemados": 20,
  "crudas": null,
  "correctosKg": 96.0,
  "quemadosKg": 4.0,
  "crudosKg": null,
  "tempHorno1": 188.0,
  "tempHorno2": 192.0,
  "velocidadCinta": 1.1,
  "createdAt": "2026-06-12T14:00:00Z",
  "updatedAt": "2026-06-12T18:00:00Z"
}
```

**Reglas de validación:**
- `productoId` requerido y no vacío
- `turno` debe ser `mañana`, `tarde` o `noche`
- `correctos + quemados + crudas (si se envía) <= totalUnidades`
- `finAt >= inicioAt`
- Unidades, pesos y velocidad no negativos

**Códigos de respuesta:**
| Código | Significado |
|---|---|
| 201 | Lote creado |
| 400 | JSON malformado o body > 1MB |
| 401 | API key inválida o ausente |
| 415 | Content-Type incorrecto |
| 422 | Error de validación de negocio |
| 500 | Error de base de datos |

---

### GET /api/v1/parametros-producto

Devuelve todos los sets de parámetros y umbrales de control configurados, uno por producto, ordenados por nombre. Usado por el panel de configuración del Supervisor.

**Respuesta 200:**
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

Da de alta un nuevo set de parámetros para un producto que todavía no tiene uno cargado. No requiere `X-API-Key` (login con Google OAuth 2.0 pendiente — ver sección de tareas).

**Body:**
```json
{
  "productoId": "a1b2c3d4-5678-90ab-cdef-1234567890ab",
  "pesoReferenciaKg": 0.03,
  "toleranciaPesoPct": 10,
  "dimensionBaseCm": 8,
  "toleranciaDimensionCm": 0.5,
  "tempMin": 160,
  "tempMax": 180,
  "velocidadCintaMin": 0.1,
  "velocidadCintaMax": 0.3
}
```

**Reglas de validación** (replican los CHECK constraints de `parametros_producto`):
- `productoId` requerido y no vacío
- `pesoReferenciaKg` y `dimensionBaseCm` deben ser mayores a 0
- `toleranciaPesoPct` y `toleranciaDimensionCm` no pueden ser negativos
- `tempMax` debe ser mayor a `tempMin`
- `velocidadCintaMax` debe ser mayor a `velocidadCintaMin`

**Códigos de respuesta:**
| Código | Significado |
|---|---|
| 201 | Parámetros creados |
| 400 | JSON malformado o body > 1MB |
| 409 | El producto ya tiene un set de parámetros cargado |
| 415 | Content-Type incorrecto |
| 422 | Error de validación de negocio, o `productoId` inexistente en el catálogo |
| 500 | Error de base de datos |

---

### PUT /api/v1/parametros-producto

Modifica el set de parámetros existente de un producto. El `productoId` (dentro del body) identifica el registro a actualizar — no hay path param. El sistema aplica los nuevos rangos de inmediato a los próximos lotes de ese producto.

**Body:** mismo formato que el POST, incluyendo todos los campos (reemplazo completo, no parcial).

**Códigos de respuesta:**
| Código | Significado |
|---|---|
| 200 | Parámetros actualizados |
| 400 | JSON malformado o body > 1MB |
| 404 | No existe un set de parámetros para el `productoId` indicado |
| 415 | Content-Type incorrecto |
| 422 | Error de validación de negocio |
| 500 | Error de base de datos |

---

### GET /api/v1/horno?id=horno-01

Consulta el estado del horno y ejecuta una inspección visual (YOLO).

---

### POST /api/v1/horno/temperatura

Actualiza la temperatura del horno y aplica reglas de umbral:

| Temperatura | Estado | Alerta |
|---|---|---|
| > 200°C | MANTENIMIENTO | CRITICAL |
| 180–200°C | ATENCION | WARNING |
| ≤ 180°C | ACTIVO | — |

**Body:**
```json
{ "id": "horno-01", "temperatura": 212.8 }
```
