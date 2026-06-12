# Smart-Check Automation Backend

Backend transaccional para el monitoreo de hornos industriales y registro de lotes productivos en Fermar S.A.

---

## Requisitos

- Go 1.22 o superior
- Instancia PostgreSQL (Aiven Cloud, AWS sa-east-1)
- Conexión a internet para alcanzar la base de datos

---

## Configuración inicial

1. Clonar el repositorio.
2. Copiar `.env.example` como `.env` en la raíz del proyecto y configurar las variables:

```env
DATABASE_URL=postgres://usuario:contraseña@host:puerto/nombre_bd?sslmode=require
TEST_DATABASE_URL=postgres://usuario:contraseña@host:puerto/nombre_bd_test?sslmode=require
API_KEY_SECRET=mi_super_clave_secreta
PORT=8080
```

> **DATABASE_URL** debe apuntar a la instancia de PostgreSQL. El parámetro `?sslmode=require` es obligatorio para conexiones a Aiven.
>
> **TEST_DATABASE_URL** es opcional para tests de integración. Si no se provee, los tests de integración con base de datos real se omitirán automáticamente.
>
> **API_KEY_SECRET** es la clave que deben enviar los clientes (Raspberry Pi) en el header `X-API-Key`.

---

## Ejecutar el servidor

```bash
go run cmd/server/main.go
```

El servidor arranca en `http://localhost:8080`. Para detenerlo, presionar `Ctrl+C` — el servidor realiza un apagado graceful, esperando hasta 10 segundos a que las conexiones activas finalicen antes de cerrar el pool de PostgreSQL.

---

## Ejecutar tests

El proyecto incluye tests unitarios y tests de integración con la base de datos:

### Correr todos los tests (unitarios + integración si está TEST_DATABASE_URL configurada)
```bash
go test ./... -v
```

### Correr solo tests unitarios (sin requerir base de datos)
Si no configuras `TEST_DATABASE_URL` en tus variables de entorno, los tests de integración que requieren la base de datos real se omitirán (skipping) de manera segura, ejecutando únicamente los tests unitarios.

---

## Endpoints

### GET /

Verifica que el servidor esté corriendo (básico).

```bash
curl http://localhost:8080/
```

---

### GET /health

Realiza un chequeo de salud del sistema, incluyendo un ping en tiempo real a la base de datos PostgreSQL.

```bash
curl http://localhost:8080/health
```

**Respuestas:**
- `200 OK`: Base de datos conectada correctamente.
- `503 Service Unavailable`: Si hay algún problema de conexión con PostgreSQL.

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
  -H "X-API-Key: mi_super_clave_secreta" \
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
| `X-API-Key` | La clave definida en `API_KEY_SECRET` del `.env` (se compara usando `crypto/subtle` para evitar ataques de canal lateral/timing attacks) |
| `Content-Type` | Debe ser exactamente `application/json` |

**Reglas de Validación de Negocio (Lotes):**
1. El `id` debe ser un UUID v4 válido y no vacío.
2. El `productoId` debe ser un UUID v4 válido y no vacío.
3. El `turno` debe ser `mañana`, `tarde` o `noche`.
4. El `totalUnidades` debe ser mayor a 0.
5. El valor de `correctos + quemados` debe ser exactamente igual a `totalUnidades`.
6. El valor de `correctosKg` debe ser mayor o igual a 0.
7. El valor de `quemadosKg` debe ser mayor o igual a 0.
8. La fecha de fin (`finAt`) debe ser posterior a la fecha de inicio (`inicioAt`).

**Respuestas:**
| Código | Significado |
|---|---|
| 201 | Lote creado exitosamente |
| 400 | JSON malformado o body excedido (Límite: 1MB para prevenir ataques DoS) |
| 401 | API key faltante o inválida |
| 415 | Content-Type no soportado (diferente a `application/json`) |
| 422 | Error de validación de negocio (retorna JSON detallado con los errores específicos) |
| 500 | Error interno del servidor (base de datos) |

#### Ejemplo de Respuesta de Error de Validación (422):
```json
{
  "error": "validation failed",
  "details": {
    "totalUnidades": "totalUnidades must be equal to correctos + quemados",
    "turno": "turno must be one of: mañana, tarde, noche"
  }
}
```
