# Smart-Check Automation Backend

Backend transaccional para el monitoreo de hornos industriales y registro de lotes productivos en Fermar S.A.

---

## Requisitos

- Go 1.20 o superior
- Instancia PostgreSQL (Aiven Cloud, AWS sa-east-1)
- Conexión a internet para alcanzar la base de datos

---

## Configuración inicial

1. Clonar el repositorio.
2. Crear un archivo `.env` en la raíz del proyecto con las siguientes variables:

```env
DATABASE_URL=postgres://usuario:contraseña@host:puerto/nombre_bd?sslmode=require
API_KEY_SECRET=
PORT=8080
```

> `DATABASE_URL` debe apuntar a la instancia de PostgreSQL. El parámetro `?sslmode=require` es obligatorio para conexiones a Aiven.
>
> `API_KEY_SECRET` es la clave que deben enviar los clientes (Raspberry Pi) en el header `X-API-Key`.

---

## Ejecutar el servidor

```bash
go run cmd/server/main.go
```

El servidor arranca en `http://localhost:8080`. Para detenerlo, presionar `Ctrl+C` — el servidor realiza un apagado graceful, esperando hasta 10 segundos a que las conexiones activas finalicen antes de cerrar el pool de PostgreSQL.

---

## Ejecutar tests

```bash
go test ./... -v
```

---

## Endpoints

### GET /

Verifica que el servidor esté corriendo.

```bash
curl http://localhost:8080/
```

---

### GET /api/v1/horno?id=horno-01

Consulta el estado del horno industrial y ejecuta una inspección visual simulada (YOLO).

```bash
curl "http://localhost:8080/api/v1/horno?id=horno-01"
```

**Respuesta:** Estado del horno, alertas recientes y resultado de la inspección visual.

---

### POST /api/v1/horno/temperatura

Actualiza la temperatura del horno. Aplica reglas de umbral térmico:

- `> 200°C` → estado `MANTENIMIENTO` + alerta `CRITICAL`
- `> 180°C` → estado `ATENCION` + alerta `WARNING`
- `<= 180°C` → estado `ACTIVO`

```bash
curl -X POST http://localhost:8080/api/v1/horno/temperatura \
  -H "Content-Type: application/json" \
  -d '{"id":"horno-01","temperatura":212.8}'
```

---

### POST /api/v1/lotes

Registra un lote productivo horneado. Requiere autenticación via `X-API-Key`.

```bash
curl -X POST http://localhost:8080/api/v1/lotes \
  -H "X-API-Key:" \
  -H "Content-Type: application/json" \
  -d '{
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "productoId": "a1b2c3d4-5678-90ab-cdef-1234567890ab",
    "productoNombre": "Tostada Integral",
    "turno": "mañana",
    "inicioAt": "2026-06-02T06:00:00Z",
    "finAt": "2026-06-02T08:30:00Z",
    "totalUnidades": 1200,
    "correctos": 1150,
    "quemados": 50,
    "correctosKg": 138.00,
    "quemadosKg": 6.00,
    "tempHorno1": 210.50,
    "tempCombHorno1": null,
    "tempHorno2": 215.00,
    "tempCombHorno2": null,
    "velocidadHorno": 3.20,
    "createdAt": "2026-06-02T06:00:01Z",
    "updatedAt": "2026-06-02T08:30:05Z"
  }'
```

**Headers requeridos:**
| Header | Valor |
|---|---|
| `X-API-Key` | La clave definida en `API_KEY_SECRET` del `.env` |
| `Content-Type` | `application/json` |

**Regla de negocio:** `correctos + quemados` debe ser exactamente igual a `totalUnidades`.

**Respuestas:**
| Código | Significado |
|---|---|
| 201 | Lote creado exitosamente |
| 400 | JSON inválido o suma incorrecta de unidades |
| 401 | API key faltante o inválida |
| 500 | Error interno del servidor (base de datos) |
