# Consigna térmica al horno — SCA-142 (histórico), SCA-320 (manual) y SCA-400 (MPC)

Este documento describe el diseño **actual** de la consigna del horno y conserva,
claramente marcada, la implementación histórica de SCA-142.

> **Estado actual (SCA-320 + SCA-400).**
> - La consigna manual sigue disponible en `POST /api/v1/horno/consigna`
>   (JWT, Operario/Supervisor/Admin).
> - El control predictivo (MPC) de SCA-400 corre por lote en modos
>   `SUPERVISION` y `AUTOMATICO`; en `MANUAL` no hay loop.
> - `POST /api/v1/lotes/inicio` **ya no dispara la consigna automática de
>   SCA-142**. Sólo abre/persiste el lote y, si el modo no es `MANUAL`, arranca
>   el loop del MPC.
> - La Pi ENTRADA es la única que escribe el PLC real, con su propio
>   `control.sensores`/`actuadores` (`modbus_tcp`/`modbus_rtu`). El backend no
>   abre conexiones Modbus: usa un stub interno determinista
>   (`SimulatedOvenController`) para auditar y actualizar el estado del horno.
> - La Raspberry aplica la consigna haciendo pull de
>   `GET /api/v1/lotes/{id}/consigna-propuesta` y confirma con
>   `POST /api/v1/lotes/{id}/consigna/ack` cuando `CONTROL_ACTUADOR=pi`.
> - El predictor es un sidecar Python (`cmd/predictor_svc`, puerto `8090`) que el
>   backend consulta por HTTP. Sin predictor, el MPC opera degradado.

## 1. Las tres puertas de entrada

| | SCA-142 (histórico) | SCA-320 (manual) | SCA-400 (MPC) |
|---|---|---|---|
| Quién dispara | La IA del nodo de entrada al identificar el producto | Un operario/supervisor desde el panel | El loop del MPC por lote |
| Endpoint | `POST /api/v1/lotes/inicio` (ya no despacha) | `POST /api/v1/horno/consigna` | `GET /lotes/{id}/consigna-propuesta` + `POST /lotes/{id}/consigna/ack` |
| Valor | `temp_setpoint`/`velocidad_cinta_setpoint` del producto | Lo que ingresa el operario | Propuesta del optimizador XGBoost |
| Validación | Producto con setpoints cargados | Rango `[temp_min,temp_max]`/`[velocidad_cinta_min,max]` del producto | Límites del producto + tuning del lote |
| Auth | Bearer del nodo | JWT `session_token` | Bearer de la Pi (pull) o JWT (panel) |
| `origen` en auditoría | `AUTOMATICO` | `MANUAL` | `AUTOMATICO` (sólo actuador `backend`) |

Las tres terminan en el mismo `ConsignaService.Dispatch`; lo único que cambia es
el origen y quién decide el setpoint.

## 2. Arquitectura actual

```
Operario (panel) ──► POST /api/v1/horno/consigna ──► ConsignaService.Dispatch
                                                          │
Loop MPC (por lote) ──► Optimizador ──► Predictor          ├─► stub interno SendSetpoint()
   │                     (sidecar XGBoost)                 │     (no escribe PLC; audita/actualiza estado)
   │                                                       ├─► historial_consignas (auditoría)
   ├─ actuador=backend ──────────────────────────────────►┤
   │                                                       ├─► horno.Repository.Update() (estado en memoria)
   └─ actuador=pi ──► SSE control.propuesta                 └─► SSE horno.consigna
                       (incluye `autorizada`)
                       + decisiones_control (audit)

Raspberry Pi ENTRADA ◄── GET /lotes/{id}/consigna-propuesta (pull)
      ├── escribe el PLC real (control.sensores/actuadores: modbus_tcp|modbus_rtu)
      └────────► POST /lotes/{id}/consigna/ack (aplicación confirmada)
```

Piezas principales:

| Capa | Archivo | Qué hace |
|---|---|---|
| Dominio | `internal/domain/consigna/consigna.go` | `Consigna`, `ConsignaManualRequest`, orígenes y sentinels |
| Dominio | `internal/domain/decisiones_control/` | Audit del MPC y aprobación |
| Servicio | `internal/service/consigna/consigna_service.go` | Valida, despacha, audita, actualiza el horno y encola el override manual |
| Servicio | `internal/service/control/control.go` | Loop por lote: precalentamiento, MPC, propuesta, aprobación y ACK |
| Servicio | `internal/service/optimizador/optimizador.go` | MPC horizonte 1 sobre grilla de T1 y V |
| Servicio | `internal/service/predictor/predictor.go` | Cliente HTTP al sidecar XGBoost |
| Provider | `internal/provider/oven_controller/` | Stub interno del controlador del horno (`SimulatedOvenController`); no abre Modbus ni escribe el PLC |
| Controller | `internal/controller/control/control_handler.go` | Endpoints de control, propuesta, aprobación y ACK |
| Controller | `internal/controller/consigna/consigna_handler.go` | Consigna manual e historial |
| Sidecar | `cmd/predictor_svc/` | FastAPI + XGBoost por producto |

