# Referencia de la API — Smart-Check Automation Backend

Documentación de **todos** los endpoints HTTP expuestos por el backend, con su propósito, contrato exacto y un ejemplo real. Los ejemplos usan `curl` contra `http://localhost:8080` (o el `PORT` configurado).

## Convenciones generales

### Envelope de respuesta estándar

Casi todos los endpoints (excepto SSE, `/`, `/health`) devuelven este formato (`pkg/response/response.go`):

```json
{ "success": true, "message": "texto descriptivo", "data": { ... }, "errors": null }
```

Los endpoints paginados (`GET /api/v1/lotes-productivos`, `GET /api/v1/dispositivos/metricas`) devuelven `total`, `page` y `pageSize` como campos de primer nivel (no anidados en `data`):

```json
{ "success": true, "message": "...", "data": [ ... ], "total": 42, "page": 1, "pageSize": 10 }
```

### Autenticación

Solo los endpoints de telemetría/transacción llamados por la Raspberry requieren `X-API-Key` (comparación a prueba de timing attacks con `crypto/subtle`): `POST /api/v1/lotes`, `POST /api/v1/lotes/inicio` y `POST /api/v1/dispositivos/ping`. Las lecturas de dispositivos (`GET /api/v1/dispositivos`, `GET /api/v1/dispositivos/metricas`) y los tres endpoints SSE requieren la cookie JWT `session_token`, sin restricción de rol. El resto conserva las reglas de autenticación indicadas en cada endpoint.

El proxy del frontend debe reenviar la cookie HttpOnly `session_token` en esas lecturas y mantenerla en la conexión SSE; no debe enviar `X-API-Key` para reemplazar la autenticación JWT.

### Errores de validación

Todos los `Validate()` de dominio acumulan **todos** los campos inválidos y los devuelven como un único string en `errors`, separados por `"; "` — no como array estructurado.

---

## Índice

| # | Endpoint | Qué hace |
|---|---|---|
| 1 | `GET /api/v1/horno` | Consultar estado actual del horno |
| 2 | `POST /api/v1/horno/temperatura` | Actualizar temperatura del horno (umbrales/alertas) |
| 3 | `POST /api/v1/lotes` | Persistir un lote productivo ya finalizado |
| 4 | `POST /api/v1/lotes/inicio` | Disparar consigna automática al iniciar un lote (IA) |
| 5 | `GET /api/v1/lotes-productivos` | Listar/consultar lotes productivos (paginado) |
| 6 | `GET /api/v1/parametros-producto` | Listar parámetros de control por producto |
| 7 | `POST /api/v1/parametros-producto` | Alta de parámetros para un producto |
| 8 | `PUT /api/v1/parametros-producto` | Modificar parámetros de un producto |
| 9 | `POST /api/v1/dispositivos/ping` | Recibir telemetría de una Raspberry Pi |
| 10 | `GET /api/v1/dispositivos` | Estado online/offline de todos los dispositivos |
| 11 | `GET /api/v1/dispositivos/metricas` | Historial de métricas de un dispositivo (paginado) |
| 12 | `POST /api/v1/horno/consigna` | Envío manual de consigna térmica al horno |
| 13 | `GET /api/v1/horno/consigna/historial` | Historial de auditoría de consignas por lote |
| 14 | `GET /api/v1/lotes-productivos/events` | SSE: nuevos lotes creados |
| 15 | `GET /api/v1/dispositivos/events` | SSE: telemetría de dispositivos en tiempo real |
| 16 | `GET /api/v1/horno/events` | SSE: consignas despachadas al horno |
| 17 | `GET /health`, `GET /healthz`, `GET /` | Salud del servicio / info |

---

## 1. `GET /api/v1/horno` — Consultar estado del horno

Devuelve el estado activo del horno (temperatura, velocidad de cinta, producto/lote en curso) más sus alertas recientes. Internamente también dispara una inspección visual simulada de la cinta transportadora (IA de defectos).

**Auth:** ninguna. **Query param:** `id` (requerido).

