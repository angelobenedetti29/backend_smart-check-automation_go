# Referencia de la API — Smart-Check Automation Backend

Documentación de **todos** los endpoints HTTP expuestos por el backend, con su propósito, contrato exacto y un ejemplo real. Los ejemplos usan `curl` contra `http://localhost:8080` (o el `PORT` configurado).

## Convenciones generales

### Envelope de respuesta estándar

Casi todos los endpoints (excepto SSE, `/`, `/health`) devuelven este formato (`pkg/response/response.go`):

```json
{ "success": true, "message": "texto descriptivo", "data": { ... }, "errors": null }
```

Los endpoints paginados (`GET /api/v1/lotes`, `GET /api/v1/dispositivos/metricas`) devuelven `total`, `page` y `pageSize` como campos de primer nivel (no anidados en `data`); `GET /api/v1/lotes` agrega además `siguiente_cursor` (omitido cuando no hay más páginas):

```json
{ "success": true, "message": "...", "data": [ ... ], "total": 42, "page": 1, "pageSize": 20, "siguiente_cursor": "..." }
```

### Autenticación

Hay dos esquemas de credencial, mutuamente excluyentes:

- **Usuarios del panel (OAuth):** cookie HttpOnly `session_token` emitida por `/api/v1/auth/login` o `/api/v1/auth/google`. El proxy del frontend debe reenviar esa cookie, también en las conexiones SSE.
- **Nodos Raspberry Pi:** header `Authorization: Bearer <secret>`, donde `<secret>` es el secreto por dispositivo enrolado.

Algunos endpoints son **dual-auth**: aceptan el Bearer de dispositivo o la cookie JWT. Si la petición trae cualquier header `Authorization`, se resuelve exclusivamente como intento de dispositivo (un Bearer inválido es 401 plano, sin degradar a la cookie). Sin `Authorization`, se valida la cookie. Son dual-auth: `GET /api/v1/productos`, `GET /api/v1/lotes/abierto` y `GET /api/v1/lotes`.

### Errores de validación

Todos los `Validate()` de dominio acumulan **todos** los campos inválidos y los devuelven como un único string en `errors`, separados por `"; "` — no como array estructurado. Los errores de negocio del ciclo de lotes usan `errors: { "code": "..." }`.

---

## Índice

| # | Endpoint | Qué hace |
|---|---|---|
| 1 | `GET /api/v1/horno` | Consultar estado actual del horno |
| 2 | `POST /api/v1/horno/temperatura` | Actualizar temperatura del horno (umbrales/alertas) |
| 3 | `GET /api/v1/productos` | Catálogo maestro de productos |
| 4 | `GET /api/v1/dispositivos/sector` | Sector y compañeros del dispositivo autenticado |
| 5 | `GET /api/v1/sectores` | Listar todos los sectores |
| 6 | `POST /api/v1/lotes/inicio` | Abrir (o adjuntarse a) el lote abierto del sector |
| 7 | `GET /api/v1/lotes/abierto` | Consultar el lote abierto del sector |
| 8 | `POST /api/v1/lotes/{id}/eventos` | Reportar detecciones del lote |
| 9 | `POST /api/v1/lotes/{id}/cierre` | Cerrar el lote con los conteos finales |
| 10 | `GET /api/v1/lotes` | Historial paginado de lotes por sector |
| 11 | `GET /api/v1/parametros-producto` | Listar parámetros de control por producto |
| 12 | `POST /api/v1/parametros-producto` | Alta de parámetros para un producto |
| 13 | `PUT /api/v1/parametros-producto` | Modificar parámetros de un producto |
| 14 | `POST /api/v1/dispositivos/ping` | Recibir telemetría de una Raspberry Pi |
| 15 | `GET /api/v1/dispositivos` | Estado online/offline de todos los dispositivos |
| 16 | `GET /api/v1/dispositivos/metricas` | Historial de métricas de un dispositivo (paginado) |
| 17 | `POST /api/v1/horno/consigna` | Envío manual de consigna térmica al horno |
| 18 | `GET /api/v1/horno/consigna/historial` | Historial de auditoría de consignas por lote |
| 19 | `GET /api/v1/lotes/events` | SSE: ciclo de vida de lotes (`lote.creado`/`actualizado`/`cerrado`) |
| 20 | `GET /api/v1/dispositivos/events` | SSE: telemetría de dispositivos en tiempo real |
| 21 | `GET /api/v1/horno/events` | SSE: consignas despachadas al horno |
| 22 | `GET /health`, `GET /healthz`, `GET /` | Salud del servicio / info |

