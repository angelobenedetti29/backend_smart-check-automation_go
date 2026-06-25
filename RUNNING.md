# Cómo levantar el proyecto

## Requisitos

- [Docker Desktop](https://www.docker.com/products/docker-desktop/) instalado y corriendo
- No se necesita Go ni PostgreSQL instalados localmente

---

## 1. Configurar variables de entorno

Crear el archivo `.env` en la raíz del proyecto (ya existe si clonaste el repo con él):

```env
POSTGRES_USER=smartcheck
POSTGRES_PASSWORD=smartcheck123
POSTGRES_DB=smart_check

DATABASE_URL=postgres://smartcheck:smartcheck123@localhost:5432/smart_check?sslmode=disable
API_KEY_SECRET=dev-secret-key
PORT=8080
```

> El `.env` está en `.gitignore` — nunca se commitea con credenciales reales.

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

# Listar lotes productivos (vacío al principio)
curl http://localhost:8080/api/v1/lotes-productivos
```

---

## 4. Probar el POST desde Postman

1. Método: `POST`
2. URL: `http://localhost:8080/api/v1/lotes`
3. Headers:
   - `Content-Type: application/json`
   - `X-API-Key: dev-secret-key`
4. Body (raw JSON):

```json
{
  "productoId": "a1b2c3d4-5678-90ab-cdef-1234567890ab",
  "productoNombre": "Tostada Integral",
  "turno": "tarde",
  "inicioAt": "2026-06-12T14:00:00Z",
  "finAt": "2026-06-12T18:00:00Z",
  "totalUnidades": 500,
  "correctos": 480,
  "quemados": 20,
  "crudas": null,
  "correctosKg": 96.0,
  "quemadosKg": 4.0,
  "crudosKg": null,
  "tempHorno1": 188.0,
  "tempHorno2": 192.0,
  "velocidadCinta": 1.1,
  "createdAt": "2026-06-12T14:00:00Z",
  "updatedAt": "2026-06-12T18:00:00Z"
}
```

Después hacer GET a `http://localhost:8080/api/v1/lotes-productivos` para ver el lote creado.

> El `productoId` del ejemplo es el único producto sembrado por el schema (`Tostada Integral`).
> Usar un UUID diferente devuelve error de FK.

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
