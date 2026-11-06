# Referencia de la API — SmartCheck Automation Backend

Documentación detallada de los endpoints de dominio del backend, con su propósito, contrato exacto y un ejemplo real. No cubre auth de usuarios ni administración: para el inventario completo (auth, admin de usuarios, registration-requests y `dispositivos/nombre`) ver `CLAUDE.md`, y para el contrato de enrolamiento `docs/device-registration-contract.md`. Los ejemplos usan `curl` contra `http://localhost:8080` (o el `PORT` configurado).

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

### Content-Type

Los endpoints que reciben JSON aceptan `application/json` con parámetros opcionales (por ejemplo `application/json; charset=utf-8`). Cualquier otro media type responde `415`.

### Autenticación

Hay dos esquemas de credencial, mutuamente excluyentes:

- **Usuarios del panel (OAuth):** cookie HttpOnly `session_token` emitida por `/api/v1/auth/login` o `/api/v1/auth/google`. El proxy del frontend debe reenviar esa cookie, también en las conexiones SSE.
- **Nodos Raspberry Pi:** header `Authorization: Bearer <secret>`, donde `<secret>` es el secreto por dispositivo enrolado.

Algunos endpoints son **dual-auth**: aceptan el Bearer de dispositivo o la cookie JWT. Si la petición trae cualquier header `Authorization`, se resuelve exclusivamente como intento de dispositivo (un Bearer inválido es 401 plano, sin degradar a la cookie). Sin `Authorization`, se valida la cookie. Son dual-auth: `GET /api/v1/productos`, `GET /api/v1/lotes/abierto`, `GET /api/v1/lotes`, `GET /api/v1/lotes/{id}/metricas-horno`, `GET /api/v1/lotes/{id}/consigna-propuesta`, `POST /api/v1/lotes/{id}/aplicar-consigna` y `POST /api/v1/lotes/{id}/aprobar-consigna`. `PUT /api/v1/lotes/{id}/modo` también usa dual-auth: el `PUT` es el push de la Pi y el `PATCH` es del panel.

### Errores de validación

Todos los `Validate()` de dominio acumulan **todos** los campos inválidos y los devuelven como un único string en `errors`, separados por `"; "` — no como array estructurado. Los errores de negocio del ciclo de lotes usan `errors: { "code": "..." }`.

---

## Índice

| # | Endpoint | Qué hace |
|---|---|---|
| 1 | `GET /api/v1/horno` | Consultar estado actual del horno |
| 2 | ~~`POST /api/v1/horno/temperatura`~~ | **Eliminado**: la temperatura sólo se lee de la telemetría de la Pi |
| 3 | `GET/POST /api/v1/productos`, `PUT/DELETE /api/v1/productos/{id}` | Catálogo maestro de productos y sus parámetros de control |
| 4 | `GET /api/v1/dispositivos/sector` | Sector y compañeros del dispositivo autenticado |
| 5 | `GET/POST /api/v1/sectores`, `PUT/DELETE /api/v1/sectores/{id}` | CRUD de sectores |
| 6 | `POST /api/v1/lotes/inicio` | Abrir (o adjuntarse a) el lote abierto del sector |
| 7 | `GET /api/v1/lotes/abierto` | Consultar el lote abierto del sector |
| 8 | `POST /api/v1/lotes/{id}/eventos` | Reportar detecciones del lote |
| 9 | `POST /api/v1/lotes/{id}/cierre` | Cerrar el lote con los conteos finales |
| 10 | `GET /api/v1/lotes` | Historial paginado de lotes por sector |
| 11 | `POST /api/v1/dispositivos/ping` | Recibir telemetría de una Raspberry Pi |
| 12 | `GET/PUT/DELETE /api/v1/dispositivos`, `PUT /api/v1/dispositivos/{id}/nombre` | Estado online/offline, modificación, baja lógica y rename desde el panel |
| 13 | `GET /api/v1/dispositivos/metricas` | Historial de métricas de un dispositivo (paginado) |
| 14 | `POST /api/v1/horno/consigna` | Envío manual de consigna térmica al horno |
| 15 | `GET /api/v1/horno/consigna/historial` | Historial de auditoría de consignas por lote |
| 16 | `GET /api/v1/lotes/events` | SSE: ciclo de vida de lotes (`lote.creado`/`actualizado`/`cerrado`) |
| 17 | `GET /api/v1/dispositivos/events` | SSE: telemetría de dispositivos en tiempo real |
| 18 | `GET /api/v1/horno/events` | SSE: consignas encoladas para la Pi |
| 19 | `GET /health`, `GET /healthz`, `GET /` | Salud del servicio / info |
| 20 | `GET /api/v1/lotes/{id}/control/estado` | Estado runtime del loop de control del lote |
| 21 | `PATCH`/`PUT /api/v1/lotes/{id}/modo` | Cambiar el modo MANUAL/SUPERVISION/AUTOMATICO |
| 22 | `POST /api/v1/lotes/{id}/control/start` y `/stop` | Arrancar o detener el loop del MPC |
| 23 | `PATCH /api/v1/lotes/{id}/control/parametros` | Ajustar tolerancia, umbrales y Δmáx del lote |
| 24 | `GET /api/v1/lotes/{id}/consigna-propuesta` | Propuesta pendiente del MPC u override manual |
| 25 | `POST /api/v1/lotes/{id}/aplicar-consigna` (alias `/aprobar-consigna`) | Aprobar/aplicar una propuesta en SUPERVISION |
| 26 | `POST /api/v1/lotes/{id}/consigna/ack` | Confirmar que la Pi aplicó la consigna |
| 27 | `GET /api/v1/lotes/{id}/decisiones` | Audit del MPC del lote |
| 28 | `GET /api/v1/lotes/{id}/confianza-clases` | Confianza media/desvío por clase |
| 29 | `POST`/`GET /api/v1/lotes/{id}/metricas-horno` | Telemetría cruda del horno |
| 30 | `GET /api/v1/lotes/{id}/control/events` | SSE: propuestas `control.propuesta` del lote |
| 31 | `GET /api/v1/reportes/resumen` | Resumen agregado de producción, calidad, proceso, alertas y automatización |
| 32 | `GET /api/v1/reportes/lotes` | Detalle paginado por cursor de lotes del período (kg y métricas derivadas) |
| 33 | `GET /api/v1/reportes/trazabilidad` | Snapshot completo de un lote para el PDF de trazabilidad |
| 34 | `GET /api/v1/reportes/alertas` | Export de alertas del período con filtros y total/truncada |
| 35 | `POST /api/v1/planificacion/{plan,paradas}/import` | Importación CSV de plan de producción y paradas |
| 36 | `GET /api/v1/reportes/oee` | OEE estándar (A/P/Q), global y por sector |
| 37 | `GET/DELETE /api/v1/planificacion/{plan,paradas}[/{id}]` | Listar lo importado por período y borrar una fila |
| 38 | `GET /api/v1/admin/notificaciones` | Canales configurados y preferencias por usuario |
| 39 | `PATCH /api/v1/admin/notificaciones/{usuarioID}` | Actualizar preferencias/canal Telegram de un usuario |
| 40 | `POST /api/v1/admin/notificaciones/test` | Enviar una notificación de prueba puntual |

---

## 1. `GET /api/v1/horno` — Consultar estado del horno

Devuelve una **proyección de solo lectura** del horno del sector a partir de la
telemetría real de la Pi (`metricas_horno`) del lote abierto, más sus alertas
recientes. Si no hay telemetría, los campos numéricos viajan `null`: el backend
no inventa temperatura. No hay horno en memoria: el backend no simula estado ni
aplica consignas.

**Auth:** cookie JWT `session_token` (cualquier rol). **Query param:** `sector_id` (requerido; `id` se acepta como alias legacy).

```bash
curl -b "session_token=$JWT" "http://localhost:8080/api/v1/horno?sector_id=horno-1"
```

```json
{
  "success": true,
  "message": "Estado de Horno verificado exitosamente",
  "data": {
    "horno": {
      "id": "horno-1",
      "nombre": "Horno 1",
      "sector_id": "horno-1",
      "lote_id": "1d1e9071-...",
      "producto_id": "a1b2c3d4-5678-90ab-cdef-1234567890ab",
      "temperatura": 170.5,
      "velocidad_cinta": 0.21,
      "temp_comb_1": 512.4,
      "humedad_horno": 12.5,
      "temp_ambiente": 24.1,
      "humedad_ambiente": 55.0,
      "temp_objetivo": 170.0,
      "vel_objetivo": 0.2,
      "confianza_deteccion": 0.93,
      "estado": "ACTIVO",
      "fuente": "telemetria",
      "ultimo_check": "2026-08-06T12:00:00Z",
      "antiguedad_segundos": 3
    },
    "alertas_recientes": []
  }
}
```

- `lote_id`/`producto_id`: lote ABIERTO del sector (o `null`).
- `temperatura`/`velocidad_cinta` y el resto de las lecturas: último sample de
  `metricas_horno` del lote (o `null` si ese sensor no reportó).
- `ultimo_check`/`antiguedad_segundos`: `received_at` del último sample y su
  antigüedad (o `null`).

| `estado` | Significado |
|---|---|
| `SIN_LOTE` | El sector no tiene lote abierto |
| `SIN_TELEMETRIA` | Hay lote abierto pero 0 muestras |
| `SIN_DATOS` | Hay muestra pero T1 y velocidad de cinta son `null` |
| `ACTIVO` | Muestra con T o V y antigüedad ≤ 30 s |
| `DESACTUALIZADO` | La última muestra tiene más de 30 s |

`fuente` vale `sin_lote`, `sin_telemetria` o `telemetria` según el origen de los datos.

| Código | Motivo |
|---|---|
| 405 | Método distinto de GET |
| 400 | Falta el query param `sector_id` (o el alias `id`) |
| 404 | El sector no existe |
| 401 | Falta la cookie JWT o la sesión es inválida/expirada |

---

## 2. ~~`POST /api/v1/horno/temperatura`~~ — Eliminado

Este endpoint fue eliminado junto con el horno en memoria y el
`SimulatedOvenController`. El backend no actualiza ni simula la temperatura:
la única fuente de estado es la telemetría real (`metricas_horno`) de la Pi.
Cualquier escritura de consigna se hace por `POST /api/v1/horno/consigna`
(§14), que **encola** la consigna para la Pi.

---

## 3. `/api/v1/productos` — Catálogo maestro de productos

El catálogo es la fuente única de productos y de sus parámetros de control de horneado (peso, dimensión, temperatura y velocidad de cinta). Ya no existe la tabla/endpoint separado `parametros-producto`.

### `GET /api/v1/productos`

Devuelve el catálogo completo, incluidos los productos inactivos (la Raspberry filtra por vigencia; el panel puebla selectores). Lo consume tanto el nodo como el panel.

**Auth:** dual-auth (Bearer de dispositivo o cookie JWT). Sin query params.

```bash
curl -H "Authorization: Bearer $DEVICE_SECRET" http://localhost:8080/api/v1/productos
```

```json
{
  "success": true, "message": "Productos obtenidos exitosamente",
  "data": [
    {
      "id": "a1b2c3d4-5678-90ab-cdef-1234567890ab", "nombre": "Tostada Integral",
      "pesoReferenciaKg": 0.25, "toleranciaPesoPct": 5.0,
      "dimensionBaseCm": 12.0, "toleranciaDimensionCm": 0.5,
      "tempMin": 180.0, "tempMax": 220.0,
      "velocidadCintaMin": 0.1, "velocidadCintaMax": 0.3,
      "tempSetpoint": 200.0, "velocidadCintaSetpoint": 0.2,
      "activo": true, "createdAt": "...", "updatedAt": "..."
    }
  ],
  "errors": null
}
```

