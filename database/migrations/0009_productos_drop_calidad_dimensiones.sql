-- Migración DESTRUCTIVA: elimina los parámetros de calidad por peso/dimensión
-- (tolerancia de peso y dimensión base/tolerancia) que ya no se usan. Los datos
-- de esas columnas se pierden; no hay vuelta atrás más que un backup.
-- Idempotente: DROP COLUMN IF EXISTS permite reaplicar sin error.
ALTER TABLE productos
    DROP COLUMN IF EXISTS tolerancia_peso_pct,
    DROP COLUMN IF EXISTS dimension_base_cm,
    DROP COLUMN IF EXISTS tolerancia_dimension_cm;