---

## 1. `GET /api/v1/horno` — Consultar estado del horno

Devuelve el estado activo del horno (temperatura, velocidad de cinta, producto/lote en curso) más sus alertas recientes. Internamente también dispara una inspección visual simulada de la cinta transportadora (IA de defectos).

**Auth:** cookie JWT `session_token` (cualquier rol). **Query param:** `id` (requerido).

```bash
curl -b "session_token=$JWT" "http://localhost:8080/api/v1/horno?id=horno-01"
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
| 401 | Falta la cookie JWT o la sesión es inválida/expirada |
| 404 | No existe un horno con ese `id` |

---

## 2. `POST /api/v1/horno/temperatura` — Actualizar temperatura del horno

Endpoint "legacy" (previo a SCA-142/320) que actualiza la temperatura del horno con lógica de umbrales hardcodeada: `>200°C` → alerta CRITICAL + `MANTENIMIENTO`; `>180°C` → alerta WARNING + `ATENCION`; si no, `ACTIVO`. No usa `parametros_producto`.

**Auth:** cookie JWT `session_token` con rol Supervisor o Administrador. Sin `Content-Type` ni límite de tamaño de body (a diferencia de casi todos los demás POST).

```bash
curl -X POST http://localhost:8080/api/v1/horno/temperatura \
  -b "session_token=$JWT" -H "Content-Type: application/json" \
  -d '{"id":"horno-01","temperatura":195.0}'