```bash
curl "http://localhost:8080/api/v1/horno?id=horno-01"
```

```json
{
  "success": true,
  "message": "Estado de Horno verificado exitosamente",
  "data": {
    "horno": {
      "id": "horno-01", "nombre": "Horno Rotativo de Clinkerización A-1",
      "temperatura": 170, "velocidad_cinta": 0.2,
      "producto_id": "a1b2c3d4-5678-90ab-cdef-1234567890ab", "lote_id": "1d1e9071-...",
      "estado": "ACTIVO", "ultimo_check": "2026-08-06T12:00:00Z"
    },
    "alertas_recientes": [],
    "conveyor_checked": true,
    "saludo": "Hola Mundo desde el controlador de Horno en Arquitectura de Capas Go!"
  }
}
```

| Código | Motivo |
|---|---|
| 405 | Método distinto de GET |
| 400 | Falta el query param `id` |
| 404 | No existe un horno con ese `id` |

---

## 2. `POST /api/v1/horno/temperatura` — Actualizar temperatura del horno

Endpoint "legacy" (previo a SCA-142/320) que actualiza la temperatura del horno con lógica de umbrales hardcodeada: `>200°C` → alerta CRITICAL + `MANTENIMIENTO`; `>180°C` → alerta WARNING + `ATENCION`; si no, `ACTIVO`. No usa `parametros_producto`.

**Auth:** ninguna. Sin `Content-Type` ni límite de tamaño de body (a diferencia de casi todos los demás POST).

```bash
curl -X POST http://localhost:8080/api/v1/horno/temperatura \
  -H "Content-Type: application/json" \
  -d '{"id":"horno-01","temperatura":195.0}'
```

```json
{
  "success": true,
  "message": "Temperatura del horno actualizada transaccionalmente",
  "data": {
    "horno": { "id": "horno-01", "temperatura": 195, "estado": "ATENCION", "...": "..." },
    "ultima_alerta": { "id": "...", "horno_id": "horno-01", "nivel": "WARNING", "mensaje": "...", "creada_en": "..." },
    "alertas_totales": 3
  }
}
```

| Código | Motivo |
|---|---|
| 405 | Método distinto de POST |
| 400 | JSON inválido o `id` ausente |
| 500 | Error interno del servicio |

---

## 3. `POST /api/v1/lotes` — Persistir un lote productivo

Lo llama la Raspberry Pi **cuando el lote ya terminó** (trae `inicioAt` y `finAt` juntos, con los conteos finales). Persiste en PostgreSQL y dispara un evento SSE `lote.created` en `/api/v1/lotes-productivos/events`.

**Auth:** `X-API-Key`. `Content-Type: application/json` obligatorio. Body máx. 1 MB.

```bash
curl -X POST http://localhost:8080/api/v1/lotes \
  -H "Content-Type: application/json" -H "X-API-Key: $API_KEY" \
  -d '{
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "productoId": "a1b2c3d4-5678-90ab-cdef-1234567890ab",
    "turno": "mañana",
    "inicioAt": "2026-08-06T08:00:00Z",
    "finAt": "2026-08-06T09:00:00Z",
    "totalUnidades": 1200,
    "correctos": 1150,
    "quemados": 50,
    "crudas": null,
    "correctosKg": 138.00,
    "quemadosKg": 6.00,
    "crudosKg": null,
    "tempHorno1": 175.5,
    "velocidadCinta": 0.20
  }'
```

Respuesta `201 Created` con el `Lote` guardado (JSON en `snake_case`, distinto del request que es `camelCase`):

```json
{
  "success": true, "message": "Lote creado exitosamente",
  "data": { "id": "550e8400-...", "producto_id": "a1b2c3d4-...", "turno": "mañana", "total_unidades": 1200, "correctos": 1150, "quemados": 50, "...": "..." },
  "errors": null
}
```

