-- ============================================================================
-- Smart-Check Automation — Fermar S.A.
-- Esquema de Base de Datos PostgreSQL (MVP)
-- Versión: 1.1
-- Motor:  PostgreSQL 15+ (Aiven Cloud, AWS sa-east-1)
-- ============================================================================
-- Este script crea la estructura completa del modelo de datos para las
-- User Stories "Persistir lotes productivos" y "ABM de Parámetros y Umbrales
-- de Control por Tipo de Producto" (SCA-142).
--
-- Orden de ejecución:
--   1. Tabla maestra: productos
--   2. Tabla transaccional: lotes_productivos (depende de productos vía FK)
--   3. Tabla de configuración: parametros_producto (depende de productos vía FK)
--   4. Seed de datos de catálogo
-- ============================================================================

BEGIN;

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
    temp_chip             NUMERIC(6,2)    NOT NULL,
    received_at           TIMESTAMPTZ     NOT NULL DEFAULT now(),

    -- FOREIGN KEY: garantiza integridad referencial con el catálogo de dispositivos
    CONSTRAINT fk_metricas_dispositivo_dispositivo
        FOREIGN KEY (dispositivo_id)
        REFERENCES dispositivos (id)
        ON DELETE CASCADE
        ON UPDATE CASCADE
);

COMMENT ON TABLE  metricas_dispositivo            IS 'Historial append-only de telemetría reportada por cada dispositivo (CPU, RAM disponible y temperatura del chip)';
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
-- SEED: Dispositivo de referencia
-- ============================================================================
INSERT INTO dispositivos (id, nombre, ubicacion) VALUES
    ('b1c2d3e4-5678-90ab-cdef-1234567890ab', 'Raspberry Pi Horno 1', 'Línea A')
ON CONFLICT (id) DO NOTHING;

COMMIT;
 