```

| Código | Motivo |
|---|---|
| 405 | Método distinto de POST |
| 401 | Falta la cookie JWT o la sesión es inválida/expirada |
| 403 | Rol insuficiente (se requiere Supervisor o Administrador) |
| 400 | JSON inválido o `id` ausente |
| 500 | Error interno del servicio |

---

## 3. `GET /api/v1/productos` — Catálogo maestro de productos

Devuelve el catálogo completo, incluidos los productos inactivos (la Raspberry filtra por vigencia; el panel puebla selectores). Lo consume tanto el nodo como el panel.

**Auth:** dual-auth (Bearer de dispositivo o cookie JWT). Sin query params.

```bash
curl -H "Authorization: Bearer $DEVICE_SECRET" http://localhost:8080/api/v1/productos
```

```json
{
  "success": true, "message": "Productos obtenidos exitosamente",
  "data": [ { "id": "a1b2c3d4-5678-90ab-cdef-1234567890ab", "nombre": "Tostada Integral", "activo": true } ],
  "errors": null
}
```

| Código | Motivo |
|---|---|
| 405 | Método distinto de GET |
| 401 | Bearer de dispositivo inválido, o falta la cookie JWT / sesión expirada |
| 500 | Error interno |

---

## 4. `GET /api/v1/dispositivos/sector` — Sector del dispositivo autenticado

Devuelve el sector al que pertenece el dispositivo del Bearer, junto con sus compañeros de línea (el otro extremo ENTRADA_HORNO/SALIDA_HORNO). Es **device-only**.

**Auth:** `Authorization: Bearer <secret>`.

```bash
curl -H "Authorization: Bearer $DEVICE_SECRET" http://localhost:8080/api/v1/dispositivos/sector
```

```json
{
  "success": true, "message": "Sector del dispositivo obtenido exitosamente",
  "data": {
    "sector_id": "horno-e2e", "nombre": "Horno E2E", "tipo": "ENTRADA_HORNO",
    "companeros": [ { "device_id": "22222222-...", "hostname": "pi-salida", "type": "SALIDA_HORNO" } ]
  }
}
```

| Código | Motivo |
|---|---|
| 405 | Método distinto de GET |
| 401 | Token de dispositivo inválido (respuesta plana `invalid_device_token`) |
| 404 | El dispositivo no pertenece a ningún sector (`sin_sector`) |
| 500 | Error interno |

---

## 5. `GET /api/v1/sectores` — Listar sectores

Devuelve todos los sectores ordenados por nombre. Lo usa el panel para poblar filtros de historial.

**Auth:** cookie JWT `session_token` (cualquier rol). Sin query params.

```bash
curl -b "session_token=$JWT" http://localhost:8080/api/v1/sectores
```

```json
{
  "success": true, "message": "Sectores obtenidos exitosamente",
  "data": [ { "id": "horno-e2e", "nombre": "Horno E2E" } ],
  "errors": null
}
```

| Código | Motivo |
|---|---|
| 405 | Método distinto de GET |
| 401 | Falta la cookie JWT o la sesión es inválida/expirada |
| 500 | Error interno |

---

## 6. `POST /api/v1/lotes/inicio` — Abrir lote del sector

Lo llama la Raspberry Pi (ENTRADA_HORNO) al identificar el producto. **Abre y persiste** el lote abierto del sector; si ya hay uno, se adjunta (get-or-create). Es idempotente por `idempotency_key`. **No dispara consigna automática** (ese despacho ya no ocurre desde este endpoint).

**Auth:** `Authorization: Bearer <secret>` (device-only). `Content-Type: application/json` obligatorio. Body máx. 1 MB.

```bash
curl -X POST http://localhost:8080/api/v1/lotes/inicio \
  -H "Authorization: Bearer $DEVICE_SECRET" -H "Content-Type: application/json" \
  -d '{"idempotency_key":"0f8fad5b-d9cb-469f-a165-70867728950e","producto_id":"a1b2c3d4-5678-90ab-cdef-1234567890ab","momento":"2026-08-06T12:00:00Z"}'
