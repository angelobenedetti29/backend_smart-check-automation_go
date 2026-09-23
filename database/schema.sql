-- ============================================================================
-- Smart-Check Automation — Fermar S.A.
-- Esquema de Base de Datos PostgreSQL (MVP)
-- Versión: 1.1
-- Motor:  PostgreSQL 15+ (Aiven Cloud, AWS sa-east-1)
-- ============================================================================
-- Este script crea la estructura completa del modelo de datos para:
--   1. Tabla maestra: productos
--   2. Tabla transaccional: lotes_productivos (depende de productos vía FK)
--   3. Tabla de configuración: parametros_producto (depende de productos vía FK)
--   4. Seed de datos de catálogo
--   5. Tablas de telemetría: dispositivos + metricas_dispositivo
--   6. Setpoints puntuales en parametros_producto + historial_consignas (SCA-142/SCA-320)
--   7. Tabla de acceso: usuarios (autenticación y RBAC)
-- ============================================================================

BEGIN;

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
    temp_horno_2        NUMERIC(6,2),
    temp_comb_horno_2   NUMERIC(6,2),
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
COMMENT ON COLUMN lotes_productivos.temp_horno_2 IS 'Temperatura del horno 2 al inicio (°C)';
COMMENT ON COLUMN lotes_productivos.temp_comb_horno_2 IS 'Temperatura de la cámara de combustión del horno 2 (°C, nullable)';
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
-- TABLA 3: parametros_producto (Parámetros y umbrales de control por producto)
-- ============================================================================
CREATE TABLE IF NOT EXISTS parametros_producto (
    id                      UUID            PRIMARY KEY DEFAULT gen_random_uuid(),
    producto_id             UUID            NOT NULL UNIQUE,
    peso_referencia_kg      NUMERIC(10,3)   NOT NULL,
    tolerancia_peso_pct     NUMERIC(5,2)    NOT NULL,
    dimension_base_cm       NUMERIC(6,2)    NOT NULL,
    tolerancia_dimension_cm NUMERIC(6,2)    NOT NULL,
    temp_min                NUMERIC(6,2)    NOT NULL,
    temp_max                NUMERIC(6,2)    NOT NULL,
    velocidad_cinta_min     NUMERIC(6,2)    NOT NULL,
    velocidad_cinta_max     NUMERIC(6,2)    NOT NULL,
    activo                  BOOLEAN         NOT NULL DEFAULT true,
    created_at              TIMESTAMPTZ     NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ     NOT NULL DEFAULT now(),

    -- FOREIGN KEY: garantiza integridad referencial con la tabla maestra
    CONSTRAINT fk_parametros_producto_producto
        FOREIGN KEY (producto_id)
        REFERENCES productos (id)
        ON DELETE RESTRICT
        ON UPDATE CASCADE,

    -- RESTRICCIONES DE DOMINIO (Check Constraints)

    -- El peso de referencia debe ser positivo
    CONSTRAINT chk_parametros_peso_positivo
        CHECK (peso_referencia_kg > 0),

    -- La tolerancia de peso no puede ser negativa
    CONSTRAINT chk_parametros_tolerancia_peso
        CHECK (tolerancia_peso_pct >= 0),

    -- La dimensión base debe ser positiva
    CONSTRAINT chk_parametros_dimension_positiva
        CHECK (dimension_base_cm > 0),

    -- La tolerancia de dimensión no puede ser negativa
    CONSTRAINT chk_parametros_tolerancia_dimension
        CHECK (tolerancia_dimension_cm >= 0),

    -- Consistencia del rango de temperatura: el máximo debe superar al mínimo
    CONSTRAINT chk_parametros_temp_rango
        CHECK (temp_max > temp_min),

    -- Consistencia del rango de velocidad de cinta: el máximo debe superar al mínimo
    CONSTRAINT chk_parametros_velocidad_rango
        CHECK (velocidad_cinta_max > velocidad_cinta_min)
);