| Código | Motivo |
|---|---|
| 401 | Bearer de dispositivo inválido, o falta la cookie JWT / sesión expirada |
| 500 | Error interno |

### `POST /api/v1/productos` — Alta de producto

Da de alta un producto nuevo con sus parámetros de control embebidos. El `id` lo asigna el servidor.

**Auth:** cookie JWT `session_token` con rol Supervisor o Administrador. `Content-Type: application/json` obligatorio.

```bash
curl -X POST http://localhost:8080/api/v1/productos \
  -b "session_token=$JWT" -H "Content-Type: application/json" \
  -d '{
    "nombre": "Pan Lactal",
    "pesoReferenciaKg": 0.5, "toleranciaPesoPct": 8.0,
    "dimensionBaseCm": 25.0, "toleranciaDimensionCm": 1.0,
    "tempMin": 180.0, "tempMax": 200.0,
    "velocidadCintaMin": 0.15, "velocidadCintaMax": 0.35
  }'
```

Respuesta `201 Created` con el producto creado (`activo: true` por defecto).

| Código | Motivo |
|---|---|
| 403 | Rol insuficiente (se requiere Supervisor o Administrador) |
| 415 | `Content-Type` inválido |
| 400 | JSON inválido |
| 422 | Validación fallida (ver reglas abajo) |
| 409 | Ya existe un producto con ese nombre (comparación case-insensitive) |
| 500 | Error interno |

### `PUT /api/v1/productos/{id}` — Modificar producto

Actualiza los datos y parámetros de control de un producto existente. El id se toma del path.

**Auth:** cookie JWT `session_token` con rol Supervisor o Administrador. Mismas reglas de Content-Type/validación que el POST.

```bash
curl -X PUT http://localhost:8080/api/v1/productos/a1b2c3d4-5678-90ab-cdef-1234567890ab \
  -b "session_token=$JWT" -H "Content-Type: application/json" \
  -d '{
    "nombre": "Tostada Integral",
    "pesoReferenciaKg": 0.28, "toleranciaPesoPct": 5.0,
    "dimensionBaseCm": 12.0, "toleranciaDimensionCm": 0.5,
    "tempMin": 185.0, "tempMax": 225.0,
    "velocidadCintaMin": 0.10, "velocidadCintaMax": 0.30
  }'
```

Respuesta `200 OK` con el producto actualizado.

| Código | Motivo |
|---|---|
| 403 | Rol insuficiente |
| 415 | `Content-Type` inválido |
| 400 | JSON inválido |
| 404 | El producto no existe |
| 409 | El nombre colisiona con otro producto |
| 422 | Validación fallida |
| 500 | Error interno |

### `DELETE /api/v1/productos/{id}` — Baja lógica de producto

Marca el producto como `activo=false` (no se ofrece para elegir, pero sigue resolviendo lotes históricos).

**Auth:** cookie JWT `session_token` con rol Supervisor o Administrador.

```bash
curl -X DELETE http://localhost:8080/api/v1/productos/a1b2c3d4-5678-90ab-cdef-1234567890ab \
  -b "session_token=$JWT"
```

Respuesta `200 OK` con `{ "id": "...", "activo": false }`.

| Código | Motivo |
|---|---|
| 403 | Rol insuficiente |
| 404 | El producto no existe |
| 500 | Error interno |

**Reglas de validación (POST/PUT):** `nombre` requerido (1–100 caracteres) · `pesoReferenciaKg > 0` · `toleranciaPesoPct >= 0` · `dimensionBaseCm > 0` · `toleranciaDimensionCm >= 0` · `tempMax > tempMin` · `velocidadCintaMax > velocidadCintaMin` · `tempSetpoint` (opcional) dentro de `[tempMin, tempMax]` · `velocidadCintaSetpoint` (opcional) dentro de `[velocidadCintaMin, velocidadCintaMax]`.

**Setpoints opcionales:** `tempSetpoint` y `velocidadCintaSetpoint` son nullable y opcionales en el body. Si se omiten, la DB los guarda como `NULL`; si se envían, deben caer dentro del rango min/max correspondiente. En la respuesta sólo aparecen cuando tienen valor (`omitempty`).

**Forma del error 422:** `errors` es un objeto campo→mensaje (no un string), para que el frontend pueda poblar `ProductoErrores` campo por campo.

```json
{
  "success": false,
  "message": "Datos de producto inválidos",
  "data": null,
  "errors": {
    "nombre": "es requerido",
    "pesoReferenciaKg": "debe ser un valor numérico mayor a 0",
    "tempSetpoint": "debe estar entre tempMin y tempMax"
  }
}
```

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

## 5. `/api/v1/sectores` — CRUD de sectores

`GET /api/v1/sectores` devuelve todos los sectores ordenados por nombre. Lo usa el panel para poblar filtros de historial. `POST` da de alta, `PUT /{id}` modifica y `DELETE /{id}` elimina.

**Auth:** `GET` cualquier rol autenticado. `POST`, `PUT` y `DELETE` requieren cookie JWT con rol **Supervisor** o **Administrador**.

```bash
curl -b "session_token=$JWT" http://localhost:8080/api/v1/sectores
```

```json
{
  "success": true, "message": "Sectores obtenidos exitosamente",
  "data": [ { "id": "horno-1", "nombre": "Horno 1" } ],
  "errors": null
}
```

**Alta (`POST`):** el body acepta únicamente `nombre` (requerido, con trim, hasta 100 caracteres). El `id` se genera en el servidor como slug del nombre (minúsculas, sin acentos, con guiones); ante colisión agrega un sufijo numérico (`-2`, `-3`, ...) y nunca supera los 50 caracteres.

```bash
curl -X POST -b "session_token=$JWT" -H "Content-Type: application/json" \
  -d '{"nombre":"Horno 1"}' http://localhost:8080/api/v1/sectores
```

```json
{ "success": true, "message": "Sector creado exitosamente", "data": { "id": "horno-1", "nombre": "Horno 1" } }
```

**Modificación (`PUT /api/v1/sectores/{id}`):** body `{ "nombre": "..." }`. El `id` es inmutable.

**Baja (`DELETE /api/v1/sectores/{id}`):** elimina el sector y desasigna sus dispositivos (`sector_id` queda `NULL` por la FK `ON DELETE SET NULL`). Se **bloquea con 409** si el sector tiene lotes productivos asociados.

| Código | Motivo |
|---|---|
| 405 | Método distinto de GET/POST en la colección, o de PUT/DELETE en `/{id}` |
| 401 | Falta la cookie JWT o la sesión es inválida/expirada |
| 403 | Rol insuficiente en POST/PUT/DELETE |
| 415 | `Content-Type` inválido en POST/PUT |
| 400 | JSON inválido en POST/PUT |
| 422 | Validación falló (`nombre` vacío o mayor a 100 caracteres) |
| 404 | El sector no existe (PUT/DELETE) |
| 409 | El sector tiene lotes asociados y no se puede eliminar |
| 500 | Error interno |

### Notas de migración y despliegue (sectores)

- **`ubicacion` eliminada e irreversible:** `database/migrations/0002_legacy_ubicacion.sql` migra la columna legacy `dispositivos.ubicacion` a `sectores` (id = slug del nombre; si dos dispositivos del mismo tipo comparten ubicación, solo el primero queda asignado y el resto en `NULL`) y luego la elimina con `DROP COLUMN`. El runner la aplica al arrancar. Un rollback al binario anterior requiere restaurar la columna manualmente.
- **Slugs colisionantes:** dos ubicaciones distintas que sluggean igual ("Línea A" y "linea-a") generan un único sector; `ON CONFLICT DO NOTHING` conserva uno de los nombres de forma no determinista. Revisar el catálogo tras la primera migración si existieran variantes.
- **Deploy coordinado frontend/backend:** el decoder es estricto. Un frontend viejo que aún envía `ubicacion` recibe 400 del backend nuevo; un frontend nuevo que envía `sectorId` recibe 400 del backend viejo. Desplegar ambos juntos.
- **Caché en memoria:** tras borrar un sector, el `MemoryDispositivoStateStore` puede conservar el `sectorId` viejo hasta el próximo ping/snapshot (≤30 s); se autocorrige.
- **Unicidad de nombre:** los nombres de sector **no son únicos por decisión de negocio** (no hay índice único sobre `sectores.nombre`); el frontend solo avisa de duplicados en memoria. Dos sectores pueden llamarse igual.

---

## 6. `POST /api/v1/lotes/inicio` — Abrir lote del sector

Lo llama la Raspberry Pi (ENTRADA_HORNO) al identificar el producto. **Abre y persiste** el lote abierto del sector; si ya hay uno, se adjunta (get-or-create). Es idempotente por `idempotency_key`. **No dispara la consigna automática de SCA-142**; si el lote nace en modo no manual, el backend arranca el loop del MPC (SCA-400).

**Auth:** `Authorization: Bearer <secret>` (device-only). `Content-Type: application/json` obligatorio. Body máx. 1 MB.

```bash
curl -X POST http://localhost:8080/api/v1/lotes/inicio \
  -H "Authorization: Bearer $DEVICE_SECRET" -H "Content-Type: application/json" \
  -d '{"idempotency_key":"0f8fad5b-d9cb-469f-a165-70867728950e","producto_id":"a1b2c3d4-5678-90ab-cdef-1234567890ab","momento":"2026-08-06T12:00:00Z"}'
```

Desde SCA-400 el body acepta además los parámetros de operación del lote:
`modo` (`MANUAL`/`SUPERVISION`/`AUTOMATICO`, default `MANUAL`),
`iniciado_manualmente`, `ventana_evaluacion_segundos`,
`ventana_actualizacion_segundos`, `tolerancia_temp_c`,
`umbral_aceptacion_crudo`, `umbral_aceptacion_quemado`, `pesos_objetivo` y
`estado_inicial` (foto del horno cuando la Pi se engancha a un horno ya
encendido).

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

## 11. `POST /api/v1/dispositivos/ping` — Telemetría de Raspberry Pi

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
    "dispositivoId": "b1c2d3e4-...", "nombre": "Raspberry Pi Horno 1", "sectorId": "horno-1",
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

**`dispositivoId` es opcional e ignorado:** la identidad la toma el servidor del principal Bearer verificado (`devicetoken`); el valor que llegue en el body se descarta antes de validar, por lo que un id distinto o no-UUID en el body no cambia el dispositivo destino.

**Reglas de validación:** `dispositivoId` opcional e ignorado (el servidor usa el del Bearer) · `cpuPct` ∈ [0,100] · `memRamDisponibleMb >= 0` · `memRamTotalMb` opcional y >= 0; si se informa, `memRamDisponibleMb <= memRamTotalMb` · `almacenamientoDisponibleMb` y `almacenamientoTotalMb` forman un par opcional: deben omitirse ambos o informarse ambos, ser >= 0 y cumplir `almacenamientoDisponibleMb <= almacenamientoTotalMb` · `tempChip` ∈ [-40,120] · `aiProcessorPct` ∈ [0,100].

---

## 12. `GET /api/v1/dispositivos` — Estado online/offline de todos los dispositivos

Lectura desde el caché en memoria (no toca Postgres) — rápida, para refrescar el panel. La misma ruta para `PUT` administra el catálogo y para `DELETE` da de baja lógica un dispositivo (ambos requieren JWT con rol Supervisor/Admin).

**Auth:** cookie JWT `session_token` obligatoria; cualquier rol autenticado.

```bash
curl -b "session_token=$JWT" http://localhost:8080/api/v1/dispositivos
```

```json
{
  "success": true, "message": "Estados de dispositivos obtenidos exitosamente",
  "data": [
    { "dispositivoId": "b1c2d3e4-...", "nombre": "Raspberry Pi Horno 1", "sectorId": "horno-1", "whepUrl": "https://camaras.example.com/whep/horno-1", "estado": "online", "ultimaMetrica": { "...": "..." }, "lastSeen": "..." }
  ]
}
```

