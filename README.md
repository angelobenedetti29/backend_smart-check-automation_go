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
  "data": {
    "items": [{ ... }],
    "total": 42,
    "page": 1,
    "pageSize": 10,
    "totalPages": 5
  }
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
  "correctosKg": 96.0,
  "quemadosKg": 4.0,
  "tempHorno1": 188.0,
  "tempHorno2": 192.0,
  "velocidadHorno": 1.1,
  "createdAt": "2026-06-12T14:00:00Z",
  "updatedAt": "2026-06-12T18:00:00Z"
}
```

**Reglas de validación:**
- `productoId` requerido y no vacío
- `turno` debe ser `mañana`, `tarde` o `noche`
- `correctos + quemados == totalUnidades`
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
