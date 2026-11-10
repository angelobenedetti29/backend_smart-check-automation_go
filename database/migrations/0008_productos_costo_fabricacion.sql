-- Costo por pieza en ARS. NULL significa sin configurar; cero es un costo
-- explícito. No se inventan costos para productos existentes.
ALTER TABLE productos ADD COLUMN IF NOT EXISTS costo_fabricacion_unitario_ars numeric(12,2);

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
        WHERE conname = 'chk_productos_costo_fabricacion'
          AND conrelid = 'productos'::regclass) THEN
        ALTER TABLE productos ADD CONSTRAINT chk_productos_costo_fabricacion
            CHECK (costo_fabricacion_unitario_ars IS NULL OR
                (costo_fabricacion_unitario_ars >= 0 AND costo_fabricacion_unitario_ars <= 9999999999.99));
    END IF;
END $$;

COMMENT ON COLUMN productos.costo_fabricacion_unitario_ars IS
    'Costo actual de fabricación por pieza en ARS; NULL = sin configurar. Los reportes valorizan crudas + quemadas al costo actual.';
