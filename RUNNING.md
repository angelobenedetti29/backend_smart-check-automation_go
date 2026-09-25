# Cómo levantar el proyecto

## Requisitos

- [Docker Desktop](https://www.docker.com/products/docker-desktop/) instalado y corriendo
- No se necesita Go ni PostgreSQL instalados localmente

---

## 1. Configurar variables de entorno

`.env` está en `.gitignore`, así que **no viene con el clone**. Copiar la plantilla y
completar los valores:

```bash
cp .env.example .env
```

Variables requeridas:

```env
POSTGRES_USER=smartcheck
POSTGRES_PASSWORD=smartcheck123
POSTGRES_DB=smart_check

DATABASE_URL=postgres://smartcheck:smartcheck123@localhost:5432/smart_check?sslmode=disable
JWT_SECRET=una-clave-de-firma-de-al-menos-32-caracteres
GOOGLE_CLIENT_ID=tu-client-id.apps.googleusercontent.com
FRONTEND_ORIGINS=http://localhost:3000
PORT=8080
```

> `JWT_SECRET` y `GOOGLE_CLIENT_ID` son obligatorios: el servidor no arranca sin ellos
> (`cmd/server/main.go`). `FRONTEND_ORIGINS` define los orígenes permitidos por CORS
> (si se omite, el default es `http://localhost:3000,http://127.0.0.1:3000`).
> En producción la base es **Aiven PostgreSQL 16**; el `DATABASE_URL` de arriba es solo
> para el Docker local.

---

## 2. Levantar los contenedores

```bash
docker compose up --build
```

Esto levanta dos contenedores:
- **db** — PostgreSQL 16, crea las tablas automáticamente desde `database/schema.sql`
- **app** — la API Go, espera a que la DB esté lista antes de arrancar

La primera vez descarga las imágenes y compila el binario (tarda ~1 min). Las siguientes veces es mucho más rápido.

Logs esperados:
```
db-1   | database system is ready to accept connections
app-1  | Pool de conexiones a PostgreSQL inicializado y verificado exitosamente.
app-1  | Server running on http://localhost:8080
```

---

## 3. Verificar que funciona

```bash
# Chequeo de salud (DB conectada)
curl http://localhost:8080/health

# Historial de lotes por sector (vacío al principio; requiere Bearer de dispositivo o cookie JWT)
curl http://localhost:8080/api/v1/lotes

# Listar parámetros por producto (requiere cookie JWT; ya viene con el seed de Tostada Integral)
curl http://localhost:8080/api/v1/parametros-producto
```

---

## 4. Probar el ciclo de lote

El alta legada `POST /api/v1/lotes` (un lote ya finalizado) **fue retirada y responde `405`**.
El ciclo actual se inicia con `POST /api/v1/lotes/inicio`, que abre y persiste el lote
abierto del sector y **no dispara consigna**. Requiere `Authorization: Bearer <secret>`
del dispositivo (el secret del `device.json` aprobado), no un header de API key.

1. Método: `POST`
2. URL: `http://localhost:8080/api/v1/lotes/inicio`
3. Headers:
   - `Content-Type: application/json`
   - `Authorization: Bearer <secret del dispositivo>`
4. Body (raw JSON):

```json
{
  "idempotency_key": "0f8fad5b-d9cb-469f-a165-70867728950e",
  "producto_id": "a1b2c3d4-5678-90ab-cdef-1234567890ab",
  "momento": "2026-08-06T12:00:00Z"
}
```

Después hacer GET a `http://localhost:8080/api/v1/lotes` (con la misma credencial) para
ver el lote abierto/creado.

> El `producto_id` del ejemplo es uno de los 6 productos sembrados por el schema.
> Usar un UUID inexistente devuelve `422 producto_desconocido`.

> Detalle completo de request/response en [`docs/api-endpoints.md`](docs/api-endpoints.md) §6.

---

## 4bis. Probar el ABM de parámetros por producto

Requiere cookie JWT `session_token` (endpoints del panel de configuración; las escrituras
`POST`/`PUT` exigen rol Supervisor o Admin).

**Modificar el rango de temperatura de un producto existente (PUT):**

```bash
curl -X PUT http://localhost:8080/api/v1/parametros-producto \
  -b "session_token=<JWT>" \
  -H "Content-Type: application/json" \
  -d '{
    "productoId": "a1b2c3d4-5678-90ab-cdef-1234567890ab",
    "pesoReferenciaKg": 0.03,
    "toleranciaPesoPct": 10,
    "dimensionBaseCm": 8,
    "toleranciaDimensionCm": 0.5,
    "tempMin": 160,
    "tempMax": 180,
    "velocidadCintaMin": 0.1,
    "velocidadCintaMax": 0.3
  }'
```

Después hacer GET a `http://localhost:8080/api/v1/parametros-producto` para confirmar el cambio.

> El body es un reemplazo completo (no parcial): hay que enviar todos los campos. `tempMax` debe ser mayor a `tempMin` y `velocidadCintaMax` mayor a `velocidadCintaMin`, o el server responde 422.

---

## 5. Comandos útiles

```bash
# Detener los contenedores
docker compose down

# Detener y borrar la base de datos (reset completo)
docker compose down -v

# Ver logs en tiempo real
docker compose logs -f app

# Correr solo la DB (para desarrollo local con go run)
docker compose up db
```

---

## Desarrollo local sin Docker

Si tenés Go 1.25 instalado y la DB corriendo (por ej. con `docker compose up db`):

```bash
go run cmd/server/main.go
```

Para correr los tests unitarios (no requieren DB):

```bash
go test ./... -v
```

Para los tests de integración con DB real, configurar `TEST_DATABASE_URL` en el `.env`.
