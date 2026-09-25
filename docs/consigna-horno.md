# Consigna térmica al horno — SCA-142 (automática) y SCA-320 (manual)

> ⚠️ **HISTÓRICO / OBSOLETO.** Este documento describe la consigna automática de la era
> **SCA-142**. El código y el flujo actuales difieren:
>
> - `POST /api/v1/lotes/inicio` **ya no despacha consigna**: solo abre/persiste el lote
>   abierto del sector (ver [`api-endpoints.md`](api-endpoints.md) §6 y
>   [`../CLAUDE.md`](../CLAUDE.md)).
> - Ya **no existe** autenticación por header de API key compartido para nodos; cada
>   dispositivo usa `Authorization: Bearer <secret>`.
> - La consigna es **manual-only** (SCA-320) vía `POST /api/v1/horno/consigna`, con auth
>   JWT (roles Operario/Supervisor/Admin).
> - El archivo `internal/controller/lote/lote_handler.go` **ya no existe**; el ciclo de
>   lote vive en `internal/controller/lote_sector/` y la consigna en
>   `internal/controller/consigna/`.
>
> Las secciones marcadas como históricas se conservan solo como registro de la
> implementación original de SCA-142.

## 1. Qué resuelven estas dos historias

| | SCA-142 | SCA-320 |
|---|---|---|
| Quién dispara | La IA del nodo de entrada, al identificar la variedad de producto | Un operario/supervisor, a mano, desde el panel web |
| Endpoint | `POST /api/v1/lotes/inicio` | `POST /api/v1/horno/consigna` |
| Valor a despachar | `temp_setpoint` / `velocidad_cinta_setpoint` precargados para el producto | Lo que el operario ingresa en el formulario |
| Validación | El producto debe tener setpoints cargados | El valor ingresado debe caer dentro de `[temp_min, temp_max]` / `[velocidad_cinta_min, velocidad_cinta_max]` del producto |
| Auth | Bearer secret del nodo (el header de API key compartido está retirado) | JWT `session_token` (Operario/Supervisor/Admin) |
| `origen` en la auditoría | `AUTOMATICO` | `MANUAL` (+ `usuario`, best-effort) |

Ambas terminan en el mismo mecanismo compartido — no hay dos implementaciones paralelas, solo dos puertas de entrada distintas al mismo `ConsignaService`.

## 2. Arquitectura

```
                    ┌─────────────────────────────┐
Raspberry Pi ──────►│ POST /api/v1/lotes/inicio    │  (SCA-142, Bearer secret — retirado)
(IA detecta          └──────────────┬──────────────┘
 producto)                          │
                                     ▼
Operario (panel) ──►┌─────────────────────────────┐
 POST /api/v1/       │ ConsignaService.dispatch()   │  (SCA-320, JWT)
 horno/consigna      └──────────────┬──────────────┘
                                     │
        ┌────────────────────────────┼────────────────────────────┐
        ▼                            ▼                            ▼
parametros_producto           oven_controller              horno.Repository
(setpoint o rango,            .SendSetpoint()              .Update()
 según origen)                (simulado, PLC)               (estado en memoria)
                                     │
                                     ▼
                          consigna.Repository.Save()
                          (historial_consignas, Postgres real)
                                     │
                                     ▼
                          sse.Broker.Broadcast("horno.consigna")
                          (fire-and-forget, GET /api/v1/horno/events)
```

### Piezas nuevas

| Capa | Archivo | Qué hace |
|---|---|---|
| Dominio | `internal/domain/consigna/consigna.go` | `Consigna` (registro de auditoría), `ConsignaManualRequest` + `Validate()`, sentinels de error, interfaces `Repository`/`Service` |
| Provider | `internal/provider/oven_controller/client.go` | Cliente simulado del controlador físico (análogo a `yolo_client`): ~40ms de latencia, ~5% de fallo aleatorio |
| Service | `internal/service/consigna/consigna_service.go` | `dispatch()` centraliza: buscar horno → despachar → actualizar estado → auditar → broadcast SSE. `DispatchAutomatico` y `DispatchManual` son las dos entradas |
| Repository | `internal/repository/consigna_repo.go` | Persiste el historial en PostgreSQL real (`historial_consignas`) |
| Controller (SCA-142) | `internal/controller/lote/lote_handler.go` (archivo eliminado — ver banner) | `POST /api/v1/lotes/inicio` (ya no dispara consigna) |
| Controller (SCA-320) | `internal/controller/consigna/consigna_handler.go` | `POST /api/v1/horno/consigna`, `GET /api/v1/horno/consigna/historial` |

