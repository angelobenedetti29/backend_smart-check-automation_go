-- 0005_alertas_dispositivo.sql
-- Ata las alertas de dispositivo al índice único parcial (dispositivo_id, tipo).
-- Idempotente: seguro sobre instalaciones limpias (schema.sql ya creó la
-- columna) y sobre bases existentes.

ALTER TABLE alertas ADD COLUMN IF NOT EXISTS dispositivo_id uuid;

DO $$
BEGIN
    ALTER TABLE alertas
        ADD CONSTRAINT alertas_dispositivo_id_fkey
        FOREIGN KEY (dispositivo_id) REFERENCES dispositivos(id) ON DELETE SET NULL;
EXCEPTION
    WHEN duplicate_object THEN NULL;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS uq_alertas_activa_dispositivo_tipo
    ON alertas USING btree (dispositivo_id, tipo)
    WHERE ((estado = 'ACTIVA'::text) AND (dispositivo_id IS NOT NULL));

COMMENT ON COLUMN alertas.dispositivo_id IS 'Dispositivo afectado (nullable; SET NULL si se elimina el dispositivo)';
COMMENT ON COLUMN alertas.tipo IS 'Origen: TEMPERATURA_HORNO, PRECALENTAMIENTO_TIMEOUT, PORCENTAJE_QUEMADOS_ALTO, PORCENTAJE_CRUDOS_ALTO, DISPOSITIVO_OFFLINE o TELEMETRIA_DESACTUALIZADA';