COMMENT ON TABLE  parametros_producto IS 'Parámetros y umbrales de control ideales de horneado por variedad de producto, usados por las reglas de detección y el recomendador inteligente.';
COMMENT ON COLUMN parametros_producto.id IS 'Identificador UUID del registro de parámetros';
COMMENT ON COLUMN parametros_producto.producto_id IS 'FK al catálogo de productos (relación 1:1, un set de parámetros activo por producto)';
COMMENT ON COLUMN parametros_producto.peso_referencia_kg IS 'Peso patrón/ideal de una unidad del producto (kg)';
COMMENT ON COLUMN parametros_producto.tolerancia_peso_pct IS 'Tolerancia admitida sobre el peso de referencia, en porcentaje';
COMMENT ON COLUMN parametros_producto.dimension_base_cm IS 'Dimensión base/tamaño ideal del producto (cm)';
COMMENT ON COLUMN parametros_producto.tolerancia_dimension_cm IS 'Tolerancia de tamaño admitida sobre la dimensión base (cm)';
COMMENT ON COLUMN parametros_producto.temp_min IS 'Temperatura mínima aceptable de horneado (°C)';
COMMENT ON COLUMN parametros_producto.temp_max IS 'Temperatura máxima aceptable de horneado (°C)';
COMMENT ON COLUMN parametros_producto.velocidad_cinta_min IS 'Velocidad mínima aceptable de la cinta transportadora (m/s)';
COMMENT ON COLUMN parametros_producto.velocidad_cinta_max IS 'Velocidad máxima aceptable de la cinta transportadora (m/s)';
COMMENT ON COLUMN parametros_producto.activo IS 'Indica si el set de parámetros está vigente (baja lógica para el ABM)';
COMMENT ON COLUMN parametros_producto.created_at IS 'Marca temporal de alta del registro';
COMMENT ON COLUMN parametros_producto.updated_at IS 'Marca temporal de la última modificación del registro';

-- ============================================================================
-- ÍNDICES (Indexes) — parametros_producto
-- ============================================================================

-- Índice para búsquedas por producto (FK lookup), explícito por consistencia
-- aunque producto_id ya tiene una restricción UNIQUE
CREATE INDEX IF NOT EXISTS idx_parametros_producto_producto_id
    ON parametros_producto (producto_id);

-- ============================================================================
-- SEED: Datos de catálogo de productos
-- Incluye el producto de referencia del JSON contrato
-- ============================================================================
INSERT INTO productos (id, nombre) VALUES
    ('a1b2c3d4-5678-90ab-cdef-1234567890ab', 'Tostada Integral')
ON CONFLICT (id) DO NOTHING;

-- ============================================================================
-- SEED: Parámetros de control por defecto para el producto de referencia
-- ============================================================================
INSERT INTO parametros_producto (
    producto_id, peso_referencia_kg, tolerancia_peso_pct,
    dimension_base_cm, tolerancia_dimension_cm,
    temp_min, temp_max, velocidad_cinta_min, velocidad_cinta_max
) VALUES (
    'a1b2c3d4-5678-90ab-cdef-1234567890ab', 0.030, 10.00,
    8.00, 0.50,
    160.00, 180.00, 0.10, 0.30
)
ON CONFLICT (producto_id) DO NOTHING;

-- ============================================================================
-- TABLA 4: dispositivos (Catálogo de nodos Raspberry Pi / SBC de monitoreo)
-- ============================================================================
CREATE TABLE IF NOT EXISTS dispositivos (
    id          UUID            PRIMARY KEY DEFAULT gen_random_uuid(),
    nombre      VARCHAR(100)    NOT NULL,
    ubicacion   VARCHAR(100),
    created_at  TIMESTAMPTZ     NOT NULL DEFAULT now()
);

COMMENT ON TABLE  dispositivos     IS 'Catálogo de nodos Raspberry Pi que reportan telemetría al backend';
COMMENT ON COLUMN dispositivos.id        IS 'Identificador UUID del dispositivo';
COMMENT ON COLUMN dispositivos.nombre    IS 'Nombre descriptivo del dispositivo (ej: Raspberry Pi Horno 1)';
COMMENT ON COLUMN dispositivos.ubicacion IS 'Ubicación física del dispositivo (ej: Línea A)';

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
-- ALTER: parametros_producto — setpoints puntuales de cocción (SCA-142/SCA-320)
-- ============================================================================
ALTER TABLE parametros_producto
    ADD COLUMN IF NOT EXISTS temp_setpoint             NUMERIC(6,2),
    ADD COLUMN IF NOT EXISTS velocidad_cinta_setpoint   NUMERIC(6,2);

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_parametros_temp_setpoint_rango') THEN
        ALTER TABLE parametros_producto
            ADD CONSTRAINT chk_parametros_temp_setpoint_rango
                CHECK (temp_setpoint IS NULL OR temp_setpoint BETWEEN temp_min AND temp_max);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_parametros_velocidad_setpoint_rango') THEN
        ALTER TABLE parametros_producto
            ADD CONSTRAINT chk_parametros_velocidad_setpoint_rango
                CHECK (velocidad_cinta_setpoint IS NULL OR velocidad_cinta_setpoint BETWEEN velocidad_cinta_min AND velocidad_cinta_max);
    END IF;
END $$;

COMMENT ON COLUMN parametros_producto.temp_setpoint IS 'Temperatura objetivo puntual a despachar al horno (°C), dentro de [temp_min, temp_max]. Nullable: si no está cargada, no se puede despachar consigna automática para el producto.';
COMMENT ON COLUMN parametros_producto.velocidad_cinta_setpoint IS 'Velocidad de cinta objetivo puntual a despachar al horno (m/s), dentro de [velocidad_cinta_min, velocidad_cinta_max]. Nullable, misma razón que temp_setpoint.';