### Piezas existentes que se extendieron (no se reescribieron)

- `internal/domain/horno/horno.go` — se agregaron `VelocidadCinta`, `ProductoID`, `LoteID`, y la constante `EstadoControlManual`. El horno **sigue en memoria** (decisión de arquitectura previa, documentada en `CLAUDE.md`); no hay hardware ni Postgres real detrás todavía.
- `internal/domain/parametros_producto/parametros_producto.go` — se agregaron `TempSetpoint`/`VelocidadCintaSetpoint` (nullable), leídos por `internal/repository/parametros_producto_repo.go`.
- `database/schema.sql` — tabla nueva `historial_consignas` + columnas nuevas en `parametros_producto` + catálogo completo de las 6 variedades de panificados.

## 2.1 Catálogo de las 6 variedades (subtarea "matriz de parámetros óptimos")

`database/schema.sql` siembra las 6 variedades con su matriz completa (rango + setpoint puntual):

| Producto | Temp (min–setpoint–max) | Velocidad cinta (min–setpoint–max) |
|---|---|---|
| Tostada Integral | 160 – **170** – 180 °C | 0.10 – **0.20** – 0.30 m/s |
| Pan Lactal | 180 – **190** – 200 °C | 0.15 – **0.25** – 0.35 m/s |
| Pan Francés | 200 – **210** – 220 °C | 0.20 – **0.30** – 0.40 m/s |
| Pan de Salvado | 170 – **180** – 190 °C | 0.15 – **0.25** – 0.35 m/s |
| Medialunas | 190 – **200** – 210 °C | 0.25 – **0.35** – 0.45 m/s |
| Pan Dulce | 150 – **160** – 170 °C | 0.08 – **0.14** – 0.20 m/s |

El mecanismo (`ConsignaService.DispatchAutomatico`/`DispatchManual` + `parametros_producto.GetByProductoID`) ya era genérico desde SCA-142 — esto fue únicamente carga de datos, sin cambios de código en el servicio. Valores de referencia, ajustables por el equipo de Producción vía `PUT /api/v1/parametros-producto` (que hoy solo actualiza rangos min/max, no los setpoints puntuales — ver nota en `CLAUDE.md`).

## 2.2 Manejo de fallo de enlace: alerta crítica + CONTROL_MANUAL (histórico)

Cuando `OvenController.SendSetpoint` devuelve `Aplicada: false` (hoy simulado con ~5% de probabilidad), además de auditar el intento fallido (como ya hacía SCA-142/320), `ConsignaService.handleFalloDeEnlace` hace dos cosas más:

1. **Fuerza `horno.Estado = "CONTROL_MANUAL"`** — un estado nuevo, distinto de `ACTIVO/ATENCION/MANTENIMIENTO`.
2. **Genera una `Alerta` de nivel `CRITICAL`** (reusa el dominio `alerta` ya existente para umbrales térmicos), visible en `GET /api/v1/horno` (`alertas_recientes`).

Mientras el horno esté en `CONTROL_MANUAL`:
- `DispatchAutomatico` (SCA-142, `POST /api/v1/lotes/inicio`) lo **rechaza** con `consigna.ErrHornoEnControlManual` → `409 Conflict`. El control automático deja de insistir.
- `DispatchManual` (SCA-320, `POST /api/v1/horno/consigna`) **sigue funcionando sin restricciones** — es la vía de escape segura para que un operario reactive el horno a mano.
- Un despacho **exitoso**, de cualquier origen, restaura `Estado = "ACTIVO"` — el control automático se reanuda solo, sin pasos manuales extra más allá de que la próxima consigna se aplique bien.

```
Fallo de enlace (Aplicada: false)
        │
        ├─► historial_consignas.exitosa = false, motivo_error   (ya existía)
        ├─► Alerta{Nivel: CRITICAL, ...}                         (nuevo)
        └─► horno.Estado = "CONTROL_MANUAL"                      (nuevo)
                     │
                     ├─► POST /api/v1/lotes/inicio  → 409 (bloqueado)
                     └─► POST /api/v1/horno/consigna → sigue funcionando
                                  │
                                  ▼ (éxito)
                          horno.Estado = "ACTIVO"  (se reanuda el automático)
```

`ConsignaService` depende de una interfaz `OvenController` (no del struct concreto `oven_controller.OvenControllerClient`), lo que permitió agregar tests deterministas del escenario de fallo sin depender del ~5% aleatorio (`internal/service/consigna/consigna_service_test.go`, `fakeController`). Es también el punto de swap para cuando exista un driver real.

## 3. Por qué está diseñado así (decisiones clave)

