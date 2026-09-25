# Migración del frontend — lotes por sector

El backend reemplazó el flujo de lotes legacy por el contrato **sector/lotes**
(`docs/backend-go-lotes-sector.md`). Este documento es el plan de migración del
panel Next.js (`frontend_smart-check-automation`).

## 0. Qué se eliminó del backend (ya no existe)

- `POST /api/v1/lotes` (alta de lote finalizado) → **eliminado** (405).
- `GET /api/v1/lotes-productivos` (historial global) → **eliminado**.
- `GET /api/v1/lotes-productivos/events` (SSE `lote.created`) → **eliminado**.
- Auto-consigna (SCA-142) → eliminada; queda solo la consigna manual.

> Consecuencia: mientras el frontend no migre, el panel de lotes queda sin datos.
> La migración del frontend es **obligatoria**, no opcional.

## 1. Prerequisitos de backend (ya disponibles)

| Endpoint | Auth | Uso en el frontend |
|---|---|---|
| `GET /api/v1/sectores` | OAuth | Poblar el selector de sector (`data: [{id, nombre}]`). |
| `GET /api/v1/dispositivos` | OAuth | Ahora incluye `sectorId` por dispositivo para agrupar entrada+salida. |

## 2. Endpoints: mapa de reemplazo

| Viejo (eliminado) | Nuevo | Notas |
|---|---|---|
| `GET /lotes-productivos?page&pageSize&productoId` | `GET /lotes?sector_id&limite&antes_de&producto_id` | Con OAuth, `sector_id` es **obligatorio**. Paginación por cursor. |
| `GET /lotes-productivos/events` (`lote.created`) | `GET /lotes/events` | Eventos `lote.creado`/`lote.actualizado`/`lote.cerrado`; payload = objeto lote **crudo** (§4). |
| — | `GET /lotes/abierto?sector_id` | Lote en vivo del sector (`{lote: {...}|null}`). OAuth exige `sector_id`. |
| — | `GET /productos` | Catálogo `{id, nombre, activo}`. |

## 3. Forma del objeto lote (§4/§8)

```json
{
  "id": "uuid",
  "sector_id": "horno-1",
  "estado": "ABIERTO",
  "producto_id": "uuid",
  "producto_nombre": "Tostada",
  "abierto_en": "2026-09-23T10:15:30.123-03:00",
  "abierto_por": { "device_id": "uuid", "type": "ENTRADA_HORNO" },
  "conteos": { "ok": 12, "crudo": null, "quemado": 2, "total": 14 },
  "ultimo_evento_en": "2026-09-23T10:20:00.000-03:00",
  "inactividad_segundos": 12.5,
  "cerrado_en": null,
  "motivo_cierre": null
}
```

Reglas clave:
- `conteos.crudo` (o cualquier estado que el modelo no produzca) es **`null`, nunca 0**.
- `total = ok + crudo + quemado` (los `null` no suman).
- Un lote `ABIERTO` tiene `cerrado_en: null` y `motivo_cierre: null`.
- `inactividad_segundos` lo calcula el **servidor**; no comparar relojes.

### Mapeo de campos viejo → nuevo

| `ProductionRun` (viejo) | `LoteSector` (nuevo) |
|---|---|
| `productoId` | `producto_id` |
| `productoNombre` | `producto_nombre` |
| `inicioAt` | `abierto_en` |
| `finAt` | `cerrado_en` |
| `totalUnidades` | `conteos.total` |
| `correctos` | `conteos.ok` |
| `quemados` | `conteos.quemado` |
| `crudas` | `conteos.crudo` |
| `turno` | **(no existe)** → reemplazar por `sector_id` / `estado` / `motivo_cierre` |
| kg / temps / `velocidadCinta` | **(no existen en el nuevo historial)** |

## 4. Cambios de tipos (TypeScript)

- **`lib/production-data.ts`**: reemplazar `ProductionRun` por `LoteSector` (campos §3)
  + `Conteos { ok: number|null; crudo: number|null; quemado: number|null; total: number }`.
- **`lib/devices-data.ts`** + **`lib/monitoring-runtime.ts` (`parseDevice`)**:
  agregar `type?: "ENTRADA_HORNO"|"SALIDA_HORNO"` y `sectorId?: string`.
- **`lib/parametros-producto.ts` (`LoteProductivo`)**: eliminar (era del historial legacy)
  o reducirlo si `parameters-history.tsx` migra a `/lotes` filtrado por `producto_id`.

## 5. Validador y parser (el gate duro)

