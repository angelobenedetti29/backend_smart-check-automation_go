# Impacto económico de las mermas

La pérdida estimada es la suma de `(crudas + quemadas) × costo de fabricación
unitario actual` de cada variedad, expresada en ARS. No se utilizan porcentajes
fijos, precios de venta, kilogramos ni proyecciones de ahorro.

## Configuración y compatibilidad

`POST /api/v1/productos` y `PUT /api/v1/productos/{id}` aceptan
`costoFabricacionUnitarioArs`: número entre 0 y 9999999999.99, con hasta dos
decimales. `null` elimina el costo; `0` es un costo cero explícito. Un PUT que
omite el campo conserva el costo existente, para soportar clientes anteriores.
Se mantienen los permisos actuales: Supervisor/Administrador para escribir,
y el acceso existente al catálogo para consultarlo.

La migración `0008_productos_costo_fabricacion.sql` agrega una columna nullable
`numeric(12,2)`, sin asignar costos ficticios a los productos existentes. El
arranque del backend aplica esta migración automáticamente. También se puede
ejecutar `go run ./cmd/migrate` antes de desplegar el frontend.

## Consultas

- `GET /api/v1/reportes/resumen` agrega `data.impacto_economico` al contrato.
- `GET /api/v1/reportes/impacto-economico` devuelve únicamente
  `{periodo, filtros, impacto_economico}` para refrescar el panel.
- Ambos requieren JWT (cualquier rol actual), admiten `desde`, `hasta`,
  `sector_id`, `producto_id`, `turno`, y comparten su validación.
- El período por defecto abarca los últimos 30 días, inclusive, en ART. El
  máximo es 366 días. Se incluyen lotes abiertos y cerrados cuyo `inicio_at`
  cae en el período. Los lotes abiertos son cifras provisionales.
- La consulta económica usa todos los lotes filtrados; no depende de la
  paginación del listado. El resumen conserva su snapshot REPEATABLE READ.
- Detalle de lotes y trazabilidad agregan
  `costo_fabricacion_unitario_ars` y `perdida_merma_ars`.

Ejemplo de `impacto_economico` completo:

```json
{
  "moneda": "ARS",
  "referencia_costo": "actual_producto",
  "perdida_ars": 1255,
  "perdida_valorizada_ars": 1255,
  "unidades_merma": 10,
  "unidades_sin_costo": 0,
  "lotes_sin_conteos": 0,
  "por_producto": [{
    "producto_id": "uuid-del-producto",
    "nombre": "Pan",
    "costo_unitario_ars": 125.5,
    "unidades_merma": 10,
    "lotes_sin_conteos": 0,
    "perdida_ars": 1255
  }]
}
```

## Datos incompletos y criterio temporal

Cuando hay piezas de merma de un producto sin costo, `unidades_sin_costo`
cuenta esas piezas y `perdida_ars` es `null`. `perdida_valorizada_ars` conserva
el subtotal conocido. Los lotes cuyos dos buckets de defectos son NULL se
cuentan en `lotes_sin_conteos` y también impiden afirmar una pérdida total.
Un bucket NULL no suma piezas; si el otro está informado, se usa su conteo.
Si no hay piezas defectuosas ni lotes sin conteos, la pérdida es cero, incluso
si no existe un costo. Un período vacío devuelve arrays vacíos y cero.

Se valoriza al **costo actual** del catálogo, incluidos productos retirados.
Cambiarlo recalcula también los períodos anteriores. Es una estimación para
análisis operativo, no un registro contable de costos históricos. Esto se
aclara en el panel y los reportes. No se modifican los conteos ni los cálculos
existentes de calidad, kg, OEE o control del horno.

## Pruebas

`go test ./...` ejecuta pruebas unitarias. Para pruebas reales de migración,
persistencia, agregados, filtros ART y trazabilidad, configurar
`TEST_DATABASE_URL` con una instancia de desarrollo aislada y un usuario que
pueda crear bases. Los fixtures crean y eliminan sus propias bases temporales;
no utilizar una conexión de producción.
