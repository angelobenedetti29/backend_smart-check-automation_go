-- ============================================================================
-- Smart-Check Automation — Fermar S.A.
-- Esquema de Base de Datos PostgreSQL (MVP)
-- Versión: 1.1
-- Motor:  PostgreSQL 16 (Aiven Cloud, AWS sa-east-1)
-- ============================================================================
-- Este script crea la estructura completa del modelo de datos para:
--   1. Tabla maestra: productos (incluye los parámetros de control embebidos)
--   2. Tabla transaccional: lotes_productivos (depende de productos vía FK)
--   3. Seed de datos de catálogo
--   4. Tablas de telemetría: dispositivos + metricas_dispositivo
--   5. Tabla de auditoría: historial_consignas (SCA-320)
--   6. Tabla de acceso: usuarios (autenticación y RBAC)
-- ============================================================================

BEGIN;

-- Defensas de la migración: si otra sesión retiene un lock o el DDL se cuelga,
-- abortamos en vez de bloquear el arranque indefinidamente. El advisory lock de
-- abajo queda acotado por lock_timeout; el resto de la migración por
-- statement_timeout.
SET lock_timeout = '3s';
SET statement_timeout = '60s';

-- Serialize the complete migration before any extension, table or seed DDL.
SELECT pg_advisory_xact_lock(hashtextextended('smart-check:schema', 0));

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ============================================================================
-- TABLA 1: productos (Catálogo maestro de productos panificados)
-- ============================================================================
CREATE TABLE IF NOT EXISTS productos (
    id          UUID            PRIMARY KEY DEFAULT gen_random_uuid(),
    nombre      VARCHAR(100)    NOT NULL,
    created_at  TIMESTAMPTZ     NOT NULL DEFAULT now()
);

COMMENT ON TABLE  productos      IS 'Catálogo maestro de productos panificados de Fermar S.A.';
COMMENT ON COLUMN productos.id   IS 'Identificador único universal del producto';
COMMENT ON COLUMN productos.nombre IS 'Nombre descriptivo del producto (ej: Tostada Integral, Pan Francés)';

-- ============================================================================
-- Unificación con parametros_producto (idempotente, ejecuta una sola vez)
-- Los parámetros de control de horneado pasan a ser columnas del producto.
-- ============================================================================
ALTER TABLE productos ADD COLUMN IF NOT EXISTS peso_referencia_kg      NUMERIC(10,3);
ALTER TABLE productos ADD COLUMN IF NOT EXISTS tolerancia_peso_pct     NUMERIC(5,2);
ALTER TABLE productos ADD COLUMN IF NOT EXISTS dimension_base_cm       NUMERIC(6,2);
ALTER TABLE productos ADD COLUMN IF NOT EXISTS tolerancia_dimension_cm NUMERIC(6,2);
ALTER TABLE productos ADD COLUMN IF NOT EXISTS temp_min                NUMERIC(6,2);
ALTER TABLE productos ADD COLUMN IF NOT EXISTS temp_max                NUMERIC(6,2);
ALTER TABLE productos ADD COLUMN IF NOT EXISTS velocidad_cinta_min     NUMERIC(6,2);
ALTER TABLE productos ADD COLUMN IF NOT EXISTS velocidad_cinta_max     NUMERIC(6,2);
ALTER TABLE productos ADD COLUMN IF NOT EXISTS updated_at              TIMESTAMPTZ     NOT NULL DEFAULT now();
-- Setpoints puntuales opcionales: sólo se usan si se reactiva el despacho
-- automático de consigna (SCA-142). Nullable.
ALTER TABLE productos ADD COLUMN IF NOT EXISTS temp_setpoint             NUMERIC(6,2);
ALTER TABLE productos ADD COLUMN IF NOT EXISTS velocidad_cinta_setpoint  NUMERIC(6,2);

COMMENT ON COLUMN productos.peso_referencia_kg      IS 'Peso patrón/ideal de una unidad del producto (kg)';
COMMENT ON COLUMN productos.tolerancia_peso_pct     IS 'Tolerancia admitida sobre el peso de referencia, en porcentaje';
COMMENT ON COLUMN productos.dimension_base_cm       IS 'Dimensión base/tamaño ideal del producto (cm)';
COMMENT ON COLUMN productos.tolerancia_dimension_cm IS 'Tolerancia de tamaño admitida sobre la dimensión base (cm)';
COMMENT ON COLUMN productos.temp_min                IS 'Temperatura mínima aceptable de horneado (°C)';
COMMENT ON COLUMN productos.temp_max                IS 'Temperatura máxima aceptable de horneado (°C)';
COMMENT ON COLUMN productos.velocidad_cinta_min     IS 'Velocidad mínima aceptable de la cinta transportadora (m/s)';
COMMENT ON COLUMN productos.velocidad_cinta_max     IS 'Velocidad máxima aceptable de la cinta transportadora (m/s)';
COMMENT ON COLUMN productos.updated_at              IS 'Marca temporal de la última modificación del registro';
COMMENT ON COLUMN productos.temp_setpoint             IS 'Temperatura objetivo dentro de [temp_min, temp_max]; nullable, opcional, sólo usado si se reactiva el despacho automático (SCA-142)';
COMMENT ON COLUMN productos.velocidad_cinta_setpoint IS 'Velocidad objetivo dentro de [velocidad_cinta_min, velocidad_cinta_max]; nullable, opcional';

-- Backfill desde la tabla legada parametros_producto (si existe) antes de dropearla.
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'parametros_producto') THEN
    UPDATE productos p SET
      peso_referencia_kg      = pp.peso_referencia_kg,
      tolerancia_peso_pct     = pp.tolerancia_peso_pct,
      dimension_base_cm       = pp.dimension_base_cm,
      tolerancia_dimension_cm = pp.tolerancia_dimension_cm,
      temp_min                = pp.temp_min,
      temp_max                = pp.temp_max,
      velocidad_cinta_min     = pp.velocidad_cinta_min,
      velocidad_cinta_max     = pp.velocidad_cinta_max,
      updated_at              = pp.updated_at
    FROM parametros_producto pp
    WHERE p.id = pp.producto_id;
  END IF;
END $$;

-- Backfill defensivo: cualquier fila NULL se rellena con valores seguros
-- antes de promover a NOT NULL. Los seeds ya cargan valores válidos.
UPDATE productos SET peso_referencia_kg = 0.001 WHERE peso_referencia_kg IS NULL;
UPDATE productos SET tolerancia_peso_pct = 0 WHERE tolerancia_peso_pct IS NULL;
UPDATE productos SET dimension_base_cm = 0.001 WHERE dimension_base_cm IS NULL;
UPDATE productos SET tolerancia_dimension_cm = 0 WHERE tolerancia_dimension_cm IS NULL;
UPDATE productos SET temp_min = 0 WHERE temp_min IS NULL;
UPDATE productos SET temp_max = 0.001 WHERE temp_max IS NULL;
UPDATE productos SET velocidad_cinta_min = 0 WHERE velocidad_cinta_min IS NULL;
UPDATE productos SET velocidad_cinta_max = 0.001 WHERE velocidad_cinta_max IS NULL;

-- Traslada los CHECK constraints de chk_parametros_* a chk_productos_*.
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'parametros_producto') THEN
    ALTER TABLE parametros_producto DROP CONSTRAINT IF EXISTS chk_parametros_peso_positivo;
    ALTER TABLE parametros_producto DROP CONSTRAINT IF EXISTS chk_parametros_tolerancia_peso;
    ALTER TABLE parametros_producto DROP CONSTRAINT IF EXISTS chk_parametros_dimension_positiva;
    ALTER TABLE parametros_producto DROP CONSTRAINT IF EXISTS chk_parametros_tolerancia_dimension;
    ALTER TABLE parametros_producto DROP CONSTRAINT IF EXISTS chk_parametros_temp_rango;
    ALTER TABLE parametros_producto DROP CONSTRAINT IF EXISTS chk_parametros_velocidad_rango;
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_productos_peso_positivo') THEN
    ALTER TABLE productos ADD CONSTRAINT chk_productos_peso_positivo CHECK (peso_referencia_kg > 0);
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_productos_tolerancia_peso') THEN
    ALTER TABLE productos ADD CONSTRAINT chk_productos_tolerancia_peso CHECK (tolerancia_peso_pct >= 0);
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_productos_dimension_positiva') THEN
    ALTER TABLE productos ADD CONSTRAINT chk_productos_dimension_positiva CHECK (dimension_base_cm > 0);
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_productos_tolerancia_dimension') THEN
    ALTER TABLE productos ADD CONSTRAINT chk_productos_tolerancia_dimension CHECK (tolerancia_dimension_cm >= 0);
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_productos_temp_rango') THEN
    ALTER TABLE productos ADD CONSTRAINT chk_productos_temp_rango CHECK (temp_max > temp_min);
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_productos_velocidad_rango') THEN
    ALTER TABLE productos ADD CONSTRAINT chk_productos_velocidad_rango CHECK (velocidad_cinta_max > velocidad_cinta_min);
  END IF;
END $$;

-- Rangos de los setpoints opcionales (NULL siempre permitido).
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_productos_temp_setpoint_rango') THEN
    ALTER TABLE productos ADD CONSTRAINT chk_productos_temp_setpoint_rango
      CHECK (temp_setpoint IS NULL OR (temp_setpoint >= temp_min AND temp_setpoint <= temp_max));
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_productos_velocidad_setpoint_rango') THEN
    ALTER TABLE productos ADD CONSTRAINT chk_productos_velocidad_setpoint_rango
      CHECK (velocidad_cinta_setpoint IS NULL OR (velocidad_cinta_setpoint >= velocidad_cinta_min AND velocidad_cinta_setpoint <= velocidad_cinta_max));
  END IF;