-- Seed: setpoints puntuales por defecto para "Tostada Integral"
UPDATE parametros_producto
SET temp_setpoint = 170.00, velocidad_cinta_setpoint = 0.20
WHERE producto_id = 'a1b2c3d4-5678-90ab-cdef-1234567890ab'
  AND temp_setpoint IS NULL;

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
-- SEED: catálogo completo de las 6 variedades de panificados (SCA-142)
-- ============================================================================
INSERT INTO productos (id, nombre) VALUES
    ('b2c3d4e5-6789-01ab-cdef-234567890abc', 'Pan Lactal'),
    ('c3d4e5f6-789a-12bc-def3-34567890abcd', 'Pan Francés'),
    ('d4e5f6a7-89ab-23cd-ef34-4567890abcde', 'Pan de Salvado'),
    ('e5f6a7b8-9abc-34de-f456-567890abcdef', 'Medialunas'),
    ('f6a7b8c9-abcd-45ef-5678-67890abcdef1', 'Pan Dulce')
ON CONFLICT (id) DO NOTHING;

INSERT INTO parametros_producto (
    producto_id, peso_referencia_kg, tolerancia_peso_pct,
    dimension_base_cm, tolerancia_dimension_cm,
    temp_min, temp_max, velocidad_cinta_min, velocidad_cinta_max,
    temp_setpoint, velocidad_cinta_setpoint
) VALUES
    ('b2c3d4e5-6789-01ab-cdef-234567890abc', 0.500, 8.00,  25.00, 1.00, 180.00, 200.00, 0.15, 0.35, 190.00, 0.25),
    ('c3d4e5f6-789a-12bc-def3-34567890abcd', 0.250, 6.00,  30.00, 1.50, 200.00, 220.00, 0.20, 0.40, 210.00, 0.30),
    ('d4e5f6a7-89ab-23cd-ef34-4567890abcde', 0.450, 8.00,  22.00, 1.00, 170.00, 190.00, 0.15, 0.35, 180.00, 0.25),
    ('e5f6a7b8-9abc-34de-f456-567890abcdef', 0.060, 12.00, 10.00, 0.50, 190.00, 210.00, 0.25, 0.45, 200.00, 0.35),
    ('f6a7b8c9-abcd-45ef-5678-67890abcdef1', 0.800, 10.00, 15.00, 1.00, 150.00, 170.00, 0.08, 0.20, 160.00, 0.14)
ON CONFLICT (producto_id) DO NOTHING;

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
-- ============================================================================
INSERT INTO usuarios (email, nombre, rol, password_hash) VALUES
    ('admin@fermar.com.ar',      'Administrador Fermar',  'Administrador', '$2a$10$5l3CZ8EQW3PCsAMgyetQeOA5j7yFW7QBWwQAgpGAhROdt48ayUJVy'),
    ('supervisor@fermar.com.ar', 'Supervisor Fermar',     'Supervisor',    '$2a$10$5l3CZ8EQW3PCsAMgyetQeOA5j7yFW7QBWwQAgpGAhROdt48ayUJVy'),
    ('operario@fermar.com.ar',   'Operario Fermar',       'Operario',      '$2a$10$5l3CZ8EQW3PCsAMgyetQeOA5j7yFW7QBWwQAgpGAhROdt48ayUJVy')
ON CONFLICT (email) DO NOTHING;

-- Reparación idempotente de bases ya sembradas con el hash roto (el literal
-- anterior NO correspondía a password123). Sólo reemplaza filas que todavía
-- llevan ese hash exacto, así que nunca pisa una contraseña ya cambiada.
UPDATE usuarios
SET password_hash = '$2a$10$5l3CZ8EQW3PCsAMgyetQeOA5j7yFW7QBWwQAgpGAhROdt48ayUJVy'
WHERE email IN ('admin@fermar.com.ar', 'supervisor@fermar.com.ar', 'operario@fermar.com.ar')
  AND password_hash = '$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy';

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
-- ALTER: registration_requests — tipo de dispositivo solicitado
-- Idempotente para bases existentes. Nullable para no invalidar solicitudes
-- legadas creadas antes de incorporar el tipo.
-- ============================================================================
ALTER TABLE registration_requests ADD COLUMN IF NOT EXISTS tipo VARCHAR(16);

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_registration_requests_tipo') THEN
        ALTER TABLE registration_requests ADD CONSTRAINT chk_registration_requests_tipo
            CHECK (tipo IS NULL OR tipo IN ('ENTRADA_HORNO', 'SALIDA_HORNO'));
    END IF;
END $$;

COMMENT ON COLUMN registration_requests.tipo IS 'Tipo/ubicación funcional que el nodo solicita al registrarse: ENTRADA_HORNO o SALIDA_HORNO (nullable para solicitudes legadas)';

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

COMMIT;