## 3. Modos de operación del lote

El modo se persiste en `lotes_productivos.modo` y se cambia con
`PATCH /api/v1/lotes/{id}/modo` (panel) o `PUT /api/v1/lotes/{id}/modo` (Pi de
su sector).

| Modo | Loop | Propuesta | Quién aplica |
|---|---|---|---|
| `MANUAL` | No corre | No hay MPC. Si hay override manual encolado, se sirve con `manual_override=true` | La Pi (pull); con `CONTROL_ACTUADOR=backend` (laboratorio), el stub interno |
| `SUPERVISION` | Sí | `autorizada=false` hasta que se apruebe | Operario/Pi aprueba y luego la Pi aplica |
| `AUTOMATICO` | Sí | `autorizada=true` | La Pi (pull); con `CONTROL_ACTUADOR=backend` (laboratorio), el stub interno |

En `AUTOMATICO`, el loop arranca en fase `CALENTANDO` salvo que el lote venga
`iniciado_manualmente`; cuando el horno se mantiene en tolerancia durante 3
muestras, pasa a `ADAPTANDO` y entrega un setpoint de transición. En
`SUPERVISION` arranca directo en `ADAPTANDO`.

## 4. Consigna manual (SCA-320)

`POST /api/v1/horno/consigna` valida el body y el rango seguro del producto, y
despacha al stub interno del backend (no al PLC). El resultado se audita siempre
en `historial_consignas`, incluso si el stub rechaza la consigna. La Pi ENTRADA
es la única que aplica el setpoint real en el PLC.

- **MANUAL + `CONTROL_ACTUADOR=pi` + lote abierto:** además de despachar, el
  backend encola una decisión ya aprobada (`muestras=0`) para la Pi. La Pi la
  obtiene por `consigna-propuesta` con `manual_override=true` y la aplica **sin
  el clamp de Δ por ciclo**; luego confirma por `/consigna/ack`.
- **SUPERVISION/AUTOMATICO:** el envío manual se despacha y audita, pero el loop
  del MPC sigue gobernando las propuestas siguientes. No se encola override.
- **Sin lote o con actuador `backend`:** no se encola override; el despacho
  manual es directo contra el stub interno.

El request exige `hornoId`, `productoId`, `temperaturaObjetivo` y
`velocidadCintaObjetivo`; `usuario` y `loteId` son opcionales.

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
4. Persiste la decisión en `decisiones_control` y publica la propuesta.

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

En `AUTOMATICO`, una predicción de placeholder no se aplica salvo
`MPC_PERMITIR_PLACEHOLDER=true` (solo demo/tesis); la decisión se audita como
`modelo_no_entrenado`. En `SUPERVISION` la propuesta se publica igual para que
el operario vea el warning.

## 6. Actuador, aprobación y ACK

`CONTROL_ACTUADOR` decide quién aplica la consigna del **loop MPC**:

- **`pi` (default):** el backend publica la propuesta; la Raspberry hace pull de
  `GET /api/v1/lotes/{id}/consigna-propuesta` y aplica en el PLC real. La
  propuesta incluye `autorizada` (`true` en AUTOMATICO; `false` en SUPERVISION
  hasta aprobar) y, cuando corresponde, `manual_override`.
- **`backend` (solo laboratorio/debug):** el backend despacha contra el stub
  interno, nunca contra un PLC real. En AUTOMATICO aplica solo; en SUPERVISION
  el operario aprueba con `POST /api/v1/lotes/{id}/aplicar-consigna`.

La consigna **manual** (`POST /api/v1/horno/consigna`) siempre pasa por
`ConsignaService.dispatch` y llama al stub interno, con independencia de
`CONTROL_ACTUADOR`; el actuador sólo decide si además se encola el override para
que la Pi lo levante en modo MANUAL. La Pi ENTRADA es la única que escribe el
PLC real.

Aprobación en SUPERVISION:

