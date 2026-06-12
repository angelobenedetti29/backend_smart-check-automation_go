# Smart-Check Automation — Backend (Go)

## Contexto del proyecto
Sistema de supervisión inteligente para hornos túnel (Fermar S.A.).
Backend en Go que expone una API REST consumida por un frontend Next.js.
Los datos son enviados por nodos Raspberry Pi via HTTP.

## Stack
- Lenguaje: Go
- Base de datos: PostgreSQL
- Frontend: Next.js (React) — consumidor de esta API
- Autenticación: Google OAuth 2.0

## Convenciones del equipo
- Nombres de structs: PascalCase
- Nombres de variables y funciones: camelCase
- Comentario breve por cada función
- Un archivo por componente/entidad
- Strings con comillas simples (en JS; en Go seguir estándar)
- Patrón: Handler → Service → Repository → PostgreSQL

## Estructura de carpetas actual
test_backend_go/
├── cmd/
│   └── server/
│       └── main.go
├── internal/
│   ├── controller/
│   │   └── horno/
│   │       ├── horno_handler.go
│   │       └── horno_handler_test.go
│   ├── domain/
│   │   ├── alerta/
│   │   │   └── alerta.go
│   │   └── horno/
│   │       └── horno.go
│   ├── provider/
│   │   ├── database/
│   │   │   ├── mysql_repo.go
│   │   │   └── mysql_repo_test.go
│   │   └── yolo_client/
│   │       └── client.go
│   └── service/
│       └── horno/
│           ├── horno_service.go
│           └── horno_service_test.go
├── pkg/
│   └── response/
│       └── response.go
├── go.mod
└── README.md

## Tarea actual: SCA-61 — Consulta de Lotes Productivos
Implementar el flujo completo para GET /lotes-productivos.

### Modelo definido (SCA-81 — ya acordado con el equipo)
// LoteProductivo representa una tanda de producción registrada en el sistema.
type LoteProductivo struct {
    ID               string     `json:"id"               db:"id"`
    ProductoID       string     `json:"productoId"       db:"producto_id"`
    ProductoNombre   string     `json:"productoNombre"   db:"producto_nombre"`
    Turno            string     `json:"turno"            db:"turno"`
    InicioAt         time.Time  `json:"inicioAt"         db:"inicio_at"`
    FinAt            *time.Time `json:"finAt,omitempty"  db:"fin_at"`
    TotalUnidades    int        `json:"totalUnidades"    db:"total_unidades"`
    Correctos        int        `json:"correctos"        db:"correctos"`
    Quemados         int        `json:"quemados"         db:"quemados"`
    CorrectosKg      float64    `json:"correctosKg"      db:"correctos_kg"`
    QuemadosKg       float64    `json:"quemadosKg"       db:"quemados_kg"`
    TempHorno1       float64    `json:"tempHorno1"       db:"temp_horno_1"`
    TempCombHorno1   *float64   `json:"tempCombHorno1,omitempty" db:"temp_comb_horno_1"`
    TempHorno2       float64    `json:"tempHorno2"       db:"temp_horno_2"`
    TempCombHorno2   *float64   `json:"tempCombHorno2,omitempty" db:"temp_comb_horno_2"`
    VelocidadHorno   float64    `json:"velocidadHorno"   db:"velocidad_horno"`
    CreatedAt        time.Time  `json:"createdAt"        db:"created_at"`
    UpdatedAt        time.Time  `json:"updatedAt"        db:"updated_at"`
}

CREATE TABLE lotes_productivos (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    producto_id         UUID NOT NULL,
    producto_nombre     VARCHAR(100) NOT NULL,
    turno               VARCHAR(20) NOT NULL,        -- 'mañana' | 'tarde' | 'noche'
    inicio_at           TIMESTAMPTZ NOT NULL,
    fin_at              TIMESTAMPTZ,                 -- NULL si el lote está activo
    total_unidades      INTEGER NOT NULL DEFAULT 0,
    correctos           INTEGER NOT NULL DEFAULT 0,
    quemados            INTEGER NOT NULL DEFAULT 0,
    correctos_kg        NUMERIC(10,2) NOT NULL DEFAULT 0.00,
    quemados_kg         NUMERIC(10,2) NOT NULL DEFAULT 0.00,
    temp_horno_1        NUMERIC(6,2) NOT NULL,       -- °C nodo entrada
    temp_comb_horno_1   NUMERIC(6,2),                -- °C combustión entrada (opcional)
    temp_horno_2        NUMERIC(6,2) NOT NULL,       -- °C nodo salida
    temp_comb_horno_2   NUMERIC(6,2),                -- °C combustión salida (opcional)
    velocidad_horno     NUMERIC(6,2) NOT NULL,       -- m/min o Hz según lo que envíe el IoT
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);


### Lo que hay que construir
- SCA-83: Service con método GetAll (paginado)
- SCA-84: Handler HTTP para GET /lotes-productivos
- SCA-86: Tests y documentación