- **El setpoint automático es un valor puntual explícito**, no el punto medio del rango. Si `temp_setpoint`/`velocidad_cinta_setpoint` no están cargados para un producto, `DispatchAutomatico` devuelve error — no se infiere nada.
- **`POST /api/v1/lotes/inicio` ≠ `POST /api/v1/lotes`**: el segundo sigue siendo un resumen que llega con el lote ya terminado (`inicioAt` y `finAt` juntos). El primero es el trigger real de "la IA identificó el producto", antes de que exista una fila en `lotes_productivos` — por eso genera un UUID de correlación propio (`crypto/rand`, sin dependencia nueva) en vez de esperar un ID real.
- **`historial_consignas.lote_id` no tiene FK** a propósito: en el despacho automático el lote todavía no está persistido en ese momento. `producto_id` sí tiene FK real.
- **El controlador físico es 100% simulado**, mismo patrón que `yolo_client`: no hay red, PLC ni hardware de por medio. El día que exista hardware real, se reemplaza `OvenControllerClient.SendSetpoint(...)` por un cliente real con la misma firma — nada más en el flujo cambia.
- **`ConsignaManualRequest.ProductoID` es obligatorio** en el envío manual: siempre se valida contra el rango real cargado en `parametros_producto`, para no tener una segunda fuente de verdad de "qué es seguro" (límites hardcodeados).
- **`ConsignaManualRequest.LoteID` es opcional**: si el panel conoce el `loteId` del lote en curso (por ejemplo, el que devolvió `POST /api/v1/lotes/inicio`), puede mandarlo en el request manual para que ese ajuste quede correlacionado en el mismo historial de auditoría del lote. Si se omite, la consigna manual queda auditada igual, solo que sin lote asociado.
- **Auditoría incluso en fallos**: si el controlador simulado rechaza la consigna (~5% de las veces), igual se guarda la fila en `historial_consignas` con `exitosa=false` y `motivo_error`.

## 4. Cómo probarlo

### 4.1 Tests automatizados

```bash
go test ./internal/domain/consigna/... \
         ./internal/service/consigna/... \
         ./internal/controller/consigna/... -v
```

Cubre: validación de ambos requests, `DispatchAutomatico`/`DispatchManual` (casos de éxito y error con mocks), y los dos handlers HTTP (200/404/422/401/405/502).

### 4.2 Levantar el entorno

```bash
docker compose up --build
```

`database/schema.sql` ya crea `historial_consignas` y siembra setpoints para "Tostada Integral" (`temp_setpoint=170`, `velocidad_cinta_setpoint=0.20`). Verificá con `GET http://localhost:8080/health`.

### 4.3 SCA-142 — flujo automático (histórico)

> ⚠️ Histórico: hoy `POST /api/v1/lotes/inicio` abre/persiste el lote del sector y **no
> despacha consigna**. El ejemplo siguiente refleja la auth actual del endpoint (Bearer
> del nodo), no el despacho automático original.

```bash
curl -X POST http://localhost:8080/api/v1/lotes/inicio \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <secret del nodo>" \
  -d '{
    "hornoId": "horno-01",
    "productoId": "a1b2c3d4-5678-90ab-cdef-1234567890ab"
  }'
```
Esperado: `200`, `consigna.temperaturaObjetivo: 170`, `consigna.velocidadCintaObjetivo: 0.20`, `origen: "AUTOMATICO"`.

### 4.4 SCA-320 — flujo manual

```bash
curl -X POST http://localhost:8080/api/v1/horno/consigna \
  -b "session_token=<JWT>" \
  -H "Content-Type: application/json" \
  -d '{
    "hornoId": "horno-01",
    "productoId": "a1b2c3d4-5678-90ab-cdef-1234567890ab",
    "temperaturaObjetivo": 175,
    "velocidadCintaObjetivo": 0.25,
    "usuario": "operario-demo",
    "loteId": "<opcional: loteId devuelto por /api/v1/lotes/inicio>"
  }'
```
Esperado: `200`, `origen: "MANUAL"`. El campo `loteId` es opcional — si se manda, el ajuste manual queda correlacionado en el historial de ese lote (ver 4.7); si se omite, la consigna se audita igual, pero sin lote asociado.

Probá también el rechazo por rango:
```bash
curl -X POST http://localhost:8080/api/v1/horno/consigna \
  -b "session_token=<JWT>" \
  -H "Content-Type: application/json" \
  -d '{
    "hornoId": "horno-01",
    "productoId": "a1b2c3d4-5678-90ab-cdef-1234567890ab",
    "temperaturaObjetivo": 999,
    "velocidadCintaObjetivo": 0.20
  }'
```
Esperado: `422`, mensaje "Los valores solicitados están fuera del rango seguro del producto".