```

`momento` es informativo: el servidor usa su propio reloj. Respuesta `201 Created` cuando el lote se crea y `200 OK` cuando se adjunta a uno existente:

```json
{
  "success": true, "message": "Lote creado exitosamente",
  "data": {
    "creado": true,
    "lote": {
      "id": "d1a2b3c4-d5e6-4789-8abc-def012345678", "sector_id": "horno-e2e", "estado": "ABIERTO",
      "producto_id": "a1b2c3d4-...", "producto_nombre": "Tostada Integral",
      "abierto_en": "2026-08-06T12:00:00Z", "abierto_por": { "device_id": "11111111-...", "type": "ENTRADA_HORNO" },
      "conteos": { "ok": null, "crudo": null, "quemado": null, "total": 0 },
      "ultimo_evento_en": null, "inactividad_segundos": 0
    }
  },
  "errors": null
}
```

| Código | Motivo |
|---|---|
| 405 | Método distinto de POST |
| 401 | Token de dispositivo inválido |
| 400 | JSON inválido o body > 1 MB |
| 404 | El dispositivo no pertenece a ningún sector (`sin_sector`) |
| 409 | La `idempotency_key` ya fue usada por un lote de otro sector (`idempotency_key_conflicto`) |
| 422 | El `producto_id` no existe en el catálogo (`producto_desconocido`) |
| 500 | Error interno |

---

## 7. `GET /api/v1/lotes/abierto` — Lote abierto del sector

Devuelve el lote ABIERTO del sector, o `lote: null` si no hay ninguno (nunca 404 por ausencia).

**Auth:** dual-auth. Un dispositivo deriva su sector del Bearer; un usuario OAuth debe enviar el query param `sector_id`.

```bash
curl -H "Authorization: Bearer $DEVICE_SECRET" http://localhost:8080/api/v1/lotes/abierto
curl -b "session_token=$JWT" "http://localhost:8080/api/v1/lotes/abierto?sector_id=horno-e2e"
```

```json
{ "success": true, "message": "Lote abierto consultado", "data": { "lote": null }, "errors": null }
```

| Código | Motivo |
|---|---|
| 405 | Método distinto de GET |
| 401 | Bearer inválido o cookie JWT ausente/expirada |
| 400 | OAuth sin `sector_id` (`payload_invalido`) |
| 404 | El dispositivo no pertenece a ningún sector (`sin_sector`) |
| 500 | Error interno |

---

## 8. `POST /api/v1/lotes/{id}/eventos` — Reportar detecciones

Lo llama la Raspberry Pi (SALIDA_HORNO) con un batch de detecciones. Deduplica por `evento_id`: un reintento del mismo batch devuelve `aceptados=0` y `duplicados=N` sin volver a contar. Emite `lote.actualizado` sólo si el batch acepta al menos un evento.

**Auth:** `Authorization: Bearer <secret>` (device-only). `Content-Type: application/json` obligatorio. Body máx. 1 MB. Máximo 100 eventos por batch.

```bash
curl -X POST "http://localhost:8080/api/v1/lotes/$LOTE/eventos" \
  -H "Authorization: Bearer $DEVICE_SECRET" -H "Content-Type: application/json" \
  -d '{"eventos":[
    {"evento_id":"c1a2b3c4-d5e6-4789-8abc-def012345678","producto_id":"a1b2c3d4-...","estado":"ok","confianza":0.95,"pista":7,"frame":1234,"modelo_id":"tostadas-v2"},
    {"evento_id":"e2b3c4d5-e6f7-4890-9abc-def012345679","producto_id":"a1b2c3d4-...","estado":"quemado","confianza":0.91}
  ]}'
```

```json
{
  "success": true, "message": "Eventos procesados exitosamente",
  "data": { "aceptados": 2, "duplicados": 0, "lote": { "...": "objeto lote actualizado" } },
  "errors": null
}
```

| Código | Motivo |
|---|---|
| 405 | Método distinto de POST |
| 401 | Token de dispositivo inválido |
| 400 | JSON inválido, batch vacío, `evento_id`/`producto_id` no-UUID, `estado` fuera de `{ok,crudo,quemado}`, `confianza` fuera de `[0,1]` (`payload_invalido`) |
| 404 | `lote_id` inexistente o no-UUID (`lote_no_encontrado`), o dispositivo sin sector (`sin_sector`) |
| 403 | El lote pertenece a otro sector (`lote_ajeno`) |
| 409 | El lote ya está cerrado (`lote_cerrado`) |
| 413 | Más de 100 eventos en el batch (`demasiados_eventos`) |
| 422 | El producto de un evento no coincide con el del lote (`producto_inconsistente`) |
| 500 | Error interno |

---

## 9. `POST /api/v1/lotes/{id}/cierre` — Cerrar el lote

Lo llama la Raspberry Pi para fijar el **conteo final autoritativo** del lote. `conteos` es obligatorio: sin él se rechaza en vez de pisar los conteos vivos. Es idempotente: un reintento devuelve 200 con el mismo estado. Si hay umbral configurado (`LOTE_CIERRE_MIN_INACTIVIDAD_SEGUNDOS`), rechaza con 409 mientras el sector siga activo.

**Auth:** `Authorization: Bearer <secret>` (device-only). `Content-Type: application/json` obligatorio.

```bash
curl -X POST "http://localhost:8080/api/v1/lotes/$LOTE/cierre" \
  -H "Authorization: Bearer $DEVICE_SECRET" -H "Content-Type: application/json" \
  -d '{"idempotency_key":"7c9e6679-7425-40de-944b-e07fc1f90ae7","motivo":"sin_detecciones","conteos":{"ok":1,"crudo":null,"quemado":1,"total":2}}'
