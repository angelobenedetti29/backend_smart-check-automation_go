# Deployment — Smart-Check Automation Backend

Guía de los pasos manuales que se realizan una sola vez en dashboards externos.
El pipeline de CI/CD (GitHub Actions) automatiza el resto a partir de aquí.

---

## 1. Supabase — crear proyectos

1. Crear cuenta en [supabase.com](https://supabase.com).
2. Crear **dos proyectos** (uno por ambiente):
   - `smartcheck-staging`
   - `smartcheck-prod`
3. En cada proyecto, ir a **Project Settings → Database** y copiar la **connection string**.

### Connection string correcta — LEER ANTES DE COPIAR

Supabase expone dos modos de conexión a Postgres:

| Modo | Puerto | Uso |
|---|---|---|
| Transaction pooler | **6543** | Serverless / conexiones de corta vida. **No soporta prepared statements.** |
| Session pooler / Direct | **5432** | Conexiones persistentes. Soporta prepared statements y `pgx`. |

El backend mantiene un pool de conexiones persistente con `pgxpool` y usa
prepared statements internamente. **Siempre usar Session pooler o conexión
directa (puerto 5432).** Si se usa el puerto 6543, las migraciones y queries
fallarán de forma intermitente y difícil de diagnosticar.

Formato esperado:
```
postgres://postgres.<project-ref>:<password>@aws-0-<region>.pooler.supabase.com:5432/<dbname>?sslmode=require
```

---

## 2. Render — conectar repositorio y sincronizar Blueprint

1. Crear cuenta en [render.com](https://render.com).
2. En el dashboard, ir a **Blueprints → New Blueprint Instance**.
3. Conectar el repositorio de GitHub y seleccionar el archivo `render.yaml`.
4. Render detectará los dos servicios (`smartcheck-backend-staging` y `smartcheck-backend-prod`).
5. Para cada servicio, cargar las variables marcadas `sync: false` en el Blueprint:

| Variable | Descripción |
|---|---|
| `DATABASE_URL` | Connection string de Supabase (Session pooler, puerto 5432) |
| `API_KEY_SECRET` | Clave que envían los nodos Raspberry Pi en el header `X-API-Key` |
| `JWT_SECRET` | Secreto para firmar JWT (implementación futura de auth) |
| `GOOGLE_CLIENT_ID` | Client ID de Google OAuth (implementación futura) |

6. **No cargar** `PORT` — ya viene en el Blueprint con valor `8080`.

---

## 3. Render — obtener Deploy Hooks

Render genera una URL única por servicio que dispara un deploy al recibir un POST.

1. En el dashboard de cada servicio, ir a **Settings → Deploy Hook**.
2. Copiar la URL generada.

Estos hooks se cargan como secrets en GitHub (ver sección 4).

---

## 4. GitHub — crear Environments y cargar secrets

El workflow `migrate.yml` selecciona el environment por nombre de rama
(`staging` → environment `staging`, `main` → environment `production`).

1. En el repositorio de GitHub, ir a **Settings → Environments**.
2. Crear environment **`staging`** con los siguientes secrets:

| Secret | Valor |
|---|---|
| `DATABASE_URL` | Connection string de Supabase staging (puerto 5432) |
| `RENDER_DEPLOY_HOOK` | Deploy Hook del servicio `smartcheck-backend-staging` |

3. Crear environment **`production`** con los mismos secrets apuntando a los recursos de producción.

---

## 5. GitHub — configurar branch protection

Para que el CI sea obligatorio antes de mergear:

1. Ir a **Settings → Branches → Add rule**.
2. Configurar para las ramas `main` y `staging`:
   - Branch name pattern: `main` / `staging`
   - Activar **Require status checks to pass before merging**.
   - Agregar el check `Lint, Test & Build` (el job del workflow `ci.yml`).
   - Activar **Require branches to be up to date before merging**.

---

## 6. Flujo de trabajo resultante

```
Push a staging/main
      │
      ▼
GitHub Actions: migrate.yml
  1. psql -f database/schema.sql  → aplica schema contra Supabase (idempotente)
  2. curl POST $RENDER_DEPLOY_HOOK → dispara deploy en Render
      │
      ▼
Render buildea el Dockerfile y lanza el contenedor.
Render llama a GET /healthz — si responde 200, el deploy se marca como exitoso.
```

---

## 7. Evolución del esquema (migrations)

Actualmente el schema se aplica via `database/schema.sql`, que es idempotente
(`CREATE TABLE IF NOT EXISTS`, `ON CONFLICT DO NOTHING`). Esto es suficiente
para el MVP.

Cuando el esquema necesite cambios incrementales (nuevas columnas, índices,
seeds adicionales), adoptar **[golang-migrate](https://github.com/golang-migrate/migrate)**:

```bash
# Instalar CLI
go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest

# Crear primer archivo de migración (contiene el schema actual)
migrate create -ext sql -dir database/migrations -seq initial_schema

# Copiar el contenido de database/schema.sql al archivo _up.sql generado
```

El workflow `migrate.yml` debería actualizarse para llamar:
```bash
migrate -path database/migrations -database "$DATABASE_URL" up
```