El objeto `ultimaMetrica` usa el contrato de telemetría descrito en #11. El campo `whepUrl` es la fuente de verdad de la cámara/stream WHEP del dispositivo: es opcional y nullable; se omite cuando el dispositivo no tiene cámara configurada. El campo `sectorId` es el sector al que pertenece el dispositivo (nullable); la columna legacy `ubicacion` fue eliminada y sus valores migrados a sectores.

**Modificación (`PUT`):** el body acepta únicamente `dispositivoId` (requerido; debe ser un UUID válido, 422 en caso contrario), `sectorId` (string o `null`) y `whepUrl` (string opcional). El `nombre` es **inmutable desde el ruta del panel**: el decoder estricto rechaza campos desconocidos (incluido `nombre`), y solo el rename autenticado puede cambiarlo. El panel lo hace en `PUT /api/v1/dispositivos/{id}/nombre` (ver subsección más abajo), y la propia Raspberry en `PUT /api/v1/dispositivos/nombre` (ver #14). **Semántica de `sectorId`:** al ser un PUT de reemplazo, **omitir la clave equivale a enviar `null`** (desasigna el dispositivo del sector); el frontend siempre la envía explícitamente para no desasignar al editar solo WHEP. Un `sectorId` inexistente → 400; violar el índice único `uq_dispositivos_sector_tipo` → 409. Si `whepUrl` se omite o se envía vacío, el dispositivo queda sin stream (`NULL`); si se informa, debe ser una URL absoluta `http`/`https` de hasta 500 caracteres; en caso contrario 422.

```json
{ "dispositivoId": "b1c2d3e4-5678-90ab-cdef-1234567890ab", "sectorId": "horno-1", "whepUrl": "https://camaras.example.com/whep/horno-1" }
```

| Código | Motivo |
|---|---|
| 405 | Método distinto de GET/PUT/DELETE |
| 401 | Falta la cookie JWT o la sesión es inválida/expirada |
| 403 | Rol insuficiente en PUT (se requiere Supervisor o Administrador) |
| 415 | `Content-Type` inválido en PUT |
| 400 | JSON inválido en PUT, o `sectorId` no existe |
| 409 | El sector ya tiene un dispositivo de ese tipo (`ENTRADA_HORNO`/`SALIDA_HORNO`) |
| 422 | Validación falló (incluye `whepUrl` no absoluta o mayor a 500 caracteres) |
| 404 | `dispositivoId` no existe en el catálogo (PUT) |
| 500 | Error interno |

**Baja lógica (`DELETE /api/v1/dispositivos?dispositivoId=...`):** da de baja el dispositivo **sin borrar la fila** (evita violar las FKs `ON DELETE RESTRICT` de auditoría/lotes/historial y conserva el historial de telemetría). La operación es transaccional y ejecuta: `auth_status='revoked'`, `secret_hash=NULL` (el nodo deja de autenticar: recibe **401** con su secret viejo), `sector_id=NULL` (libera el slot `uq_dispositivos_sector_tipo`) y `auth_updated_at=clock_timestamp()`; además inserta una traza en `device_lifecycle_audit` con el actor resuelto desde su email. El dispositivo deja de aparecer en `GET /api/v1/dispositivos` y sus métricas históricas se conservan (`GET /api/v1/dispositivos/metricas`). Es **idempotente**: repetir la baja de un dispositivo ya revocado responde 200 sin duplicar la traza.

**Auth:** cookie JWT `session_token` con rol **Supervisor** o **Administrador**.

```bash
curl -X DELETE -b "session_token=$JWT" "http://localhost:8080/api/v1/dispositivos?dispositivoId=b1c2d3e4-5678-90ab-cdef-1234567890ab"
```

```json
{ "success": true, "message": "Dispositivo dado de baja exitosamente", "data": null, "errors": null }
```

| Código | Motivo |
|---|---|
| 405 | Método distinto de GET/PUT/DELETE |
| 400 | Falta el query param `dispositivoId` |
| 401 | Falta la cookie JWT o la sesión es inválida/expirada |
| 403 | Rol insuficiente (se requiere Supervisor/Admin) |
| 404 | `dispositivoId` no existe en el catálogo (o ya no es un UUID válido) |
| 500 | Error interno |

**Renombrar desde el panel (`PUT /api/v1/dispositivos/{id}/nombre`):** el operador autenticado cambia el nombre de un dispositivo identificado por `{id}` en el path. Es la contraparte simétrica del rename que emite la propia Raspberry (`PUT /api/v1/dispositivos/nombre`, ver #14), pensada para que el panel de Next.js pueda corregir/etiquetar un nodo sin necesidad de esperar a que el nodo renombre su hostname. La Raspberry sigue usando la ruta sin `{id}` porque sólo conoce su principal autenticado; el id en el path es la fuente de verdad para el panel (nunca se acepta desde el body). La operación preserva `sector` y `whepUrl` y emite el mismo evento SSE `dispositivo.state` que el rename del nodo, así el panel en vivo se entera del cambio sin re-fetchear.

**Auth:** cookie JWT `session_token` con rol **Supervisor** o **Administrador**.

```bash
curl -X PUT -b "session_token=$JWT" -H "Content-Type: application/json" \
  -d '{"nombre":"Raspberry Pi Horno 1 - Reasignada"}' \
  http://localhost:8080/api/v1/dispositivos/b1c2d3e4-5678-90ab-cdef-1234567890ab/nombre
```

```json
{
  "success": true, "message": "Dispositivo renombrado exitosamente",
  "data": {
    "dispositivoId": "b1c2d3e4-5678-90ab-cdef-1234567890ab",
    "nombre": "Raspberry Pi Horno 1 - Reasignada",
    "sectorId": "horno-1",
    "whepUrl": "https://camaras.example.com/whep/horno-1",
    "estado": "online",
    "ultimaMetrica": { "...": "..." },
    "lastSeen": "..."
  }
}
```

| Código | Motivo |
|---|---|
| 405 | Método distinto de PUT |
| 415 | `Content-Type` inválido (debe ser `application/json`) |
| 401 | Falta la cookie JWT o la sesión es inválida/expirada |
| 403 | Rol insuficiente (se requiere Supervisor/Admin) |
| 400 | Path sin `{id}` o con un id que no es un UUID válido |
| 422 | `nombre` vacío o mayor a 100 caracteres |
| 404 | `{id}` no existe en el catálogo |
| 500 | Error interno |

---

## 13. `GET /api/v1/dispositivos/metricas` — Historial de métricas de un dispositivo

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

## 14. `POST /api/v1/horno/consigna` — Envío manual de consigna térmica — SCA-320

Un operario carga a mano temperatura y velocidad de cinta desde el panel. La
consigna se **encola** (no se aplica en el backend): se valida contra el lote
abierto en modo **MANUAL** y contra el rango seguro
`[temp_min, temp_max]`/`[velocidad_cinta_min, velocidad_cinta_max]` del producto,
se deja una decisión aprobada (`muestras=0`) para que la Pi ENTRADA la levante
por pull como `manual_override` (ver §24) y se audita en `historial_consignas`
con `origen=MANUAL`. La única actuadora es la Pi.

`loteId` es **obligatorio** y debe corresponder a un lote ABIERTO en modo
MANUAL. `sectorId` (o el alias legacy `hornoId`) es opcional; si viene, debe
coincidir con el `sector_id` del lote.

**Auth:** cookie JWT `session_token` con rol Operario, Supervisor o Administrador. `Content-Type: application/json` obligatorio.

```bash
curl -X POST http://localhost:8080/api/v1/horno/consigna \
  -b "session_token=$JWT" -H "Content-Type: application/json" \
  -d '{
    "loteId": "1d1e9071-e707-4821-b089-10d4a404d023",
    "sectorId": "horno-1",
    "productoId": "a1b2c3d4-5678-90ab-cdef-1234567890ab",
    "temperaturaObjetivo": 175,
    "velocidadCintaObjetivo": 0.25,
    "usuario": "operario-demo"
  }'
```

```json
{
  "success": true, "message": "Consigna encolada para la Pi",
  "data": {
    "id": "0cd37ba5-...", "hornoId": "horno-1", "loteId": "1d1e9071-...", "productoId": "a1b2c3d4-...",
    "temperaturaObjetivo": 175, "velocidadCintaObjetivo": 0.25, "origen": "MANUAL",
    "usuario": "operario-demo", "exitosa": true,
    "decisionId": "8f14e45f-ceea-467a-9a3b-0b9a1e2f3c4d", "estado": "PENDIENTE",
    "temperaturaPrevia": 170, "velocidadCintaPrevia": 0.2, "creadaEn": "2026-08-06T12:00:00Z"
  },
  "errors": null
}
```

`usuario` es opcional; `productoId` y `loteId` son **obligatorios**.
`exitosa=true` y `estado=PENDIENTE` significan **aceptada y encolada para la
Pi**; no confirman que el PLC la haya aplicado. `decisionId` es el id de la
decisión de control encolada y `estado` el ciclo de vida de la consigna:
`PENDIENTE` (encolada, esperando el ack), `APLICADA` (confirmada por el ack de
la Pi) o `RECHAZADA` (no se pudo encolar; llega con `exitosa=false`, sin
`decisionId` y con `motivoError`).

| Código | Motivo |
|---|---|
| 405 | Método distinto de POST |
| 401 | Falta la cookie JWT o la sesión es inválida/expirada |
| 403 | Rol insuficiente |
| 415 | `Content-Type` inválido |
| 400 | JSON inválido |
| 422 | Validación de forma falló (p.ej. falta `loteId`), el producto no tiene parámetros cargados, los valores están fuera del rango seguro, el sector no coincide con el del lote o el lote no tiene sector |
| 404 | El lote no existe |
| 409 | El lote no está ABIERTO o no está en modo MANUAL |
| 502 | No se pudo encolar la consigna para la Pi (queda auditado con `exitosa=false`, `estado=RECHAZADA` y `motivo_error`) |
| 500 | Error interno |

---

## 15. `GET /api/v1/horno/consigna/historial` — Historial de auditoría por lote

Devuelve todas las consignas asociadas a un `loteId`, ordenadas de más reciente a más antigua. `origen='MANUAL'` corresponde al envío del panel (SCA-320) ya encolado para la Pi; `origen='AUTOMATICO'` sólo aparece en filas históricas (SCA-142/SCA-400) y ya no se escribe.

**Auth:** cookie JWT `session_token` (cualquier rol). **Query param:** `loteId` (requerido).

```bash
curl -b "session_token=$JWT" "http://localhost:8080/api/v1/horno/consigna/historial?loteId=1d1e9071-e707-4821-b089-10d4a404d023"
```

```json
{
  "success": true, "message": "Historial de consignas obtenido exitosamente",
  "data": [
    { "id": "0cd37ba5-...", "origen": "MANUAL", "temperaturaObjetivo": 175, "exitosa": true,
      "decisionId": "8f14e45f-...", "estado": "APLICADA", "creadaEn": "..." },
    { "id": "e7727358-...", "origen": "AUTOMATICO", "temperaturaObjetivo": 170, "exitosa": true,
      "estado": "APLICADA", "creadaEn": "..." }
  ]
}
```

`estado` es `PENDIENTE` mientras la consigna espera el ack de la Pi, `APLICADA`
cuando el ack confirmó la aplicación y `RECHAZADA` cuando no se pudo encolar.
`decisionId` viaja en las consignas nuevas encoladas y se omite en las filas
históricas sin decisión. `exitosa` se mantiene por compatibilidad; la fuente
fina es `estado`.

| Código | Motivo |
|---|---|
| 405 | Método distinto de GET |
| 400 | Falta `loteId` |
| 401 | Falta la cookie JWT o la sesión es inválida/expirada |
| 500 | Error interno |

---

## 16–18. Endpoints SSE (Server-Sent Events)

Los tres comparten el mismo handler genérico, cada uno con su propio broker (sin cruce de eventos entre sí). Requieren la cookie JWT `session_token` y aceptan cualquier rol autenticado. No usan el envelope JSON estándar — son streams `text/event-stream`.

| Endpoint | Eventos que emite |
|---|---|
| `GET /api/v1/lotes/events` | `lote.creado`, `lote.actualizado` y `lote.cerrado` — el payload es el objeto lote crudo |
| `GET /api/v1/dispositivos/events` | `dispositivo.metric` (cada ping) y `dispositivo.state` (transición online↔offline) |
| `GET /api/v1/horno/events` | `horno.consigna` — cada vez que se despacha una consigna manual (exitosa o fallida) |

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

## 19. Salud del servicio

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

## 20. `GET /api/v1/lotes/{id}/control/estado` — Estado del control

Devuelve el modo persistido del lote y el estado runtime del loop en memoria.
`started_at` viaja `null` cuando no hay loop activo.

**Auth:** cookie JWT `session_token` (Administrador, Supervisor u Operario).

```bash
curl -b "session_token=$JWT" \
  http://localhost:8080/api/v1/lotes/$LOTE/control/estado
```

```json
{
  "success": true, "message": "Estado del control",
  "data": {
    "lote_id": "d1a2b3c4-...",
    "modo": "SUPERVISION",
    "loop_activo": true,
    "started_at": "2026-11-03T12:00:00Z"
  }
}
```

| Código | Motivo |
|---|---|
| 405 | Método distinto de GET |
| 401 | Falta la cookie JWT o la sesión es inválida/expirada |
| 403 | Rol insuficiente |
| 404 | `{id}` no es un lote válido o no existe |
| 500 | Error interno |

---

## 21. `PATCH`/`PUT /api/v1/lotes/{id}/modo` — Cambiar modo de operación

Cambia el modo de un lote **ABIERTO** entre `MANUAL`, `SUPERVISION` y
`AUTOMATICO`. Cancela el loop vigente, persiste el modo, arranca un loop nuevo si
el modo lo requiere y deja una fila de auditoría en `decisiones_control`.

- `PATCH` es la vía del panel (cookie JWT, Supervisor/Admin).
- `PUT` es el push de la Pi (Bearer de dispositivo); sólo puede afectar lotes de
  su propio sector. La ruta usa dual-auth, así que un `PUT` con cookie JWT
  también se acepta si el usuario tiene rol suficiente.

```bash
curl -X PATCH http://localhost:8080/api/v1/lotes/$LOTE/modo \
  -b "session_token=$JWT" -H "Content-Type: application/json" \
  -d '{"modo":"SUPERVISION"}'
```

```json
{
  "success": true, "message": "Modo actualizado",
  "data": {
    "lote_id": "d1a2b3c4-...",
    "modo_anterior": "MANUAL",
    "modo_nuevo": "SUPERVISION",
    "cambiado": true,
    "loop_activo": true
  }
}
```

Si el modo enviado es igual al vigente, responde `200` con `cambiado: false` y
`modo`.

| Código | Motivo |
|---|---|
| 405 | Método distinto de PATCH/PUT |
| 401 | Bearer de dispositivo inválido, o falta la cookie JWT |
| 403 | Rol insuficiente (JWT) o dispositivo sin sector / de otro sector |
| 400 | JSON inválido o `modo` inválido |
| 404 | Lote no encontrado |
| 409 | El lote no está ABIERTO |
| 422 | Producto desconocido, receta incompleta (sin setpoint de temperatura/velocidad) o modelo XGBoost no entrenado |
| 503 | El predictor no está disponible al validar AUTOMATICO |
| 500 | Error interno |

---

## 22. `POST /api/v1/lotes/{id}/control/start` y `/stop`

`start` crea el loop del MPC si el lote está ABIERTO y su modo no es `MANUAL`.
Es idempotente: si el loop ya existe, no lo reinicia.

**Auth:** cookie JWT `session_token` con rol Supervisor o Administrador.

```bash
curl -X POST -b "session_token=$JWT" \
  http://localhost:8080/api/v1/lotes/$LOTE/control/start
```

```json
{
  "success": true, "message": "Control iniciado",
  "data": {
    "lote_id": "d1a2b3c4-...",
    "modo": "SUPERVISION",
    "ventana_evaluacion_segundos": 600,
    "ventana_actualizacion_segundos": 300
  }
}
```

`stop` cancela el loop en memoria; el lote sigue abierto.

```bash
curl -X POST -b "session_token=$JWT" \
  http://localhost:8080/api/v1/lotes/$LOTE/control/stop
```

```json
{
  "success": true, "message": "Control detenido",
  "data": { "lote_id": "d1a2b3c4-...", "stopped": true }
}
```

Si no había loop activo, `stop` responde `200` con `stopped: false` y
`motivo: "no_loop_activo"`.

| Código | Motivo |
|---|---|
| 405 | Método distinto de POST |
| 401 | Falta la cookie JWT o la sesión es inválida/expirada |
| 403 | Rol insuficiente |
| 404 | Lote no encontrado |
| 409 | `start` sobre un lote cerrado o en modo MANUAL |
| 422 | `start` con producto desconocido |
| 500 | Error interno |

---

## 23. `PATCH /api/v1/lotes/{id}/control/parametros` — Tuning del lote

Ajusta la tolerancia de temperatura, los umbrales de aceptación y los Δmáx por
ciclo del lote. Todos los campos son opcionales; ausente = no cambiar. El loop
vivo relee los límites en cada tick, así que no hace falta reiniciarlo.

**Auth:** cookie JWT `session_token` con rol Supervisor o Administrador.

```bash
curl -X PATCH http://localhost:8080/api/v1/lotes/$LOTE/control/parametros \
  -b "session_token=$JWT" -H "Content-Type: application/json" \
  -d '{
    "tolerancia_temp_c": 2.5,
    "umbral_aceptacion_crudo": 5,
    "umbral_aceptacion_quemado": 5,
    "delta_temp1_max_c": 8,
    "delta_vel_max": 0.04
  }'
```

```json
{
  "success": true, "message": "Parámetros actualizados",
  "data": {
    "lote_id": "d1a2b3c4-...",
    "tolerancia_temp_c": 2.5,
    "umbral_aceptacion_crudo": 5,
    "umbral_aceptacion_quemado": 5,
    "delta_temp1_max_c": 8,
    "delta_vel_max": 0.04
  }
}
```

Rangos: `tolerancia_temp_c` en `(0,20]`, umbrales en `[0,100]`,
`delta_temp1_max_c` en `(0,50]` y `delta_vel_max` en `(0,2]`.

| Código | Motivo |
|---|---|
| 405 | Método distinto de PATCH |
| 401 | Falta la cookie JWT o la sesión es inválida/expirada |
| 403 | Rol insuficiente |
| 400 | JSON inválido, sin parámetros o valor fuera de rango |
| 404 | Lote no encontrado |
| 409 | El lote no está ABIERTO |
| 500 | Error interno |

---

## 24. `GET /api/v1/lotes/{id}/consigna-propuesta` — Propuesta pendiente

La Pi (o el panel) consulta la propuesta pendiente del lote. El endpoint es
pull-based y no destructivo: leerlo no consume la propuesta.

**Auth:** dual-auth (Bearer de dispositivo o cookie JWT).

```bash
curl -H "Authorization: Bearer $DEVICE_SECRET" \
  http://localhost:8080/api/v1/lotes/$LOTE/consigna-propuesta
```

```json
{
  "success": true, "message": "Consigna propuesta",
  "data": {
    "decision_id": "8f14e45f-...",
    "temp1_objetivo": 196,
    "vel_objetivo": 0.22,
    "generated_at": "2026-11-03T12:00:00Z",
    "ttl_segundos": 300,
    "ventana_inicio": "2026-11-03T11:55:00Z",
    "ventana_fin": "2026-11-03T12:00:00Z",
    "autorizada": true,
    "fase": "ADAPTANDO",
    "limites": {
      "temp_min": 180, "temp_max": 220,
      "vel_min": 0.1, "vel_max": 0.3,
      "delta_temp1_max_c": 10, "delta_vel_max": 0.05,
      "tolerancia_temp_c": 2
    }
  }
}
```

Semántica:

- **AUTOMATICO:** `autorizada: true`; la Pi aplica sin aprobación.
- **SUPERVISION:** `autorizada: false` hasta que se apruebe; después pasa a
  `true`.
- **MANUAL:** no hay MPC, pero si existe un override manual encolado se sirve
  con `manual_override: true` y `autorizada: true`; la Pi lo aplica sin el clamp
  de Δ por ciclo.
- Sin propuesta pendiente, lote cerrado, propuesta vencida o MPC en MANUAL sin
  override: **`204 No Content`**.

En MANUAL con override encolado, la respuesta es la decisión persistida y se
marca explícitamente:

```json
{
  "decision_id": "8f14e45f-...",
  "temp1_objetivo": 195,
  "vel_objetivo": 0.22,
  "autorizada": true,
  "manual_override": true,
  "aprobada_por": "operario@fermar.com.ar"
}
```

`manual_override` y `aprobada_por` sólo aparecen cuando tienen valor.

| Código | Motivo |
|---|---|
| 405 | Método distinto de GET |
| 401 | Bearer inválido o cookie JWT ausente/expirada |
| 404 | Lote no encontrado |
| 204 | No hay propuesta pendiente |
| 500 | Error interno |

---

## 25. `POST /api/v1/lotes/{id}/aplicar-consigna` (alias `/aprobar-consigna`)

Aprueba la propuesta pendiente en modo SUPERVISION. Puede hacerlo un operario
del panel (JWT) o la propia Pi (Bearer de dispositivo, sólo para lotes de su
sector). La Pi ENTRADA es la única actuadora: el endpoint sólo autoriza y la
aplicación real en el PLC la confirma después `/consigna/ack`.

**Auth:** dual-auth. Con JWT se requiere rol Operario, Supervisor o
Administrador.

Body opcional:

```json
{ "decision_id": "8f14e45f-..." }
```

Si `decision_id` viene y no coincide con la propuesta vigente, se rechaza para
evitar aprobar una propuesta distinta de la que el operario está viendo.

```bash
curl -X POST http://localhost:8080/api/v1/lotes/$LOTE/aplicar-consigna \
  -b "session_token=$JWT" -H "Content-Type: application/json" \
  -d '{"decision_id":"8f14e45f-..."}'
```

```json
{
  "success": true, "message": "Consigna aprobada",
  "data": {
    "lote_id": "d1a2b3c4-...",
    "aprobada": true,
    "aplicada": false,
    "decision_id": "8f14e45f-...",
    "temp1_objetivo": 196,
    "vel_objetivo": 0.22
  }
}
```

| Código | Motivo |
|---|---|
| 405 | Método distinto de POST |
| 401 | Bearer inválido o cookie JWT ausente/expirada |
| 403 | Rol insuficiente, dispositivo sin sector o de otro sector |
| 404 | Lote no encontrado |
| 409 | Lote cerrado, modo MANUAL, modo AUTOMATICO (no requiere aprobación), sin propuesta pendiente o propuesta cambiada |
| 500 | Error interno |

---

## 26. `POST /api/v1/lotes/{id}/consigna/ack` — ACK de la Pi

La Pi confirma que aplicó la propuesta y con qué setpoints. Es idempotente: un
reenvío del mismo ACK vuelve a responder `200`.

**Auth:** `Authorization: Bearer <secret>` (device-only).

```bash
curl -X POST http://localhost:8080/api/v1/lotes/$LOTE/consigna/ack \
  -H "Authorization: Bearer $DEVICE_SECRET" -H "Content-Type: application/json" \
  -d '{"decision_id":"8f14e45f-...","temp_aplicado":196,"vel_aplicado":0.22}'
```

```json
{
  "success": true, "message": "Consigna aplicada",
  "data": {
    "decision_id": "8f14e45f-...",
    "aplicado": true,
    "aplicado_por": "OPERARIO"
  }
}
```

`aplicado_por` es `MPC` en AUTOMATICO y `OPERARIO` en SUPERVISION. El ACK
también actualiza los setpoints aplicados del lote y marca como `APLICADA` la
consigna de `historial_consignas` encolada con ese `decision_id` (idempotente:
si no hay fila o ya estaba aplicada, el ACK sigue respondiendo `200`).

| Código | Motivo |
|---|---|
| 405 | Método distinto de POST |
| 401 | Token de dispositivo inválido |
| 400 | JSON inválido o faltan `decision_id`, `temp_aplicado` o `vel_aplicado` |
| 403 | Dispositivo sin sector o de otro sector |
| 404 | Lote o decisión no encontrados |
| 409 | Lote cerrado o propuesta de SUPERVISION no aprobada |
| 500 | Error interno |

---

## 27. `GET /api/v1/lotes/{id}/decisiones` — Audit del MPC

Devuelve las últimas 100 decisiones del lote, más recientes primero. Cada fila
incluye la observación, la predicción XGBoost (`prediccion`), la propuesta, los
valores aplicados, `razon_skipped`, `aplicado_por` y la aprobación
(`aprobada_por`/`aprobada_at`).

**Auth:** cookie JWT `session_token` (cualquier rol).

```bash
curl -b "session_token=$JWT" \
  http://localhost:8080/api/v1/lotes/$LOTE/decisiones
```

```json
{
  "success": true, "message": "Decisiones del lote",
  "data": {
    "lote_id": "d1a2b3c4-...",
    "decisiones": [
      {
        "id": "8f14e45f-...", "lote_id": "d1a2b3c4-...",
        "ventana_inicio": "...", "ventana_fin": "...",
        "muestras": 120, "temp_actual": 195, "vel_actual": 0.22,
        "pct_crudos_obs": 1.2, "pct_ok_obs": 96.5, "pct_quemados_obs": 2.3,
        "conf_promedio": 0.93,
        "temp_propuesto": 196, "vel_propuesto": 0.22,
        "prediccion": { "pctCrudos": 1.1, "pctOk": 96.8, "pctQuemados": 2.1, "modelVersion": "v3", "esPlaceholder": false },
        "z_observado": 0.04, "z_predicho": 0.03,
        "aplicado_por": "OPERARIO",
        "aprobada_por": "operario@fermar.com.ar",
        "created_at": "..."
      }
    ],
    "total": 1
  }
}
```

| Código | Motivo |
|---|---|
| 405 | Método distinto de GET |
| 401 | Falta la cookie JWT o la sesión es inválida/expirada |
| 403 | Rol insuficiente |
| 404 | Lote no encontrado |
| 500 | Error interno |

---

## 28. `GET /api/v1/lotes/{id}/confianza-clases` — Confianza por clase

Agrega la confianza media y el desvío por clase (`ok`, `crudo`, `quemado`)
sobre las últimas N detecciones del lote. Sirve para detectar un modelo
degradado por clase.

**Auth:** cookie JWT `session_token` (cualquier rol). **Query param:** `limite`
(default `200`, máximo `2000`; fuera de rango se clampea).

```bash
curl -b "session_token=$JWT" \
  "http://localhost:8080/api/v1/lotes/$LOTE/confianza-clases?limite=200"
```

```json
{
  "success": true, "message": "Confianza por clase",
  "data": {
    "lote_id": "d1a2b3c4-...",
    "clases": [
      { "clase": "ok", "confianza_media": 0.93, "confianza_stddev": 0.04, "muestras": 180 },
      { "clase": "quemado", "confianza_media": 0.88, "confianza_stddev": null, "muestras": 1 }
    ],
    "total": 2
  }
}
```

`confianza_stddev` es `null` con menos de dos muestras.

| Código | Motivo |
|---|---|
| 405 | Método distinto de GET |
| 401 | Falta la cookie JWT o la sesión es inválida/expirada |
| 403 | Rol insuficiente |
| 404 | Lote no encontrado |
| 500 | Error interno |

---

## 29. `POST`/`GET /api/v1/lotes/{id}/metricas-horno` — Telemetría del horno

La Pi ENTRADA envía heartbeats con las lecturas reales del horno. El servidor
completa `lote_id` (de la URL), `dispositivo_id` (del principal autenticado) y
`sector_id` (del dispositivo), valida que el lote esté ABIERTO y pertenezca al
sector, y persiste append-only en `metricas_horno`.

**POST — Auth:** `Authorization: Bearer <secret>` (device-only).
`Content-Type: application/json` obligatorio.

```bash
curl -X POST http://localhost:8080/api/v1/lotes/$LOTE/metricas-horno \
  -H "Authorization: Bearer $DEVICE_SECRET" -H "Content-Type: application/json" \
  -d '{
    "temp_horno_1": 195.5,
    "temp_comb_1": 800.0,
    "humedad_horno": 12.0,
    "temp_ambiente": 28.0,
    "humedad_ambiente": 45.0,
    "velocidad_cinta": 0.22,
    "temp_objetivo": 196.0,
    "vel_objetivo": 0.22,
    "confianza_deteccion": 0.93
  }'
```

```json
{
  "success": true, "message": "Métrica registrada",
  "data": { "id": "b1c2d3e4-...", "received_at": "2026-11-03T12:00:00Z" }
}
```

`received_at` es opcional; si se omite, se usa el reloj del servidor.
`confianza_deteccion`, si viene, debe estar en `[0,1]`.

**GET — Auth:** dual-auth. Devuelve las últimas N lecturas (default `1`,
máximo `200`) para telemetría en vivo.

```bash
curl -H "Authorization: Bearer $DEVICE_SECRET" \
  "http://localhost:8080/api/v1/lotes/$LOTE/metricas-horno?limite=10"
```

```json
{
  "success": true, "message": "Métricas del horno",
  "data": { "metricas": [ { "id": "...", "temp_horno_1": 195.5, "received_at": "..." } ] }
}
```

| Código | Motivo |
|---|---|
| 405 | Método distinto de POST/GET |
| 401 | Token de dispositivo inválido (POST) o credencial dual inválida (GET) |
| 400 | JSON inválido o `confianza_deteccion` fuera de `[0,1]` |
| 403 | POST: el lote pertenece a otro sector (`lote_ajeno`) |
| 404 | Lote no encontrado; POST además devuelve `sin_sector` si el dispositivo no tiene sector |
| 409 | POST: el lote no está ABIERTO (`lote_no_abierto`) |
| 500 | Error interno |

---

## 30. `GET /api/v1/lotes/{id}/control/events` — SSE de propuestas del MPC

Stream `text/event-stream` con los eventos `control.propuesta` del lote
indicado. El broker es global y el handler filtra por `lote_id`.

**Auth:** cookie JWT `session_token` (cualquier rol autenticado). El proxy del
frontend debe reenviar la cookie también en SSE.

```bash
curl -N -b "session_token=$JWT" \
  http://localhost:8080/api/v1/lotes/$LOTE/control/events
```

```
: connected lote=d1a2b3c4-...

event: control.propuesta
data: {"lote_id":"d1a2b3c4-...","propuesta":{"temp1":196,"velocidad":0.22,"z":0.03,"prediccion":{"pctCrudos":1.1,"pctOk":96.8,"pctQuemados":2.1,"modelVersion":"v3","esPlaceholder":false}},"decision_id":"8f14e45f-...","autorizada":true,"ventana":"2026-11-03T11:55:00Z"}

: heartbeat
```

`autorizada` es `true` en AUTOMATICO y `false` en SUPERVISION hasta la
aprobación. Se manda `: heartbeat` cada 30s.

| Código | Motivo |
|---|---|
| 405 (texto plano) | Método distinto de GET |
| 401 (JSON) | Falta la cookie JWT o la sesión es inválida/expirada |
| 500 (texto plano) | El cliente HTTP no soporta streaming |

---

## 31. `GET /api/v1/reportes/resumen` — Resumen agregado del período

Devuelve agregados de producción, calidad, proceso, alertas y automatización
para un rango de fechas ART (`America/Argentina/Buenos_Aires`), con filtros
opcionales por sector, producto y turno. Los días se agrupan por fecha ART y
las series sólo incluyen días con datos.

**Auth:** cookie JWT `session_token` (cualquier rol autenticado).

| Query param | Obligatorio | Descripción |
|---|---|---|
| `desde` / `hasta` | No | `YYYY-MM-DD`, inclusivos. Default: últimos 30 días (hasta = hoy ART). Rango máximo: 366 días inclusive. |
| `sector_id` | No | Filtra lotes/alertas del sector. |
| `producto_id` | No | UUID del producto. |
| `turno` | No | `mañana`, `tarde` o `noche`. |

```bash
curl -b "session_token=$JWT" \
  "http://localhost:8080/api/v1/reportes/resumen?desde=2026-10-01&hasta=2026-10-31&sector_id=horno-1"
```

```json
{
  "success": true,
  "message": "Resumen generado",
  "data": {
    "periodo": {"desde":"2026-10-01","hasta":"2026-10-31","timezone":"America/Argentina/Buenos_Aires"},
    "filtros": {"sector_id":"horno-1","producto_id":null,"turno":null},
    "produccion": {
      "lotes_total":42,"lotes_cerrados":40,"lotes_abiertos":2,
      "unidades":12500,"unidades_ok":11800,"unidades_crudas":400,"unidades_quemadas":300,
      "kg_total_estimados":3125.0,"kg_ok_estimados":2950.0,"kg_merma_estimados":175.0,
      "duracion_media_min":48.2,"duracion_p95_min":75.0,
      "unidades_por_hora":260.4,"kg_por_hora":65.1,
      "por_turno":[{"clave":"mañana","nombre":"","lotes":14,"unidades":4200,"kg_estimados":1050.0,"fpy_pct":94.5}],
      "por_sector":[{"clave":"horno-1","nombre":"Horno 1","lotes":20,"unidades":6000,"kg_estimados":1500.0,"fpy_pct":93.0}],
      "por_producto":[{"clave":"a1b2c3d4-...","nombre":"Tostada Integral","lotes":30,"unidades":9000,"kg_estimados":2250.0,"fpy_pct":95.0}]
    },
    "calidad": {
      "fpy_pct":94.4,"crudo_pct":3.2,"quemado_pct":2.4,
      "confianza_promedio":0.87,"lotes_conformes_pct":85.0,
      "serie_diaria":[{"fecha":"2026-10-01","lotes":2,"unidades":420,"kg_estimados":105.0,"fpy_pct":93.8,"crudo_pct":3.5,"quemado_pct":2.7}]
    },
    "proceso": {
      "temp_mae_c":2.1,"temp_en_tolerancia_pct":88.5,
      "vel_mae":0.012,"vel_en_tolerancia_pct":91.2,
      "serie_diaria":[{"fecha":"2026-10-01","temp_real_prom":181.2,"temp_objetivo_prom":180.0,"temp_en_tolerancia_pct":87.0}]
    },
    "alertas": {
      "total":18,"por_severidad":{"INFO":10,"WARNING":6,"CRITICAL":2},
      "por_tipo":[{"clave":"TEMPERATURA_HORNO","total":8}],
      "ack_pct":83.3,"mtta_min":12.5,
      "serie_diaria":[{"fecha":"2026-10-01","total":2,"criticas":0}]
    },
    "automatizacion": {
      "decisiones_total":320,"mpc_pct":62.5,"operario_pct":20.0,"sin_aplicar_pct":17.5,
      "z_mae":0.18,
      "top_skip":[{"clave":"confianza_baja","total":12}],
      "modo_pct":{"AUTOMATICO":55.0,"SUPERVISION":35.0,"MANUAL":10.0}
    }
  }
}
```

Sólo los lotes `CERRADO` con `inicio_at` en el período suman unidades/kg y
calidad; los `ABIERTO` se informan aparte en `lotes_abiertos`. Las tasas,
promedios y duraciones sin datos viajan como `null`; los arrays siempre son
`[]`.

| Código | Motivo |
|---|---|
| 405 | Método distinto de GET |
| 401 | Falta la cookie JWT o la sesión es inválida/expirada |
| 400 | Fecha inválida, `desde > hasta`, rango > 366 días, turno inválido o `producto_id` no UUID |
| 500 | Error interno |

---

## 32. `GET /api/v1/reportes/lotes` — Detalle paginado de lotes del período

Devuelve el detalle de lotes (CERRADOS y ABIERTOS) cuyo `inicio_at` cae en el
período ART, con conteos, kg estimados y métricas derivadas. Es la fuente para
el CSV y el anexo del PDF; no reemplaza al historial operativo de
`GET /api/v1/lotes`.

**Auth:** cookie JWT `session_token` (cualquier rol autenticado).

| Query param | Obligatorio | Descripción |
|---|---|---|
| `desde` / `hasta` | No | `YYYY-MM-DD`, inclusivos. Default: últimos 30 días (hasta = hoy ART). Rango máximo: 366 días inclusive. |
| `sector_id` | No | Filtra por sector. |
| `producto_id` | No | UUID del producto. |
| `turno` | No | `mañana`, `tarde` o `noche`. |
| `limite` | No | Filas por página. Default 100, máximo 1000. |
| `antes_de` | No | Cursor opaco de la página anterior (base64url de `<inicio_at RFC3339Nano>\|<id>`). Se devuelve en `siguiente_cursor`. |

Orden: `inicio_at DESC, id DESC`. `siguiente_cursor` viaja sólo cuando la
página se llenó (hay más filas). Los lotes ABIERTOS traen
`cerrado_en`/`motivo_cierre`/`duracion_min` en `null` y conteos parciales; los
buckets nulos se preservan en `conteos` y `kg`.

```bash
curl -b "session_token=$JWT" \
  "http://localhost:8080/api/v1/reportes/lotes?desde=2026-10-01&hasta=2026-10-31&limite=100"
```

```json
{
  "success": true,
  "message": "Lotes del reporte obtenidos",
  "data": [
    {
      "id": "a1b2c3d4-5678-90ab-cdef-1234567890ab",
      "sector_id": "horno-1",
      "sector_nombre": "Sector 1",
      "producto_id": "b2c3d4e5-6789-0abc-def1-234567890abc",
      "producto_nombre": "Tostada Integral",
      "turno": "mañana",
      "estado": "CERRADO",
      "abierto_en": "2026-10-01T10:00:00-03:00",
      "cerrado_en": "2026-10-01T11:00:00-03:00",
      "motivo_cierre": "manual",
      "modo": "AUTOMATICO",
      "conteos": {"ok": 90, "crudo": 6, "quemado": 4, "total": 100},
      "kg": {"ok": 22.5, "crudo": 1.5, "quemado": 1.0, "total": 25.0, "merma": 2.5},
      "confianza_promedio": 0.8,
      "duracion_min": 60.0,
      "fpy_pct": 90.0,
      "crudo_pct": 6.0,
      "quemado_pct": 4.0
    }
  ],
  "total": 42,
  "page": 1,
  "pageSize": 100,
  "siguiente_cursor": "MjAyNi0xMC0wMVQxMzowMDowMFp8YTFCMkMzRDQtNTY3OC05MEFCLUNERUYtMTIzNDU2Nzg5MEFC"
}
```

`total` cuenta las filas que matchean los filtros sin aplicar el cursor. Las
tasas viajan en `null` cuando la suma de buckets es 0; `duracion_min` sólo se
calcula si el lote tiene `fin_at`.

| Código | Motivo |
|---|---|
| 405 | Método distinto de GET |
| 401 | Falta la cookie JWT o la sesión es inválida/expirada |
| 400 | Fecha inválida, `desde > hasta`, rango > 366 días, turno inválido, `producto_id` no UUID, `limite` no numérico o `antes_de` malformado |
| 500 | Error interno |

---

## 33. `GET /api/v1/reportes/trazabilidad` — Snapshot de trazabilidad de un lote

Devuelve en una sola respuesta todo lo necesario para el PDF de trazabilidad de
un lote: cabecera con conteos/kg/métricas derivadas y parámetros de control,
KPIs y serie temporal de proceso, confianza por clase, decisiones del MPC,
consignas, alertas y timestamp de generación. La lectura se hace en una
transacción read-only `REPEATABLE READ` para que todos los bloques compartan el
mismo snapshot.

**Auth:** cookie JWT `session_token` (cualquier rol autenticado).

| Query param | Obligatorio | Descripción |
|---|---|---|
| `lote_id` | Sí | UUID del lote. Si falta o es inválido → 400; si no existe → 404. |

Proceso: se calculan MAE y % en tolerancia sobre `metricas_horno` del lote,
ignorando filas con real u objetivo nulo. El objetivo de temperatura es
`COALESCE(m.temp_objetivo, lp.temp1_objetivo)` y el de velocidad
`COALESCE(m.vel_objetivo, lp.vel_objetivo)`; las tolerancias son
`lp.tolerancia_temp_c` y `COALESCE(lp.delta_vel_max, pr.delta_vel_max)`. La
serie agrega por buckets de `max(60s, ceil(duración/120))` (usa `now()` si el
lote está abierto) para acotar a ~120 puntos. Decisiones: últimas 100 por
`created_at DESC`. Consignas y alertas: todas, por fecha ascendente. Los arrays
siempre viajan como `[]`; tasas/promedios/valores opcionales viajan en `null`.

```bash
curl -b "session_token=$JWT" \
  "http://localhost:8080/api/v1/reportes/trazabilidad?lote_id=a1b2c3d4-5678-90ab-cdef-1234567890ab"
```

```json
{
  "success": true,
  "message": "Trazabilidad del lote",
  "data": {
    "lote": {
      "id": "a1b2c3d4-5678-90ab-cdef-1234567890ab",
      "sector_id": "horno-1",
      "sector_nombre": "Sector 1",
      "producto_id": "b2c3d4e5-6789-0abc-def1-234567890abc",
      "producto_nombre": "Tostada Integral",
      "turno": "mañana",
      "estado": "CERRADO",
      "abierto_en": "2026-11-04T10:00:00-03:00",
      "cerrado_en": "2026-11-04T11:00:00-03:00",
      "motivo_cierre": "manual",
      "modo": "AUTOMATICO",
      "conteos": {"ok": 90, "crudo": 6, "quemado": 4, "total": 100},
      "kg": {"ok": 22.5, "crudo": 1.5, "quemado": 1.0, "total": 25.0, "merma": 2.5},
      "confianza_promedio": 0.8,
      "duracion_min": 60.0,
      "fpy_pct": 90.0,
      "crudo_pct": 6.0,
      "quemado_pct": 4.0,
      "temp1_objetivo": 180.0,
      "vel_objetivo": 0.2,
      "tolerancia_temp_c": 2.0,
      "umbral_aceptacion_crudo": 5.0,
      "umbral_aceptacion_quemado": 5.0,
      "delta_temp1_max_c": null,
      "delta_vel_max": null
    },
    "proceso": {
      "temp_mae_c": 1.2,
      "temp_en_tolerancia_pct": 88.5,
      "vel_mae": 0.012,
      "vel_en_tolerancia_pct": 91.2,
      "serie": [
        {"ts": "2026-11-04T14:00:00-03:00", "temp_real": 181.2, "temp_objetivo": 180.0,
         "vel_real": 0.2, "vel_objetivo": 0.2, "muestras": 12}
      ]
    },
    "confianza_clases": [
      {"clase": "ok", "confianza_media": 0.9, "confianza_stddev": 0.05, "muestras": 100}
    ],
    "decisiones": [
      {"id": "d1e2f3a4-5678-90ab-cdef-1234567890ab", "creada_en": "2026-11-04T10:05:00-03:00",
       "ventana_inicio": "2026-11-04T10:00:00-03:00", "ventana_fin": "2026-11-04T10:05:00-03:00",
       "muestras": 30, "temp_actual": 180.0, "vel_actual": 0.2,
       "pct_ok_obs": 95.0, "pct_crudos_obs": 2.0, "pct_quemados_obs": 3.0,
       "conf_promedio": 0.9, "temp_propuesto": 181.0, "vel_propuesto": 0.2,
       "temp_aplicado": 181.0, "vel_aplicado": 0.2, "z_observado": 0.2,
       "z_predicho": 0.3, "razon_skipped": null, "aplicado_por": "MPC",
       "aprobada_por": null, "aprobada_at": null}
    ],
    "consignas": [
      {"id": "c1d2e3f4-5678-90ab-cdef-1234567890ab", "creada_en": "2026-11-04T10:00:00-03:00",
       "temperatura_objetivo": 180.0, "velocidad_cinta_objetivo": 0.2, "origen": "MANUAL",
       "usuario": "operario", "decision_id": "d1e2f3a4-5678-90ab-cdef-1234567890ab",
       "estado": "APLICADA", "exitosa": true, "motivo_error": null,
       "temperatura_previa": 179.0, "velocidad_cinta_previa": 0.19}
    ],
    "alertas": [
      {"id": "a1f2e3d4-5678-90ab-cdef-1234567890ab", "creada_en": "2026-11-04T10:10:00-03:00",
       "tipo": "TEMPERATURA_HORNO", "severidad": "CRITICAL", "estado": "ACK",
       "mensaje": "Temperatura fuera de rango", "acked_at": "2026-11-04T10:12:00-03:00"}
    ],
    "generado_en": "2026-11-04T11:05:00-03:00"
  }
}
```

| Código | Motivo |
|---|---|
| 405 | Método distinto de GET |
| 401 | Falta la cookie JWT o la sesión es inválida/expirada |
| 400 | `lote_id` ausente o no es un UUID |
| 404 | El lote no existe |
| 500 | Error interno |

---

## 34. `GET /api/v1/reportes/alertas` — Export de alertas del período

Devuelve las alertas cuyo `created_at` cae en el período ART, con los mismos
filtros del resumen más `estado`, y sin el tope operativo de 200 de
`GET /api/v1/alertas`. Es la fuente para exportar alertas desde el panel.

**Auth:** cookie JWT `session_token` (cualquier rol autenticado).

| Query param | Obligatorio | Descripción |
|---|---|---|
| `desde` / `hasta` | No | `YYYY-MM-DD`, inclusivos. Default: últimos 30 días (hasta = hoy ART). Rango máximo: 366 días inclusive. |
| `sector_id` | No | Filtra por `a.sector_id`. |
| `producto_id` | No | UUID del producto; se aplica vía el lote (las alertas sin lote quedan excluidas). |
| `turno` | No | `mañana`, `tarde` o `noche`; vía el lote (excluye alertas sin lote). |
| `estado` | No | `ACTIVA`, `ACK` o `RESUELTA`. Vacío = todas. Otro valor → 400. |
| `limite` | No | Filas a devolver. Default 1000, máximo 5000. No numérico → 400. |

Orden: `created_at DESC`. `total` cuenta las alertas que matchean los filtros
sin límite; `truncada` es `true` cuando `total` supera las filas devueltas.
`sector_id`, `lote_id`, `producto_id`, `turno`, `acked_at`, `acked_por` y
`mtta_min` viajan en `null` cuando no aplican. `mtta_min` es
`(acked_at - created_at)` en minutos y sólo se calcula con `acked_at` no nulo;
`acked_por` es el email del usuario que reconoció la alerta. Lista y total se
leen en una transacción read-only `REPEATABLE READ`.

Tipos de alerta actuales: `TEMPERATURA_HORNO`, `PRECALENTAMIENTO_TIMEOUT`,
`PORCENTAJE_QUEMADOS_ALTO`, `PORCENTAJE_CRUDOS_ALTO`, `DISPOSITIVO_OFFLINE` y
`TELEMETRIA_DESACTUALIZADA`. Quedan `ACTIVA` hasta el ACK (no hay auto-resolve).

```bash
curl -b "session_token=$JWT" \
  "http://localhost:8080/api/v1/reportes/alertas?desde=2026-10-01&hasta=2026-10-31&estado=ACK&limite=1000"
```

```json
{
  "success": true,
  "message": "Alertas del período",
  "data": {
    "total": 120,
    "truncada": false,
    "alertas": [
      {
        "id": "a1f2e3d4-5678-90ab-cdef-1234567890ab",
        "creada_en": "2026-11-04T14:02:00-03:00",
        "sector_id": "s1",
        "sector_nombre": "Sector 1",
        "lote_id": "b2c3d4e5-6789-0abc-def1-234567890abc",
        "producto_id": "c3d4e5f6-7890-abcd-ef12-34567890abcd",
        "producto_nombre": "Tostada Integral",
        "turno": "tarde",
        "tipo": "TEMPERATURA_HORNO",
        "severidad": "CRITICAL",
        "estado": "ACK",
        "mensaje": "Temperatura fuera de rango",
        "acked_at": "2026-11-04T14:12:00-03:00",
        "acked_por": "admin@fermar.com.ar",
        "mtta_min": 10.0
      }
    ]
  }
}
```

| Código | Motivo |
|---|---|
| 405 | Método distinto de GET |
| 401 | Falta la cookie JWT o la sesión es inválida/expirada |
| 400 | Fecha inválida, `desde > hasta`, rango > 366 días, turno inválido, `producto_id` no UUID, `estado` inválido o `limite` no numérico |
| 500 | Error interno |

---

## 35. `POST /api/v1/planificacion/{plan,paradas}/import` — Importación CSV para OEE

Importa el plan de producción y los eventos de parada que alimentan los
indicadores de OEE. Cada endpoint recibe un único archivo CSV en un body
`multipart/form-data` con el campo `file`.

**Auth:** cookie JWT `session_token`, roles **Supervisor** o **Administrador**.

| Endpoint | Tabla destino | Clave de UPSERT |
|---|---|---|
| `POST /api/v1/planificacion/plan/import` | `plan_produccion` | `(sector_id, inicio_at, producto_id)` |
| `POST /api/v1/planificacion/paradas/import` | `eventos_parada` | `(sector_id, inicio_at, causa_codigo)` |

### Formato y reglas comunes

- Límite de archivo: **5 MB**. Body `multipart/form-data`; el campo debe llamarse
  `file`. Si falta el archivo, el header es inválido o el archivo excede el
  límite, responde **400**.
- CSV con encoding UTF-8; se tolera **BOM** al inicio. La cabecera debe ser exacta
  (el orden importa); los valores se normalizan con `TrimSpace`.
- Fechas/horas: `YYYY-MM-DD HH:MM` se interpretan como hora de Argentina
  (`America/Argentina/Buenos_Aires`); también se acepta RFC3339 con offset
  (`2026-11-04T14:00:00-03:00`). Las fechas sueltas usan `YYYY-MM-DD`.
- Cada fila se valida de forma independiente. Las inválidas se reportan en
  `errores` con el número de fila (la fila 1 es la cabecera) y **no se insertan**.
- Antes de insertar se valida que `sector_id`, `producto_id`, y `lote_id` cuando
  viene, existan. Las filas válidas se escriben en una sola transacción con
  `ON CONFLICT ... DO UPDATE`.
- **Plan:** se rechazan por fila los planes que se solapen con otro plan del
  mismo sector (en la base o dentro del mismo CSV), con semántica half-open
  (`inicio < fin_otro && inicio_otro < fin`). El error se reporta como
  `"solape con otro plan del sector"` y la fila no se inserta; el resto del CSV
  se procesa igual. Reimportar la misma clave de UPSERT
  `(sector_id, inicio_at, producto_id)` actualiza sin considerarse solape.
- Si todas las filas fallan, la respuesta es **200** con `data.errores`; un error
  inesperado de base de datos es **500**.

**Cabeceras CSV:**

| Endpoint | Cabecera exacta |
|---|---|
| Plan | `sector_id,fecha,turno,producto_id,inicio,fin,kg_plan` |
| Paradas | `sector_id,lote_id,causa_codigo,tipo,inicio,fin,notas` |

Reglas por columna:

- **Plan:** `turno` ∈ `mañana|tarde|noche`; `kg_plan > 0`; `fin > inicio`.
- **Paradas:** `lote_id` vacío = sin lote; `tipo` ∈ `PLANIFICADA|NO_PLANIFICADA`;
  `causa_codigo` requerido; `notas` opcional; `fin > inicio`.

### Ejemplo

```bash
curl -b "session_token=$JWT" \
  -F "file=@plan.csv" \
  "http://localhost:8080/api/v1/planificacion/plan/import"
```

`plan.csv`:

```csv
sector_id,fecha,turno,producto_id,inicio,fin,kg_plan
horno-1,2026-11-04,mañana,a1b2c3d4-5678-90ab-cdef-1234567890ab,2026-11-04 06:00,2026-11-04 14:00,500.500
horno-1,2026-11-04,tarde,a1b2c3d4-5678-90ab-cdef-1234567890ab,2026-11-04 14:00,2026-11-04 22:00,480.000
```

```json
{
  "success": true,
  "message": "Plan importado",
  "data": {
    "procesadas": 2,
    "insertadas": 1,
    "actualizadas": 1,
    "errores": []
  }
}
```

Si alguna fila falla:

```json
{
  "success": true,
  "message": "Plan importado",
  "data": {
    "procesadas": 3,
    "insertadas": 2,
    "actualizadas": 0,
    "errores": [
      { "fila": 3, "mensaje": "sector_id inexistente" }
    ]
  }
}
```

| Código | Motivo |
|---|---|
| 200 | Importación procesada (aunque todas las filas tengan errores) |
| 400 | Falta el campo `file`, el archivo supera 5 MB, el body no es multipart o la cabecera/estructura CSV es inválida |
| 401 | Falta la cookie JWT o la sesión es inválida/expirada |
| 403 | El rol no es Supervisor ni Administrador |
| 405 | Método distinto de POST |
| 500 | Error inesperado de base de datos |

---

## 36. `GET /api/v1/reportes/oee` — OEE estándar

Calcula el OEE estándar del período, **global** y **por sector**. Usa el plan
de producción, las paradas y los lotes cerrados cargados por la importación CSV
(sección 35).

**Auth:** cookie JWT `session_token`, cualquier rol.

**Query params:**

| Param | Obligatorio | Descripción |
|---|---|---|
| `desde` | No | Fecha ART inclusive `YYYY-MM-DD`. Default: `hasta - 29 días`. |
| `hasta` | No | Fecha ART inclusive `YYYY-MM-DD`. Default: hoy. |
| `sector_id` | No | Sector a calcular. Vacío u omitido = todos. |

Mismas reglas de período que el resto de reportes: fechas inclusivas, máximo
**366 días**. **No** se admite filtro por `producto_id` ni `turno`: el plan y
las paradas no se pueden desagregar de forma consistente por producto/turno.

### Fórmulas

El período se traduce a `[desde 00:00, hasta+1día 00:00)` ART. Plan y paradas
se prorratean por su **solape** con esa ventana; los lotes se cuentan por
`inicio_at` dentro de la ventana (sin prorrateo).

- **Tiempo planificado** = suma del solape del plan, en minutos.
- **Horas planificadas por producto** = solape por producto / 3600.
- **Capacidad teórica kg** = Σ (`productos.capacidad_nominal_kg_h` × horas
  planificadas del producto). Si un producto planificado no tiene capacidad
  `> 0`, `performance_pct` es `null` y se agrega
  `sin_capacidad_producto:<uuid>`.
- **planned_downtime** = solape de paradas `PLANIFICADA`.
- **unplanned_downtime** = solape de paradas `NO_PLANIFICADA`.
- **operating** = `max(0, tiempo_planificado - unplanned_downtime)`.
- **kg_plan** = suma de `kg_plan` de las filas de plan **prorrateada por su
  solape** con el período (`kg_plan × solape / duración del plan`).
- **kg_producidos** = Σ `COALESCE(correctos_kg,0)+COALESCE(crudos_kg,0)+COALESCE(quemados_kg,0)`
  de lotes `CERRADO`.
- **kg_ok** = Σ `COALESCE(correctos_kg,0)` de esos lotes.
- **Availability** = `operating / tiempo_planificado × 100`. Sin plan: `null`
  y motivo `sin_plan_produccion`.
- **Performance** = `kg_producidos / capacidad_teórica × 100`. Sin capacidad:
  `null` y motivo `sin_capacidad_producto:<uuid>`.
- **Quality** = `kg_ok / kg_producidos × 100`. Sin producción: `null` y motivo
  `sin_produccion`.
- **OEE** = `A × P × Q / 10000`. Si falta A, P o Q, es `null`.

Redondeos: porcentajes a **2** decimales, kg a **3**, minutos a **1**.
`calculable` es `true` sólo si A, P y Q están calculados. `motivos` siempre es
un array (vacío si no hay faltantes).

### Sectores incluidos

`por_sector` incluye los sectores con datos de plan, lotes o paradas
en el período. Si se filtró `sector_id`, se incluye ese sector aunque no tenga
datos (`calculable=false` con sus motivos). Sin datos y sin filtro,
`por_sector` es `[]` y `global` queda `calculable=false` con
`sin_plan_produccion`. `global` agrega todos los sectores filtrados
(`sector_id=""`, `sector_nombre="Todos"`); los cocientes se recalculan sobre
los totales, no se promedian tasas.

### Ejemplo

```bash
curl -b "session_token=$JWT" \
  "http://localhost:8080/api/v1/reportes/oee?desde=2026-11-01&hasta=2026-11-04"
```

```json
{
  "success": true,
  "message": "OEE del período",
  "data": {
    "periodo": { "desde": "2026-11-01", "hasta": "2026-11-04", "timezone": "America/Argentina/Buenos_Aires" },
    "filtros": { "sector_id": null },
    "global": {
      "sector_id": "",
      "sector_nombre": "Todos",
      "calculable": true,
      "motivos": [],
      "availability_pct": 87.5,
      "performance_pct": 85.71,
      "quality_pct": 95.0,
      "oee_pct": 71.25,
      "planned_time_min": 480.0,
      "planned_downtime_min": 0.0,
      "unplanned_downtime_min": 60.0,
      "operating_time_min": 420.0,
      "kg_plan": 1000.0,
      "kg_producidos": 600.0,
      "kg_ok": 570.0,
      "capacidad_teorica_kg": 700.0
    },
    "por_sector": [
      {
        "sector_id": "horno-1",
        "sector_nombre": "Horno 1",
        "calculable": true,
        "motivos": [],
        "availability_pct": 87.5,
        "performance_pct": 85.71,
        "quality_pct": 95.0,
        "oee_pct": 71.25,
        "planned_time_min": 480.0,
        "planned_downtime_min": 0.0,
        "unplanned_downtime_min": 60.0,
        "operating_time_min": 420.0,
        "kg_plan": 1000.0,
        "kg_producidos": 600.0,
        "kg_ok": 570.0,
        "capacidad_teorica_kg": 700.0
      }
    ]
  }
}
```

| Código | Motivo |
|---|---|
| 200 | Cálculo generado (aunque no sea `calculable`) |
| 400 | Fecha inválida, `desde` posterior a `hasta` o rango mayor a 366 días |
| 401 | Falta la cookie JWT o la sesión es inválida/expirada |
| 405 | Método distinto de GET |
| 500 | Error inesperado de base de datos |

---

## 37. `GET`/`DELETE /api/v1/planificacion/{plan,paradas}` — Listado y borrado de lo importado

Lista lo importado en un período para revisarlo y permite borrar una fila por
id. La corrección se hace reimportando el CSV (UPSERT de la sección 35).

**Auth:** cookie JWT `session_token`, roles **Supervisor** o **Administrador**.

| Método y ruta | Tabla |
|---|---|
| `GET /api/v1/planificacion/plan` | `plan_produccion` |
| `GET /api/v1/planificacion/paradas` | `eventos_parada` |
| `DELETE /api/v1/planificacion/plan/{id}` | `plan_produccion` |
| `DELETE /api/v1/planificacion/paradas/{id}` | `eventos_parada` |

### Parámetros del listado

| Parámetro | Default | Reglas |
|---|---|---|
| `desde` | `hasta - 29 días` | `YYYY-MM-DD`, día ART inclusive |
| `hasta` | hoy (ART) | `YYYY-MM-DD`, día ART inclusive |
| `sector_id` | — | filtro opcional por sector |
| `limite` | 500 | entero; no numérico → **400**; no positivo usa el default; máximo 2000 |

- Rango máximo: **366 días** inclusive. `desde > hasta` → **400**.
- Orden: `inicio_at` (plan, paradas) **descendente**.
- `total` es el count con los mismos filtros, sin límite; `truncada` indica
  `total > len(items)`.
- El count y la lista salen de una transacción read-only `REPEATABLE READ`.
- Los timestamps viajan en hora ART (`-03:00`); `fecha` es `YYYY-MM-DD`.

Items:

- **Plan:** `id, sector_id, sector_nombre, producto_id, producto_nombre, fecha,
  turno, inicio_at, fin_at, kg_plan`.
- **Paradas:** `id, sector_id, sector_nombre, lote_id (null), inicio_at, fin_at,
  causa_codigo, tipo, notas (null)`.

### Borrado

`{id}` debe ser un UUID válido (**400** si no lo es) y existir (**404** si no
existe). Éxito:

```json
{"success": true, "message": "Registro eliminado"}
```

### Ejemplo

```json
{
  "success": true,
  "message": "Plan obtenido",
  "data": {
    "total": 12,
    "truncada": false,
    "items": [
      {
        "id": "11111111-1111-1111-1111-111111111111",
        "sector_id": "horno-1",
        "sector_nombre": "Horno 1",
        "producto_id": "22222222-2222-2222-2222-222222222222",
        "producto_nombre": "Pan Francés",
        "fecha": "2026-11-04",
        "turno": "mañana",
        "inicio_at": "2026-11-04T06:00:00-03:00",
        "fin_at": "2026-11-04T14:00:00-03:00",
        "kg_plan": 500
      }
    ]
  }
}
```

| Código | Motivo |
|---|---|
| 200 | Listado o borrado correcto |
| 400 | Fecha inválida, `desde > hasta`, rango > 366 días, `limite` no numérico o `id` no UUID |
| 401 | Falta la cookie JWT o la sesión es inválida/expirada |
| 403 | Rol distinto de Supervisor/Administrador |
| 404 | `id` inexistente o recurso desconocido |
| 405 | Método distinto de GET/DELETE (o DELETE sin `id`) |
| 500 | Error inesperado de base de datos |

---

## 38–40. Notificaciones de alertas (sólo Administrador)

Las alertas **WARNING/CRITICAL** se encolan por usuario activo en un outbox
persistente. Cada fila se reintenta con backoff hasta `NOTIFICACIONES_MAX_INTENTOS`
y el unique `(alerta_id, usuario_id, canal)` evita duplicados. Un canal sin
configuración (Resend, Gmail API o Telegram) queda deshabilitado sin impedir el
arranque.

### 38. `GET /api/v1/admin/notificaciones`

Devuelve el estado de los canales y todos los usuarios con sus preferencias
resueltas (sin fila de preferencias, ambos canales quedan en `true`).

```json
{
  "success": true,
  "message": "Configuración de notificaciones obtenida",
  "data": {
    "canales": {
      "email": { "configurado": true, "remitente": "alertas@fermar.com.ar" },
      "telegram": { "configurado": false }
    },
    "destinatarios": [
      {
        "usuario_id": "11111111-1111-1111-1111-111111111111",
        "email": "supervisor@fermar.com.ar",
        "nombre": "Supervisor Demo",
        "rol": "Supervisor",
        "activo": true,
        "telegram_chat_id": "123456789",
        "notificar_email": true,
        "notificar_telegram": true
      }
    ]
  }
}
```

### 39. `PATCH /api/v1/admin/notificaciones/{usuarioID}`

Body parcial; los campos omitidos no cambian. `telegram_chat_id: ""` limpia el
chat ID. El chat ID no vacío debe cumplir `^-?[0-9]{5,20}$`.

```json
{ "telegram_chat_id": "123456789", "notificar_email": false }
```

Devuelve el destinatario actualizado en `data`.

| Código | Motivo |
|---|---|
| 200 | Preferencias actualizadas |
| 400 | JSON inválido o chat ID inválido |
| 404 | `usuarioID` desconocido |
| 415 | Content-Type distinto de `application/json` |
| 500 | Error inesperado de base de datos |

### 40. `POST /api/v1/admin/notificaciones/test`

Envía una prueba directa (no pasa por el outbox). Body:

```json
{ "canal": "EMAIL", "usuario_id": "11111111-1111-1111-1111-111111111111" }
```

`canal` acepta `EMAIL` o `TELEGRAM` (case-insensitive). El email usa el email
del usuario; Telegram exige un chat ID cargado.

| Código | Motivo |
|---|---|
| 200 | Prueba enviada |
| 400 | Canal inválido, falta `usuario_id`, sin destino (`errors.code = "sin_destino"`) o chat ID inválido |
| 404 | Usuario desconocido |
| 409 | Canal no configurado (`errors.code = "canal_no_configurado"`) |
| 415 | Content-Type distinto de `application/json` |
| 502 | El proveedor rechazó el envío (sin exponer secretos) |

---

## Tabla resumen: auth y validaciones por endpoint

| Endpoint | Auth | Content-Type obligatorio | Límite body 1MB |
|---|---|---|---|
| `GET /api/v1/productos` | Dual (Bearer de dispositivo o JWT cookie) | — | — |
| `GET /api/v1/dispositivos/sector` | Bearer de dispositivo | — | — |
| `GET /api/v1/sectores` | JWT cookie (`session_token`) | — | — |
| `POST/PUT/DELETE /api/v1/sectores[/{id}]` | JWT cookie, Supervisor/Admin | ✅ (POST/PUT) | ✅ (POST/PUT) |
| `POST /api/v1/lotes/inicio` | Bearer de dispositivo | ✅ | ✅ |
| `GET /api/v1/lotes/abierto` | Dual (Bearer de dispositivo o JWT cookie) | — | — |
| `POST /api/v1/lotes/{id}/eventos` | Bearer de dispositivo | ✅ | ✅ |
| `POST /api/v1/lotes/{id}/cierre` | Bearer de dispositivo | ✅ | ✅ |
| `GET /api/v1/lotes` | Dual (Bearer de dispositivo o JWT cookie) | — | — |
| `POST /api/v1/dispositivos/ping` | Bearer de dispositivo | ✅ | ✅ |
| `PUT /api/v1/dispositivos` | JWT cookie (`session_token`), Supervisor/Admin | ✅ | ✅ |
| `PUT /api/v1/dispositivos/nombre` | Bearer de dispositivo | ✅ | ✅ |
| `PUT /api/v1/dispositivos/{id}/nombre` | JWT cookie (`session_token`), Supervisor/Admin | ✅ | ✅ |
| `DELETE /api/v1/dispositivos` | JWT cookie (`session_token`), Supervisor/Admin | — | — |
| `POST /api/v1/productos`, `PUT/DELETE /api/v1/productos/{id}` | JWT cookie, Supervisor/Admin | ✅ (POST/PUT) | ✅ (POST/PUT) |
| `POST /api/v1/horno/consigna` | JWT cookie, Operario/Supervisor/Admin | ✅ | ✅ |
| `GET /api/v1/dispositivos` | JWT cookie (`session_token`) | — | — |
| `GET /api/v1/dispositivos/metricas` | JWT cookie (`session_token`) | — | — |
| `GET /api/v1/lotes/{id}/control/estado` | JWT cookie | — | — |
| `PATCH /api/v1/lotes/{id}/modo` | JWT cookie, Supervisor/Admin | ✅ | ✅ |
| `PUT /api/v1/lotes/{id}/modo` | Bearer de dispositivo (dual-auth) | ✅ | ✅ |
| `POST /api/v1/lotes/{id}/control/start` y `/stop` | JWT cookie, Supervisor/Admin | ❌ | ❌ |
| `PATCH /api/v1/lotes/{id}/control/parametros` | JWT cookie, Supervisor/Admin | ✅ | ✅ |
| `GET /api/v1/lotes/{id}/consigna-propuesta` | Dual (Bearer de dispositivo o JWT cookie) | — | — |
| `POST /api/v1/lotes/{id}/aplicar-consigna` | Dual (Bearer de dispositivo o JWT Operario+) | ✅ | ✅ |
| `POST /api/v1/lotes/{id}/consigna/ack` | Bearer de dispositivo | ✅ | ✅ |
| `GET /api/v1/lotes/{id}/decisiones` | JWT cookie | — | — |
| `GET /api/v1/lotes/{id}/confianza-clases` | JWT cookie | — | — |
| `POST /api/v1/lotes/{id}/metricas-horno` | Bearer de dispositivo | ✅ | ✅ |
| `GET /api/v1/lotes/{id}/metricas-horno` | Dual (Bearer de dispositivo o JWT cookie) | — | — |
| `GET /api/v1/lotes/{id}/control/events` | JWT cookie (`session_token`) | — | — |
| `GET /*/events` (los SSE) | JWT cookie (`session_token`) | — | — |
| `GET /api/v1/reportes/resumen` | JWT cookie (`session_token`), cualquier rol | — | — |
| `GET /api/v1/reportes/lotes` | JWT cookie (`session_token`), cualquier rol | — | — |
| `GET /api/v1/reportes/trazabilidad` | JWT cookie (`session_token`), cualquier rol | — | — |
| `GET /api/v1/reportes/alertas` | JWT cookie (`session_token`), cualquier rol | — | — |
| `POST /api/v1/planificacion/plan/import` | JWT cookie, Supervisor/Admin | ✅ multipart | ✅ 5 MB |
| `POST /api/v1/planificacion/paradas/import` | JWT cookie, Supervisor/Admin | ✅ multipart | ✅ 5 MB |
| `GET /api/v1/planificacion/{plan,paradas}` | JWT cookie, Supervisor/Admin | — | — |
| `DELETE /api/v1/planificacion/{plan,paradas}/{id}` | JWT cookie, Supervisor/Admin | — | — |
| `GET /api/v1/reportes/oee` | JWT cookie (`session_token`), cualquier rol | — | — |
| `GET /api/v1/admin/notificaciones` | JWT cookie, Administrador | — | — |
| `PATCH /api/v1/admin/notificaciones/{usuarioID}` | JWT cookie, Administrador | ✅ | ✅ |
| `POST /api/v1/admin/notificaciones/test` | JWT cookie, Administrador | ✅ | ✅ |
| Otros `GET` | Según la sección del endpoint | — | — |