- `POST /api/v1/lotes/{id}/aplicar-consigna` (alias `/aprobar-consigna`).
- El body opcional `decision_id` evita aprobar una propuesta distinta de la que
  el operario está viendo.
- La aprobación se persiste en `decisiones_control.aprobada_por` y
  `aprobada_at`.

ACK:

- `POST /api/v1/lotes/{id}/consigna/ack` (Bearer de la Pi) con
  `decision_id`, `temp_aplicado` y `vel_aplicado`.
- Marca la decisión como aplicada (`aplicado_por=OPERARIO` en SUPERVISION,
  `MPC` en AUTOMATICO) y actualiza los setpoints aplicados del lote.
- Es idempotente: un reenvío del mismo ACK vuelve a responder `200`.

## 7. SSE y estado real del horno

- `GET /api/v1/lotes/{id}/control/events` emite `control.propuesta` filtrado por
  lote. El evento incluye `lote_id`, `propuesta`, `decision_id`, `autorizada` y
  `ventana`. La `propuesta` serializa `temp1`, `velocidad`, `z` y `prediccion`.
- `GET /api/v1/horno/events` emite `horno.consigna` con el resultado de cada
  despacho.
- `GET /api/v1/horno?id=horno-01` devuelve el estado **en memoria** del horno
  (temperatura, velocidad, producto/lote y estado). Se actualiza con las
  consignas aplicadas exitosamente.
- La telemetría real (T1, combustión, humedad, ambiente, velocidad y setpoints
  observados) llega por `POST /api/v1/lotes/{id}/metricas-horno`; el panel la lee
  con `GET /api/v1/lotes/{id}/metricas-horno`.

## 8. Fallo de enlace

Si el stub interno (`SimulatedOvenController.SendSetpoint`) devuelve
`Aplicada=false` —con el stub actual siempre acepta, así que este camino queda
como salvaguarda—:

1. El intento se audita en `historial_consignas` con `exitosa=false` y
   `motivo_error`.
2. El horno pasa a `CONTROL_MANUAL`.
3. Se genera una alerta `CRITICAL` (`CONSIGNA_ENLACE`) visible en
   `GET /api/v1/horno` (`alertas_recientes`) y en `GET /api/v1/alertas`.
4. Un despacho exitoso posterior restaura `Estado="ACTIVO"`.

En el código actual no hay un bloqueo explícito por `CONTROL_MANUAL` en los
endpoints de consigna: el estado se usa como señal operativa y el loop sigue
operando. La vía de escape manual (`POST /api/v1/horno/consigna`) sigue
disponible.

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
    "hornoId": "horno-01",
    "productoId": "a1b2c3d4-5678-90ab-cdef-1234567890ab",
    "temperaturaObjetivo": 195,
    "velocidadCintaObjetivo": 0.22,
    "usuario": "operario-demo",
    "loteId": "<loteId opcional>"
  }'
```

Esperado: `200`, `origen: "MANUAL"`. Un valor fuera del rango del producto
responde `422`.

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
`modelo_no_entrenado` y no se aplica.

### 9.6 Verificar auditoría

```bash
docker compose exec db psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c \
  "SELECT horno_id, lote_id, origen, exitosa, motivo_error, creada_en
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
- El controlador físico era simulado en SCA-142. Hoy el backend usa un stub
  interno y la Pi ENTRADA es la única que escribe el PLC real; el driver Modbus
  del backend fue eliminado.

Diferencias con el presente:

- `POST /api/v1/lotes/inicio` ya no dispara consigna.
- La autenticación de nodos es `Authorization: Bearer <secret>`; no existe el
  header de API key compartido.
- El MPC no depende exclusivamente de `temp_setpoint` del producto: propone
  setpoints por ventana y usa el setpoint del producto sólo como receta de
  arranque cuando el lote no tiene setpoints propios.
- La consigna ya no es manual-only: conviven `MANUAL`, `SUPERVISION` y
  `AUTOMATICO`, con el MPC de SCA-400 gobernando los dos últimos.

## 11. Referencias

- [`api-endpoints.md`](api-endpoints.md) — contrato HTTP de consigna, control,
  propuesta, aprobación, ACK y telemetría.
- [`../CLAUDE.md`](../CLAUDE.md) — estado y decisiones de arquitectura.
- [`../MANUAL_INSTALACION.md`](../MANUAL_INSTALACION.md) — instalación,
  variables y despliegue.
- [`../cmd/predictor_svc/README.md`](../cmd/predictor_svc/README.md) — sidecar
  predictor y entrenamiento.