> El controlador simulado falla ~5% de las veces a propósito (para ejercitar la rama de error). Si te toca en cualquiera de los dos flujos, la respuesta es `502` con `exitosa: false` — reintentá para ver el camino feliz.

### 4.5 Verificar que el horno refleja el estado real

```bash
curl http://localhost:8080/api/v1/horno?id=horno-01
```
Debe mostrar `temperatura`, `velocidad_cinta`, `producto_id` y `lote_id` con los últimos valores despachados (por cualquiera de los dos flujos).

### 4.6 Verificar la auditoría en Postgres

```bash
docker compose exec db psql -U <POSTGRES_USER> -d <POSTGRES_DB> -c \
  "SELECT horno_id, lote_id, producto_id, temperatura_objetivo, velocidad_cinta_objetivo, origen, exitosa, motivo_error, creada_en FROM historial_consignas ORDER BY creada_en DESC LIMIT 10;"
```
Deberías ver filas con `origen = 'AUTOMATICO'` (4.3) y `origen = 'MANUAL'` (4.4), incluidas las fallidas.

### 4.7 Consultar el historial por lote

```bash
curl "http://localhost:8080/api/v1/horno/consigna/historial?loteId=<loteId devuelto por 4.3>"
```

El `loteId` es generado por `POST /api/v1/lotes/inicio` (despacho automático). Si además se manda ese mismo `loteId` en un `POST /api/v1/horno/consigna` manual posterior (campo opcional `loteId` del request), ambos quedan en el mismo historial — así el panel puede mostrar "todo lo que le pasó a este lote": la consigna automática inicial más cualquier corrección manual posterior.

### 4.8 Fallo de enlace: alerta crítica + CONTROL_MANUAL (histórico)

Como el fallo es simulado con ~5% de probabilidad, hay que insistir hasta capturarlo:

```bash
for i in $(seq 1 40); do
  curl -s -o /dev/null -w "%{http_code} " -X POST http://localhost:8080/api/v1/horno/consigna \
    -H "Content-Type: application/json" \
    -d '{"hornoId":"horno-01","productoId":"a1b2c3d4-5678-90ab-cdef-1234567890ab","temperaturaObjetivo":170,"velocidadCintaObjetivo":0.20}'
done
```
Apenas veas un `502` en la salida, verificá **inmediatamente** (antes de mandar otro request, porque un éxito subsiguiente restaura el estado):

```bash
curl -s http://localhost:8080/api/v1/horno?id=horno-01
```
Esperado: `estado: "CONTROL_MANUAL"` y una `Alerta` nueva en `alertas_recientes` con `nivel: "CRITICAL"`.

```bash
curl -X POST http://localhost:8080/api/v1/lotes/inicio \
  -H "Content-Type: application/json" -H "Authorization: Bearer <secret del nodo>" \
  -d '{"hornoId":"horno-01","productoId":"a1b2c3d4-5678-90ab-cdef-1234567890ab"}'
```
Esperado: `409` "El horno está en modo CONTROL_MANUAL...". El envío manual, en cambio, sigue aceptando requests con normalidad — reintentando `POST /api/v1/horno/consigna` hasta que salga exitoso, `estado` vuelve a `"ACTIVO"` y el automático vuelve a responder `200`.

> Para probar esto de forma determinista sin depender del azar, ver los tests `TestDispatch_Fallido_GeneraAlertaCriticaYPasaAControlManual`, `TestDispatchAutomatico_BloqueadoSiHornoEnControlManual`, `TestDispatchManual_NoSeBloqueaAunqueHornoEsteEnControlManual` y `TestDispatchManual_ExitosoTrasFalloRestauraEstadoActivo` en `internal/service/consigna/consigna_service_test.go` (usan un `fakeController` inyectable en vez del cliente simulado real).

### 4.9 Casos de error — tabla resumen

| Escenario | Endpoint | Esperado |
|---|---|---|
| Sin Bearer secret | `/api/v1/lotes/inicio` | `401` |
| `hornoId` inexistente | ambos | `404` |
| `productoId` sin setpoints (automático) / sin parámetros (manual) | ambos | `422` |
| Valor fuera de rango | `/api/v1/horno/consigna` | `422` |
| Body incompleto | ambos | `422` |
| Método incorrecto | ambos | `405` |
| `loteId` faltante | `/api/v1/horno/consigna/historial` | `400` |
| Horno en `CONTROL_MANUAL` | `/api/v1/lotes/inicio` | `409` |
