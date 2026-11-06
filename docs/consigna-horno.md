# Consigna térmica al horno — SCA-320 (manual) y SCA-400 (MPC)

Este documento describe el diseño **actual** de la consigna del horno. La Pi
ENTRADA es la única actuadora: hace pull por lote y escribe el PLC real. El
backend no simula ni aplica consignas.

> **Estado actual (SCA-320 + SCA-400).**
> - La consigna manual está disponible en `POST /api/v1/horno/consigna`
>   (JWT, Operario/Supervisor/Admin) y queda **encolada** para la Pi; no se
>   aplica en el backend.
> - El control predictivo (MPC) de SCA-400 corre por lote en modos
>   `SUPERVISION` y `AUTOMATICO`; en `MANUAL` no hay loop.
> - `POST /api/v1/lotes/inicio` **ya no dispara la consigna automática de
>   SCA-142**. Sólo abre/persiste el lote y, si el modo no es `MANUAL`, arranca
>   el loop del MPC.
> - La Pi ENTRADA es la única que escribe el PLC real, con su propio
>   `control.sensores`/`actuadores` (`modbus_tcp`/`modbus_rtu`). El backend no
>   abre conexiones Modbus ni mantiene un horno en memoria.
> - La Raspberry aplica la consigna haciendo pull de
>   `GET /api/v1/lotes/{id}/consigna-propuesta` y confirma con
>   `POST /api/v1/lotes/{id}/consigna/ack`.
> - El predictor es un sidecar Python (`cmd/predictor_svc`, puerto `8090`) que el
>   backend consulta por HTTP. Sin predictor, el MPC opera degradado.

## 1. Las tres puertas de entrada

| | SCA-142 (histórico) | SCA-320 (manual) | SCA-400 (MPC) |
|---|---|---|---|
| Quién dispara | La IA del nodo de entrada al identificar el producto | Un operario/supervisor desde el panel | El loop del MPC por lote |
| Endpoint | `POST /api/v1/lotes/inicio` (ya no despacha) | `POST /api/v1/horno/consigna` | `GET /lotes/{id}/consigna-propuesta` + `POST /lotes/{id}/consigna/ack` |
| Valor | `temp_setpoint`/`velocidad_cinta_setpoint` del producto | Lo que ingresa el operario | Propuesta del optimizador XGBoost |
| Validación | Producto con setpoints cargados | Lote abierto MANUAL + rango del producto | Límites del producto + tuning del lote |
| Auth | Bearer del nodo | JWT `session_token` | Bearer de la Pi (pull) o JWT (panel) |
| `origen` en auditoría | `AUTOMATICO` (histórico) | `MANUAL` | No escribe `historial_consignas`; audita en `decisiones_control` |

## 2. Arquitectura actual

```
Operario (panel) ──► POST /api/v1/horno/consigna
                         │
                         ▼
                 ConsignaService.DispatchManual
                         ├─► valida lote ABIERTO + MANUAL y rango del producto
                         ├─► decisiones_control (override ya aprobado, muestras=0)
                         ├─► historial_consignas (exitosa=true, estado=PENDIENTE = encolada)
                         └─► SSE horno.consigna

Loop MPC (por lote) ──► Optimizador ──► Predictor (sidecar XGBoost)
                         ├─► decisiones_control (audit)
                         └─► SSE control.propuesta (incluye `autorizada`)

Raspberry Pi ENTRADA ◄── GET /lotes/{id}/consigna-propuesta (pull)
      ├── escribe el PLC real (control.sensores/actuadores: modbus_tcp|modbus_rtu)
      └────────► POST /lotes/{id}/consigna/ack (aplicación confirmada)
```

Piezas principales:

| Capa | Archivo | Qué hace |
|---|---|---|
| Dominio | `internal/domain/consigna/consigna.go` | `Consigna`, `ConsignaManualRequest`, orígenes y sentinels |
| Dominio | `internal/domain/decisiones_control/` | Audit del MPC, aprobación y override manual |
| Servicio | `internal/service/consigna/consigna_service.go` | Valida el lote/rango y encola el override manual; audita |
| Servicio | `internal/service/control/control.go` | Loop por lote: precalentamiento, MPC y publicación de propuestas |
| Servicio | `internal/service/optimizador/optimizador.go` | MPC horizonte 1 sobre grilla de T1 y V |
| Servicio | `internal/service/predictor/predictor.go` | Cliente HTTP al sidecar XGBoost |
| Controller | `internal/controller/control/control_handler.go` | Endpoints de control, propuesta, aprobación y ACK |
| Controller | `internal/controller/consigna/consigna_handler.go` | Consigna manual e historial |
| Controller | `internal/controller/horno/horno_handler.go` | Proyección de solo lectura `GET /api/v1/horno` |
| Sidecar | `cmd/predictor_svc/` | FastAPI + XGBoost por producto |

