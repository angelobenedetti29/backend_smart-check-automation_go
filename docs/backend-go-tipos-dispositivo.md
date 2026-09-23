# Backend Go — tipo de dispositivo

El nodo Raspberry declara su **tipo** al pedir el alta y el backend lo persiste junto
al dispositivo. El tipo es una propiedad directa del dispositivo (no un rol) y queda
inmutable una vez registrado: cambiarlo exige volver a registrar el nodo.

Valores permitidos (enum, mayúsculas exactas):

- `ENTRADA_HORNO`
- `SALIDA_HORNO`

Se normaliza con `trim` + mayúsculas antes de validar. Ausente, `null`, vacío o un
valor desconocido es siempre un error. **No existe un estado "sin tipo"** para una
solicitud ni para un dispositivo dado de alta por el flujo de registro.

## 1. Alta de registro — aceptar y validar `type`

Endpoint: `POST /api/v1/registration-requests` (público, JSON plano).

```json
{
    "hostname": "smartcheck-rbpi-01",
    "type": "ENTRADA_HORNO"
}
```

Reglas:

- `type` es obligatorio en el cuerpo.
- Sólo se aceptan `ENTRADA_HORNO` o `SALIDA_HORNO`.
- Ausente, `null`, vacío o valor desconocido → `400` con detalle del error.
- `hostname` mantiene sus reglas (requerido, ≤100 runes, sin espacios ni caracteres
  de control); cualquier error de validación del alta responde `400`.

## 2. Persistencia

- El `type` de la solicitud se guarda en `registration_requests.tipo` y, al aprobar,
  se copia al dispositivo en `dispositivos.tipo` (misma transacción).
- Un dispositivo tiene **un solo** tipo.
- Puede haber **múltiples dispositivos del mismo tipo**.
- El tipo **no se puede modificar** una vez registrado: no hay endpoint de cambio y
  ni el `PUT /api/v1/dispositivos` (panel) ni `PUT /api/v1/dispositivos/nombre`
  (rename del nodo) tocan `tipo`. Si una Raspberry necesita otro tipo, se registra de
  nuevo con el nuevo tipo; eso crea una fila de dispositivo nueva.
- Compatibilidad: la columna admite `NULL` para dispositivos legados creados antes de
  este cambio. Las altas nuevas (registro y panel) siempre exigen tipo.

## 3. Consulta de la solicitud — devolver `type`

Endpoint: `GET /api/v1/registration-requests/{request_id}` (público, JSON plano).

La respuesta incluye `type` además de `status`/`device_id`/`secret`:

```json
{
    "status": "APPROVED",
    "device_id": "f3e331e4-...",
    "secret": "...",
    "type": "ENTRADA_HORNO"
}
```

## 4. Respuestas con información del dispositivo

Cualquier endpoint que devuelva un dispositivo incluye `type`. No se infiere a partir
de otras propiedades.

```json
{
    "dispositivoId": "f3e331e4-...",
    "nombre": "smartcheck-rbpi-01",
    "type": "ENTRADA_HORNO"
}
```

Aplica al listado `GET /api/v1/dispositivos` (`DeviceRead`), a las respuestas de alta
/ modificación del panel (`EstadoDispositivo`) y al `Pickup` de la solicitud. El alta
manual por panel (`POST /api/v1/dispositivos`) también requiere `type`.

## 5. Fuera de alcance

- Conteo de productos, identificación/clasificación de productos.
- Detección de crudos/quemados, temperatura, velocidad de cinta.
- Procesamiento de archivos, estadísticas, inferencia.
- Comunicación específica por tipo de dispositivo.
- Roles o permisos para determinar el tipo (es una propiedad del dispositivo).

## Referencia del cliente

El cliente Python (`rework-rb/backend/device/`) ya envía `type` en el alta, lo valida,
lo persiste localmente y descarta un `device.json` legado sin `type` (fuerza un nuevo
registro). Este backend debe persistirlo y devolverlo para que el tipo sea consistente
del lado servidor.
