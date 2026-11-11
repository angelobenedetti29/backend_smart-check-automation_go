-- ============================================================================
-- legacy_upgrade.sql — puente para bases anteriores al esquema actual.
-- Se ejecuta ANTES de schema.sql en cada arranque. Es idempotente y no destructivo:
--   * En una base vacía no hace nada (las tablas no existen todavía).
--   * En una base vieja agrega las columnas que schema.sql / el código asumen
--     (schema.sql sólo crea tablas faltantes; sus COMMENT/índices fallarían).
--   * Copia los parámetros de control de parametros_producto a productos y deja
--     la tabla vieja intacta (se puede borrar a mano cuando se verifique).
-- ============================================================================

-- ---- productos: parámetros de control unificados --------------------------
DO $$
BEGIN
    IF to_regclass('productos') IS NULL THEN
        RETURN;
    END IF;

    ALTER TABLE productos
        ADD COLUMN IF NOT EXISTS peso_referencia_kg numeric(10,3),
        ADD COLUMN IF NOT EXISTS temp_min numeric(6,2),
        ADD COLUMN IF NOT EXISTS temp_max numeric(6,2),
        ADD COLUMN IF NOT EXISTS velocidad_cinta_min numeric(6,2),
        ADD COLUMN IF NOT EXISTS velocidad_cinta_max numeric(6,2),
        ADD COLUMN IF NOT EXISTS temp_setpoint numeric(6,2),
        ADD COLUMN IF NOT EXISTS velocidad_cinta_setpoint numeric(6,2),
        ADD COLUMN IF NOT EXISTS updated_at timestamp with time zone NOT NULL DEFAULT now(),
        ADD COLUMN IF NOT EXISTS delta_temp1_max_c numeric(5,2) NOT NULL DEFAULT 10.0,
        ADD COLUMN IF NOT EXISTS delta_vel_max numeric(5,3) NOT NULL DEFAULT 0.05;

    -- Copia desde la tabla vieja (una fila por producto; prioriza la activa y la
    -- más reciente). Sólo completa valores todavía nulos: nunca pisa datos.
    IF to_regclass('parametros_producto') IS NOT NULL THEN
        UPDATE productos p
        SET peso_referencia_kg       = COALESCE(p.peso_referencia_kg, s.peso_referencia_kg),
            temp_min                 = COALESCE(p.temp_min, s.temp_min),
            temp_max                 = COALESCE(p.temp_max, s.temp_max),
            velocidad_cinta_min      = COALESCE(p.velocidad_cinta_min, s.velocidad_cinta_min),
            velocidad_cinta_max      = COALESCE(p.velocidad_cinta_max, s.velocidad_cinta_max),
            temp_setpoint            = COALESCE(p.temp_setpoint, s.temp_setpoint),
            velocidad_cinta_setpoint = COALESCE(p.velocidad_cinta_setpoint, s.velocidad_cinta_setpoint)
        FROM (
            SELECT DISTINCT ON (producto_id) *
            FROM parametros_producto
            ORDER BY producto_id, activo DESC, updated_at DESC
        ) s
        WHERE s.producto_id = p.id;
    END IF;

    -- Productos sin parámetros en la tabla vieja: valores de arranque editables
    -- desde el panel (los mismos del seed de Tostada Integral).
    UPDATE productos SET
        peso_referencia_kg      = COALESCE(peso_referencia_kg, 0.250),
        temp_min                = COALESCE(temp_min, 180.00),
        temp_max                = COALESCE(temp_max, 220.00),
        velocidad_cinta_min     = COALESCE(velocidad_cinta_min, 0.10),
        velocidad_cinta_max     = COALESCE(velocidad_cinta_max, 0.30)
    WHERE peso_referencia_kg IS NULL
       OR temp_min IS NULL OR temp_max IS NULL
       OR velocidad_cinta_min IS NULL OR velocidad_cinta_max IS NULL;

    ALTER TABLE productos
        ALTER COLUMN peso_referencia_kg SET NOT NULL,
        ALTER COLUMN temp_min SET NOT NULL,
        ALTER COLUMN temp_max SET NOT NULL,
        ALTER COLUMN velocidad_cinta_min SET NOT NULL,
        ALTER COLUMN velocidad_cinta_max SET NOT NULL;