## 3. Modos de operación del lote

El modo se persiste en `lotes_productivos.modo` y se cambia con
`PATCH /api/v1/lotes/{id}/modo` (panel) o `PUT /api/v1/lotes/{id}/modo` (Pi de
su sector).

| Modo | Loop | Propuesta | Quién aplica |
|---|---|---|---|
| `MANUAL` | No corre | No hay MPC. Si hay override manual encolado, se sirve con `manual_override=true` | La Pi (pull) |
| `SUPERVISION` | Sí | `autorizada=false` hasta que se apruebe | Operario/Pi aprueba y luego la Pi aplica |
| `AUTOMATICO` | Sí | `autorizada=true` | La Pi (pull) |

En `AUTOMATICO`, el loop arranca en fase `CALENTANDO` salvo que el lote venga
`iniciado_manualmente`; cuando el horno se mantiene en tolerancia durante 3
muestras, pasa a `ADAPTANDO` y entrega un setpoint de transición. En
`SUPERVISION` arranca directo en `ADAPTANDO`.

## 4. Consigna manual (SCA-320)

`POST /api/v1/horno/consigna` valida el body, exige un lote **ABIERTO** y en
modo **MANUAL**, y valida el rango seguro del producto. Si todo es válido:

1. Deja una decisión en `decisiones_control` con `muestras=0`, aprobada, para
   que la Pi la levante por `consigna-propuesta` como `manual_override=true`.
2. Audita en `historial_consignas` con `origen=MANUAL`, `decision_id` de la
   decisión encolada, `estado=PENDIENTE` y `exitosa=true` (**encolada**, no
   aplicada).
3. Emite `horno.consigna` por SSE.

Si el encolado falla, deja la fila con `exitosa=false`, `estado=RECHAZADA`,
`decision_id=null` y `motivo_error`, y responde `502`.

- `loteId` es obligatorio. `sectorId` (o el alias legacy `hornoId`) es opcional;
  si viene, debe coincidir con el sector del lote.
- `productoId`, `temperaturaObjetivo` y `velocidadCintaObjetivo` son
  obligatorios; `usuario` es opcional.
- `exitosa=true` no confirma que el PLC haya aplicado el setpoint. El ciclo de
  vida fino vive en `estado`: `PENDIENTE` al encolar, `APLICADA` cuando el ack
  de la Pi lo confirma y `RECHAZADA` si no se pudo encolar. `exitosa` se
  mantiene por compatibilidad.

## 5. Control MPC y predictor (SCA-400)

Cada tick del loop:

1. Lee las últimas métricas del horno (`metricas_horno`) y los eventos del lote
   en la ventana.
2. Calcula la confianza promedio; si está por debajo de `MPC_CONFIANZA_MIN`, la
   decisión se audita como `confianza_baja`.
3. Pide al optimizador una propuesta. El optimizador muestrea una grilla de T1 y
   V dentro de los Δmáx, evalúa el predictor XGBoost en cada candidato y elige
   el menor Z que cumpla los umbrales de aceptación. Si ninguno cumple, devuelve
   el mejor Z absoluto con `restriccion_relajada=true`.
4. Persiste la decisión en `decisiones_control` y publica la propuesta por SSE
   (`autorizada=true` en AUTOMATICO, `false` en SUPERVISION). El backend no
   aplica nada.

El predictor:

- Es un sidecar FastAPI en `cmd/predictor_svc`, puerto `8090`.
- Usa un modelo XGBoost por `producto_id`; si no hay modelo entrenado, usa un
  placeholder determinístico.
- Los artefactos se guardan como JSON (`save_raw(raw_format="json")`) en
  `models_xgb/producto_{uuid}_v{n}.json`.