END $$;

-- La tabla legada queda sin uso una vez migrados los datos.
DROP TABLE IF EXISTS parametros_producto;

-- Promueve los parámetros de control a NOT NULL. El backfill defensivo de
-- arriba garantiza que no queden NULL; ALTER COLUMN SET NOT NULL es idempotente.
DO $$
DECLARE
  null_count INTEGER;
BEGIN
  SELECT COUNT(*) INTO null_count FROM productos WHERE peso_referencia_kg IS NULL;
  IF null_count = 0 THEN
    ALTER TABLE productos ALTER COLUMN peso_referencia_kg SET NOT NULL;
  END IF;

  SELECT COUNT(*) INTO null_count FROM productos WHERE tolerancia_peso_pct IS NULL;
  IF null_count = 0 THEN
    ALTER TABLE productos ALTER COLUMN tolerancia_peso_pct SET NOT NULL;
  END IF;

  SELECT COUNT(*) INTO null_count FROM productos WHERE dimension_base_cm IS NULL;
  IF null_count = 0 THEN
    ALTER TABLE productos ALTER COLUMN dimension_base_cm SET NOT NULL;
  END IF;

  SELECT COUNT(*) INTO null_count FROM productos WHERE tolerancia_dimension_cm IS NULL;
  IF null_count = 0 THEN
    ALTER TABLE productos ALTER COLUMN tolerancia_dimension_cm SET NOT NULL;
  END IF;

  SELECT COUNT(*) INTO null_count FROM productos WHERE temp_min IS NULL;
  IF null_count = 0 THEN
    ALTER TABLE productos ALTER COLUMN temp_min SET NOT NULL;
  END IF;

  SELECT COUNT(*) INTO null_count FROM productos WHERE temp_max IS NULL;
  IF null_count = 0 THEN
    ALTER TABLE productos ALTER COLUMN temp_max SET NOT NULL;
  END IF;

  SELECT COUNT(*) INTO null_count FROM productos WHERE velocidad_cinta_min IS NULL;
  IF null_count = 0 THEN
    ALTER TABLE productos ALTER COLUMN velocidad_cinta_min SET NOT NULL;
  END IF;

  SELECT COUNT(*) INTO null_count FROM productos WHERE velocidad_cinta_max IS NULL;
  IF null_count = 0 THEN
    ALTER TABLE productos ALTER COLUMN velocidad_cinta_max SET NOT NULL;
  END IF;
END $$;

-- ============================================================================
-- TABLA 2: lotes_productivos (Registro transaccional de lotes horneados)
-- ============================================================================
CREATE TABLE IF NOT EXISTS lotes_productivos (
    id                  UUID            PRIMARY KEY DEFAULT gen_random_uuid(),
    producto_id         UUID            NOT NULL,
    turno               VARCHAR(10)     NOT NULL,
    inicio_at           TIMESTAMPTZ     NOT NULL,
    fin_at              TIMESTAMPTZ     NOT NULL,
    total_unidades      INTEGER         NOT NULL,
    correctos           INTEGER         NOT NULL,
    quemados            INTEGER         NOT NULL,
    crudas              INTEGER,
    correctos_kg        NUMERIC(10,2)   NOT NULL,
    quemados_kg         NUMERIC(10,2)   NOT NULL,
    crudos_kg           NUMERIC(10,2),
    temp_horno_1        NUMERIC(6,2),
    temp_comb_horno_1   NUMERIC(6,2),
    velocidad_cinta     NUMERIC(6,2),
    created_at          TIMESTAMPTZ     NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ     NOT NULL DEFAULT now(),

    -- FOREIGN KEY: garantiza integridad referencial con la tabla maestra
    CONSTRAINT fk_lotes_productos
        FOREIGN KEY (producto_id)
        REFERENCES productos (id)
        ON DELETE RESTRICT
        ON UPDATE CASCADE,

    -- RESTRICCIONES DE DOMINIO (Check Constraints)

    -- Turno limitado a los 3 valores definidos por el negocio
    CONSTRAINT chk_lotes_turno
        CHECK (turno IN ('mañana', 'tarde', 'noche')),

    -- Total de unidades no negativo (permite lote vacío para cero operativo)
    CONSTRAINT chk_lotes_total_unidades_no_negativo
        CHECK (total_unidades >= 0),

    -- Los panes correctos no pueden ser negativos
    CONSTRAINT chk_lotes_correctos_no_negativo
        CHECK (correctos >= 0),

    -- Los panes quemados no pueden ser negativos
    CONSTRAINT chk_lotes_quemados_no_negativo
        CHECK (quemados >= 0),

    -- Los panes crudos no pueden ser negativos
    CONSTRAINT chk_lotes_crudas_no_negativo
        CHECK (crudas >= 0),

    -- Consistencia: la suma de correctos + quemados + crudas no puede exceder el total
    CONSTRAINT chk_lotes_sum_unidades_consistente
        CHECK (correctos + quemados + COALESCE(crudas, 0) <= total_unidades),

    -- Consistencia temporal: fin no puede ser anterior a inicio
    CONSTRAINT chk_lotes_fin_after_inicio
        CHECK (fin_at >= inicio_at)
);

COMMENT ON TABLE  lotes_productivos          IS 'Registro transaccional de lotes productivos horneados. Cada fila representa una horneada completa de un producto en un turno.';
COMMENT ON COLUMN lotes_productivos.id        IS 'Identificador UUID del lote productivo';
COMMENT ON COLUMN lotes_productivos.producto_id IS 'FK al catálogo de productos';
COMMENT ON COLUMN lotes_productivos.turno     IS 'Turno de producción: mañana, tarde o noche';
COMMENT ON COLUMN lotes_productivos.inicio_at IS 'Marca temporal de inicio de la horneada';
COMMENT ON COLUMN lotes_productivos.fin_at    IS 'Marca temporal de fin de la horneada';
COMMENT ON COLUMN lotes_productivos.total_unidades IS 'Total de unidades horneadas en el lote';
COMMENT ON COLUMN lotes_productivos.correctos IS 'Unidades en estado correcto (aprobadas)';
COMMENT ON COLUMN lotes_productivos.quemados  IS 'Unidades quemadas (rechazadas)';
COMMENT ON COLUMN lotes_productivos.crudas    IS 'Unidades crudas / sin clasificar (nullable)';
COMMENT ON COLUMN lotes_productivos.correctos_kg IS 'Peso total en kg de panes correctos';
COMMENT ON COLUMN lotes_productivos.quemados_kg  IS 'Peso total en kg de panes quemados';
COMMENT ON COLUMN lotes_productivos.crudos_kg    IS 'Peso total en kg de panes crudos (nullable)';
COMMENT ON COLUMN lotes_productivos.temp_horno_1 IS 'Temperatura del horno 1 al inicio (°C)';
COMMENT ON COLUMN lotes_productivos.temp_comb_horno_1 IS 'Temperatura de la cámara de combustión del horno 1 (°C, nullable)';
COMMENT ON COLUMN lotes_productivos.velocidad_cinta IS 'Velocidad de la cinta transportadora del horno (m/s)';

-- ============================================================================
-- ÍNDICES (Indexes)
-- ============================================================================

-- Índice para búsquedas por producto (FK lookup)
CREATE INDEX IF NOT EXISTS idx_lotes_producto_id
    ON lotes_productivos (producto_id);

-- Índice para filtros por rango de fecha de inicio
CREATE INDEX IF NOT EXISTS idx_lotes_inicio_at
    ON lotes_productivos (inicio_at);

-- Índice compuesto para la consulta más frecuente: todos los lotes de un
-- producto en un rango de fechas
CREATE INDEX IF NOT EXISTS idx_lotes_producto_fecha
    ON lotes_productivos (producto_id, inicio_at);

-- Índice para ordenamiento por creación
CREATE INDEX IF NOT EXISTS idx_lotes_created_at
    ON lotes_productivos (created_at);

-- ============================================================================
-- SEED: Datos de catálogo de productos
-- El producto de referencia incluye sus parámetros de control embebidos.
-- ============================================================================
INSERT INTO productos (
    id, nombre,
    peso_referencia_kg, tolerancia_peso_pct,
    dimension_base_cm, tolerancia_dimension_cm,
    temp_min, temp_max, velocidad_cinta_min, velocidad_cinta_max
) VALUES (
    'a1b2c3d4-5678-90ab-cdef-1234567890ab', 'Tostada Integral',
    0.250, 5.00,
    12.00, 0.50,
    180.00, 220.00, 0.10, 0.30
)
ON CONFLICT (id) DO NOTHING;

-- ============================================================================
-- TABLA 4: dispositivos (Catálogo de nodos Raspberry Pi / SBC de monitoreo)
-- ============================================================================
CREATE TABLE IF NOT EXISTS dispositivos (
    id             UUID           PRIMARY KEY DEFAULT gen_random_uuid(),
    nombre         VARCHAR(100)   NOT NULL,
    rename_url     TEXT,
    rename_token   TEXT,
    created_at     TIMESTAMPTZ    NOT NULL DEFAULT now()
);

COMMENT ON TABLE  dispositivos     IS 'Catálogo de nodos Raspberry Pi que reportan telemetría al backend';
COMMENT ON COLUMN dispositivos.id        IS 'Identificador UUID del dispositivo';
COMMENT ON COLUMN dispositivos.nombre    IS 'Nombre descriptivo del dispositivo (ej: Raspberry Pi Horno 1)';