```

```json
{
  "success": true, "message": "Lote cerrado exitosamente",
  "data": { "cerrado": true, "lote": { "estado": "CERRADO", "...": "..." } },
  "errors": null
}
```

`motivo` ∈ {`sin_detecciones`, `manual`, `apagado`, `seguridad`}. `total` debe ser la suma de los buckets no nulos.

| Código | Motivo |
|---|---|
| 405 | Método distinto de POST |
| 401 | Token de dispositivo inválido |
| 400 | JSON inválido, `conteos` ausente, `motivo` fuera del conjunto, conteos inconsistentes o negativos (`payload_invalido`) |
| 404 | `lote_id` inexistente o no-UUID (`lote_no_encontrado`), o dispositivo sin sector (`sin_sector`) |
| 403 | El lote pertenece a otro sector (`lote_ajeno`) |
| 409 | El sector todavía está activo (`sector_activo`) |
| 500 | Error interno |

---

## 10. `GET /api/v1/lotes` — Historial de lotes por sector

Devuelve una página de lotes del sector (más nuevos primero) y el total que matchea los filtros. Paginación por cursor opaco.

**Auth:** dual-auth. Un dispositivo deriva su sector del Bearer; un usuario OAuth debe enviar `sector_id`. **Query params:** `sector_id` (obligatorio con OAuth), `producto_id` (opcional, UUID), `limite` (default 20, máx. 100), `antes_de` (cursor opaco de la página anterior).

```bash
curl -H "Authorization: Bearer $DEVICE_SECRET" "http://localhost:8080/api/v1/lotes?limite=20"
curl -b "session_token=$JWT" "http://localhost:8080/api/v1/lotes?sector_id=horno-e2e&producto_id=a1b2c3d4-..."
```

```json
{
  "success": true, "message": "Lotes obtenidos exitosamente",
  "data": [ { "id": "d1a2b3c4-...", "sector_id": "horno-e2e", "estado": "CERRADO", "conteos": { "ok": 1, "crudo": null, "quemado": 1, "total": 2 }, "...": "..." } ],
  "total": 1, "page": 1, "pageSize": 20, "siguiente_cursor": "...", "errors": null
}
```

`siguiente_cursor` se omite cuando la página no está completa (no hay más resultados). Para la página anterior, reenviarlo como `antes_de`.

| Código | Motivo |
|---|---|
| 405 | Método distinto de GET |
| 401 | Bearer inválido o cookie JWT ausente/expirada |
| 400 | OAuth sin `sector_id`, `producto_id` no-UUID o cursor malformado (`payload_invalido`) |
| 404 | El dispositivo no pertenece a ningún sector (`sin_sector`) |
| 500 | Error interno |

---

## 11. `GET /api/v1/parametros-producto` — Listar parámetros de control por producto

Devuelve el ABM completo: rangos de temperatura/velocidad y setpoints puntuales por producto (JOIN con `productos`).

**Auth:** cookie JWT `session_token` (cualquier rol).

```bash
curl -b "session_token=$JWT" http://localhost:8080/api/v1/parametros-producto
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
| 405 | Método distinto de GET/POST/PUT (los tres endpoints #11/12/13 comparten un único `Handle()` que despacha por `r.Method`) |
| 401 | Falta la cookie JWT o la sesión es inválida/expirada |
| 500 | Error interno |

---

## 12. `POST /api/v1/parametros-producto` — Alta de parámetros para un producto

Da de alta el set de umbrales de control para un producto que todavía no lo tiene (relación 1:1). No acepta `tempSetpoint`/`velocidadCintaSetpoint` en el body — esos se cargan aparte.

**Auth:** cookie JWT `session_token` con rol Supervisor o Administrador. `Content-Type: application/json` obligatorio.

```bash
curl -X POST http://localhost:8080/api/v1/parametros-producto \
  -b "session_token=$JWT" -H "Content-Type: application/json" \
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
| 403 | Rol insuficiente (se requiere Supervisor o Administrador) |
| 415 | `Content-Type` inválido |
| 400 | JSON inválido |
| 422 | Validación falló (ver reglas abajo), o `productoId` no existe en el catálogo (FK) |
| 409 | El producto ya tiene un set de parámetros cargado |
| 500 | Error interno |

**Reglas de validación:** `productoId` requerido · `pesoReferenciaKg > 0` · `toleranciaPesoPct >= 0` · `dimensionBaseCm > 0` · `toleranciaDimensionCm >= 0` · `tempMax > tempMin` · `velocidadCintaMax > velocidadCintaMin`.

---

## 13. `PUT /api/v1/parametros-producto` — Modificar parámetros de un producto

Actualiza el set existente. El producto a modificar se identifica por `productoId` **en el body**, no en la URL (mismo endpoint que el GET/POST, dispatch por `r.Method`).

**Auth:** cookie JWT `session_token` con rol Supervisor o Administrador. Mismas reglas de Content-Type/validación que el POST.

```bash
curl -X PUT http://localhost:8080/api/v1/parametros-producto \
  -b "session_token=$JWT" -H "Content-Type: application/json" \
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
| 403 | Rol insuficiente |
| 415 | `Content-Type` inválido |
| 400 | JSON inválido |
| 422 | Validación falló |
| 404 | No existe un set de parámetros para ese `productoId` |
| 500 | Error interno |

---

## 14. `POST /api/v1/dispositivos/ping` — Telemetría de Raspberry Pi

Cada Raspberry Pi manda esto cada ~10s con su estado de salud (CPU/RAM/almacenamiento/temperatura del chip). Actualiza el caché de estado online/offline en memoria, persiste el historial en Postgres (fire-and-forget) y emite SSE.

**Auth:** `Authorization: Bearer <secret>` (device-only). `Content-Type: application/json` obligatorio.

```bash
curl -X POST http://localhost:8080/api/v1/dispositivos/ping \
  -H "Authorization: Bearer $DEVICE_SECRET" -H "Content-Type: application/json" \
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
| 401 | Token de dispositivo inválido |
| 400 | JSON inválido |
| 422 | Validación de negocio falló, o `dispositivoId` no existe en el catálogo |
| 500 | Error interno |

**Reglas de validación:** `dispositivoId` requerido y UUID válido · `cpuPct` ∈ [0,100] · `memRamDisponibleMb >= 0` · `memRamTotalMb` opcional y >= 0; si se informa, `memRamDisponibleMb <= memRamTotalMb` · `almacenamientoDisponibleMb` y `almacenamientoTotalMb` forman un par opcional: deben omitirse ambos o informarse ambos, ser >= 0 y cumplir `almacenamientoDisponibleMb <= almacenamientoTotalMb` · `tempChip` ∈ [-40,120] · `aiProcessorPct` ∈ [0,100].

---

## 15. `GET /api/v1/dispositivos` — Estado online/offline de todos los dispositivos

Lectura desde el caché en memoria (no toca Postgres) — rápida, para refrescar el panel. La misma ruta para `PUT` administra el catálogo (requiere JWT con rol Supervisor/Admin).

**Auth:** cookie JWT `session_token` obligatoria; cualquier rol autenticado.

```bash
curl -b "session_token=$JWT" http://localhost:8080/api/v1/dispositivos
```

```json
{
  "success": true, "message": "Estados de dispositivos obtenidos exitosamente",
  "data": [
    { "dispositivoId": "b1c2d3e4-...", "nombre": "Raspberry Pi Horno 1", "ubicacion": "Línea A", "whepUrl": "https://camaras.example.com/whep/horno-1", "estado": "online", "ultimaMetrica": { "...": "..." }, "lastSeen": "..." }
  ]
}
```

El objeto `ultimaMetrica` usa el contrato de telemetría descrito en #14. El campo `whepUrl` es la fuente de verdad de la cámara/stream WHEP del dispositivo: es opcional y nullable; se omite cuando el dispositivo no tiene cámara configurada.

**Modificación (`PUT`):** el body acepta `whepUrl` (string opcional). Si se omite o se envía vacío, el dispositivo queda sin stream (`NULL`). Si se informa, debe ser una URL absoluta `http`/`https` de hasta 500 caracteres; en caso contrario la validación devuelve 422.

```json
{ "nombre": "Raspberry Pi Horno 1", "ubicacion": "Línea A", "whepUrl": "https://camaras.example.com/whep/horno-1" }
```

| Código | Motivo |
|---|---|
| 405 | Método distinto de GET/PUT |
| 401 | Falta la cookie JWT o la sesión es inválida/expirada |
| 403 | Rol insuficiente en PUT |
| 415 | `Content-Type` inválido en PUT |
| 400 | JSON inválido en PUT |
| 422 | Validación falló (incluye `whepUrl` no absoluta o mayor a 500 caracteres) |
| 404 | `dispositivoId` no existe en el catálogo (PUT) |
| 500 | Error interno |

---

## 16. `GET /api/v1/dispositivos/metricas` — Historial de métricas de un dispositivo

Historial paginado desde Postgres (`metricas_dispositivo`), no el caché.

**Auth:** cookie JWT `session_token` obligatoria; cualquier rol. **Query params:** `dispositivoId` (requerido), `page`/`pageSize` (default 1/10, máx. 100).

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

## 17. `POST /api/v1/horno/consigna` — Envío manual de consigna térmica — SCA-320

Un operario carga a mano temperatura y velocidad de cinta desde el panel. Se valida contra el rango seguro `[temp_min, temp_max]`/`[velocidad_cinta_min, velocidad_cinta_max]` del producto (no contra el setpoint puntual, que es el que usa el flujo automático).

**Auth:** cookie JWT `session_token` con rol Operario, Supervisor o Administrador. `Content-Type: application/json` obligatorio.

```bash
curl -X POST http://localhost:8080/api/v1/horno/consigna \
  -b "session_token=$JWT" -H "Content-Type: application/json" \
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

`usuario` y `loteId` son opcionales; `productoId` es **obligatorio**.

| Código | Motivo |
|---|---|
| 405 | Método distinto de POST |
| 401 | Falta la cookie JWT o la sesión es inválida/expirada |
| 403 | Rol insuficiente |
| 415 | `Content-Type` inválido |
| 400 | JSON inválido |
| 422 | Validación de forma falló, el producto no tiene parámetros cargados, o los valores están fuera del rango seguro |
| 404 | El horno indicado no existe |
| 502 | El controlador físico rechazó la consigna (queda auditado; el horno pasa a `CONTROL_MANUAL` + se genera una alerta CRITICAL) |
| 500 | Error interno |

---

## 18. `GET /api/v1/horno/consigna/historial` — Historial de auditoría por lote

Devuelve todas las consignas (automáticas y manuales) asociadas a un `loteId`, ordenadas de más reciente a más antigua.

**Auth:** cookie JWT `session_token` (cualquier rol). **Query param:** `loteId` (requerido).

```bash
curl -b "session_token=$JWT" "http://localhost:8080/api/v1/horno/consigna/historial?loteId=1d1e9071-e707-4821-b089-10d4a404d023"
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
| 401 | Falta la cookie JWT o la sesión es inválida/expirada |
| 500 | Error interno |

---

## 19–21. Endpoints SSE (Server-Sent Events)

Los tres comparten el mismo handler genérico, cada uno con su propio broker (sin cruce de eventos entre sí). Requieren la cookie JWT `session_token` y aceptan cualquier rol autenticado. No usan el envelope JSON estándar — son streams `text/event-stream`.

| Endpoint | Eventos que emite |
|---|---|
| `GET /api/v1/lotes/events` | `lote.creado`, `lote.actualizado` y `lote.cerrado` — el payload es el objeto lote crudo |
| `GET /api/v1/dispositivos/events` | `dispositivo.metric` (cada ping) y `dispositivo.state` (transición online↔offline) |
| `GET /api/v1/horno/events` | `horno.consigna` — cada vez que se despacha una consigna (automática o manual, exitosa o fallida) |

El evento `dispositivo.metric` contiene el mismo envelope y estado que el ping REST.

```bash
curl -N -b "session_token=$JWT" http://localhost:8080/api/v1/lotes/events
```

```
: connected

event: lote.creado
data: {"id":"d1a2b3c4-...","sector_id":"horno-e2e","estado":"ABIERTO","conteos":{"ok":null,"crudo":null,"quemado":null,"total":0},"...":"..."}

: heartbeat

```

Se manda un comentario `: heartbeat` cada 30s para mantener la conexión viva. La conexión se corta cuando el cliente se desconecta.

| Código | Motivo |
|---|---|
| 405 (texto plano, no JSON) | Método distinto de GET |
| 401 (JSON) | Falta la cookie JWT o la sesión es inválida/expirada |
| 500 (texto plano) | El cliente HTTP no soporta streaming |

---

## 22. Salud del servicio

**`GET /health`, `GET /healthz`** — hacen `ping` a PostgreSQL con timeout de 2s.

```bash
curl http://localhost:8080/health
```
```json
{ "status": "healthy" }
```
Si la DB no responde: `503` con `{ "status": "unhealthy", "reason": "database unreachable" }` (JSON simple, no el envelope estándar).

**`GET /`** — página de texto plano informativa, sin JSON.

---

## Tabla resumen: auth y validaciones por endpoint

| Endpoint | Auth | Content-Type obligatorio | Límite body 1MB |
|---|---|---|---|
| `GET /api/v1/productos` | Dual (Bearer de dispositivo o JWT cookie) | — | — |
| `GET /api/v1/dispositivos/sector` | Bearer de dispositivo | — | — |
| `GET /api/v1/sectores` | JWT cookie (`session_token`) | — | — |
| `POST /api/v1/lotes/inicio` | Bearer de dispositivo | ✅ | ✅ |
| `GET /api/v1/lotes/abierto` | Dual (Bearer de dispositivo o JWT cookie) | — | — |
| `POST /api/v1/lotes/{id}/eventos` | Bearer de dispositivo | ✅ | ✅ |
| `POST /api/v1/lotes/{id}/cierre` | Bearer de dispositivo | ✅ | ✅ |
| `GET /api/v1/lotes` | Dual (Bearer de dispositivo o JWT cookie) | — | — |
| `POST /api/v1/dispositivos/ping` | Bearer de dispositivo | ✅ | ✅ |
| `PUT /api/v1/dispositivos` | JWT cookie (`session_token`), Supervisor/Admin | ✅ | ✅ |
| `POST /api/v1/horno/temperatura` | JWT cookie, Supervisor/Admin | ❌ | ❌ |
| `POST/PUT /api/v1/parametros-producto` | JWT cookie, Supervisor/Admin | ✅ | ✅ |
| `POST /api/v1/horno/consigna` | JWT cookie, Operario/Supervisor/Admin | ✅ | ✅ |
| `GET /api/v1/dispositivos` | JWT cookie (`session_token`) | — | — |
| `GET /api/v1/dispositivos/metricas` | JWT cookie (`session_token`) | — | — |
| `GET /*/events` (los 3 SSE) | JWT cookie (`session_token`) | — | — |
| Otros `GET` | Según la sección del endpoint | — | — |
