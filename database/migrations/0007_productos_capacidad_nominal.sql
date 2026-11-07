-- 0007_productos_capacidad_nominal.sql
-- Capacidad nominal de producción por producto (kg/h).
-- Idempotente: seguro sobre instalaciones limpias y bases existentes.
ALTER TABLE productos ADD COLUMN IF NOT EXISTS capacidad_nominal_kg_h numeric(10,3);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'chk_productos_capacidad_nominal_positiva'
          AND conrelid = 'productos'::regclass
    ) THEN
        ALTER TABLE productos ADD CONSTRAINT chk_productos_capacidad_nominal_positiva
            CHECK (capacidad_nominal_kg_h IS NULL OR capacidad_nominal_kg_h > 0);
    END IF;
END $$;

COMMENT ON COLUMN productos.capacidad_nominal_kg_h IS 'Capacidad nominal de producción del producto en kg por hora; nullable, si se informa debe ser mayor a 0';