| Código | Motivo |
|---|---|
| 405 | Método distinto de POST |
| 415 | `Content-Type` distinto de `application/json` |
| 401 | `X-API-Key` ausente o inválida |
| 400 | JSON inválido o body > 1 MB |
| 422 | Validación de negocio falló (ver abajo) |
| 500 | Error al persistir en PostgreSQL |

**Reglas de validación:** `productoId` requerido · `turno` ∈ {`mañana`,`tarde`,`noche`} · `finAt >= inicioAt` · `totalUnidades/correctos/quemados/crudas >= 0` · **`correctos + quemados + crudas <= totalUnidades`** · pesos ≥ 0.

---

## 4. `POST /api/v1/lotes/inicio` — Iniciar lote (consigna automática) — SCA-142

Lo llama la Raspberry Pi **apenas la IA identifica el producto**, antes de que el lote termine. Dispara el despacho automático de consigna (temperatura + velocidad de cinta) al controlador del horno. No crea fila en `lotes_productivos` — genera un `loteId` de correlación.

**Auth:** `X-API-Key` (mismo esquema que #3). `Content-Type: application/json` obligatorio.

```bash
curl -X POST http://localhost:8080/api/v1/lotes/inicio \
  -H "Content-Type: application/json" -H "X-API-Key: $API_KEY" \
  -d '{"hornoId":"horno-01","productoId":"a1b2c3d4-5678-90ab-cdef-1234567890ab"}'
```

```json
{
  "success": true,
  "message": "Lote iniciado: consigna despachada al horno",
  "data": {
    "loteId": "1d1e9071-e707-4821-b089-10d4a404d023",
    "consigna": {
      "id": "e7727358-...", "hornoId": "horno-01", "loteId": "1d1e9071-...",
      "productoId": "a1b2c3d4-...", "temperaturaObjetivo": 170, "velocidadCintaObjetivo": 0.2,
      "origen": "AUTOMATICO", "exitosa": true,
      "temperaturaPrevia": 185.3, "velocidadCintaPrevia": 0.2, "creadaEn": "2026-08-06T12:00:00Z"
    }
  }
}
```

| Código | Motivo |
|---|---|
| 405 | Método distinto de POST |
| 415 | `Content-Type` inválido |
| 401 | `X-API-Key` ausente o inválida |
| 400 | JSON inválido |
| 422 | Falta `hornoId`/`productoId`, o el producto no tiene `temp_setpoint`/`velocidad_cinta_setpoint` cargados |
| 404 | El horno indicado no existe |
| 409 | El horno está en `CONTROL_MANUAL` (fallo de enlace previo sin resolver) |
| 502 | El controlador físico rechazó la consigna (queda auditado igual, `data` trae el registro fallido) |
| 500 | Error interno |

---

## 5. `GET /api/v1/lotes-productivos` — Listar lotes productivos

Lectura paginada de lotes ya persistidos (JOIN con `productos` para el nombre). Soporta filtro opcional por producto.

**Auth:** ninguna. **Query params (todos opcionales):** `productoId`, `page` (default 1), `pageSize` (default 10, máx. 100).

```bash
curl "http://localhost:8080/api/v1/lotes-productivos?productoId=a1b2c3d4-5678-90ab-cdef-1234567890ab&page=1&pageSize=10"
```

```json
{
  "success": true, "message": "Lotes productivos obtenidos exitosamente",
  "data": [
    { "id": "550e8400-...", "productoId": "a1b2c3d4-...", "productoNombre": "Tostada Integral", "turno": "mañana", "totalUnidades": 1200, "correctos": 1150, "...": "..." }
  ],
  "total": 1, "page": 1, "pageSize": 10
}
```

| Código | Motivo |
|---|---|
| 405 | Método distinto de GET |
| 404 | `productoId` indicado no existe en el catálogo |
| 500 | Error interno |

---

## 6. `GET /api/v1/parametros-producto` — Listar parámetros de control por producto

Devuelve el ABM completo: rangos de temperatura/velocidad y setpoints puntuales por producto (JOIN con `productos`).

**Auth:** ninguna (TODO: restringir a Supervisor con OAuth).

```bash
curl http://localhost:8080/api/v1/parametros-producto
```

```json
{
  "success": true, "message": "Parámetros por producto obtenidos exitosamente",
  "data": [
    {
      "id": "pp-1", "productoId": "a1b2c3d4-...", "productoNombre": "Tostada Integral",
      "pesoReferenciaKg": 0.030, "toleranciaPesoPct": 10.00,
      "dimensionBaseCm": 8.00, "toleranciaDimensionCm": 0.50,
      "tempMin": 160.00, "tempMax": 180.00, "tempSetpoint": 170.00,
      "velocidadCintaMin": 0.10, "velocidadCintaMax": 0.30, "velocidadCintaSetpoint": 0.20,
      "activo": true, "createdAt": "...", "updatedAt": "..."
    }
  ]
}
```

| Código | Motivo |
|---|---|
| 405 | Método distinto de GET/POST/PUT (los tres endpoints #6/7/8 comparten un único `Handle()` que despacha por `r.Method`) |
| 500 | Error interno |

---

## 7. `POST /api/v1/parametros-producto` — Alta de parámetros para un producto

Da de alta el set de umbrales de control para un producto que todavía no lo tiene (relación 1:1). No acepta `tempSetpoint`/`velocidadCintaSetpoint` en el body — esos se cargan aparte.

**Auth:** ninguna. `Content-Type: application/json` obligatorio.

```bash
curl -X POST http://localhost:8080/api/v1/parametros-producto \
  -H "Content-Type: application/json" \
  -d '{
    "productoId": "b2c3d4e5-6789-01ab-cdef-234567890abc",
    "pesoReferenciaKg": 0.5, "toleranciaPesoPct": 8.0,
    "dimensionBaseCm": 25.0, "toleranciaDimensionCm": 1.0,
    "tempMin": 180.0, "tempMax": 200.0,
    "velocidadCintaMin": 0.15, "velocidadCintaMax": 0.35
  }'
```

Respuesta `201 Created` con el registro creado (`activo: true` por defecto).

| Código | Motivo |
|---|---|
| 415 | `Content-Type` inválido |
| 400 | JSON inválido |
| 422 | Validación falló (ver reglas abajo), o `productoId` no existe en el catálogo (FK) |
| 409 | El producto ya tiene un set de parámetros cargado |
| 500 | Error interno |

**Reglas de validación:** `productoId` requerido · `pesoReferenciaKg > 0` · `toleranciaPesoPct >= 0` · `dimensionBaseCm > 0` · `toleranciaDimensionCm >= 0` · `tempMax > tempMin` · `velocidadCintaMax > velocidadCintaMin`.

---

## 8. `PUT /api/v1/parametros-producto` — Modificar parámetros de un producto

Actualiza el set existente. El producto a modificar se identifica por `productoId` **en el body**, no en la URL (mismo endpoint que el GET/POST, dispatch por `r.Method`).

**Auth:** ninguna. Mismas reglas de Content-Type/validación que el POST.

```bash
curl -X PUT http://localhost:8080/api/v1/parametros-producto \
  -H "Content-Type: application/json" \
  -d '{
    "productoId": "a1b2c3d4-5678-90ab-cdef-1234567890ab",
    "pesoReferenciaKg": 0.035, "toleranciaPesoPct": 10.0,
    "dimensionBaseCm": 8.0, "toleranciaDimensionCm": 0.5,
    "tempMin": 165.0, "tempMax": 185.0,
    "velocidadCintaMin": 0.10, "velocidadCintaMax": 0.30
  }'
```

Respuesta `200 OK` con el registro actualizado.

| Código | Motivo |
|---|---|
| 415 | `Content-Type` inválido |
| 400 | JSON inválido |
| 422 | Validación falló |
| 404 | No existe un set de parámetros para ese `productoId` |
| 500 | Error interno |

---

## 9. `POST /api/v1/dispositivos/ping` — Telemetría de Raspberry Pi

Cada Raspberry Pi manda esto cada ~10s con su estado de salud (CPU/RAM/almacenamiento/temperatura del chip). Actualiza el caché de estado online/offline en memoria, persiste el historial en Postgres (fire-and-forget) y emite SSE.

**Auth:** `X-API-Key`. `Content-Type: application/json` obligatorio.

```bash
curl -X POST http://localhost:8080/api/v1/dispositivos/ping \
  -H "Content-Type: application/json" -H "X-API-Key: $API_KEY" \
  -d '{"dispositivoId":"b1c2d3e4-5678-90ab-cdef-1234567890ab","cpuPct":35.2,"memRamDisponibleMb":512.0,"memRamTotalMb":1024.0,"almacenamientoDisponibleMb":20000.0,"almacenamientoTotalMb":64000.0,"tempChip":45.5,"aiProcessorPct":42.0}'
```

```json
{
  "success": true, "message": "Ping recibido correctamente",
  "data": {
    "dispositivoId": "b1c2d3e4-...", "nombre": "Raspberry Pi Horno 1", "ubicacion": "Línea A",
    "estado": "online",
    "ultimaMetrica": { "cpuPct": 35.2, "memRamDisponibleMb": 512.0, "memRamTotalMb": 1024.0, "almacenamientoDisponibleMb": 20000.0, "almacenamientoTotalMb": 64000.0, "tempChip": 45.5, "aiProcessorPct": 42.0, "receivedAt": "..." },
    "lastSeen": "2026-08-06T12:00:00Z"
  }
}
```

| Código | Motivo |
|---|---|
| 405 | Método distinto de POST |
| 415 | `Content-Type` inválido |
| 401 | `X-API-Key` ausente o inválida |
| 400 | JSON inválido |
| 422 | Validación de negocio falló, o `dispositivoId` no existe en el catálogo |
| 500 | Error interno |

**Reglas de validación:** `dispositivoId` requerido y UUID válido · `cpuPct` ∈ [0,100] · `memRamDisponibleMb >= 0` · `memRamTotalMb` opcional y >= 0; si se informa, `memRamDisponibleMb <= memRamTotalMb` · `almacenamientoDisponibleMb` y `almacenamientoTotalMb` forman un par opcional: deben omitirse ambos o informarse ambos, ser >= 0 y cumplir `almacenamientoDisponibleMb <= almacenamientoTotalMb` · `tempChip` ∈ [-40,120] · `aiProcessorPct` ∈ [0,100]. Los tres campos nuevos pueden omitirse para mantener compatibilidad con pings legacy.

---

## 10. `GET /api/v1/dispositivos` — Estado online/offline de todos los dispositivos

Lectura desde el caché en memoria (no toca Postgres) — rápida, para refrescar el panel.
La misma ruta para `POST`, `PUT` y `DELETE` administra el catálogo y también requiere JWT, sin restricción de rol.

**Auth:** cookie JWT `session_token` obligatoria; cualquier rol autenticado.

```bash
curl -b "session_token=$JWT" http://localhost:8080/api/v1/dispositivos
```

```json
{
  "success": true, "message": "Estados de dispositivos obtenidos exitosamente",
  "data": [
    { "dispositivoId": "b1c2d3e4-...", "nombre": "Raspberry Pi Horno 1", "ubicacion": "Línea A", "estado": "online", "ultimaMetrica": { "...": "..." }, "lastSeen": "..." }
  ]
}
```

El objeto `ultimaMetrica` usa el contrato de telemetría descrito en #9: además de `memRamDisponibleMb`, CPU, IA y temperatura, puede incluir `memRamTotalMb`, `almacenamientoDisponibleMb` y `almacenamientoTotalMb`. Los campos nuevos se omiten cuando el dispositivo todavía envía un ping legacy.

| Código | Motivo |
|---|---|
| 405 | Método distinto de GET |
| 401 | Falta la cookie JWT o la sesión es inválida/expirada |

---

## 11. `GET /api/v1/dispositivos/metricas` — Historial de métricas de un dispositivo

Historial paginado desde Postgres (`metricas_dispositivo`), no el caché.

**Auth:** cookie JWT `session_token` obligatoria; cualquier rol. **Query params:** `dispositivoId` (requerido), `page`/`pageSize` (igual que #5).

```bash
curl -b "session_token=$JWT" "http://localhost:8080/api/v1/dispositivos/metricas?dispositivoId=b1c2d3e4-5678-90ab-cdef-1234567890ab&page=1&pageSize=20"
```

```json
{
  "success": true, "message": "Métricas del dispositivo obtenidas exitosamente",
  "data": [ { "id": "...", "dispositivoId": "b1c2d3e4-...", "cpuPct": 35.2, "memRamDisponibleMb": 512.0, "memRamTotalMb": 1024.0, "almacenamientoDisponibleMb": 20000.0, "almacenamientoTotalMb": 64000.0, "tempChip": 45.5, "aiProcessorPct": 42.0, "receivedAt": "..." } ],
  "total": 1, "page": 1, "pageSize": 20
}
```

| Código | Motivo |
|---|---|
| 405 | Método distinto de GET |
| 400 | Falta `dispositivoId` |
| 401 | Falta la cookie JWT o la sesión es inválida/expirada |
| 404 | `dispositivoId` no existe en el catálogo |
| 500 | Error interno |

---

## 12. `POST /api/v1/horno/consigna` — Envío manual de consigna térmica — SCA-320

Un operario carga a mano temperatura y velocidad de cinta desde el panel. Se valida contra el rango seguro `[temp_min, temp_max]`/`[velocidad_cinta_min, velocidad_cinta_max]` del producto (no contra el setpoint puntual, que es el que usa el flujo automático).

**Auth:** ninguna (TODO: OAuth + rol Supervisor/Operario). `Content-Type: application/json` obligatorio.

```bash
curl -X POST http://localhost:8080/api/v1/horno/consigna \
  -H "Content-Type: application/json" \
  -d '{
    "hornoId": "horno-01",
    "productoId": "a1b2c3d4-5678-90ab-cdef-1234567890ab",
    "temperaturaObjetivo": 175,
    "velocidadCintaObjetivo": 0.25,
    "usuario": "operario-demo",
    "loteId": "1d1e9071-e707-4821-b089-10d4a404d023"
  }'
```

```json
{
  "success": true, "message": "Consigna manual despachada al horno",
  "data": {
    "id": "0cd37ba5-...", "hornoId": "horno-01", "loteId": "1d1e9071-...", "productoId": "a1b2c3d4-...",
    "temperaturaObjetivo": 175, "velocidadCintaObjetivo": 0.25, "origen": "MANUAL",
    "usuario": "operario-demo", "exitosa": true,
    "temperaturaPrevia": 170, "velocidadCintaPrevia": 0.2, "creadaEn": "2026-08-06T12:00:00Z"
  },
  "errors": null
}
```

`usuario` y `loteId` son opcionales; `productoId` es **obligatorio** (siempre valida contra datos reales, sin límites hardcodeados).

| Código | Motivo |
|---|---|
| 405 | Método distinto de POST |
| 415 | `Content-Type` inválido |
| 400 | JSON inválido |
| 422 | Validación de forma falló, el producto no tiene parámetros cargados, o los valores están fuera del rango seguro |
| 404 | El horno indicado no existe |
| 502 | El controlador físico rechazó la consigna (queda auditado; el horno pasa a `CONTROL_MANUAL` + se genera una alerta CRITICAL) |
| 500 | Error interno |

---

## 13. `GET /api/v1/horno/consigna/historial` — Historial de auditoría por lote

Devuelve todas las consignas (automáticas y manuales) asociadas a un `loteId`, ordenadas de más reciente a más antigua.

**Auth:** ninguna. **Query param:** `loteId` (requerido).

```bash
curl "http://localhost:8080/api/v1/horno/consigna/historial?loteId=1d1e9071-e707-4821-b089-10d4a404d023"
```

```json
{
  "success": true, "message": "Historial de consignas obtenido exitosamente",
  "data": [
    { "id": "0cd37ba5-...", "origen": "MANUAL", "temperaturaObjetivo": 175, "exitosa": true, "creadaEn": "..." },
    { "id": "e7727358-...", "origen": "AUTOMATICO", "temperaturaObjetivo": 170, "exitosa": true, "creadaEn": "..." }
  ]
}
```

| Código | Motivo |
|---|---|
| 405 | Método distinto de GET |
| 400 | Falta `loteId` |
| 500 | Error interno |

---

## 14–16. Endpoints SSE (Server-Sent Events)

Los tres comparten el mismo handler genérico, cada uno con su propio broker (sin cruce de eventos entre sí). Requieren la cookie JWT `session_token` y aceptan cualquier rol autenticado. No usan el envelope JSON estándar — son streams `text/event-stream`.

| Endpoint | Eventos que emite |
|---|---|
| `GET /api/v1/lotes-productivos/events` | `lote.created` — cuando se persiste un lote (#3) |
| `GET /api/v1/dispositivos/events` | `dispositivo.metric` (cada ping) y `dispositivo.state` (transición online↔offline) |
| `GET /api/v1/horno/events` | `horno.consigna` — cada vez que se despacha una consigna (automática o manual, exitosa o fallida) |

El evento `dispositivo.metric` contiene el mismo envelope y estado que el ping REST. En `data.ultimaMetrica`, los campos `memRamTotalMb`, `almacenamientoDisponibleMb` y `almacenamientoTotalMb` aparecen cuando fueron informados por la Raspberry; los pings legacy los omiten.

```bash
curl -N -b "session_token=$JWT" http://localhost:8080/api/v1/horno/events
```

```
: connected

event: horno.consigna
data: {"success":true,"message":"Consigna despachada al horno","data":{"id":"...","origen":"MANUAL","exitosa":true,"...":"..."}}

: heartbeat

```

Se manda un comentario `: heartbeat` cada 30s para mantener la conexión viva. La conexión se corta cuando el cliente se desconecta.

| Código | Motivo |
|---|---|
| 405 (texto plano, no JSON) | Método distinto de GET |
| 401 (JSON) | Falta la cookie JWT o la sesión es inválida/expirada |
| 500 (texto plano) | El cliente HTTP no soporta streaming |

---

## 17. Salud del servicio

**`GET /health`, `GET /healthz`** — hacen `ping` a PostgreSQL con timeout de 2s.

```bash
curl http://localhost:8080/health
```
```json
{ "status": "healthy" }
```
Si la DB no responde: `503` con `{ "status": "unhealthy", "reason": "database unreachable" }` (JSON simple, no el envelope estándar).

**`GET /`** — página de texto plano con la lista de endpoints disponibles (informativa, sin JSON).

---

## Tabla resumen: auth y validaciones por endpoint

| Endpoint | Auth | Content-Type obligatorio | Límite body 1MB |
|---|---|---|---|
| `POST /api/v1/lotes` | X-API-Key | ✅ | ✅ |
| `POST /api/v1/lotes/inicio` | X-API-Key | ✅ | ✅ |
| `POST /api/v1/dispositivos/ping` | X-API-Key | ✅ | ✅ |
| `POST/PUT/DELETE /api/v1/dispositivos` | JWT cookie (`session_token`) | Según método | Según método |
| `POST /api/v1/horno/temperatura` | — | ❌ | ❌ |
| `POST/PUT /api/v1/parametros-producto` | — | ✅ | ✅ |
| `POST /api/v1/horno/consigna` | — | ✅ | ✅ |
| `GET /api/v1/dispositivos` | JWT cookie (`session_token`) | — | — |
| `GET /api/v1/dispositivos/metricas` | JWT cookie (`session_token`) | — | — |
| `GET /*/events` (los 3 SSE) | JWT cookie (`session_token`) | — | — |
| Otros `GET` | Según la sección del endpoint | — | — |