- El request acepta `snake_case` y alias `camelCase`; la respuesta es
  `camelCase` (`pctCrudos`, `pctOk`, `pctQuemados`, `modelVersion`,
  `esPlaceholder`).
- Se puede entrenar con el dataset sintético
  `cmd/predictor_svc/data/sintetico_6_productos.csv`.
- Si el sidecar no responde, el optimizador no propone y la decisión se audita
  como `skipped`; el horno conserva el último setpoint aplicado.

En `AUTOMATICO`, una predicción de placeholder no se publica salvo
`MPC_PERMITIR_PLACEHOLDER=true` (solo demo/tesis); la decisión se audita como
`modelo_no_entrenado`. En `SUPERVISION` la propuesta se publica igual para que
el operario vea el warning.

## 6. Aprobación y ACK

Aprobación en SUPERVISION:

- `POST /api/v1/lotes/{id}/aplicar-consigna` (alias `/aprobar-consigna`).
- El body opcional `decision_id` evita aprobar una propuesta distinta de la que
  el operario está viendo.
- El endpoint sólo autoriza; la aprobación se persiste en
  `decisiones_control.aprobada_por` y `aprobada_at`. La aplicación real la hace
  la Pi.

ACK:

- `POST /api/v1/lotes/{id}/consigna/ack` (Bearer de la Pi) con
  `decision_id`, `temp_aplicado` y `vel_aplicado`.
- Marca la decisión como aplicada (`aplicado_por=OPERARIO` en SUPERVISION,
  `MPC` en AUTOMATICO) y actualiza los setpoints aplicados del lote.
- Cierra como `APLICADA` la consigna de `historial_consignas` encolada con ese
  `decision_id` (update best-effort: si falla, el ack igual responde `200`).
- Es idempotente: un reenvío del mismo ACK vuelve a responder `200` (no hay
  fila pendiente o ya está `APLICADA`).

## 7. SSE y estado real del horno

- `GET /api/v1/lotes/{id}/control/events` emite `control.propuesta` filtrado por
  lote. El evento incluye `lote_id`, `propuesta`, `decision_id`, `autorizada` y
  `ventana`. La `propuesta` serializa `temp1`, `velocidad`, `z` y `prediccion`.
- `GET /api/v1/horno/events` emite `horno.consigna` con el resultado de cada
  encolado manual.
- `GET /api/v1/horno?sector_id=horno-1` devuelve una **proyección de solo
  lectura** del horno (`data.horno.id` es el id del sector): lote abierto +
  último sample de `metricas_horno`. No hay horno en memoria. Sin
  telemetría, T/V y el resto de las lecturas viajan `null`.
- La telemetría real (T1, combustión, humedad, ambiente, velocidad y setpoints
  observados) llega por `POST /api/v1/lotes/{id}/metricas-horno`; es el estado
  real del horno y el panel la lee por `GET /api/v1/horno` (o cruda con
  `GET /api/v1/lotes/{id}/metricas-horno`).

## 8. Fallo de encolado

Si no se puede persistir o aprobar la decisión del override manual:

1. El intento se audita en `historial_consignas` con `exitosa=false`,
   `estado=RECHAZADA` y `motivo_error`.
2. Se responde `502` ("No se pudo encolar la consigna para la Pi").
3. La Pi no ve ninguna propuesta nueva; el horno sigue con el último setpoint
   aplicado.

No se generan alertas ni se cambia el estado del horno: el backend no tiene un
actuador propio cuyo enlace pueda fallar.

## 9. Cómo probarlo

### 9.1 Tests

```bash
go test ./internal/domain/consigna/... \
         ./internal/service/consigna/... \
         ./internal/controller/consigna/... \
         ./internal/service/control/... \
         ./internal/service/optimizador/... -v
```

### 9.2 Entorno

```bash
cp .env.example .env   # completar variables
docker compose up --build
```

Verificar `GET http://localhost:8080/health`, `GET /healthz` y
`GET http://localhost:8090/health` (predictor). Instalación completa en
[`../MANUAL_INSTALACION.md`](../MANUAL_INSTALACION.md).

### 9.3 Consigna manual

```bash
curl -X POST http://localhost:8080/api/v1/horno/consigna \
  -b "session_token=<JWT>" \
  -H "Content-Type: application/json" \
  -d '{
    "loteId": "<lote abierto MANUAL>",
    "sectorId": "horno-1",
    "productoId": "a1b2c3d4-5678-90ab-cdef-1234567890ab",
    "temperaturaObjetivo": 195,
    "velocidadCintaObjetivo": 0.22,
    "usuario": "operario-demo"
  }'
```