-- ============================================================================
-- TABLA 5: metricas_dispositivo (Historial append-only de telemetría)
-- ============================================================================
CREATE TABLE IF NOT EXISTS metricas_dispositivo (
    id                    UUID            PRIMARY KEY DEFAULT gen_random_uuid(),
    dispositivo_id        UUID            NOT NULL,
    cpu_pct               NUMERIC(5,2)    NOT NULL CHECK (cpu_pct BETWEEN 0 AND 100),
    mem_ram_disponible_mb NUMERIC(10,2)   NOT NULL CHECK (mem_ram_disponible_mb >= 0),
    mem_ram_total_mb      NUMERIC(10,2),
    almacenamiento_disponible_mb NUMERIC(12,2),
    almacenamiento_total_mb      NUMERIC(12,2),
    temp_chip             NUMERIC(6,2)    NOT NULL,
    ai_processor_pct      NUMERIC(5,2)    NOT NULL CHECK (ai_processor_pct BETWEEN 0 AND 100),
    received_at           TIMESTAMPTZ     NOT NULL DEFAULT now(),

    -- FOREIGN KEY: garantiza integridad referencial con el catálogo de dispositivos
    CONSTRAINT fk_metricas_dispositivo_dispositivo
        FOREIGN KEY (dispositivo_id)
        REFERENCES dispositivos (id)
        ON DELETE CASCADE
        ON UPDATE CASCADE
);

COMMENT ON TABLE  metricas_dispositivo            IS 'Historial append-only de telemetría reportada por cada dispositivo (CPU, RAM, almacenamiento y temperatura del chip)';
COMMENT ON COLUMN metricas_dispositivo.id                    IS 'Identificador UUID del registro de métricas';
COMMENT ON COLUMN metricas_dispositivo.dispositivo_id        IS 'FK al catálogo de dispositivos';
COMMENT ON COLUMN metricas_dispositivo.cpu_pct               IS 'Uso de CPU en porcentaje (0-100)';
COMMENT ON COLUMN metricas_dispositivo.mem_ram_disponible_mb IS 'Memoria RAM disponible en MB';
COMMENT ON COLUMN metricas_dispositivo.temp_chip             IS 'Temperatura interna del chip en °C';
COMMENT ON COLUMN metricas_dispositivo.received_at           IS 'Marca temporal en que el backend recibió la métrica';

-- Índice para la consulta más frecuente: historial por dispositivo ordenado por tiempo
CREATE INDEX IF NOT EXISTS idx_metricas_dispositivo_disp_received
    ON metricas_dispositivo (dispositivo_id, received_at DESC);

-- ============================================================================
-- ALTER: metricas_dispositivo — uso del procesador de IA (NPU) (SCA-172)
-- Idempotente para bases existentes: agrega la columna solo si falta.
-- ============================================================================
ALTER TABLE metricas_dispositivo
    ADD COLUMN IF NOT EXISTS ai_processor_pct NUMERIC(5,2) NOT NULL DEFAULT 0
        CHECK (ai_processor_pct BETWEEN 0 AND 100);

COMMENT ON COLUMN metricas_dispositivo.ai_processor_pct IS 'Uso del procesador de IA (NPU) en porcentaje (0-100)';

-- ============================================================================
-- ALTER: metricas_dispositivo — capacidad de memoria y almacenamiento (SCA-36)
-- Idempotente para bases existentes. Las columnas quedan nullable para aceptar
-- pings legacy que solo informan memoria RAM disponible.
-- ============================================================================
ALTER TABLE metricas_dispositivo
    ADD COLUMN IF NOT EXISTS mem_ram_total_mb NUMERIC(10,2),
    ADD COLUMN IF NOT EXISTS almacenamiento_disponible_mb NUMERIC(12,2),
    ADD COLUMN IF NOT EXISTS almacenamiento_total_mb NUMERIC(12,2);

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_metricas_mem_ram_total_no_negativo') THEN
        ALTER TABLE metricas_dispositivo ADD CONSTRAINT chk_metricas_mem_ram_total_no_negativo
            CHECK (mem_ram_total_mb IS NULL OR mem_ram_total_mb >= 0);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_metricas_mem_ram_disponible_no_supera_total') THEN
        ALTER TABLE metricas_dispositivo ADD CONSTRAINT chk_metricas_mem_ram_disponible_no_supera_total
            CHECK (mem_ram_total_mb IS NULL OR mem_ram_disponible_mb <= mem_ram_total_mb);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_metricas_almacenamiento_disponible_no_negativo') THEN
        ALTER TABLE metricas_dispositivo ADD CONSTRAINT chk_metricas_almacenamiento_disponible_no_negativo
            CHECK (almacenamiento_disponible_mb IS NULL OR almacenamiento_disponible_mb >= 0);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_metricas_almacenamiento_total_no_negativo') THEN
        ALTER TABLE metricas_dispositivo ADD CONSTRAINT chk_metricas_almacenamiento_total_no_negativo
            CHECK (almacenamiento_total_mb IS NULL OR almacenamiento_total_mb >= 0);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_metricas_almacenamiento_par') THEN
        ALTER TABLE metricas_dispositivo ADD CONSTRAINT chk_metricas_almacenamiento_par
            CHECK ((almacenamiento_disponible_mb IS NULL) = (almacenamiento_total_mb IS NULL));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_metricas_almacenamiento_disponible_no_supera_total') THEN
        ALTER TABLE metricas_dispositivo ADD CONSTRAINT chk_metricas_almacenamiento_disponible_no_supera_total
            CHECK (almacenamiento_total_mb IS NULL OR almacenamiento_disponible_mb IS NULL OR almacenamiento_disponible_mb <= almacenamiento_total_mb);
    END IF;
END $$;

COMMENT ON COLUMN metricas_dispositivo.mem_ram_total_mb IS 'Memoria RAM total en MB (nullable para compatibilidad con pings legacy)';
COMMENT ON COLUMN metricas_dispositivo.almacenamiento_disponible_mb IS 'Almacenamiento disponible en MB (nullable para compatibilidad con pings legacy)';
COMMENT ON COLUMN metricas_dispositivo.almacenamiento_total_mb IS 'Almacenamiento total en MB (nullable para compatibilidad con pings legacy)';

-- ============================================================================
-- ALTER: dispositivos — cámara/stream WHEP y credencial de registro
-- Idempotente para bases existentes. whep_url y secret_hash quedan nullable:
-- un dispositivo sin cámara no expone stream y uno no registrado no tiene secret.
-- ============================================================================
ALTER TABLE dispositivos
    ADD COLUMN IF NOT EXISTS whep_url VARCHAR(500),
    ADD COLUMN IF NOT EXISTS auth_status VARCHAR(12) NOT NULL DEFAULT 'unenrolled',
    ADD COLUMN IF NOT EXISTS auth_updated_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS secret_hash VARCHAR(64);

