# Levantar y probar Smart-Check Automation Backend (Go)

Este archivo describe pasos mínimos para instalar dependencias, ejecutar tests, levantar el servidor y probar el endpoint `GET /api/v1/lotes-productivos` en el entorno local.

## Requisitos
- Go 1.20 o superior instalado y en `PATH`.
- (Opcional) `curl` o PowerShell para llamadas HTTP.

## 1) Ejecutar la suite de tests
Abrir una terminal en la carpeta del proyecto:

```powershell
cd "d:\UTN\Proyecto Final\Codigo\backend-principal\test_backend_go"
# Ejecuta todos los tests del módulo
go test ./... -v
```

Resultado esperado: todos los tests actuales deben pasar. Si falta `go` en el PATH, instalá Go y volvé a ejecutar.

## 2) Levantar el servidor
En la misma carpeta ejecutá:

```powershell
cd "d:\UTN\Proyecto Final\Codigo\backend-principal\test_backend_go"
go run cmd/server/main.go
```

El servidor por defecto escucha en `:8080`. Mensaje esperado en logs:

```
Initializing Smart-Check Automation Backend (Layered/Clean Architecture - Modular)...
Transactional server running on http://localhost:8080
```

> Nota: el servidor usa repositorios simulados en memoria para datos (no PostgreSQL por defecto).

## 3) Probar el endpoint de lotes productivos
Llamada con PowerShell (recomendada en Windows):

```powershell
Invoke-RestMethod -Uri "http://localhost:8080/api/v1/lotes-productivos?page=1&pageSize=10" | ConvertTo-Json -Depth 10
```

Alternativa con curl:

```bash
curl -s "http://localhost:8080/api/v1/lotes-productivos?page=1&pageSize=10" | jq
```

Respuesta esperada (estructura resumida):

```json
{
  "success": true,
  "message": "Lotes productivos obtenidos exitosamente",
  "data": {
    "items": [ /* array de lotes */ ],
    "total": 2,
    "page": 1,
    "pageSize": 10,
    "totalPages": 1
  }
}
```

## 4) Archivos relevantes
- `cmd/server/main.go` — punto de entrada y registro de rutas.
- `internal/controller/lote_productivo/lote_productivo_handler.go` — handler HTTP.
- `internal/service/lote_productivo/lote_productivo_service.go` — lógica de paginación y límites.
- `internal/provider/database/lote_productivo_repo.go` — repo en memoria (seed data).
- `internal/domain/lote_productivo/lote_productivo.go` — modelo `LoteProductivo` y `PaginatedResult`.

## 5) Observaciones y recomendaciones
- Actualmente no hay tests unitarios para `lote_productivo` (handler ni servicio). Recomiendo agregar:
  - `internal/service/lote_productivo/lote_productivo_service_test.go` (paginación, límites, edge cases).
  - `internal/controller/lote_productivo/lote_productivo_handler_test.go` (httptest para la ruta).
- El repositorio documenta que debe devolver lotes "ordenados por inicio_at descendente", pero hoy devuelve el slice en el orden sembrado. Si necesitás orden garantizado, implementar `sort.Slice` por `InicioAt` antes de paginar en `lote_productivo_repo.go`.
- Para producción/CI: reemplazar el repo en memoria por uno que conecte a PostgreSQL.

## 6) Próximos pasos (sugeridos)
- Agregar tests para `lote_productivo`.
- Implementar ordenamiento por `InicioAt` en el repo.
- Añadir README/documentación del endpoint en `README.md` principal.

---
Archivo generado automáticamente para facilitar pruebas locales.