Esperado: `200`, `origen: "MANUAL"`, `exitosa: true` (encolada para la Pi). Un
lote cerrado, no MANUAL o inexistente responde `409`/`404`; un valor fuera del
rango del producto responde `422`.

### 9.4 Flujo MPC en SUPERVISION

1. Abrir un lote con `modo: "SUPERVISION"` vía `POST /api/v1/lotes/inicio`.
2. Esperar a que el loop publique una propuesta; consultarla con
   `GET /api/v1/lotes/{id}/consigna-propuesta`. Debe traer `autorizada: false`.
3. Aprobarla con `POST /api/v1/lotes/{id}/aplicar-consigna`.
4. La Pi (o el panel) vuelve a consultar la propuesta: ahora `autorizada: true`.
5. La Pi aplica y confirma con `POST /api/v1/lotes/{id}/consigna/ack`.

### 9.5 Flujo MPC en AUTOMATICO

Abrir el lote con `modo: "AUTOMATICO"`. La propuesta se publica con
`autorizada: true`; la Pi la aplica y ackea. Sin modelo entrenado y con
`MPC_PERMITIR_PLACEHOLDER=false`, la decisión se audita como
`modelo_no_entrenado` y no se publica.

### 9.6 Verificar auditoría

```bash
docker compose exec db psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c \
  "SELECT horno_id, lote_id, origen, decision_id, estado, exitosa, motivo_error, creada_en
   FROM historial_consignas ORDER BY creada_en DESC LIMIT 10;"

docker compose exec db psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c \
  "SELECT lote_id, temp_propuesto, vel_propuesto, aplicado_por, razon_skipped, aprobada_por, created_at
   FROM decisiones_control ORDER BY created_at DESC LIMIT 10;"
```

## 10. Histórico: SCA-142 (despacho automático original)

> ⚠️ **HISTÓRICO / OBSOLETO.** Esta sección conserva el diseño original de
> SCA-142. El trigger automático desde `POST /api/v1/lotes/inicio` fue retirado.
> Hoy ese endpoint sólo abre/persiste el lote; el control automático es el MPC de
> SCA-400.

En SCA-142, la IA del nodo de entrada identificaba el producto y llamaba a
`POST /api/v1/lotes/inicio`. El backend tomaba `temp_setpoint` y
`velocidad_cinta_setpoint` del producto y los despachaba con
`origen=AUTOMATICO`. El flujo terminaba en el mismo `ConsignaService` que la
consigna manual.

Decisiones históricas que siguen siendo válidas conceptualmente:

- El setpoint automático era un valor puntual explícito, no el punto medio del
  rango.
- `historial_consignas.lote_id` no tiene FK a propósito: el lote podía no estar
  persistido al momento del despacho.
- La auditoría se guardaba incluso en fallos.
- El controlador físico era simulado en SCA-142. Hoy la Pi ENTRADA es la única
  que escribe el PLC real; el backend no tiene driver Modbus ni horno en
  memoria.

Diferencias con el presente:

- `POST /api/v1/lotes/inicio` ya no dispara consigna.
- La autenticación de nodos es `Authorization: Bearer <secret>`; no existe el
  header de API key compartido.
- El MPC no depende exclusivamente de `temp_setpoint` del producto: propone
  setpoints por ventana y usa el setpoint del producto sólo como receta de
  arranque cuando el lote no tiene setpoints propios.
- La consigna manual se encola para la Pi; las filas `origen=AUTOMATICO` de
  `historial_consignas` quedan sólo como histórico y ya no se escriben.

## 11. Referencias

- [`api-endpoints.md`](api-endpoints.md) — contrato HTTP de consigna, control,
  propuesta, aprobación, ACK y telemetría.
- [`../CLAUDE.md`](../CLAUDE.md) — estado y decisiones de arquitectura.
- [`../MANUAL_INSTALACION.md`](../MANUAL_INSTALACION.md) — instalación,
  variables y despliegue.
- [`../cmd/predictor_svc/README.md`](../cmd/predictor_svc/README.md) — sidecar
  predictor y entrenamiento.