- **`lib/monitoring-runtime.ts`**: reemplazar `isProductionRun` por `isLoteSector`.
  Debe aceptar `null` en `conteos.ok/crudo/quemado`, `abierto_por` opcional, y
  `cerrado_en`/`motivo_cierre` nullables. `total` numérico.
- `parseProductionPayload` → `parseLoteSectorPayload` (mantener la tolerancia a
  envelope `{success,data}` y a objeto crudo).
- Un solo registro inválido invalida todo el snapshot: revisar la política atómica
  (conviene descartar la fila inválida y loguear, en vez de tirar todo el panel).

## 6. SSE

- Nuevo route handler `app/api/lotes/events/route.ts` → upstream `GET /api/v1/lotes/events`
  (con cookie `session_token`). **No** reusar el proxy de `lotes-productivos/events`.
- `components/lotes/dashboard-content.tsx`: escuchar `lote.creado`, `lote.actualizado`,
  `lote.cerrado`; el payload es el objeto lote crudo.
- Eliminar el listener `lote.created` y el proxy viejo.

## 7. UI / nuevas vistas

- **Selector de sector** (`components/lotes/filters-bar.tsx` o nuevo componente):
  poblado desde `GET /sectores`; persiste en la URL (`?sector_id=`) como el
  `?productoId=` actual. Todas las llamadas a `/lotes` y `/lotes/abierto` lo usan.
- **Lote abierto en vivo**: tarjeta/panel con `conteos` en tiempo real,
  `inactividad_segundos`, `abierto_por.type` (avisar si es lote **degradado**, es
  decir `abierto_por.type != "ENTRADA_HORNO"`).
- **Historial** (`supervision-table.tsx`): columnas por `conteos`; quitar turno/kg/temps.
- **Dashboard global** (home): como `/lotes` es **por sector**, iterar `GET /sectores`
  y mergear para los KPIs globales (el número de sectores es chico).
- **Catálogo**: usar `GET /productos` para el selector (filtrar `activo: true`) o
  seguir con `/parametros-producto` para la configuración de umbrales.
- **Consigna**: si se necesita consigna manual desde el panel, agregar UI sobre
  `POST /api/v1/horno/consigna` (UI aún no existe; el endpoint ya está disponible, SCA-320).

## 8. Checklist de archivos a tocar

1. `lib/production-data.ts` — tipos `LoteSector`/`Conteos`.
2. `lib/monitoring-runtime.ts` — `isLoteSector` + parser.
3. `lib/devices-data.ts`, `lib/monitoring-runtime.ts` (`parseDevice`) — `type`/`sectorId`.
4. `actions/api.ts` — `getAllProductionRuns` → `/lotes`; agregar `getSectores`,
   `getLoteAbierto`; borrar el fetcher legacy.
5. `app/api/lotes/snapshot/route.ts` — proxy a `/lotes` (con `sector_id`).
6. `app/api/lotes/events/route.ts` — proxy a `/lotes/events` + nombres de evento.
7. `components/lotes/supervision-table.tsx`, `kpi-cards.tsx`, `filters-bar.tsx`,
   `dashboard-content.tsx` — `conteos` + selector de sector + SSE.
8. `components/dashboard-content.tsx` (home) + `lib/format.ts` (`qualityRate`) —
   agregaciones sobre `conteos`.
9. `components/configuracion/parameters-history.tsx` + `lib/parametros-producto.ts`.
10. `components/supervision/*` — (opcional) agrupar por sector usando `sectorId`.

## 9. Tests a actualizar

- `lib/__tests__/monitoring-runtime.test.ts` — validador nuevo.
- `actions/__tests__/api.test.ts` — endpoints nuevos.
- `components/__tests__/monitoring-provider.test.ts` — payload SSE nuevo.
- `components/lotes/__tests__/*` — tabla/KPIs con `conteos`.

## 10. Orden de despliegue

1. Backend desplegado (ya: `/sectores`, `sectorId`, `/lotes`, `/lotes/abierto`, `/lotes/events`).
2. Frontend migrado en la misma ventana.
3. Sin ventana de convivencia: el legacy ya no existe en el backend.

## 11. Decisiones abiertas (para el equipo)

- **Dashboard global**: iterar sectores (recomendado, pocos sectores) vs. pedir un
  endpoint global (`/lotes` sin `sector_id` para OAuth).
- **kg/temperaturas/velocidad**: el contrato de sector no los persiste. Si el panel
  los necesita, definir de dónde salen (no están en `/lotes`).
- **Turno**: ya no es una propiedad del lote de sector; si la UI lo necesita, debe
  derivarlo de `abierto_en` o eliminarlo.
