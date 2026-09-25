# Device registration contract

Reemplaza al enrolamiento Ed25519 (retirado). El nodo Raspberry pide el alta, un
Supervisor/Admin la aprueba desde el panel, y desde ese momento autentica todos
sus POST con `Authorization: Bearer <secret>`. No hay `X-API-Key`, `DeviceProof`
ni clave pública.

## Flujo

1. **El nodo pide el registro** — `POST /api/v1/registration-requests` con
   `{"hostname":"smartcheck-rpi-01","type":"ENTRADA_HORNO"}`. Público. `type` es
   obligatorio (`ENTRADA_HORNO`/`SALIDA_HORNO`); ausente o inválido responde `400`.
   Devuelve `201` con JSON **plano** `{"request_id":"req_...","status":"PENDING"}`.
   El `request_id` son 32 bytes de `crypto/rand` en base64url (43 chars) y vence a
   los 15 minutos. Ver `docs/backend-go-tipos-dispositivo.md`.
2. **El backend guarda la solicitud** con estado `PENDING`. Un hostname no puede
   tener más de una solicitud `PENDING` (índice único parcial): repetir el alta
   devuelve la misma.
3. **El supervisor la ve** — `GET /api/v1/registration-requests`, con
   cookie JWT. Devuelve el envelope estándar con las solicitudes pendientes
   (siempre `PENDING`; el backend ignora cualquier filtro `status`).
4. **El supervisor aprueba o rechaza** — `POST /api/v1/registration-requests/{id}/approve`
   o `/reject`, cookie JWT y rol Supervisor/Admin. Aprobar crea el dispositivo
   (`nombre = hostname`, `auth_status='active'`), genera `device_id` (UUID de
   `dispositivos.id`) y un `secret`, y guarda **sólo** `SHA-256(secret)` en
   `dispositivos.secret_hash`. La respuesta nunca incluye el secret.
5. **El nodo consulta su solicitud** — `GET /api/v1/registration-requests/{id}`,
   público. Mientras no se resuelva devuelve `{"status":"PENDING"}` / `REJECTED` /
   `EXPIRED`. Aprobada, entrega **una sola vez**:
   `{"status":"APPROVED","device_id":"<uuid>","secret":"<secret>","type":"ENTRADA_HORNO"}`.
6. **El nodo ya está registrado** — envía `Authorization: Bearer <secret>`. El
   backend hashea el secret y busca el dispositivo activo por `secret_hash`, así
   que el nodo nunca manda `device_id`.

## Endpoints

| Método | Ruta | Auth | Formato |
|---|---|---|---|
| POST | `/api/v1/registration-requests` | público | plano |
| GET | `/api/v1/registration-requests` | JWT Supervisor/Admin | envelope |
| POST | `/api/v1/registration-requests/{id}/approve` | JWT Supervisor/Admin | envelope |
| POST | `/api/v1/registration-requests/{id}/reject` | JWT Supervisor/Admin | envelope |
| GET | `/api/v1/registration-requests/{id}` | público | plano |

Los endpoints de dispositivo usan JSON plano (sin envelope) porque el cliente
Raspberry ya parsea `request_id`/`status`/`device_id`/`secret` en la raíz. Los
endpoints de panel mantienen el envelope `{success,message,data,errors}`.

El cliente Raspberry debe apuntar `api.registration_requests_endpoint` a
`/api/v1/registration-requests` (config.json del repo de la Raspberry).

Errores de aprobación: `404` solicitud inexistente, `409` no está `PENDING`,
`410` vencida. Errores de pickup: `404` inexistente, plano.

## Autenticación de nodos

Endpoints device-only: `POST /api/v1/dispositivos/ping`,
`POST /api/v1/lotes/inicio`, `POST /api/v1/lotes/{id}/eventos`,
`POST /api/v1/lotes/{id}/cierre`, `GET /api/v1/dispositivos/sector` y
`PUT /api/v1/dispositivos/nombre`. El middleware `internal/controller/devicetoken` corre
antes del `ServeMux`, exige exactamente un header `Authorization: Bearer <token>`,
hashea el token y resuelve el dispositivo activo por `secret_hash`. El principal
(`deviceauth.Principal{DeviceID}`) se inyecta en el contexto: los handlers y
servicios toman de ahí la identidad y la provenance (`dispositivo_id` en lotes y
consignas). En `ping`, el `dispositivoId` del body es opcional y el servidor lo
inyecta desde el principal.

## Base de datos

- `registration_requests`: `request_id` PK, `hostname`, `tipo`
  (`ENTRADA_HORNO`/`SALIDA_HORNO`), `status`
  (`PENDING`/`APPROVED`/`REJECTED`/`EXPIRED`), `device_id` FK nullable, `secret`
  (texto plano sólo hasta el pickup), `resolved_by`/`resolved_at`, `created_at`,
  `expires_at`. Índice único parcial por hostname `PENDING`.
- `dispositivos`: `tipo` (`ENTRADA_HORNO`/`SALIDA_HORNO`, inmutable tras el alta),
  `auth_status` (`unenrolled`/`active`/`disabled`/`revoked`) y
  `secret_hash VARCHAR(64)` único parcial, nullable, nulo al revocar.
- `device_lifecycle_audit`: conservada; `approve`/`reject` dejan traza con actor,
  `request_id` y estados anterior/nuevo (sin secretos).
- Se eliminaron `device_credentials`, `device_enrollments`,
  `device_request_replays` y la columna `current_key_fingerprint`.

## Seguridad y límites

- `request_id` y `secret` son credenciales de alta capacidad: entropía de 256
  bits, `crypto/rand`, `Cache-Control: no-store`, y el `request_id` en la ruta se
  redacta en los logs.
- El pickup usa un `UPDATE ... RETURNING` transaccional: sólo un poll concurrente
  obtiene el secret. Se anula en la misma sentencia y vence a los 10 minutos.
- Aprobar/rechazar toman `SELECT ... FOR UPDATE` sobre la solicitud y un advisory
  lock; el dispositivo se crea en la misma transacción.
- **Limitación conocida:** si la respuesta del pickup se pierde en tránsito, el
  secret no se puede recuperar. Re-aprobar devuelve `409` (sólo se aprueba una
  solicitud `PENDING`), así que la recuperación exige que el nodo pida un nuevo
  registro; hoy eso crea una fila de dispositivo nueva para el mismo hostname.
  Queda como follow-up una rotación de secret sobre el dispositivo existente.

## Migración

`schema.Apply` es idempotente y hace los `DROP` del esquema Ed25519 en cada
arranque. Los dispositivos viejos quedan `unenrolled` hasta completar el nuevo
flujo. `DEVICE_AUTH_AUDIENCE` ya no se usa.