-- El registro por solicitud/aprobación reemplaza la identidad Ed25519: el secret
-- se persiste sólo como SHA-256 hexadecimal (64 chars) y se resuelve por ese hash.
ALTER TABLE dispositivos DROP COLUMN IF EXISTS current_key_fingerprint;
DROP INDEX IF EXISTS idx_dispositivos_current_key;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_dispositivos_auth_status') THEN
        ALTER TABLE dispositivos ADD CONSTRAINT chk_dispositivos_auth_status
            CHECK (auth_status IN ('unenrolled', 'active', 'disabled', 'revoked'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_dispositivos_secret_revoked') THEN
        ALTER TABLE dispositivos ADD CONSTRAINT chk_dispositivos_secret_revoked
            CHECK (secret_hash IS NULL OR auth_status <> 'revoked');
    END IF;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS uq_dispositivos_secret_hash
    ON dispositivos (secret_hash) WHERE secret_hash IS NOT NULL;

COMMENT ON COLUMN dispositivos.whep_url IS 'URL del stream WHEP de la cámara asociada al dispositivo (nullable: sin cámara configurada)';
COMMENT ON COLUMN dispositivos.auth_status IS 'Estado de admisión del dispositivo (unenrolled/active/disabled/revoked), independiente del heartbeat';
COMMENT ON COLUMN dispositivos.secret_hash IS 'SHA-256 hexadecimal del secret del dispositivo; NULL si no está registrado o fue revocado';

-- ============================================================================
-- ALTER: dispositivos — tipo de dispositivo (ENTRADA_HORNO/SALIDA_HORNO)
-- Idempotente para bases existentes. Nullable para no invalidar filas legadas.
-- ============================================================================
ALTER TABLE dispositivos ADD COLUMN IF NOT EXISTS tipo VARCHAR(16);

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_dispositivos_tipo') THEN
        ALTER TABLE dispositivos ADD CONSTRAINT chk_dispositivos_tipo
            CHECK (tipo IS NULL OR tipo IN ('ENTRADA_HORNO', 'SALIDA_HORNO'));
    END IF;
END $$;

COMMENT ON COLUMN dispositivos.tipo IS 'Tipo/ubicación funcional del nodo en la línea: ENTRADA_HORNO o SALIDA_HORNO (nullable para filas legadas)';

-- ============================================================================
-- ALTER: dispositivos — URL de renombre del nodo y credencial del push
-- Idempotente para bases existentes. Reemplaza el anuncio previo por par
-- (listen_address/listen_port): se eliminan esas columnas y su CHECK, y se
-- agrega rename_url, la URL completa (esquema + host + puerto) que el nodo
-- expone para el push de renombre. rename_token es la credencial Bearer de un
-- solo uso que el backend usa para empujar el renombre. Ambas nullable: una Pi
-- legacy no anuncia URL y no recibe token (su push queda deshabilitado sin
-- romper el alta).
-- ============================================================================
ALTER TABLE dispositivos DROP CONSTRAINT IF EXISTS chk_dispositivos_listen_port;
ALTER TABLE dispositivos
    DROP COLUMN IF EXISTS listen_address,
    DROP COLUMN IF EXISTS listen_port;

ALTER TABLE dispositivos
    ADD COLUMN IF NOT EXISTS rename_url TEXT,
    ADD COLUMN IF NOT EXISTS rename_token TEXT;

COMMENT ON COLUMN dispositivos.rename_url IS 'URL completa (esquema + host + puerto) que el nodo expone para el push de renombre (PUT {rename_url}/api/v1/dispositivos/nombre); NULL = no anunciada (legacy)';
COMMENT ON COLUMN dispositivos.rename_token IS 'Credencial Bearer entregada al nodo en el pickup; el backend la usa para empujar el renombre. Plaintext, NULL para dispositivos legacy o sin push';

-- ============================================================================
-- SEED: catálogo completo de las 6 variedades de panificados (SCA-142)
-- Incluye los parámetros de control embebidos de cada producto.
-- ============================================================================
INSERT INTO productos (
    id, nombre,
    peso_referencia_kg, tolerancia_peso_pct,
    dimension_base_cm, tolerancia_dimension_cm,
    temp_min, temp_max, velocidad_cinta_min, velocidad_cinta_max
) VALUES
    ('b2c3d4e5-6789-01ab-cdef-234567890abc', 'Pan Lactal',     0.500,  8.00, 25.00, 1.00, 180.00, 200.00, 0.15, 0.35),
    ('c3d4e5f6-789a-12bc-def3-34567890abcd', 'Pan Francés',    0.250,  6.00, 30.00, 1.50, 200.00, 220.00, 0.20, 0.40),
    ('d4e5f6a7-89ab-23cd-ef34-4567890abcde', 'Pan de Salvado', 0.450,  8.00, 22.00, 1.00, 170.00, 190.00, 0.15, 0.35),
    ('e5f6a7b8-9abc-34de-f456-567890abcdef', 'Medialunas',     0.060, 12.00, 10.00, 0.50, 190.00, 210.00, 0.25, 0.45),
    ('f6a7b8c9-abcd-45ef-5678-67890abcdef1', 'Pan Dulce',      0.800, 10.00, 15.00, 1.00, 150.00, 170.00, 0.08, 0.20)
ON CONFLICT (id) DO NOTHING;

-- ============================================================================
-- TABLA 6: historial_consignas (Auditoría de consignas térmicas/velocidad)
-- ============================================================================
CREATE TABLE IF NOT EXISTS historial_consignas (
    id                        UUID            PRIMARY KEY DEFAULT gen_random_uuid(),
    horno_id                  VARCHAR(50)     NOT NULL,
    lote_id                   UUID,
    producto_id               UUID,
    temperatura_objetivo      NUMERIC(6,2)    NOT NULL,
    velocidad_cinta_objetivo  NUMERIC(6,2)    NOT NULL,
    origen                    VARCHAR(12)     NOT NULL,
    usuario                   VARCHAR(150),
    exitosa                   BOOLEAN         NOT NULL,
    motivo_error              TEXT,
    temperatura_previa        NUMERIC(6,2),
    velocidad_cinta_previa    NUMERIC(6,2),
    creada_en                 TIMESTAMPTZ     NOT NULL DEFAULT now(),

    CONSTRAINT fk_historial_consignas_producto
        FOREIGN KEY (producto_id)
        REFERENCES productos (id)
        ON DELETE SET NULL,

    CONSTRAINT chk_historial_consignas_origen
        CHECK (origen IN ('AUTOMATICO', 'MANUAL'))
);

COMMENT ON TABLE  historial_consignas IS 'Auditoría de toda consigna térmica/velocidad despachada (o intentada) al controlador físico del horno, de origen automático (SCA-142) o manual (SCA-320).';
COMMENT ON COLUMN historial_consignas.horno_id IS 'Identificador del horno (dominio horno vive en memoria, sin tabla propia; no es FK)';
COMMENT ON COLUMN historial_consignas.lote_id IS 'Correlación con el lote productivo, sin FK: en el despacho automático el lote puede no estar persistido aún en lotes_productivos';
COMMENT ON COLUMN historial_consignas.producto_id IS 'FK al producto cuyos parámetros originaron la consigna (nullable: puede no aplicar en consignas manuales sin producto asociado)';
COMMENT ON COLUMN historial_consignas.origen IS 'AUTOMATICO: disparado por detección de IA al iniciar el lote. MANUAL: cargado por un operario desde el panel';
COMMENT ON COLUMN historial_consignas.usuario IS 'Identificador del operario que disparó una consigna manual';
COMMENT ON COLUMN historial_consignas.exitosa IS 'Indica si el controlador físico (simulado) aplicó la consigna con éxito';
COMMENT ON COLUMN historial_consignas.motivo_error IS 'Motivo del rechazo/fallo cuando exitosa=false';
COMMENT ON COLUMN historial_consignas.temperatura_previa IS 'Temperatura activa del horno inmediatamente antes de este despacho (para trazabilidad)';
COMMENT ON COLUMN historial_consignas.velocidad_cinta_previa IS 'Velocidad de cinta activa del horno inmediatamente antes de este despacho (para trazabilidad)';

CREATE INDEX IF NOT EXISTS idx_historial_consignas_lote_id
    ON historial_consignas (lote_id);

CREATE INDEX IF NOT EXISTS idx_historial_consignas_horno_id
    ON historial_consignas (horno_id, creada_en DESC);

-- ============================================================================
-- TABLA 7: usuarios (Usuarios corporativos autorizados para la plataforma)
-- ============================================================================
CREATE TABLE IF NOT EXISTS usuarios (
    id            UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    email         VARCHAR(254) NOT NULL UNIQUE,
    nombre        VARCHAR(150) NOT NULL,
    rol           VARCHAR(20)  NOT NULL,
    password_hash VARCHAR(255),
    activo        BOOLEAN      NOT NULL DEFAULT true,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),

    -- Solo roles válidos del sistema
    CONSTRAINT chk_usuarios_rol
        CHECK (rol IN ('Administrador', 'Supervisor', 'Operario')),

    -- Validación de formato de email (segunda línea de defensa)
    CONSTRAINT chk_usuarios_email_formato
        CHECK (email ~* '^[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}$')
);

COMMENT ON TABLE  usuarios               IS 'Usuarios corporativos autorizados para acceder a la plataforma Smart-Check.';
COMMENT ON COLUMN usuarios.email         IS 'Correo corporativo. Debe existir aquí para poder autenticarse.';
COMMENT ON COLUMN usuarios.rol           IS 'Nivel de acceso: Administrador, Supervisor u Operario.';
COMMENT ON COLUMN usuarios.password_hash IS 'Hash bcrypt de la contraseña para autenticación local (nullable si solo usa OAuth).';
COMMENT ON COLUMN usuarios.activo        IS 'Revocar acceso sin eliminar el registro: UPDATE usuarios SET activo=false WHERE email=...';

-- Índice para la búsqueda por email en cada login (operación más frecuente)
CREATE INDEX IF NOT EXISTS idx_usuarios_email ON usuarios (email);

-- ============================================================================
-- SEED: Usuarios de prueba (modificar con emails corporativos reales)
-- Contraseña por defecto para usuarios seed: password123
--
-- El hash se genera en el propio PostgreSQL con pgcrypto (bcrypt, prefijo $2a$)
-- en lugar de incrustar un literal bcrypt. Así el seed siempre es consistente
-- consigo mismo y no depende de copiar/pegar un hash a mano (origen del bug
-- histórico: un literal que en realidad no correspondía a password123).
-- pgcrypto ya se habilita arriba con CREATE EXTENSION IF NOT EXISTS.
-- ============================================================================
INSERT INTO usuarios (email, nombre, rol, password_hash) VALUES
    ('admin@fermar.com.ar',      'Administrador Fermar',  'Administrador', crypt('password123', gen_salt('bf', 10))),
    ('supervisor@fermar.com.ar', 'Supervisor Fermar',     'Supervisor',    crypt('password123', gen_salt('bf', 10))),
    ('operario@fermar.com.ar',   'Operario Fermar',       'Operario',      crypt('password123', gen_salt('bf', 10)))
ON CONFLICT (email) DO NOTHING;

-- Reparación idempotente de bases ya sembradas con hashes rotos históricos
-- (literales que NO correspondían a password123). Sólo reemplaza filas que
-- todavía llevan exactamente esos hashes, así que nunca pisa una contraseña
-- ya cambiada por un operario. `crypt(...)` se evalúa sólo en las filas que
-- hacen match con el IN, por lo que no agrega costo al arranque normal.
UPDATE usuarios
SET password_hash = crypt('password123', gen_salt('bf', 10))
WHERE email IN ('admin@fermar.com.ar', 'supervisor@fermar.com.ar', 'operario@fermar.com.ar')
  AND password_hash IN (
    '$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy',
    '$2a$10$5l3CZ8EQW3PCsAMgyetQeOA5j7yFW7QBWwQAgpGAhROdt48ayUJVy'
  );

-- Provenance for writes made by an authenticated device. Existing rows remain
-- nullable so this upgrade never invents an owner for historical data.
ALTER TABLE lotes_productivos ADD COLUMN IF NOT EXISTS dispositivo_id UUID;
ALTER TABLE historial_consignas ADD COLUMN IF NOT EXISTS dispositivo_id UUID;
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_lotes_dispositivo') THEN
        ALTER TABLE lotes_productivos ADD CONSTRAINT fk_lotes_dispositivo
            FOREIGN KEY (dispositivo_id) REFERENCES dispositivos(id) ON DELETE RESTRICT;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_historial_consignas_dispositivo') THEN
        ALTER TABLE historial_consignas ADD CONSTRAINT fk_historial_consignas_dispositivo
            FOREIGN KEY (dispositivo_id) REFERENCES dispositivos(id) ON DELETE RESTRICT;
    END IF;
END $$;

-- ============================================================================
-- Registro de dispositivos por solicitud + aprobación (reemplaza Ed25519)
-- ============================================================================
-- La Raspberry solicita el alta con su hostname; el backend genera un
-- request_id de un solo uso. Un Supervisor/Admin aprueba (o rechaza), y sólo
-- entonces se crea la identidad del dispositivo. El secret en texto plano vive
-- aquí únicamente hasta que el nodo hace su única consulta de estado; el
-- dispositivo guarda sólo secret_hash. request_id y secret son credenciales de
-- alta capacidad: expiran y se entregan como máximo una vez.
CREATE TABLE IF NOT EXISTS registration_requests (
    request_id   VARCHAR(64)  PRIMARY KEY,
    hostname     VARCHAR(100) NOT NULL,
    status       VARCHAR(12)  NOT NULL DEFAULT 'PENDING',
    device_id    UUID         REFERENCES dispositivos(id) ON DELETE SET NULL,
    secret       TEXT,
    rename_url   TEXT,
    resolved_by  UUID         REFERENCES usuarios(id) ON DELETE RESTRICT,
    resolved_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ  NOT NULL,

    CONSTRAINT chk_registration_requests_status
        CHECK (status IN ('PENDING', 'APPROVED', 'REJECTED', 'EXPIRED')),
    CONSTRAINT chk_registration_requests_approved
        CHECK (status <> 'APPROVED' OR (device_id IS NOT NULL AND resolved_by IS NOT NULL AND resolved_at IS NOT NULL)),
    CONSTRAINT chk_registration_requests_rejected
        CHECK (status <> 'REJECTED' OR (resolved_by IS NOT NULL AND resolved_at IS NOT NULL))
);

-- Un hostname sólo puede tener una solicitud PENDING: evita que un mismo nodo
-- acumule identidades por reintentos.
CREATE UNIQUE INDEX IF NOT EXISTS uq_registration_requests_pending_hostname
    ON registration_requests (hostname) WHERE status = 'PENDING';

CREATE INDEX IF NOT EXISTS idx_registration_requests_status
    ON registration_requests (status, created_at DESC);

COMMENT ON TABLE  registration_requests IS 'Solicitudes de registro de nodos Raspberry pendientes de aprobación por Supervisor/Admin';
COMMENT ON COLUMN registration_requests.request_id IS 'Identificador de alta capacidad que el nodo usa para consultar su solicitud';
COMMENT ON COLUMN registration_requests.secret IS 'Secret en texto plano retenido sólo hasta la primera consulta del nodo; se anula tras la entrega';
COMMENT ON COLUMN registration_requests.expires_at IS 'Vencimiento de la solicitud; vencida no puede aprobarse ni entregarse';

-- ============================================================================
-- ALTER: registration_requests — tipo solicitado y URL de renombre
-- Idempotente para bases existentes. Reemplaza el anuncio previo por par
-- (listen_address/listen_port) por una única rename_url. Nullable para no
-- invalidar solicitudes legadas creadas antes de incorporar estos campos.
-- ============================================================================
ALTER TABLE registration_requests ADD COLUMN IF NOT EXISTS tipo VARCHAR(16);

ALTER TABLE registration_requests DROP CONSTRAINT IF EXISTS chk_registration_requests_listen_port;
ALTER TABLE registration_requests
    DROP COLUMN IF EXISTS listen_address,
    DROP COLUMN IF EXISTS listen_port;

ALTER TABLE registration_requests ADD COLUMN IF NOT EXISTS rename_url TEXT;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_registration_requests_tipo') THEN
        ALTER TABLE registration_requests ADD CONSTRAINT chk_registration_requests_tipo
            CHECK (tipo IS NULL OR tipo IN ('ENTRADA_HORNO', 'SALIDA_HORNO'));
    END IF;
END $$;

COMMENT ON COLUMN registration_requests.tipo IS 'Tipo/ubicación funcional que el nodo solicita al registrarse: ENTRADA_HORNO o SALIDA_HORNO (nullable para solicitudes legadas)';
COMMENT ON COLUMN registration_requests.rename_url IS 'URL completa (esquema + host + puerto) anunciada por el nodo para el push de renombre; NULL si el firmware es legacy';

-- Auditoría de ciclo de vida de dispositivos. Se conserva la tabla: el alta
-- (approve) y el rechazo (reject) dejan traza con actor y estado anterior/nuevo.
CREATE TABLE IF NOT EXISTS device_lifecycle_audit (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id UUID NOT NULL REFERENCES usuarios(id) ON DELETE RESTRICT,
    action VARCHAR(20) NOT NULL,
    dispositivo_id UUID REFERENCES dispositivos(id) ON DELETE RESTRICT,
    enrollment_id VARCHAR(64),
    request_id VARCHAR(64),
    old_status VARCHAR(12) NOT NULL,
    new_status VARCHAR(12) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE device_lifecycle_audit ADD COLUMN IF NOT EXISTS enrollment_id VARCHAR(64);
ALTER TABLE device_lifecycle_audit ADD COLUMN IF NOT EXISTS request_id VARCHAR(64);
ALTER TABLE device_lifecycle_audit ALTER COLUMN dispositivo_id DROP NOT NULL;
CREATE INDEX IF NOT EXISTS idx_device_lifecycle_audit_device ON device_lifecycle_audit(dispositivo_id, created_at DESC);

-- Reemplazo total del enrolamiento Ed25519: se eliminan las tablas de
-- credenciales por clave pública, invitaciones y replay de pruebas.
DROP TABLE IF EXISTS device_credentials;
DROP TABLE IF EXISTS device_enrollments;
DROP TABLE IF EXISTS device_request_replays;

-- ============================================================================
-- SECTORES, LOTES ABIERTOS Y EVENTOS (contrato rework-rb)
-- ============================================================================
-- Un sector agrupa a lo sumo un dispositivo ENTRADA_HORNO y uno SALIDA_HORNO
-- de la misma línea. El lote es la unidad de producción de un sector para un
-- producto: máximo un lote ABIERTO por sector. eventos_lote es el detalle
-- append-only de las detecciones que alimentan los conteos en vivo.
-- ============================================================================

CREATE TABLE IF NOT EXISTS sectores (
    id          VARCHAR(50)  PRIMARY KEY,
    nombre      VARCHAR(100) NOT NULL,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);

COMMENT ON TABLE  sectores           IS 'Sectores de producción: agrupan un dispositivo ENTRADA_HORNO y uno SALIDA_HORNO de la misma línea.';
COMMENT ON COLUMN sectores.id        IS 'Identificador legible del sector (ej: horno-1)';
COMMENT ON COLUMN sectores.nombre    IS 'Nombre para mostrar del sector (ej: Horno 1)';
COMMENT ON COLUMN sectores.created_at IS 'Marca temporal de alta del sector';

-- Baja lógica del catálogo de productos: activo=false no se ofrece para elegir,
-- pero sigue resolviendo lotes históricos.
ALTER TABLE productos ADD COLUMN IF NOT EXISTS activo BOOLEAN NOT NULL DEFAULT true;

COMMENT ON COLUMN productos.activo IS 'Vigencia del producto en el catálogo: false = retirado (no se ofrece para elegir, sigue resolviendo lotes históricos)';

-- Pertenencia de un dispositivo a un sector (nullable: dispositivos legados sin sector).
ALTER TABLE dispositivos ADD COLUMN IF NOT EXISTS sector_id VARCHAR(50);

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_dispositivos_sector') THEN
        ALTER TABLE dispositivos ADD CONSTRAINT fk_dispositivos_sector
            FOREIGN KEY (sector_id) REFERENCES sectores(id) ON DELETE SET NULL;
    END IF;
END $$;

COMMENT ON COLUMN dispositivos.sector_id IS 'Sector al que pertenece el dispositivo (nullable: dispositivo sin sector asignado)';

-- Un sector agrupa a lo sumo un dispositivo de cada tipo funcional
-- (ENTRADA_HORNO/SALIDA_HORNO). Los dispositivos legados sin sector (NULL) y sin
-- tipo quedan fuera del índice parcial, así que el upgrade no lo viola.
CREATE UNIQUE INDEX IF NOT EXISTS uq_dispositivos_sector_tipo
    ON dispositivos (sector_id, tipo) WHERE sector_id IS NOT NULL AND tipo IS NOT NULL;

-- ============================================================================
-- MIGRACIÓN: ubicacion (legacy) -> sectores
-- Idempotente: sólo corre mientras la columna dispositivos.ubicacion exista.
-- (a) crea un sector por cada ubicación distinta no vacía (id = slug del
--     nombre, sin acentos, hasta 50 chars);
-- (b) asigna ese sector a los dispositivos que aún no tienen sector_id.
--     CRÍTICO: si dos dispositivos del mismo tipo comparten ubicación, sólo el
--     primero (por created_at, id) se asigna; el resto queda en NULL para no
--     violar uq_dispositivos_sector_tipo ni romper el arranque. Un dispositivo
--     ya asignado al mismo sector se prioriza para conservar la asignación.
--     Además, antes de asignar se verifica que (sector, tipo) no esté ya
--     ocupado por otro dispositivo pre-asignado (aunque su ubicación sea NULL o
--     sluggee a otro sector): en ese caso el candidato queda en NULL. Sin esta
--     guarda el UPDATE podía violar el índice único y abortar el arranque.
-- (c) elimina la columna y su comentario (el DROP COLUMN borra el comentario).
-- ============================================================================
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name = 'dispositivos'
          AND column_name = 'ubicacion'
    ) THEN
        -- (a) Alta de sectores por ubicación distinta.
        EXECUTE $mig$
            INSERT INTO sectores (id, nombre)
            SELECT DISTINCT
                COALESCE(NULLIF(trim(both '-' from left(regexp_replace(
                    translate(lower(btrim(ubicacion)), 'áàäâãéèëêíìïîóòöôõúùüûñçÁÀÄÂÃÉÈËÊÍÌÏÎÓÒÖÔÕÚÙÜÛÑÇ', 'aaaaaeeeeiiiiooooouuuuncAAAAAEEEEIIIIOOOOOUUUUNC'),
                    '[^a-z0-9]+', '-', 'g'), 50)), ''), 'sector') AS id,
                btrim(ubicacion) AS nombre
            FROM dispositivos
            WHERE ubicacion IS NOT NULL AND btrim(ubicacion) <> ''
            ON CONFLICT (id) DO NOTHING
        $mig$;

        -- (b) Asignación a los dispositivos, a lo sumo uno por tipo y sector.
        EXECUTE $mig$
            WITH ranking AS (
                SELECT d.id AS device_id,
                       COALESCE(NULLIF(trim(both '-' from left(regexp_replace(
                           translate(lower(btrim(d.ubicacion)), 'áàäâãéèëêíìïîóòöôõúùüûñçÁÀÄÂÃÉÈËÊÍÌÏÎÓÒÖÔÕÚÙÜÛÑÇ', 'aaaaaeeeeiiiiooooouuuuncAAAAAEEEEIIIIOOOOOUUUUNC'),
                           '[^a-z0-9]+', '-', 'g'), 50)), ''), 'sector') AS sector_id,
                       d.tipo AS tipo,
                       CASE WHEN d.sector_id IS NOT NULL THEN 0 ELSE 1 END AS prioridad,
                       ROW_NUMBER() OVER (
                           PARTITION BY
                               COALESCE(NULLIF(trim(both '-' from left(regexp_replace(
                                   translate(lower(btrim(d.ubicacion)), 'áàäâãéèëêíìïîóòöôõúùüûñçÁÀÄÂÃÉÈËÊÍÌÏÎÓÒÖÔÕÚÙÜÛÑÇ', 'aaaaaeeeeiiiiooooouuuuncAAAAAEEEEIIIIOOOOOUUUUNC'),
                                   '[^a-z0-9]+', '-', 'g'), 50)), ''), 'sector'),
                               COALESCE(d.tipo, d.id::text)
                           ORDER BY CASE WHEN d.sector_id IS NOT NULL THEN 0 ELSE 1 END,
                                    d.created_at, d.id
                       ) AS rn
                FROM dispositivos d
                WHERE d.ubicacion IS NOT NULL AND btrim(d.ubicacion) <> ''
                  AND (
                      d.sector_id IS NULL
                      OR d.sector_id = COALESCE(NULLIF(trim(both '-' from left(regexp_replace(
                          translate(lower(btrim(d.ubicacion)), 'áàäâãéèëêíìïîóòöôõúùüûñçÁÀÄÂÃÉÈËÊÍÌÏÎÓÒÖÔÕÚÙÜÛÑÇ', 'aaaaaeeeeiiiiooooouuuuncAAAAAEEEEIIIIOOOOOUUUUNC'),
                          '[^a-z0-9]+', '-', 'g'), 50)), ''), 'sector')
                  )
            )
            UPDATE dispositivos d
            SET sector_id = r.sector_id
            FROM ranking r
            WHERE d.id = r.device_id
              AND d.sector_id IS NULL
              AND r.rn = 1
              AND r.prioridad = 1
              AND (
                  r.tipo IS NULL
                  OR NOT EXISTS (
                      SELECT 1 FROM dispositivos o
                      WHERE o.sector_id = r.sector_id AND o.tipo = r.tipo
                  )
              )
        $mig$;
    END IF;
END $$;

-- (c) Baja de la columna legacy (idempotente; borra también su comentario).
ALTER TABLE dispositivos DROP COLUMN IF EXISTS ubicacion;

-- Ciclo de vida del lote: columnas nuevas de lotes_productivos.
ALTER TABLE lotes_productivos
    ADD COLUMN IF NOT EXISTS sector_id VARCHAR(50),
    ADD COLUMN IF NOT EXISTS estado VARCHAR(10) NOT NULL DEFAULT 'CERRADO',
    ADD COLUMN IF NOT EXISTS abierto_por UUID,
    ADD COLUMN IF NOT EXISTS ultimo_evento_en TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS motivo_cierre VARCHAR(20),
    ADD COLUMN IF NOT EXISTS abrir_idempotency_key VARCHAR(64),
    ADD COLUMN IF NOT EXISTS cerrar_idempotency_key VARCHAR(64);

-- Un lote ABIERTO nuevo no trae turno/fin/conteos/kg: se relajan los NOT NULL
-- de las columnas del flujo legado (los CHECK de dominio se conservan).
ALTER TABLE lotes_productivos
    ALTER COLUMN turno DROP NOT NULL,
    ALTER COLUMN fin_at DROP NOT NULL,
    ALTER COLUMN total_unidades DROP NOT NULL,
    ALTER COLUMN correctos DROP NOT NULL,
    ALTER COLUMN quemados DROP NOT NULL,
    ALTER COLUMN correctos_kg DROP NOT NULL,
    ALTER COLUMN quemados_kg DROP NOT NULL;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_lotes_sector') THEN
        ALTER TABLE lotes_productivos ADD CONSTRAINT fk_lotes_sector
            FOREIGN KEY (sector_id) REFERENCES sectores(id) ON DELETE RESTRICT;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_lotes_abierto_por') THEN
        ALTER TABLE lotes_productivos ADD CONSTRAINT fk_lotes_abierto_por
            FOREIGN KEY (abierto_por) REFERENCES dispositivos(id) ON DELETE RESTRICT;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_lotes_estado') THEN
        ALTER TABLE lotes_productivos ADD CONSTRAINT chk_lotes_estado
            CHECK (estado IN ('ABIERTO', 'CERRADO'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_lotes_motivo_cierre') THEN
        ALTER TABLE lotes_productivos ADD CONSTRAINT chk_lotes_motivo_cierre
            CHECK (motivo_cierre IS NULL OR motivo_cierre IN ('sin_detecciones', 'manual', 'apagado', 'seguridad'));
    END IF;
    -- Un lote ABIERTO siempre tiene sector y nunca tiene fin_at.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_lotes_abierto_sector') THEN
        ALTER TABLE lotes_productivos ADD CONSTRAINT chk_lotes_abierto_sector
            CHECK (estado <> 'ABIERTO' OR (sector_id IS NOT NULL AND fin_at IS NULL));
    END IF;
END $$;

COMMENT ON COLUMN lotes_productivos.sector_id IS 'Sector al que pertenece el lote (nullable para lotes legados; obligatorio si estado=ABIERTO)';
COMMENT ON COLUMN lotes_productivos.estado IS 'Ciclo de vida del lote: ABIERTO (en curso) o CERRADO (finalizado)';
COMMENT ON COLUMN lotes_productivos.abierto_por IS 'FK al dispositivo que abrió el lote (ENTRADA_HORNO, o SALIDA_HORNO en lote degradado)';
COMMENT ON COLUMN lotes_productivos.ultimo_evento_en IS 'Marca temporal del último evento aceptado del lote; insumo del cálculo de inactividad';
COMMENT ON COLUMN lotes_productivos.motivo_cierre IS 'Motivo del cierre: sin_detecciones, manual, apagado o seguridad (nullable)';
COMMENT ON COLUMN lotes_productivos.abrir_idempotency_key IS 'Clave de idempotencia de apertura enviada por la entrada; única cuando no es NULL';
COMMENT ON COLUMN lotes_productivos.cerrar_idempotency_key IS 'Clave de idempotencia de cierre enviada por la salida (nullable)';

-- Un sector tiene a lo sumo un lote ABIERTO.
CREATE UNIQUE INDEX IF NOT EXISTS uq_lotes_abierto_por_sector
    ON lotes_productivos (sector_id) WHERE estado = 'ABIERTO';

-- La clave de idempotencia de apertura, cuando existe, es única.
CREATE UNIQUE INDEX IF NOT EXISTS uq_lotes_abrir_idem
    ON lotes_productivos (abrir_idempotency_key) WHERE abrir_idempotency_key IS NOT NULL;

-- Soporte del historial por sector ordenado por fecha.
CREATE INDEX IF NOT EXISTS idx_lotes_sector_inicio
    ON lotes_productivos (sector_id, inicio_at DESC);

-- ============================================================================
-- TABLA: eventos_lote (detalle append-only de detecciones por lote)
-- ============================================================================
-- evento_id es la clave de deduplicación global generada por la Pi: un reintento
-- reutiliza el mismo UUID y el INSERT ... ON CONFLICT DO NOTHING lo ignora.
CREATE TABLE IF NOT EXISTS eventos_lote (
    id             UUID             PRIMARY KEY DEFAULT gen_random_uuid(),
    lote_id        UUID             NOT NULL REFERENCES lotes_productivos(id) ON DELETE CASCADE,
    evento_id      UUID             NOT NULL UNIQUE,
    producto_id    UUID             NOT NULL REFERENCES productos(id) ON DELETE RESTRICT,
    estado         VARCHAR(10),
    confianza      DOUBLE PRECISION,
    pista          INTEGER,
    frame          BIGINT,
    modelo_id      VARCHAR(100),
    dispositivo_id UUID             REFERENCES dispositivos(id) ON DELETE RESTRICT,
    momento        TIMESTAMPTZ,
    created_at     TIMESTAMPTZ      NOT NULL DEFAULT now(),

    -- Un estado que el modelo no produce viaja NULL, nunca 0.
    CONSTRAINT chk_eventos_lote_estado
        CHECK (estado IS NULL OR estado IN ('ok', 'crudo', 'quemado')),
    CONSTRAINT chk_eventos_lote_confianza
        CHECK (confianza IS NULL OR confianza BETWEEN 0 AND 1)
);

COMMENT ON TABLE  eventos_lote              IS 'Detalle append-only de detecciones reportadas por la salida; alimenta los conteos en vivo del lote.';
COMMENT ON COLUMN eventos_lote.id           IS 'Identificador UUID del registro de evento';
COMMENT ON COLUMN eventos_lote.lote_id      IS 'FK al lote productivo; se borra en cascada con el lote';
COMMENT ON COLUMN eventos_lote.evento_id    IS 'UUIDv4 generado por la Pi y reutilizado en cada reintento: clave de deduplicación';
COMMENT ON COLUMN eventos_lote.producto_id  IS 'FK al producto detectado (debe coincidir con el producto del lote)';
COMMENT ON COLUMN eventos_lote.estado       IS 'Estado de calidad: ok, crudo o quemado (nullable si el modelo no lo determina)';
COMMENT ON COLUMN eventos_lote.confianza    IS 'Confianza de la detección entre 0 y 1 (nullable)';
COMMENT ON COLUMN eventos_lote.pista        IS 'Número de pista/banda de la detección (nullable)';
COMMENT ON COLUMN eventos_lote.frame        IS 'Número de frame del video de origen (nullable)';
COMMENT ON COLUMN eventos_lote.modelo_id    IS 'Identificador del modelo de inferencia que produjo la detección (nullable)';
COMMENT ON COLUMN eventos_lote.dispositivo_id IS 'FK al dispositivo que reportó el evento (nullable)';
COMMENT ON COLUMN eventos_lote.momento      IS 'Marca temporal informativa del momento de la detección según la Pi (nullable)';
COMMENT ON COLUMN eventos_lote.created_at   IS 'Marca temporal en que el backend recibió el evento';

-- Índice para reconstruir el detalle de un lote en orden cronológico.
CREATE INDEX IF NOT EXISTS idx_eventos_lote_lote_created
    ON eventos_lote (lote_id, created_at);

-- ============================================================================
-- SCA-400: MODO DE OPERACIÓN, VENTANAS Y CICLO DE CONTROL
-- ============================================================================
-- Esta sección extiende el modelo de datos para soportar tres modos de
-- operación del lote (MANUAL, SUPERVISION, AUTOMATICO), ventanas de decisión,
-- tolerancias, umbrales de aceptación, telemetría del horno y registro de
-- decisiones del optimizador.
--
-- Todas las extensiones son idempotentes: si una columna ya existe no se
-- vuelve a crear, y si el CHECK ya está definido no se duplica. Defaults
-- seguros preservan la semántica actual: el modo default es MANUAL y los
-- defaults de ventana/tolerancia son razonables para una primera versión.
-- ============================================================================

-- ----------------------------------------------------------------------------
-- 1) productos: Δmáx por ciclo del optimizador.
-- ----------------------------------------------------------------------------
ALTER TABLE productos
    ADD COLUMN IF NOT EXISTS delta_temp1_max_c    NUMERIC(5,2) NOT NULL DEFAULT 10.0,
    ADD COLUMN IF NOT EXISTS delta_vel_max        NUMERIC(5,3) NOT NULL DEFAULT 0.05;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_productos_delta_temp1_pos') THEN
        ALTER TABLE productos ADD CONSTRAINT chk_productos_delta_temp1_pos CHECK (delta_temp1_max_c > 0);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_productos_delta_vel_pos') THEN
        ALTER TABLE productos ADD CONSTRAINT chk_productos_delta_vel_pos CHECK (delta_vel_max > 0);
    END IF;
END $$;

COMMENT ON COLUMN productos.delta_temp1_max_c  IS 'Máxima variación permitida de T1 por ciclo del optimizador (°C/cycle)';
COMMENT ON COLUMN productos.delta_vel_max      IS 'Máxima variación permitida de velocidad de cinta por ciclo (m/s/cycle)';

-- ----------------------------------------------------------------------------
-- 2) lotes_productivos: modo, ventanas, tolerancias, umbrales, pesos, último
--    setpoint aplicado y flag de inicio manual.
-- ----------------------------------------------------------------------------
ALTER TABLE lotes_productivos
    ADD COLUMN IF NOT EXISTS modo                       VARCHAR(12) NOT NULL DEFAULT 'MANUAL',
    ADD COLUMN IF NOT EXISTS ventana_evaluacion_segundos INT         NOT NULL DEFAULT 600,
    ADD COLUMN IF NOT EXISTS ventana_actualizacion_segundos INT       NOT NULL DEFAULT 300,
    ADD COLUMN IF NOT EXISTS tolerancia_temp_c          NUMERIC(5,2) NOT NULL DEFAULT 2.0,
    ADD COLUMN IF NOT EXISTS umbral_aceptacion_crudo    NUMERIC(5,2) NOT NULL DEFAULT 5.0,
    ADD COLUMN IF NOT EXISTS umbral_aceptacion_quemado  NUMERIC(5,2) NOT NULL DEFAULT 5.0,
    ADD COLUMN IF NOT EXISTS pesos_objetivo             JSONB        NOT NULL DEFAULT '{"wc":1.0,"wq":1.0,"wo":1.0,"dt":0.05,"dc":0.5}'::jsonb,
    ADD COLUMN IF NOT EXISTS iniciado_manualmente       BOOLEAN      NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS temp1_objetivo             NUMERIC(6,2),
    ADD COLUMN IF NOT EXISTS vel_objetivo               NUMERIC(6,3),
    ADD COLUMN IF NOT EXISTS delta_temp1_max_c          NUMERIC(6,3),
    ADD COLUMN IF NOT EXISTS delta_vel_max              NUMERIC(6,4);

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_lotes_modo') THEN
        ALTER TABLE lotes_productivos ADD CONSTRAINT chk_lotes_modo
            CHECK (modo IN ('MANUAL', 'SUPERVISION', 'AUTOMATICO'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_lotes_ventana_evaluacion_pos') THEN
        ALTER TABLE lotes_productivos ADD CONSTRAINT chk_lotes_ventana_evaluacion_pos
            CHECK (ventana_evaluacion_segundos > 0);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_lotes_ventana_actualizacion_pos') THEN
        ALTER TABLE lotes_productivos ADD CONSTRAINT chk_lotes_ventana_actualizacion_pos
            CHECK (ventana_actualizacion_segundos > 0);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_lotes_tolerancia_temp_pos') THEN
        ALTER TABLE lotes_productivos ADD CONSTRAINT chk_lotes_tolerancia_temp_pos
            CHECK (tolerancia_temp_c >= 0);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_lotes_umbrales_aceptacion') THEN
        ALTER TABLE lotes_productivos ADD CONSTRAINT chk_lotes_umbrales_aceptacion
            CHECK (umbral_aceptacion_crudo >= 0 AND umbral_aceptacion_quemado >= 0
                AND umbral_aceptacion_crudo <= 100 AND umbral_aceptacion_quemado <= 100);
    END IF;
    -- Columnas de tuning opcionales: NULL = usar el valor del producto, así el
    -- CHECK admite NULL (delta_temp1_max_c > 0 sólo aplica cuando está seteado).
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_lotes_delta_temp1_pos') THEN
        ALTER TABLE lotes_productivos ADD CONSTRAINT chk_lotes_delta_temp1_pos
            CHECK (delta_temp1_max_c > 0);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_lotes_delta_vel_pos') THEN
        ALTER TABLE lotes_productivos ADD CONSTRAINT chk_lotes_delta_vel_pos
            CHECK (delta_vel_max > 0);
    END IF;
END $$;

COMMENT ON COLUMN lotes_productivos.modo                       IS 'Modo de operación: MANUAL/SUPERVISION/AUTOMATICO (default MANUAL para no romper lotes existentes)';
COMMENT ON COLUMN lotes_productivos.ventana_evaluacion_segundos IS 'Cada cuántos segundos se re-evalúa el estado y se decide un nuevo setpoint';
COMMENT ON COLUMN lotes_productivos.ventana_actualizacion_segundos IS 'Cada cuántos segundos se aplica un nuevo setpoint (>= ventana_evaluacion)';
COMMENT ON COLUMN lotes_productivos.tolerancia_temp_c          IS 'Tolerancia sobre la temperatura objetivo al iniciar (°C, default 2.0)';
COMMENT ON COLUMN lotes_productivos.umbral_aceptacion_crudo    IS 'Porcentaje máximo de crudos aceptado (% del total); alerta si se supera N ventanas';
COMMENT ON COLUMN lotes_productivos.umbral_aceptacion_quemado  IS 'Porcentaje máximo de quemados aceptado (% del total)';
COMMENT ON COLUMN lotes_productivos.pesos_objetivo             IS 'Pesos de la función objetivo del MPC: wc, wq, wo, dt, dc';
COMMENT ON COLUMN lotes_productivos.iniciado_manualmente       IS 'TRUE = el operador arrancó horno+cinta por su cuenta y el sistema se enganchó después';
COMMENT ON COLUMN lotes_productivos.temp1_objetivo             IS 'Último setpoint de T1 aplicado por el control (lectura del estado activo)';
COMMENT ON COLUMN lotes_productivos.vel_objetivo               IS 'Último setpoint de velocidad de cinta aplicado por el control';
COMMENT ON COLUMN lotes_productivos.delta_temp1_max_c          IS 'Override de tuning del ΔT1 máximo por ciclo (°C); NULL = usa el valor del producto';
COMMENT ON COLUMN lotes_productivos.delta_vel_max              IS 'Override de tuning del ΔV máximo por ciclo (m/s); NULL = usa el valor del producto';

-- ----------------------------------------------------------------------------
-- 3) metricas_horno: telemetría cruda del horno reportada por la Pi ENTRADA.
--    Una fila por heartbeat (default 5s). Sirve como fuente para la ventana de
--    evaluación del optimizador.
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS metricas_horno (
    id                   UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    lote_id              UUID         NOT NULL REFERENCES lotes_productivos(id) ON DELETE CASCADE,
    dispositivo_id       UUID         NOT NULL REFERENCES dispositivos(id) ON DELETE RESTRICT,
    sector_id            VARCHAR(50)  NOT NULL,
    temp_horno_1         NUMERIC(6,2),
    temp_comb_1          NUMERIC(6,2),
    humedad_horno        NUMERIC(5,2),
    temp_ambiente        NUMERIC(5,2),
    humedad_ambiente     NUMERIC(5,2),
    velocidad_cinta      NUMERIC(6,3),
    temp_objetivo        NUMERIC(6,2),
    vel_objetivo         NUMERIC(6,3),
    confianza_deteccion  NUMERIC(4,3) CHECK (confianza_deteccion IS NULL OR (confianza_deteccion BETWEEN 0 AND 1)),
    received_at          TIMESTAMPTZ  NOT NULL DEFAULT now()
);

COMMENT ON TABLE  metricas_horno                   IS 'Telemetría cruda del horno (T1/combustión/humedad/ambiente/velocidad) reportada por la Pi ENTRADA';
COMMENT ON COLUMN metricas_horno.temp_horno_1      IS 'Temperatura leída del horno 1 (°C, NULL permitido si el sensor cae)';
COMMENT ON COLUMN metricas_horno.temp_comb_1       IS 'Temperatura de la cámara de combustión del horno 1 (°C, opcional)';
COMMENT ON COLUMN metricas_horno.humedad_horno     IS 'Humedad relativa dentro del horno (%, opcional)';
COMMENT ON COLUMN metricas_horno.temp_ambiente     IS 'Temperatura ambiente (°C, opcional)';
COMMENT ON COLUMN metricas_horno.humedad_ambiente  IS 'Humedad ambiente (%, opcional)';
COMMENT ON COLUMN metricas_horno.velocidad_cinta   IS 'Velocidad activa de la cinta (m/s)';
COMMENT ON COLUMN metricas_horno.temp_objetivo     IS 'Setpoint activo de T1 al momento de la lectura';
COMMENT ON COLUMN metricas_horno.vel_objetivo      IS 'Setpoint activo de velocidad al momento de la lectura';
COMMENT ON COLUMN metricas_horno.confianza_deteccion IS 'Promedio de confianza del modelo de visión en la última ventana (0..1)';
COMMENT ON COLUMN metricas_horno.received_at       IS 'Marca temporal en que el backend recibió la métrica';

CREATE INDEX IF NOT EXISTS idx_metricas_horno_lote_received
    ON metricas_horno (lote_id, received_at DESC);

-- ----------------------------------------------------------------------------
-- 4) decisiones_control: audit del MPC. Una fila por decisión tomada (no por
--    tick si la decisión fue "no cambiar"). Permite auditar qué predijo el
--    modelo, qué propuso el optimizador y qué terminó aplicándose.
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS decisiones_control (
    id                  UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    lote_id             UUID         NOT NULL REFERENCES lotes_productivos(id) ON DELETE CASCADE,
    ventana_inicio      TIMESTAMPTZ  NOT NULL,
    ventana_fin         TIMESTAMPTZ  NOT NULL,
    muestras            INT          NOT NULL CHECK (muestras >= 0),
    temp_actual         NUMERIC(6,2) NOT NULL,
    vel_actual          NUMERIC(6,3) NOT NULL,
    pct_crudos_obs      NUMERIC(5,2) NOT NULL CHECK (pct_crudos_obs BETWEEN 0 AND 100),
    pct_ok_obs          NUMERIC(5,2) NOT NULL CHECK (pct_ok_obs BETWEEN 0 AND 100),
    pct_quemados_obs    NUMERIC(5,2) NOT NULL CHECK (pct_quemados_obs BETWEEN 0 AND 100),
    conf_promedio       NUMERIC(4,3) NOT NULL CHECK (conf_promedio BETWEEN 0 AND 1),
    temp_propuesto      NUMERIC(6,2) NOT NULL,
    vel_propuesto       NUMERIC(6,3) NOT NULL,
    temp_aplicado       NUMERIC(6,2),
    vel_aplicado        NUMERIC(6,3),
    prediccion          JSONB,
    z_observado         NUMERIC(10,4),
    z_predicho          NUMERIC(10,4),
    razon_skipped       TEXT,
    aplicado_por        VARCHAR(20), -- 'MPC' (automático), 'OPERARIO' (supervisión aprobado), 'NADIE' (no aplicado)
    decision_origen_id  UUID,
    aprobada_por        VARCHAR(255),
    aprobada_at         TIMESTAMPTZ,
    created_at          TIMESTAMPTZ  NOT NULL DEFAULT now(),
    CONSTRAINT fk_decisiones_control_origen
        FOREIGN KEY (decision_origen_id) REFERENCES decisiones_control(id)
        ON DELETE SET NULL
);

COMMENT ON TABLE  decisiones_control                IS 'Audit del MPC: una fila por decisión propuesta (aplicada o no)';
COMMENT ON COLUMN decisiones_control.ventana_inicio IS 'Inicio de la ventana de evaluación que originó la decisión';
COMMENT ON COLUMN decisiones_control.ventana_fin    IS 'Fin de la ventana de evaluación';
COMMENT ON COLUMN decisiones_control.muestras       IS 'Cantidad de detecciones en la ventana';
COMMENT ON COLUMN decisiones_control.temp_actual    IS 'T1 activa al momento de la decisión';
COMMENT ON COLUMN decisiones_control.vel_actual     IS 'Velocidad activa al momento de la decisión';
COMMENT ON COLUMN decisiones_control.pct_crudos_obs IS '% crudo observado en la ventana (crudos/total*100)';
COMMENT ON COLUMN decisiones_control.pct_ok_obs     IS '% OK observado en la ventana';
COMMENT ON COLUMN decisiones_control.pct_quemados_obs IS '% quemados observado en la ventana';
COMMENT ON COLUMN decisiones_control.conf_promedio  IS 'Confianza promedio del detector en la ventana (0..1)';
COMMENT ON COLUMN decisiones_control.temp_propuesto IS 'T1 que el MPC propuso';
COMMENT ON COLUMN decisiones_control.vel_propuesto  IS 'Velocidad que el MPC propuso';
COMMENT ON COLUMN decisiones_control.temp_aplicado  IS 'T1 efectivamente aplicada (NULL en modo SUPERVISION si no se aprobó)';
COMMENT ON COLUMN decisiones_control.vel_aplicado   IS 'Velocidad efectivamente aplicada (idem)';
COMMENT ON COLUMN decisiones_control.prediccion     IS 'Salida cruda del XGBoost (JSONB con pctCrudos/pctOk/pctQuemados/modelVersion)';
COMMENT ON COLUMN decisiones_control.z_observado     IS 'Valor de la función objetivo con datos observados';
COMMENT ON COLUMN decisiones_control.z_predicho     IS 'Valor de la función objetivo con la predicción del XGBoost';
COMMENT ON COLUMN decisiones_control.razon_skipped  IS 'Si no se propuso cambio, motivo (confianza_baja, ventana_sin_muestras, sin_convergencia, etc.)';
COMMENT ON COLUMN decisiones_control.aplicado_por   IS 'MPC = aplicado por el control automático; OPERARIO = aprobado manualmente en SUPERVISION; NADIE = no aplicado';

-- F-B12: migración idempotente para bases creadas antes de decision_origen_id.
ALTER TABLE decisiones_control ADD COLUMN IF NOT EXISTS decision_origen_id UUID;
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_decisiones_control_origen') THEN
        ALTER TABLE decisiones_control
            ADD CONSTRAINT fk_decisiones_control_origen
            FOREIGN KEY (decision_origen_id) REFERENCES decisiones_control(id)
            ON DELETE SET NULL;
    END IF;
END $$;

COMMENT ON COLUMN decisiones_control.decision_origen_id IS 'Cuando esta decisión fue promovida (NADIE→OPERARIO o AUTO→...), apunta a la decisión origen. Self-ref cuando se promueve la misma fila.';

-- Aprobación de propuestas en SUPERVISION (persistida al confirmar el operario).
ALTER TABLE decisiones_control ADD COLUMN IF NOT EXISTS aprobada_por VARCHAR(255);
ALTER TABLE decisiones_control ADD COLUMN IF NOT EXISTS aprobada_at TIMESTAMPTZ;

COMMENT ON COLUMN decisiones_control.aprobada_por IS 'Usuario que aprobó la propuesta en SUPERVISION (NULL si nadie la aprobó)';
COMMENT ON COLUMN decisiones_control.aprobada_at  IS 'Momento en que se aprobó la propuesta en SUPERVISION';

CREATE INDEX IF NOT EXISTS idx_decisiones_control_lote_created
    ON decisiones_control (lote_id, created_at DESC);

-- Soporte del lookup del origen de una decisión promovida, sin indexar las
-- filas raíz (NULL). Acota el fan-out del ON DELETE SET NULL.
CREATE INDEX IF NOT EXISTS idx_decisiones_control_origen
    ON decisiones_control (decision_origen_id) WHERE decision_origen_id IS NOT NULL;

COMMIT;
