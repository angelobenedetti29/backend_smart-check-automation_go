# Smart-Check Automation — Backend

> ⚠️ **Desactualizado / histórico.** La referencia detallada de endpoints de este README
> quedó atrás del código actual. La fuente vigente es
> [`docs/api-endpoints.md`](docs/api-endpoints.md) y las convenciones/estado del proyecto
> están en [`CLAUDE.md`](CLAUDE.md). Abajo se conserva solo un resumen de alto nivel con
> los hechos de mayor impacto.

Backend REST en Go para el sistema de supervisión inteligente de hornos túnel de Fermar S.A.
Registra y consulta lotes productivos, monitorea temperaturas de hornos y recibe datos de nodos Raspberry Pi via HTTP.

## Stack

| Capa | Tecnología |
|---|---|
| Lenguaje | Go 1.25 |
| Base de datos | Aiven PostgreSQL 16 |
| Driver DB | pgx/v5 |
| Infraestructura local | Docker + Docker Compose |
| Autenticación (usuarios) | Google OAuth 2.0 + credenciales locales (bcrypt) → JWT en cookie HttpOnly `session_token` |
| Autenticación (nodos Raspberry Pi) | secret por nodo vía header `Authorization: Bearer <secret>` |

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

## Endpoints (resumen)

La referencia completa y actualizada —con auth, rol mínimo, request/response y códigos—
está en [`docs/api-endpoints.md`](docs/api-endpoints.md). Los puntos de mayor impacto:

- **Auth del panel/usuarios:** JWT en cookie HttpOnly `session_token` (Google OAuth 2.0 o
  credenciales locales). Ya no existe un header de API key compartido.
- **Auth de dispositivos (Raspberry Pi):** cada nodo aprobado usa
  `Authorization: Bearer <secret>`; el alta se hace vía
  `POST /api/v1/registration-requests`.
- **Ciclo de lote por sector:** se inicia con `POST /api/v1/lotes/inicio` (device-only) y
  el historial se consulta con `GET /api/v1/lotes` (device o JWT). El alta legada
  `POST /api/v1/lotes` responde **405** y el antiguo listado global de lotes fue
  **eliminado**.
- **Consigna:** `POST /api/v1/lotes/inicio` **ya no dispara consigna automática**
  (SCA-142 retirado). La consigna es **manual-only** (SCA-320) vía
  `POST /api/v1/horno/consigna` (JWT; Operario/Supervisor/Admin).
- **`/api/v1/parametros-producto`:** requiere JWT; las escrituras (`POST`/`PUT`) exigen
  rol Supervisor o Admin.
- **`/api/v1/dispositivos/ping`:** se autentica con el Bearer secret del dispositivo.
  En `/api/v1/dispositivos`, `POST` responde **405** y `PUT`/`DELETE` requieren
  Supervisor/Admin.
- **Salud:** `GET /health` y `GET /healthz` verifican conectividad con PostgreSQL.

La base de datos productiva es **Aiven PostgreSQL 16**.