END $$;

-- ---- dispositivos ----------------------------------------------------------
ALTER TABLE IF EXISTS dispositivos
    ADD COLUMN IF NOT EXISTS rename_url text,
    ADD COLUMN IF NOT EXISTS rename_token text;

-- ---- lotes_productivos: control MPC y ventanas -----------------------------
ALTER TABLE IF EXISTS lotes_productivos
    ADD COLUMN IF NOT EXISTS modo character varying(12) NOT NULL DEFAULT 'MANUAL',
    ADD COLUMN IF NOT EXISTS ventana_evaluacion_segundos integer NOT NULL DEFAULT 600,
    ADD COLUMN IF NOT EXISTS ventana_actualizacion_segundos integer NOT NULL DEFAULT 300,
    ADD COLUMN IF NOT EXISTS tolerancia_temp_c numeric(5,2) NOT NULL DEFAULT 2.0,
    ADD COLUMN IF NOT EXISTS umbral_aceptacion_crudo numeric(5,2) NOT NULL DEFAULT 5.0,
    ADD COLUMN IF NOT EXISTS umbral_aceptacion_quemado numeric(5,2) NOT NULL DEFAULT 5.0,
    ADD COLUMN IF NOT EXISTS pesos_objetivo jsonb NOT NULL DEFAULT '{"dc": 0.5, "dt": 0.05, "wc": 1.0, "wo": 1.0, "wq": 1.0}'::jsonb,
    ADD COLUMN IF NOT EXISTS iniciado_manualmente boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS temp1_objetivo numeric(6,2),
    ADD COLUMN IF NOT EXISTS vel_objetivo numeric(6,3),
    ADD COLUMN IF NOT EXISTS delta_temp1_max_c numeric(6,3),
    ADD COLUMN IF NOT EXISTS delta_vel_max numeric(6,4),
    ADD COLUMN IF NOT EXISTS confianza_promedio numeric(4,3);

-- ---- historial_consignas: ciclo de vida ------------------------------------
DO $$
BEGIN
    IF to_regclass('historial_consignas') IS NULL THEN
        RETURN;
    END IF;

    ALTER TABLE historial_consignas
        ADD COLUMN IF NOT EXISTS decision_id uuid;

    -- Filas previas a la columna estado: se reconstruye desde exitosa.
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name = 'historial_consignas' AND column_name = 'estado'
    ) THEN
        ALTER TABLE historial_consignas
            ADD COLUMN estado character varying(20) NOT NULL DEFAULT 'PENDIENTE';
        UPDATE historial_consignas
        SET estado = CASE WHEN exitosa THEN 'APLICADA' ELSE 'RECHAZADA' END;
    END IF;
END $$;

-- ---- registration_requests: alta de nodos ----------------------------------
ALTER TABLE IF EXISTS registration_requests
    ADD COLUMN IF NOT EXISTS device_id uuid,
    ADD COLUMN IF NOT EXISTS secret text,
    ADD COLUMN IF NOT EXISTS rename_url text,
    ADD COLUMN IF NOT EXISTS resolved_by uuid,
    ADD COLUMN IF NOT EXISTS resolved_at timestamp with time zone,
    ADD COLUMN IF NOT EXISTS tipo character varying(16);

-- ---- usuarios / sectores ---------------------------------------------------
ALTER TABLE IF EXISTS usuarios
    ADD COLUMN IF NOT EXISTS password_hash character varying(255),
    ADD COLUMN IF NOT EXISTS activo boolean NOT NULL DEFAULT true,
    ADD COLUMN IF NOT EXISTS updated_at timestamp with time zone NOT NULL DEFAULT now();

ALTER TABLE IF EXISTS sectores
    ADD COLUMN IF NOT EXISTS created_at timestamp with time zone NOT NULL DEFAULT now();
