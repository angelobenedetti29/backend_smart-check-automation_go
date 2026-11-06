-- ============================================================================
-- SmartCheck Automation — Fermar S.A.
-- Esquema de Base de Datos PostgreSQL (MVP)
-- Versión: 1.1
-- Motor:  PostgreSQL 16 (Supabase)
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

-- Defensas de la aplicación del esquema: si otra sesión retiene un lock o el
-- DDL se cuelga, abortamos en vez de bloquear el arranque indefinidamente. El
-- advisory lock de abajo queda acotado por lock_timeout; el resto de la
-- aplicación por statement_timeout.
SET LOCAL lock_timeout = '3s';
SET LOCAL statement_timeout = '60s';

-- Serialize the complete schema application before any extension, table or seed DDL.
SELECT pg_advisory_xact_lock(hashtextextended('smart-check:schema', 0));

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ============================================================================
-- TABLA 1: productos (Catálogo maestro de productos panificados)
-- ============================================================================
CREATE TABLE IF NOT EXISTS productos (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    nombre character varying(100) NOT NULL,
    capacidad_nominal_kg_h numeric(10,3),
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    peso_referencia_kg numeric(10,3) NOT NULL,
    tolerancia_peso_pct numeric(5,2) NOT NULL,
    dimension_base_cm numeric(6,2) NOT NULL,
    tolerancia_dimension_cm numeric(6,2) NOT NULL,
    temp_min numeric(6,2) NOT NULL,
    temp_max numeric(6,2) NOT NULL,
    velocidad_cinta_min numeric(6,2) NOT NULL,
    velocidad_cinta_max numeric(6,2) NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    temp_setpoint numeric(6,2),
    velocidad_cinta_setpoint numeric(6,2),
    activo boolean DEFAULT true NOT NULL,
    delta_temp1_max_c numeric(5,2) DEFAULT 10.0 NOT NULL,
    delta_vel_max numeric(5,3) DEFAULT 0.05 NOT NULL,
    CONSTRAINT chk_productos_capacidad_nominal_positiva CHECK (((capacidad_nominal_kg_h IS NULL) OR (capacidad_nominal_kg_h > (0)::numeric))),
    CONSTRAINT chk_productos_delta_temp1_pos CHECK ((delta_temp1_max_c > (0)::numeric)),
    CONSTRAINT chk_productos_delta_vel_pos CHECK ((delta_vel_max > (0)::numeric)),
    CONSTRAINT chk_productos_dimension_positiva CHECK ((dimension_base_cm > (0)::numeric)),
    CONSTRAINT chk_productos_peso_positivo CHECK ((peso_referencia_kg > (0)::numeric)),
    CONSTRAINT chk_productos_temp_rango CHECK ((temp_max > temp_min)),
    CONSTRAINT chk_productos_temp_setpoint_rango CHECK (((temp_setpoint IS NULL) OR ((temp_setpoint >= temp_min) AND (temp_setpoint <= temp_max)))),
    CONSTRAINT chk_productos_tolerancia_dimension CHECK ((tolerancia_dimension_cm >= (0)::numeric)),
    CONSTRAINT chk_productos_tolerancia_peso CHECK ((tolerancia_peso_pct >= (0)::numeric)),
    CONSTRAINT chk_productos_velocidad_rango CHECK ((velocidad_cinta_max > velocidad_cinta_min)),
    CONSTRAINT chk_productos_velocidad_setpoint_rango CHECK (((velocidad_cinta_setpoint IS NULL) OR ((velocidad_cinta_setpoint >= velocidad_cinta_min) AND (velocidad_cinta_setpoint <= velocidad_cinta_max)))),
    CONSTRAINT productos_pkey PRIMARY KEY (id)
);

COMMENT ON TABLE productos IS 'Catálogo maestro de productos panificados de Fermar S.A.';
COMMENT ON COLUMN productos.id IS 'Identificador único universal del producto';
COMMENT ON COLUMN productos.nombre IS 'Nombre descriptivo del producto (ej: Tostada Integral, Pan Francés)';
COMMENT ON COLUMN productos.capacidad_nominal_kg_h IS 'Capacidad nominal de producción del producto en kg por hora; nullable, si se informa debe ser mayor a 0';
COMMENT ON COLUMN productos.peso_referencia_kg IS 'Peso patrón/ideal de una unidad del producto (kg)';
COMMENT ON COLUMN productos.tolerancia_peso_pct IS 'Tolerancia admitida sobre el peso de referencia, en porcentaje';
COMMENT ON COLUMN productos.dimension_base_cm IS 'Dimensión base/tamaño ideal del producto (cm)';
COMMENT ON COLUMN productos.tolerancia_dimension_cm IS 'Tolerancia de tamaño admitida sobre la dimensión base (cm)';
COMMENT ON COLUMN productos.temp_min IS 'Temperatura mínima aceptable de horneado (°C)';
COMMENT ON COLUMN productos.temp_max IS 'Temperatura máxima aceptable de horneado (°C)';
COMMENT ON COLUMN productos.velocidad_cinta_min IS 'Velocidad mínima aceptable de la cinta transportadora (m/s)';
COMMENT ON COLUMN productos.velocidad_cinta_max IS 'Velocidad máxima aceptable de la cinta transportadora (m/s)';
COMMENT ON COLUMN productos.updated_at IS 'Marca temporal de la última modificación del registro';
COMMENT ON COLUMN productos.temp_setpoint IS 'Temperatura objetivo dentro de [temp_min, temp_max]; nullable, opcional, sólo usado si se reactiva el despacho automático (SCA-142)';
COMMENT ON COLUMN productos.velocidad_cinta_setpoint IS 'Velocidad objetivo dentro de [velocidad_cinta_min, velocidad_cinta_max]; nullable, opcional';
COMMENT ON COLUMN productos.activo IS 'Vigencia del producto en el catálogo: false = retirado (no se ofrece para elegir, sigue resolviendo lotes históricos)';
COMMENT ON COLUMN productos.delta_temp1_max_c IS 'Máxima variación permitida de T1 por ciclo del optimizador (°C/cycle)';
COMMENT ON COLUMN productos.delta_vel_max IS 'Máxima variación permitida de velocidad de cinta por ciclo (m/s/cycle)';

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
-- TABLA 2: usuarios (Usuarios corporativos autorizados para la plataforma)
-- ============================================================================
CREATE TABLE IF NOT EXISTS usuarios (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    email character varying(254) NOT NULL,
    nombre character varying(150) NOT NULL,
    rol character varying(20) NOT NULL,
    password_hash character varying(255),
    activo boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT chk_usuarios_email_formato CHECK (((email)::text ~* '^[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}$'::text)),
    CONSTRAINT chk_usuarios_rol CHECK (rol IN ('Administrador', 'Supervisor', 'Operario')),
    CONSTRAINT usuarios_email_key UNIQUE (email),
    CONSTRAINT usuarios_pkey PRIMARY KEY (id)
);

COMMENT ON TABLE usuarios IS 'Usuarios corporativos autorizados para acceder a la plataforma SmartCheck.';
COMMENT ON COLUMN usuarios.email IS 'Correo corporativo. Debe existir aquí para poder autenticarse.';
COMMENT ON COLUMN usuarios.rol IS 'Nivel de acceso: Administrador, Supervisor u Operario.';
COMMENT ON COLUMN usuarios.password_hash IS 'Hash bcrypt de la contraseña para autenticación local (nullable si solo usa OAuth).';
COMMENT ON COLUMN usuarios.activo IS 'Revocar acceso sin eliminar el registro (activo=false)';

INSERT INTO usuarios (email, nombre, rol, password_hash) VALUES
    ('admin@fermar.com.ar',      'Administrador Fermar',  'Administrador', crypt('password123', gen_salt('bf', 10))),
    ('supervisor@fermar.com.ar', 'Supervisor Fermar',     'Supervisor',    crypt('password123', gen_salt('bf', 10))),
    ('operario@fermar.com.ar',   'Operario Fermar',       'Operario',      crypt('password123', gen_salt('bf', 10)))
ON CONFLICT (email) DO NOTHING;

-- Índices
CREATE INDEX IF NOT EXISTS idx_usuarios_email
    ON usuarios USING btree (email);

-- ============================================================================
-- TABLA 3: sectores (Sectores de producción: línea ENTRADA/SALIDA)
-- ============================================================================
CREATE TABLE IF NOT EXISTS sectores (
    id character varying(50) NOT NULL,
    nombre character varying(100) NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT sectores_pkey PRIMARY KEY (id)
);

COMMENT ON TABLE sectores IS 'Sectores de producción: agrupan un dispositivo ENTRADA_HORNO y uno SALIDA_HORNO de la misma línea.';
COMMENT ON COLUMN sectores.id IS 'Identificador legible del sector (ej: horno-1)';
COMMENT ON COLUMN sectores.nombre IS 'Nombre para mostrar del sector (ej: Horno 1)';
COMMENT ON COLUMN sectores.created_at IS 'Marca temporal de alta del sector';

-- ============================================================================
-- TABLA 4: dispositivos (Catálogo de nodos Raspberry Pi / SBC de monitoreo)
-- ============================================================================
CREATE TABLE IF NOT EXISTS dispositivos (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    nombre character varying(100) NOT NULL,
    rename_url text,
    rename_token text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    whep_url character varying(500),
    auth_status character varying(12) DEFAULT 'unenrolled'::character varying NOT NULL,
    auth_updated_at timestamp with time zone,
    secret_hash character varying(64),
    tipo character varying(16),
    sector_id character varying(50),
    CONSTRAINT chk_dispositivos_auth_status CHECK (auth_status IN ('unenrolled', 'active', 'disabled', 'revoked')),
    CONSTRAINT chk_dispositivos_secret_revoked CHECK (((secret_hash IS NULL) OR ((auth_status)::text <> 'revoked'::text))),
    CONSTRAINT chk_dispositivos_tipo CHECK (tipo IS NULL OR tipo IN ('ENTRADA_HORNO', 'SALIDA_HORNO')),
    CONSTRAINT dispositivos_pkey PRIMARY KEY (id),
    CONSTRAINT fk_dispositivos_sector FOREIGN KEY (sector_id) REFERENCES sectores(id) ON DELETE SET NULL
);

COMMENT ON TABLE dispositivos IS 'Catálogo de nodos Raspberry Pi que reportan telemetría al backend';
COMMENT ON COLUMN dispositivos.id IS 'Identificador UUID del dispositivo';
COMMENT ON COLUMN dispositivos.nombre IS 'Nombre descriptivo del dispositivo (ej: Raspberry Pi Horno 1)';
COMMENT ON COLUMN dispositivos.rename_url IS 'URL completa (esquema + host + puerto) que el nodo expone para el push de renombre (PUT {rename_url}/api/v1/dispositivos/nombre); NULL = no anunciada (legacy)';
COMMENT ON COLUMN dispositivos.rename_token IS 'Credencial Bearer entregada al nodo en el pickup; el backend la usa para empujar el renombre. Plaintext, NULL para dispositivos legacy o sin push';
COMMENT ON COLUMN dispositivos.whep_url IS 'URL del stream WHEP de la cámara asociada al dispositivo (nullable: sin cámara configurada)';
COMMENT ON COLUMN dispositivos.auth_status IS 'Estado de admisión del dispositivo (unenrolled/active/disabled/revoked), independiente del heartbeat';
COMMENT ON COLUMN dispositivos.secret_hash IS 'SHA-256 hexadecimal del secret del dispositivo; NULL si no está registrado o fue revocado';
COMMENT ON COLUMN dispositivos.tipo IS 'Tipo/ubicación funcional del nodo en la línea: ENTRADA_HORNO o SALIDA_HORNO (nullable para filas legadas)';
COMMENT ON COLUMN dispositivos.sector_id IS 'Sector al que pertenece el dispositivo (nullable: dispositivo sin sector asignado)';

-- Índices
CREATE UNIQUE INDEX IF NOT EXISTS uq_dispositivos_secret_hash
    ON dispositivos USING btree (secret_hash) WHERE (secret_hash IS NOT NULL);
CREATE UNIQUE INDEX IF NOT EXISTS uq_dispositivos_sector_tipo
    ON dispositivos USING btree (sector_id, tipo) WHERE ((sector_id IS NOT NULL) AND (tipo IS NOT NULL));

-- ============================================================================
-- TABLA 5: lotes_productivos (Registro transaccional de lotes horneados)
-- ============================================================================
CREATE TABLE IF NOT EXISTS lotes_productivos (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    producto_id uuid NOT NULL,
    turno character varying(10),
    inicio_at timestamp with time zone NOT NULL,
    fin_at timestamp with time zone,
    total_unidades integer,
    correctos integer,
    quemados integer,
    crudas integer,
    correctos_kg numeric(12,3),
    quemados_kg numeric(12,3),
    crudos_kg numeric(12,3),
    temp_horno_1 numeric(6,2),
    temp_comb_horno_1 numeric(6,2),
    velocidad_cinta numeric(6,2),
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    dispositivo_id uuid,
    sector_id character varying(50),
    estado character varying(10) DEFAULT 'CERRADO'::character varying NOT NULL,
    abierto_por uuid,
    ultimo_evento_en timestamp with time zone,
    motivo_cierre character varying(20),
    abrir_idempotency_key character varying(64),
    cerrar_idempotency_key character varying(64),
    modo character varying(12) DEFAULT 'MANUAL'::character varying NOT NULL,
    ventana_evaluacion_segundos integer DEFAULT 600 NOT NULL,
    ventana_actualizacion_segundos integer DEFAULT 300 NOT NULL,
    tolerancia_temp_c numeric(5,2) DEFAULT 2.0 NOT NULL,
    umbral_aceptacion_crudo numeric(5,2) DEFAULT 5.0 NOT NULL,
    umbral_aceptacion_quemado numeric(5,2) DEFAULT 5.0 NOT NULL,
    pesos_objetivo jsonb DEFAULT '{"dc": 0.5, "dt": 0.05, "wc": 1.0, "wo": 1.0, "wq": 1.0}'::jsonb NOT NULL,
    iniciado_manualmente boolean DEFAULT false NOT NULL,
    temp1_objetivo numeric(6,2),
    vel_objetivo numeric(6,3),
    delta_temp1_max_c numeric(6,3),
    delta_vel_max numeric(6,4),
    confianza_promedio numeric(4,3),
    CONSTRAINT chk_lotes_abierto_sector CHECK ((((estado)::text <> 'ABIERTO'::text) OR ((sector_id IS NOT NULL) AND (fin_at IS NULL)))),
    CONSTRAINT chk_lotes_confianza_promedio CHECK (((confianza_promedio IS NULL) OR ((confianza_promedio >= (0)::numeric) AND (confianza_promedio <= (1)::numeric)))),
    CONSTRAINT chk_lotes_correctos_no_negativo CHECK ((correctos >= 0)),
    CONSTRAINT chk_lotes_crudas_no_negativo CHECK ((crudas >= 0)),
    CONSTRAINT chk_lotes_delta_temp1_pos CHECK ((delta_temp1_max_c > (0)::numeric)),
    CONSTRAINT chk_lotes_delta_vel_pos CHECK ((delta_vel_max > (0)::numeric)),
    CONSTRAINT chk_lotes_estado CHECK (estado IN ('ABIERTO', 'CERRADO')),
    CONSTRAINT chk_lotes_fin_after_inicio CHECK ((fin_at >= inicio_at)),
    CONSTRAINT chk_lotes_modo CHECK (modo IN ('MANUAL', 'SUPERVISION', 'AUTOMATICO')),
    CONSTRAINT chk_lotes_motivo_cierre CHECK (motivo_cierre IS NULL OR motivo_cierre IN ('sin_detecciones', 'manual', 'apagado', 'seguridad')),
    CONSTRAINT chk_lotes_quemados_no_negativo CHECK ((quemados >= 0)),
    CONSTRAINT chk_lotes_sum_unidades_consistente CHECK ((((correctos + quemados) + COALESCE(crudas, 0)) <= total_unidades)),
    CONSTRAINT chk_lotes_tolerancia_temp_pos CHECK ((tolerancia_temp_c >= (0)::numeric)),
    CONSTRAINT chk_lotes_total_unidades_no_negativo CHECK ((total_unidades >= 0)),
    CONSTRAINT chk_lotes_turno CHECK (turno IN ('mañana', 'tarde', 'noche')),
    CONSTRAINT chk_lotes_umbrales_aceptacion CHECK (((umbral_aceptacion_crudo >= (0)::numeric) AND (umbral_aceptacion_quemado >= (0)::numeric) AND (umbral_aceptacion_crudo <= (100)::numeric) AND (umbral_aceptacion_quemado <= (100)::numeric))),
    CONSTRAINT chk_lotes_ventana_actualizacion_pos CHECK ((ventana_actualizacion_segundos > 0)),
    CONSTRAINT chk_lotes_ventana_evaluacion_pos CHECK ((ventana_evaluacion_segundos > 0)),
    CONSTRAINT lotes_productivos_pkey PRIMARY KEY (id),
    CONSTRAINT fk_lotes_abierto_por FOREIGN KEY (abierto_por) REFERENCES dispositivos(id) ON DELETE RESTRICT,
    CONSTRAINT fk_lotes_dispositivo FOREIGN KEY (dispositivo_id) REFERENCES dispositivos(id) ON DELETE RESTRICT,
    CONSTRAINT fk_lotes_productos FOREIGN KEY (producto_id) REFERENCES productos(id) ON UPDATE CASCADE ON DELETE RESTRICT,
    CONSTRAINT fk_lotes_sector FOREIGN KEY (sector_id) REFERENCES sectores(id) ON DELETE RESTRICT
);

COMMENT ON TABLE lotes_productivos IS 'Registro transaccional de lotes productivos horneados. Cada fila representa una horneada completa de un producto en un turno.';
COMMENT ON COLUMN lotes_productivos.id IS 'Identificador UUID del lote productivo';
COMMENT ON COLUMN lotes_productivos.producto_id IS 'FK al catálogo de productos';
COMMENT ON COLUMN lotes_productivos.turno IS 'Turno de producción: mañana, tarde o noche';
COMMENT ON COLUMN lotes_productivos.inicio_at IS 'Marca temporal de inicio de la horneada';
COMMENT ON COLUMN lotes_productivos.fin_at IS 'Marca temporal de fin de la horneada';
COMMENT ON COLUMN lotes_productivos.total_unidades IS 'Total de unidades horneadas en el lote';
COMMENT ON COLUMN lotes_productivos.correctos IS 'Unidades en estado correcto (aprobadas)';
COMMENT ON COLUMN lotes_productivos.quemados IS 'Unidades quemadas (rechazadas)';
COMMENT ON COLUMN lotes_productivos.crudas IS 'Unidades crudas / sin clasificar (nullable)';
COMMENT ON COLUMN lotes_productivos.correctos_kg IS 'Peso total ESTIMADO en kg de panes correctos = correctos × productos.peso_referencia_kg (3 decimales)';
COMMENT ON COLUMN lotes_productivos.quemados_kg IS 'Peso total ESTIMADO en kg de panes quemados = quemados × productos.peso_referencia_kg (3 decimales)';
COMMENT ON COLUMN lotes_productivos.crudos_kg IS 'Peso total ESTIMADO en kg de panes crudos = crudas × productos.peso_referencia_kg (3 decimales, nullable)';
COMMENT ON COLUMN lotes_productivos.temp_horno_1 IS 'Temperatura del horno 1 al inicio (°C)';
COMMENT ON COLUMN lotes_productivos.temp_comb_horno_1 IS 'Temperatura de la cámara de combustión del horno 1 (°C, nullable)';
COMMENT ON COLUMN lotes_productivos.velocidad_cinta IS 'Velocidad de la cinta transportadora del horno (m/s)';
COMMENT ON COLUMN lotes_productivos.sector_id IS 'Sector al que pertenece el lote (nullable para lotes legados; obligatorio si estado=ABIERTO)';
COMMENT ON COLUMN lotes_productivos.estado IS 'Ciclo de vida del lote: ABIERTO (en curso) o CERRADO (finalizado)';
COMMENT ON COLUMN lotes_productivos.abierto_por IS 'FK al dispositivo que abrió el lote (ENTRADA_HORNO, o SALIDA_HORNO en lote degradado)';
COMMENT ON COLUMN lotes_productivos.ultimo_evento_en IS 'Marca temporal del último evento aceptado del lote; insumo del cálculo de inactividad';
COMMENT ON COLUMN lotes_productivos.motivo_cierre IS 'Motivo del cierre: sin_detecciones, manual, apagado o seguridad (nullable)';
COMMENT ON COLUMN lotes_productivos.abrir_idempotency_key IS 'Clave de idempotencia de apertura enviada por la entrada; única cuando no es NULL';
COMMENT ON COLUMN lotes_productivos.cerrar_idempotency_key IS 'Clave de idempotencia de cierre enviada por la salida (nullable)';
COMMENT ON COLUMN lotes_productivos.modo IS 'Modo de operación: MANUAL/SUPERVISION/AUTOMATICO (default MANUAL para no romper lotes existentes)';
COMMENT ON COLUMN lotes_productivos.ventana_evaluacion_segundos IS 'Cada cuántos segundos se re-evalúa el estado y se decide un nuevo setpoint';
COMMENT ON COLUMN lotes_productivos.ventana_actualizacion_segundos IS 'Cada cuántos segundos se aplica un nuevo setpoint (>= ventana_evaluacion)';
COMMENT ON COLUMN lotes_productivos.tolerancia_temp_c IS 'Tolerancia sobre la temperatura objetivo al iniciar (°C, default 2.0)';
COMMENT ON COLUMN lotes_productivos.umbral_aceptacion_crudo IS 'Porcentaje máximo de crudos aceptado (% del total); alerta si se supera N ventanas';
COMMENT ON COLUMN lotes_productivos.umbral_aceptacion_quemado IS 'Porcentaje máximo de quemados aceptado (% del total)';
COMMENT ON COLUMN lotes_productivos.pesos_objetivo IS 'Pesos de la función objetivo del MPC: wc, wq, wo, dt, dc';
COMMENT ON COLUMN lotes_productivos.iniciado_manualmente IS 'TRUE = el operador arrancó horno+cinta por su cuenta y el sistema se enganchó después';
COMMENT ON COLUMN lotes_productivos.temp1_objetivo IS 'Último setpoint de T1 aplicado por el control (lectura del estado activo)';
COMMENT ON COLUMN lotes_productivos.vel_objetivo IS 'Último setpoint de velocidad de cinta aplicado por el control';
COMMENT ON COLUMN lotes_productivos.delta_temp1_max_c IS 'Override de tuning del ΔT1 máximo por ciclo (°C); NULL = usa el valor del producto';
COMMENT ON COLUMN lotes_productivos.delta_vel_max IS 'Override de tuning del ΔV máximo por ciclo (m/s); NULL = usa el valor del producto';
COMMENT ON COLUMN lotes_productivos.confianza_promedio IS 'Confianza media de las detecciones del lote, fijada al cerrarlo desde eventos_lote.confianza; NULL si el lote no tuvo detecciones con confianza';

-- Índices
CREATE INDEX IF NOT EXISTS idx_lotes_created_at
    ON lotes_productivos USING btree (created_at);
CREATE INDEX IF NOT EXISTS idx_lotes_inicio_at
    ON lotes_productivos USING btree (inicio_at);
CREATE INDEX IF NOT EXISTS idx_lotes_producto_fecha
    ON lotes_productivos USING btree (producto_id, inicio_at);
CREATE INDEX IF NOT EXISTS idx_lotes_producto_id
    ON lotes_productivos USING btree (producto_id);
CREATE INDEX IF NOT EXISTS idx_lotes_sector_inicio
    ON lotes_productivos USING btree (sector_id, inicio_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS uq_lotes_abierto_por_sector
    ON lotes_productivos USING btree (sector_id) WHERE ((estado)::text = 'ABIERTO'::text);
CREATE UNIQUE INDEX IF NOT EXISTS uq_lotes_abrir_idem
    ON lotes_productivos USING btree (abrir_idempotency_key) WHERE (abrir_idempotency_key IS NOT NULL);

-- ============================================================================
-- TABLA 6: historial_consignas (Auditoría de consignas térmicas/velocidad)
-- ============================================================================
CREATE TABLE IF NOT EXISTS historial_consignas (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    horno_id character varying(50) NOT NULL,
    lote_id uuid,
    producto_id uuid,
    temperatura_objetivo numeric(6,2) NOT NULL,
    velocidad_cinta_objetivo numeric(6,2) NOT NULL,
    origen character varying(12) NOT NULL,
    usuario character varying(150),
    exitosa boolean NOT NULL,
    motivo_error text,
    temperatura_previa numeric(6,2),
    velocidad_cinta_previa numeric(6,2),
    decision_id uuid,
    estado character varying(20) DEFAULT 'PENDIENTE'::character varying NOT NULL,
    creada_en timestamp with time zone DEFAULT now() NOT NULL,
    dispositivo_id uuid,
    CONSTRAINT chk_historial_consignas_estado CHECK (estado IN ('PENDIENTE', 'APLICADA', 'RECHAZADA')),
    CONSTRAINT chk_historial_consignas_origen CHECK (origen IN ('AUTOMATICO', 'MANUAL')),
    CONSTRAINT historial_consignas_pkey PRIMARY KEY (id),
    CONSTRAINT fk_historial_consignas_dispositivo FOREIGN KEY (dispositivo_id) REFERENCES dispositivos(id) ON DELETE RESTRICT,
    CONSTRAINT fk_historial_consignas_producto FOREIGN KEY (producto_id) REFERENCES productos(id) ON DELETE SET NULL
);

COMMENT ON TABLE historial_consignas IS 'Auditoría de toda consigna térmica/velocidad manual (SCA-320) aceptada y encolada para la Pi ENTRADA; conserva filas históricas de origen automático (SCA-142).';
COMMENT ON COLUMN historial_consignas.horno_id IS 'Identificador del sector/horno al que se encoló la consigna (no es FK: el sector vive en sectores)';
COMMENT ON COLUMN historial_consignas.lote_id IS 'Correlación con el lote productivo, sin FK: en el despacho automático el lote puede no estar persistido aún en lotes_productivos';
COMMENT ON COLUMN historial_consignas.producto_id IS 'FK al producto cuyos parámetros originaron la consigna (nullable: puede no aplicar en consignas manuales sin producto asociado)';
COMMENT ON COLUMN historial_consignas.origen IS 'MANUAL: cargado por un operario desde el panel. AUTOMATICO: sólo lectura de filas históricas (ya no se escribe)';
COMMENT ON COLUMN historial_consignas.usuario IS 'Identificador del operario que disparó una consigna manual';
COMMENT ON COLUMN historial_consignas.exitosa IS 'Compatibilidad: true = aceptada y encolada para la Pi. La fuente fina del ciclo de vida es estado (PENDIENTE/APLICADA/RECHAZADA)';
COMMENT ON COLUMN historial_consignas.motivo_error IS 'Motivo del fallo de encolado cuando exitosa=false';
COMMENT ON COLUMN historial_consignas.temperatura_previa IS 'Temperatura real del lote (metricas_horno) inmediatamente antes de encolar la consigna; NULL si no había telemetría';
COMMENT ON COLUMN historial_consignas.velocidad_cinta_previa IS 'Velocidad de cinta real del lote (metricas_horno) inmediatamente antes de encolar la consigna; NULL si no había telemetría';
COMMENT ON COLUMN historial_consignas.decision_id IS 'Decisión de control encolada para la Pi; permite cerrar el estado con el ack (sin FK)';
COMMENT ON COLUMN historial_consignas.estado IS 'PENDIENTE = encolada; APLICADA = confirmada por ack de la Pi; RECHAZADA = no se pudo encolar';

-- Índices
CREATE INDEX IF NOT EXISTS idx_historial_consignas_decision_id
    ON historial_consignas USING btree (decision_id) WHERE (decision_id IS NOT NULL);
CREATE INDEX IF NOT EXISTS idx_historial_consignas_horno_id
    ON historial_consignas USING btree (horno_id, creada_en DESC);
CREATE INDEX IF NOT EXISTS idx_historial_consignas_lote_id
    ON historial_consignas USING btree (lote_id);

-- ============================================================================
-- TABLA 7: decisiones_control (Audit del MPC)
-- ============================================================================
CREATE TABLE IF NOT EXISTS decisiones_control (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    lote_id uuid NOT NULL,
    ventana_inicio timestamp with time zone NOT NULL,
    ventana_fin timestamp with time zone NOT NULL,
    muestras integer NOT NULL,
    temp_actual numeric(6,2) NOT NULL,
    vel_actual numeric(6,3) NOT NULL,
    pct_crudos_obs numeric(5,2) NOT NULL,
    pct_ok_obs numeric(5,2) NOT NULL,
    pct_quemados_obs numeric(5,2) NOT NULL,
    conf_promedio numeric(4,3) NOT NULL,
    temp_propuesto numeric(6,2) NOT NULL,
    vel_propuesto numeric(6,3) NOT NULL,
    temp_aplicado numeric(6,2),
    vel_aplicado numeric(6,3),
    prediccion jsonb,
    z_observado numeric(10,4),
    z_predicho numeric(10,4),
    razon_skipped text,
    aplicado_por character varying(20),
    decision_origen_id uuid,
    aprobada_por character varying(255),
    aprobada_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT decisiones_control_conf_promedio_check CHECK (((conf_promedio >= (0)::numeric) AND (conf_promedio <= (1)::numeric))),
    CONSTRAINT decisiones_control_muestras_check CHECK ((muestras >= 0)),
    CONSTRAINT decisiones_control_pct_crudos_obs_check CHECK (((pct_crudos_obs >= (0)::numeric) AND (pct_crudos_obs <= (100)::numeric))),
    CONSTRAINT decisiones_control_pct_ok_obs_check CHECK (((pct_ok_obs >= (0)::numeric) AND (pct_ok_obs <= (100)::numeric))),
    CONSTRAINT decisiones_control_pct_quemados_obs_check CHECK (((pct_quemados_obs >= (0)::numeric) AND (pct_quemados_obs <= (100)::numeric))),
    CONSTRAINT decisiones_control_pkey PRIMARY KEY (id),
    CONSTRAINT decisiones_control_lote_id_fkey FOREIGN KEY (lote_id) REFERENCES lotes_productivos(id) ON DELETE CASCADE,
    CONSTRAINT fk_decisiones_control_origen FOREIGN KEY (decision_origen_id) REFERENCES decisiones_control(id) ON DELETE SET NULL
);

COMMENT ON TABLE decisiones_control IS 'Audit del MPC: una fila por decisión propuesta (aplicada o no)';
COMMENT ON COLUMN decisiones_control.ventana_inicio IS 'Inicio de la ventana de evaluación que originó la decisión';
COMMENT ON COLUMN decisiones_control.ventana_fin IS 'Fin de la ventana de evaluación';
COMMENT ON COLUMN decisiones_control.muestras IS 'Cantidad de detecciones en la ventana';
COMMENT ON COLUMN decisiones_control.temp_actual IS 'T1 activa al momento de la decisión';
COMMENT ON COLUMN decisiones_control.vel_actual IS 'Velocidad activa al momento de la decisión';
COMMENT ON COLUMN decisiones_control.pct_crudos_obs IS '% crudo observado en la ventana (crudos/total*100)';
COMMENT ON COLUMN decisiones_control.pct_ok_obs IS '% OK observado en la ventana';
COMMENT ON COLUMN decisiones_control.pct_quemados_obs IS '% quemados observado en la ventana';
COMMENT ON COLUMN decisiones_control.conf_promedio IS 'Confianza promedio del detector en la ventana (0..1)';
COMMENT ON COLUMN decisiones_control.temp_propuesto IS 'T1 que el MPC propuso';
COMMENT ON COLUMN decisiones_control.vel_propuesto IS 'Velocidad que el MPC propuso';
COMMENT ON COLUMN decisiones_control.temp_aplicado IS 'T1 efectivamente aplicada (NULL en modo SUPERVISION si no se aprobó)';
COMMENT ON COLUMN decisiones_control.vel_aplicado IS 'Velocidad efectivamente aplicada (idem)';
COMMENT ON COLUMN decisiones_control.prediccion IS 'Salida cruda del XGBoost (JSONB con pctCrudos/pctOk/pctQuemados/modelVersion)';
COMMENT ON COLUMN decisiones_control.z_observado IS 'Valor de la función objetivo con datos observados';
COMMENT ON COLUMN decisiones_control.z_predicho IS 'Valor de la función objetivo con la predicción del XGBoost';
COMMENT ON COLUMN decisiones_control.razon_skipped IS 'Si no se propuso cambio, motivo (confianza_baja, ventana_sin_muestras, sin_convergencia, etc.)';
COMMENT ON COLUMN decisiones_control.aplicado_por IS 'MPC = aplicado por el control automático; OPERARIO = aprobado manualmente en SUPERVISION; NADIE = no aplicado';
COMMENT ON COLUMN decisiones_control.decision_origen_id IS 'Cuando esta decisión fue promovida (NADIE→OPERARIO o AUTO→...), apunta a la decisión origen. Self-ref cuando se promueve la misma fila.';
COMMENT ON COLUMN decisiones_control.aprobada_por IS 'Usuario que aprobó la propuesta en SUPERVISION (NULL si nadie la aprobó)';
COMMENT ON COLUMN decisiones_control.aprobada_at IS 'Momento en que se aprobó la propuesta en SUPERVISION';

-- Índices
CREATE INDEX IF NOT EXISTS idx_decisiones_control_lote_created
    ON decisiones_control USING btree (lote_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_decisiones_control_origen
    ON decisiones_control USING btree (decision_origen_id) WHERE (decision_origen_id IS NOT NULL);

-- ============================================================================
-- TABLA 8: registration_requests (Registro de dispositivos por solicitud)
-- ============================================================================
CREATE TABLE IF NOT EXISTS registration_requests (
    request_id character varying(64) NOT NULL,
    hostname character varying(100) NOT NULL,
    status character varying(12) DEFAULT 'PENDING'::character varying NOT NULL,
    device_id uuid,
    secret text,
    rename_url text,
    resolved_by uuid,
    resolved_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    tipo character varying(16),
    CONSTRAINT chk_registration_requests_approved CHECK ((((status)::text <> 'APPROVED'::text) OR ((device_id IS NOT NULL) AND (resolved_by IS NOT NULL) AND (resolved_at IS NOT NULL)))),
    CONSTRAINT chk_registration_requests_rejected CHECK ((((status)::text <> 'REJECTED'::text) OR ((resolved_by IS NOT NULL) AND (resolved_at IS NOT NULL)))),
    CONSTRAINT chk_registration_requests_status CHECK (status IN ('PENDING', 'APPROVED', 'REJECTED', 'EXPIRED')),
    CONSTRAINT chk_registration_requests_tipo CHECK (tipo IS NULL OR tipo IN ('ENTRADA_HORNO', 'SALIDA_HORNO')),
    CONSTRAINT registration_requests_pkey PRIMARY KEY (request_id),
    CONSTRAINT registration_requests_device_id_fkey FOREIGN KEY (device_id) REFERENCES dispositivos(id) ON DELETE SET NULL,
    CONSTRAINT registration_requests_resolved_by_fkey FOREIGN KEY (resolved_by) REFERENCES usuarios(id) ON DELETE RESTRICT
);

COMMENT ON TABLE registration_requests IS 'Solicitudes de registro de nodos Raspberry pendientes de aprobación por Supervisor/Admin';
COMMENT ON COLUMN registration_requests.request_id IS 'Identificador de alta capacidad que el nodo usa para consultar su solicitud';
COMMENT ON COLUMN registration_requests.secret IS 'Secret en texto plano retenido sólo hasta la primera consulta del nodo; se anula tras la entrega';
COMMENT ON COLUMN registration_requests.rename_url IS 'URL completa (esquema + host + puerto) anunciada por el nodo para el push de renombre; NULL si el firmware es legacy';
COMMENT ON COLUMN registration_requests.expires_at IS 'Vencimiento de la solicitud; vencida no puede aprobarse ni entregarse';
COMMENT ON COLUMN registration_requests.tipo IS 'Tipo/ubicación funcional que el nodo solicita al registrarse: ENTRADA_HORNO o SALIDA_HORNO (nullable para solicitudes legadas)';

-- Índices
CREATE INDEX IF NOT EXISTS idx_registration_requests_status
    ON registration_requests USING btree (status, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS uq_registration_requests_pending_hostname
    ON registration_requests USING btree (hostname) WHERE ((status)::text = 'PENDING'::text);

-- ============================================================================
-- TABLA 9: device_lifecycle_audit (Auditoría del ciclo de vida de dispositivos)
-- ============================================================================
CREATE TABLE IF NOT EXISTS device_lifecycle_audit (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    actor_id uuid NOT NULL,
    action character varying(20) NOT NULL,
    dispositivo_id uuid,
    enrollment_id character varying(64),
    request_id character varying(64),
    old_status character varying(12) NOT NULL,
    new_status character varying(12) NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT device_lifecycle_audit_pkey PRIMARY KEY (id),
    CONSTRAINT device_lifecycle_audit_actor_id_fkey FOREIGN KEY (actor_id) REFERENCES usuarios(id) ON DELETE RESTRICT,
    CONSTRAINT device_lifecycle_audit_dispositivo_id_fkey FOREIGN KEY (dispositivo_id) REFERENCES dispositivos(id) ON DELETE RESTRICT
);

-- Índices
CREATE INDEX IF NOT EXISTS idx_device_lifecycle_audit_device
    ON device_lifecycle_audit USING btree (dispositivo_id, created_at DESC);

-- ============================================================================
-- TABLA 10: metricas_dispositivo (Historial append-only de telemetría)
-- ============================================================================
CREATE TABLE IF NOT EXISTS metricas_dispositivo (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    dispositivo_id uuid NOT NULL,
    cpu_pct numeric(5,2) NOT NULL,
    mem_ram_disponible_mb numeric(10,2) NOT NULL,
    mem_ram_total_mb numeric(10,2),
    almacenamiento_disponible_mb numeric(12,2),
    almacenamiento_total_mb numeric(12,2),
    temp_chip numeric(6,2) NOT NULL,
    ai_processor_pct numeric(5,2) NOT NULL,
    received_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT chk_metricas_almacenamiento_disponible_no_negativo CHECK (((almacenamiento_disponible_mb IS NULL) OR (almacenamiento_disponible_mb >= (0)::numeric))),
    CONSTRAINT chk_metricas_almacenamiento_disponible_no_supera_total CHECK (((almacenamiento_total_mb IS NULL) OR (almacenamiento_disponible_mb IS NULL) OR (almacenamiento_disponible_mb <= almacenamiento_total_mb))),
    CONSTRAINT chk_metricas_almacenamiento_par CHECK (((almacenamiento_disponible_mb IS NULL) = (almacenamiento_total_mb IS NULL))),
    CONSTRAINT chk_metricas_almacenamiento_total_no_negativo CHECK (((almacenamiento_total_mb IS NULL) OR (almacenamiento_total_mb >= (0)::numeric))),
    CONSTRAINT chk_metricas_mem_ram_disponible_no_supera_total CHECK (((mem_ram_total_mb IS NULL) OR (mem_ram_disponible_mb <= mem_ram_total_mb))),
    CONSTRAINT chk_metricas_mem_ram_total_no_negativo CHECK (((mem_ram_total_mb IS NULL) OR (mem_ram_total_mb >= (0)::numeric))),
    CONSTRAINT metricas_dispositivo_ai_processor_pct_check CHECK (((ai_processor_pct >= (0)::numeric) AND (ai_processor_pct <= (100)::numeric))),
    CONSTRAINT metricas_dispositivo_cpu_pct_check CHECK (((cpu_pct >= (0)::numeric) AND (cpu_pct <= (100)::numeric))),
    CONSTRAINT metricas_dispositivo_mem_ram_disponible_mb_check CHECK ((mem_ram_disponible_mb >= (0)::numeric)),
    CONSTRAINT metricas_dispositivo_pkey PRIMARY KEY (id),
    CONSTRAINT fk_metricas_dispositivo_dispositivo FOREIGN KEY (dispositivo_id) REFERENCES dispositivos(id) ON UPDATE CASCADE ON DELETE CASCADE
);

COMMENT ON TABLE metricas_dispositivo IS 'Historial append-only de telemetría reportada por cada dispositivo (CPU, RAM, almacenamiento y temperatura del chip)';
COMMENT ON COLUMN metricas_dispositivo.id IS 'Identificador UUID del registro de métricas';
COMMENT ON COLUMN metricas_dispositivo.dispositivo_id IS 'FK al catálogo de dispositivos';
COMMENT ON COLUMN metricas_dispositivo.cpu_pct IS 'Uso de CPU en porcentaje (0-100)';
COMMENT ON COLUMN metricas_dispositivo.mem_ram_disponible_mb IS 'Memoria RAM disponible en MB';
COMMENT ON COLUMN metricas_dispositivo.mem_ram_total_mb IS 'Memoria RAM total en MB (nullable para compatibilidad con pings legacy)';
COMMENT ON COLUMN metricas_dispositivo.almacenamiento_disponible_mb IS 'Almacenamiento disponible en MB (nullable para compatibilidad con pings legacy)';
COMMENT ON COLUMN metricas_dispositivo.almacenamiento_total_mb IS 'Almacenamiento total en MB (nullable para compatibilidad con pings legacy)';
COMMENT ON COLUMN metricas_dispositivo.temp_chip IS 'Temperatura interna del chip en °C';
COMMENT ON COLUMN metricas_dispositivo.ai_processor_pct IS 'Uso del procesador de IA (NPU) en porcentaje (0-100)';
COMMENT ON COLUMN metricas_dispositivo.received_at IS 'Marca temporal en que el backend recibió la métrica';

-- Índices
CREATE INDEX IF NOT EXISTS idx_metricas_dispositivo_disp_received
    ON metricas_dispositivo USING btree (dispositivo_id, received_at DESC);

-- ============================================================================
-- TABLA 11: metricas_horno (Telemetría cruda del horno)
-- ============================================================================
CREATE TABLE IF NOT EXISTS metricas_horno (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    lote_id uuid NOT NULL,
    dispositivo_id uuid NOT NULL,
    sector_id character varying(50) NOT NULL,
    temp_horno_1 numeric(6,2),
    temp_comb_1 numeric(6,2),
    humedad_horno numeric(5,2),
    temp_ambiente numeric(5,2),
    humedad_ambiente numeric(5,2),
    velocidad_cinta numeric(6,3),
    temp_objetivo numeric(6,2),
    vel_objetivo numeric(6,3),
    confianza_deteccion numeric(4,3),
    received_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT metricas_horno_confianza_deteccion_check CHECK (((confianza_deteccion IS NULL) OR ((confianza_deteccion >= (0)::numeric) AND (confianza_deteccion <= (1)::numeric)))),
    CONSTRAINT metricas_horno_pkey PRIMARY KEY (id),
    CONSTRAINT metricas_horno_dispositivo_id_fkey FOREIGN KEY (dispositivo_id) REFERENCES dispositivos(id) ON DELETE RESTRICT,
    CONSTRAINT metricas_horno_lote_id_fkey FOREIGN KEY (lote_id) REFERENCES lotes_productivos(id) ON DELETE CASCADE
);

COMMENT ON TABLE metricas_horno IS 'Telemetría cruda del horno (T1/combustión/humedad/ambiente/velocidad) reportada por la Pi ENTRADA';
COMMENT ON COLUMN metricas_horno.temp_horno_1 IS 'Temperatura leída del horno 1 (°C, NULL permitido si el sensor cae)';
COMMENT ON COLUMN metricas_horno.temp_comb_1 IS 'Temperatura de la cámara de combustión del horno 1 (°C, opcional)';
COMMENT ON COLUMN metricas_horno.humedad_horno IS 'Humedad relativa dentro del horno (%, opcional)';
COMMENT ON COLUMN metricas_horno.temp_ambiente IS 'Temperatura ambiente (°C, opcional)';
COMMENT ON COLUMN metricas_horno.humedad_ambiente IS 'Humedad ambiente (%, opcional)';
COMMENT ON COLUMN metricas_horno.velocidad_cinta IS 'Velocidad activa de la cinta (m/s)';
COMMENT ON COLUMN metricas_horno.temp_objetivo IS 'Setpoint activo de T1 al momento de la lectura';
COMMENT ON COLUMN metricas_horno.vel_objetivo IS 'Setpoint activo de velocidad al momento de la lectura';
COMMENT ON COLUMN metricas_horno.confianza_deteccion IS 'Promedio de confianza del modelo de visión en la última ventana (0..1)';
COMMENT ON COLUMN metricas_horno.received_at IS 'Marca temporal en que el backend recibió la métrica';

-- Índices
CREATE INDEX IF NOT EXISTS idx_metricas_horno_lote_received
    ON metricas_horno USING btree (lote_id, received_at DESC);

-- ============================================================================
-- TABLA 12: eventos_lote (Detalle append-only de detecciones por lote)
-- ============================================================================
CREATE TABLE IF NOT EXISTS eventos_lote (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    lote_id uuid NOT NULL,
    evento_id uuid NOT NULL,
    producto_id uuid NOT NULL,
    estado character varying(10),
    confianza double precision,
    pista integer,
    frame bigint,
    modelo_id character varying(100),
    dispositivo_id uuid,
    momento timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT chk_eventos_lote_confianza CHECK (((confianza IS NULL) OR ((confianza >= (0)::double precision) AND (confianza <= (1)::double precision)))),
    CONSTRAINT chk_eventos_lote_estado CHECK (estado IS NULL OR estado IN ('ok', 'crudo', 'quemado')),
    CONSTRAINT eventos_lote_evento_id_key UNIQUE (evento_id),
    CONSTRAINT eventos_lote_pkey PRIMARY KEY (id),
    CONSTRAINT eventos_lote_dispositivo_id_fkey FOREIGN KEY (dispositivo_id) REFERENCES dispositivos(id) ON DELETE RESTRICT,
    CONSTRAINT eventos_lote_lote_id_fkey FOREIGN KEY (lote_id) REFERENCES lotes_productivos(id) ON DELETE CASCADE,
    CONSTRAINT eventos_lote_producto_id_fkey FOREIGN KEY (producto_id) REFERENCES productos(id) ON DELETE RESTRICT
);

COMMENT ON TABLE eventos_lote IS 'Detalle append-only de detecciones reportadas por la salida; alimenta los conteos en vivo del lote.';
COMMENT ON COLUMN eventos_lote.id IS 'Identificador UUID del registro de evento';
COMMENT ON COLUMN eventos_lote.lote_id IS 'FK al lote productivo; se borra en cascada con el lote';
COMMENT ON COLUMN eventos_lote.evento_id IS 'UUIDv4 generado por la Pi y reutilizado en cada reintento: clave de deduplicación';
COMMENT ON COLUMN eventos_lote.producto_id IS 'FK al producto detectado (debe coincidir con el producto del lote)';
COMMENT ON COLUMN eventos_lote.estado IS 'Estado de calidad: ok, crudo o quemado (nullable si el modelo no lo determina)';
COMMENT ON COLUMN eventos_lote.confianza IS 'Confianza de la detección entre 0 y 1 (nullable)';
COMMENT ON COLUMN eventos_lote.pista IS 'Número de pista/banda de la detección (nullable)';
COMMENT ON COLUMN eventos_lote.frame IS 'Número de frame del video de origen (nullable)';
COMMENT ON COLUMN eventos_lote.modelo_id IS 'Identificador del modelo de inferencia que produjo la detección (nullable)';
COMMENT ON COLUMN eventos_lote.dispositivo_id IS 'FK al dispositivo que reportó el evento (nullable)';
COMMENT ON COLUMN eventos_lote.momento IS 'Marca temporal informativa del momento de la detección según la Pi (nullable)';
COMMENT ON COLUMN eventos_lote.created_at IS 'Marca temporal en que el backend recibió el evento';

-- Índices
CREATE INDEX IF NOT EXISTS idx_eventos_lote_lote_created
    ON eventos_lote USING btree (lote_id, created_at);

-- ============================================================================
-- TABLA 13: plan_produccion (Plan de producción por sector y turno)
-- ============================================================================
CREATE TABLE IF NOT EXISTS plan_produccion (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    sector_id character varying(50) NOT NULL,
    producto_id uuid NOT NULL,
    fecha date NOT NULL,
    turno character varying(10) NOT NULL,
    inicio_at timestamp with time zone NOT NULL,
    fin_at timestamp with time zone NOT NULL,
    kg_plan numeric(10,3) NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT chk_plan_fin_after_inicio CHECK ((fin_at > inicio_at)),
    CONSTRAINT chk_plan_kg_positivo CHECK ((kg_plan > (0)::numeric)),
    CONSTRAINT chk_plan_turno CHECK (turno IN ('mañana', 'tarde', 'noche')),
    CONSTRAINT plan_produccion_pkey PRIMARY KEY (id),
    CONSTRAINT uq_plan_produccion_sector_inicio_producto UNIQUE (sector_id, inicio_at, producto_id),
    CONSTRAINT plan_produccion_producto_id_fkey FOREIGN KEY (producto_id) REFERENCES productos(id) ON DELETE RESTRICT,
    CONSTRAINT plan_produccion_sector_id_fkey FOREIGN KEY (sector_id) REFERENCES sectores(id) ON DELETE RESTRICT
);

COMMENT ON TABLE plan_produccion IS 'Plan de producción por sector y turno; una fila por producto y ventana planificada.';
COMMENT ON COLUMN plan_produccion.id IS 'Identificador UUID del plan de producción';
COMMENT ON COLUMN plan_produccion.sector_id IS 'FK al sector de producción';
COMMENT ON COLUMN plan_produccion.producto_id IS 'FK al producto planificado';
COMMENT ON COLUMN plan_produccion.fecha IS 'Fecha del plan (calendario de Argentina)';
COMMENT ON COLUMN plan_produccion.turno IS 'Turno planificado: mañana, tarde o noche';
COMMENT ON COLUMN plan_produccion.inicio_at IS 'Inicio de la ventana planificada';
COMMENT ON COLUMN plan_produccion.fin_at IS 'Fin de la ventana planificada, posterior al inicio';
COMMENT ON COLUMN plan_produccion.kg_plan IS 'Kilogramos planificados para la ventana; debe ser mayor a 0';
COMMENT ON COLUMN plan_produccion.created_at IS 'Marca temporal de alta del registro';
COMMENT ON COLUMN plan_produccion.updated_at IS 'Marca temporal de la última modificación del registro';

-- Índices
CREATE INDEX IF NOT EXISTS idx_plan_produccion_sector_inicio
    ON plan_produccion USING btree (sector_id, inicio_at);

-- ============================================================================
-- TABLA 14: eventos_parada (Eventos de parada de producción / OEE)
-- ============================================================================
CREATE TABLE IF NOT EXISTS eventos_parada (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    sector_id character varying(50) NOT NULL,
    lote_id uuid,
    inicio_at timestamp with time zone NOT NULL,
    fin_at timestamp with time zone NOT NULL,
    causa_codigo text NOT NULL,
    tipo character varying(20) NOT NULL,
    notas text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT chk_eventos_parada_fin_after_inicio CHECK ((fin_at > inicio_at)),
    CONSTRAINT chk_eventos_parada_tipo CHECK (tipo IN ('PLANIFICADA', 'NO_PLANIFICADA')),
    CONSTRAINT eventos_parada_pkey PRIMARY KEY (id),
    CONSTRAINT uq_eventos_parada_sector_inicio_causa UNIQUE (sector_id, inicio_at, causa_codigo),
    CONSTRAINT eventos_parada_lote_id_fkey FOREIGN KEY (lote_id) REFERENCES lotes_productivos(id) ON DELETE SET NULL,
    CONSTRAINT eventos_parada_sector_id_fkey FOREIGN KEY (sector_id) REFERENCES sectores(id) ON DELETE RESTRICT
);

COMMENT ON TABLE eventos_parada IS 'Eventos de parada de producción que explican el tiempo no productivo (OEE).';
COMMENT ON COLUMN eventos_parada.id IS 'Identificador UUID del evento de parada';
COMMENT ON COLUMN eventos_parada.sector_id IS 'FK al sector afectado';
COMMENT ON COLUMN eventos_parada.lote_id IS 'FK opcional al lote productivo afectado; SET NULL si se elimina el lote';
COMMENT ON COLUMN eventos_parada.inicio_at IS 'Inicio de la parada';
COMMENT ON COLUMN eventos_parada.fin_at IS 'Fin de la parada, posterior al inicio';
COMMENT ON COLUMN eventos_parada.causa_codigo IS 'Código de causa de la parada (texto libre normalizado por el importador)';
COMMENT ON COLUMN eventos_parada.tipo IS 'Tipo de parada: PLANIFICADA o NO_PLANIFICADA';
COMMENT ON COLUMN eventos_parada.notas IS 'Observaciones libres de la parada (nullable)';
COMMENT ON COLUMN eventos_parada.created_at IS 'Marca temporal de alta del registro';

-- Índices
CREATE INDEX IF NOT EXISTS idx_eventos_parada_sector_inicio
    ON eventos_parada USING btree (sector_id, inicio_at);

-- ============================================================================
-- TABLA 15: alertas (Alertas operativas del panel)
-- ============================================================================
CREATE TABLE IF NOT EXISTS alertas (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    sector_id character varying(50),
    lote_id uuid,
    dispositivo_id uuid,
    horno_id text,
    tipo text NOT NULL,
    severidad text NOT NULL,
    estado text DEFAULT 'ACTIVA'::text NOT NULL,
    mensaje text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    acked_by uuid,
    acked_at timestamp with time zone,
    CONSTRAINT alertas_estado_check CHECK ((estado = ANY (ARRAY['ACTIVA'::text, 'ACK'::text, 'RESUELTA'::text]))),
    CONSTRAINT alertas_severidad_check CHECK ((severidad = ANY (ARRAY['INFO'::text, 'WARNING'::text, 'CRITICAL'::text]))),
    CONSTRAINT alertas_pkey PRIMARY KEY (id),
    CONSTRAINT alertas_acked_by_fkey FOREIGN KEY (acked_by) REFERENCES usuarios(id) ON DELETE SET NULL,
    CONSTRAINT alertas_lote_id_fkey FOREIGN KEY (lote_id) REFERENCES lotes_productivos(id) ON DELETE SET NULL,
    CONSTRAINT alertas_dispositivo_id_fkey FOREIGN KEY (dispositivo_id) REFERENCES dispositivos(id) ON DELETE SET NULL,
    CONSTRAINT alertas_sector_id_fkey FOREIGN KEY (sector_id) REFERENCES sectores(id) ON DELETE SET NULL
);

COMMENT ON TABLE alertas IS 'Alertas operativas (temperatura, timeout de precalentamiento, calidad del lote, dispositivo offline, telemetría desactualizada) visibles en el panel; el operario las reconoce (ACK).';
COMMENT ON COLUMN alertas.sector_id IS 'Sector afectado (nullable; SET NULL si se elimina el sector)';
COMMENT ON COLUMN alertas.lote_id IS 'Lote productivo afectado (nullable; SET NULL si se elimina el lote)';
-- dispositivo_id, su comentario y su índice se agregan en
-- database/migrations/0005_alertas_dispositivo.sql para no romper bases
-- existentes (schema.sql se aplica antes que las migraciones).
COMMENT ON COLUMN alertas.horno_id IS 'Identificador del horno físico (no es FK: el dominio horno vive en memoria)';
COMMENT ON COLUMN alertas.tipo IS 'Origen: TEMPERATURA_HORNO, PRECALENTAMIENTO_TIMEOUT, PORCENTAJE_QUEMADOS_ALTO, PORCENTAJE_CRUDOS_ALTO, DISPOSITIVO_OFFLINE o TELEMETRIA_DESACTUALIZADA';
COMMENT ON COLUMN alertas.severidad IS 'Gravedad: INFO, WARNING o CRITICAL';
COMMENT ON COLUMN alertas.estado IS 'Ciclo de vida: ACTIVA (sin reconocer), ACK (reconocida por un operario) o RESUELTA';
COMMENT ON COLUMN alertas.mensaje IS 'Texto legible de la alerta para el panel';
COMMENT ON COLUMN alertas.created_at IS 'Marca temporal de generación de la alerta';
COMMENT ON COLUMN alertas.acked_by IS 'FK al usuario que reconoció la alerta (nullable mientras está ACTIVA)';
COMMENT ON COLUMN alertas.acked_at IS 'Marca temporal del reconocimiento (nullable mientras está ACTIVA)';

-- Índices
CREATE INDEX IF NOT EXISTS idx_alertas_estado_created
    ON alertas USING btree (estado, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS uq_alertas_activa_horno_tipo
    ON alertas USING btree (horno_id, tipo) WHERE ((estado = 'ACTIVA'::text) AND (horno_id IS NOT NULL));
CREATE UNIQUE INDEX IF NOT EXISTS uq_alertas_activa_lote_tipo
    ON alertas USING btree (lote_id, tipo) WHERE ((estado = 'ACTIVA'::text) AND (lote_id IS NOT NULL));


-- ============================================================================
-- TABLA 16: usuarios_notificaciones (Preferencias de notificación por usuario)
-- ============================================================================
CREATE TABLE IF NOT EXISTS usuarios_notificaciones (
    usuario_id uuid NOT NULL,
    telegram_chat_id text,
    notificar_email boolean NOT NULL DEFAULT true,
    notificar_telegram boolean NOT NULL DEFAULT true,
    updated_at timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT usuarios_notificaciones_pkey PRIMARY KEY (usuario_id),
    CONSTRAINT usuarios_notificaciones_usuario_id_fkey FOREIGN KEY (usuario_id) REFERENCES usuarios(id) ON DELETE CASCADE
);

COMMENT ON TABLE usuarios_notificaciones IS 'Preferencias de notificación por usuario; la ausencia de fila equivale a ambos canales habilitados.';
COMMENT ON COLUMN usuarios_notificaciones.usuario_id IS 'FK al usuario; una fila de preferencias por usuario';
COMMENT ON COLUMN usuarios_notificaciones.telegram_chat_id IS 'Chat ID de Telegram del usuario (nullable; sin chat no se envía por Telegram)';
COMMENT ON COLUMN usuarios_notificaciones.notificar_email IS 'Si true, el usuario recibe alertas WARNING/CRITICAL por email';
COMMENT ON COLUMN usuarios_notificaciones.notificar_telegram IS 'Si true, el usuario recibe alertas WARNING/CRITICAL por Telegram';
COMMENT ON COLUMN usuarios_notificaciones.updated_at IS 'Marca temporal de la última modificación de las preferencias';

-- ============================================================================
-- TABLA 17: notificaciones_outbox (Outbox persistente de notificaciones)
-- ============================================================================
CREATE TABLE IF NOT EXISTS notificaciones_outbox (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    alerta_id uuid NOT NULL,
    usuario_id uuid NOT NULL,
    canal text NOT NULL,
    destino text NOT NULL,
    asunto text NOT NULL,
    cuerpo text NOT NULL,
    cuerpo_html text NOT NULL DEFAULT '',
    estado text NOT NULL DEFAULT 'PENDIENTE'::text,
    intentos integer NOT NULL DEFAULT 0,
    proximo_intento_at timestamp with time zone NOT NULL DEFAULT now(),
    ultimo_error text,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    sent_at timestamp with time zone,
    CONSTRAINT notificaciones_outbox_pkey PRIMARY KEY (id),
    CONSTRAINT notificaciones_outbox_canal_check CHECK (canal IN ('EMAIL'::text, 'TELEGRAM'::text)),
    CONSTRAINT notificaciones_outbox_estado_check CHECK (estado IN ('PENDIENTE'::text, 'ENVIADO'::text, 'FALLIDO'::text)),
    CONSTRAINT notificaciones_outbox_alerta_id_fkey FOREIGN KEY (alerta_id) REFERENCES alertas(id) ON DELETE CASCADE,
    CONSTRAINT notificaciones_outbox_usuario_id_fkey FOREIGN KEY (usuario_id) REFERENCES usuarios(id) ON DELETE CASCADE
);

COMMENT ON TABLE notificaciones_outbox IS 'Outbox persistente de notificaciones por alerta y usuario; el worker reintenta con backoff.';
COMMENT ON COLUMN notificaciones_outbox.id IS 'Identificador UUID de la notificación';
COMMENT ON COLUMN notificaciones_outbox.alerta_id IS 'FK a la alerta que originó la notificación';
COMMENT ON COLUMN notificaciones_outbox.usuario_id IS 'FK al usuario destinatario';
COMMENT ON COLUMN notificaciones_outbox.canal IS 'Canal de entrega: EMAIL o TELEGRAM';
COMMENT ON COLUMN notificaciones_outbox.destino IS 'Email o chat ID de Telegram según el canal';
COMMENT ON COLUMN notificaciones_outbox.asunto IS 'Asunto del email (para Telegram se ignora)';
COMMENT ON COLUMN notificaciones_outbox.cuerpo IS 'Cuerpo del mensaje listo para enviar';
COMMENT ON COLUMN notificaciones_outbox.estado IS 'Ciclo de vida: PENDIENTE, ENVIADO o FALLIDO';
COMMENT ON COLUMN notificaciones_outbox.intentos IS 'Cantidad de intentos de envío fallidos';
COMMENT ON COLUMN notificaciones_outbox.proximo_intento_at IS 'Instante a partir del cual el worker puede reintentar';
COMMENT ON COLUMN notificaciones_outbox.ultimo_error IS 'Último error de envío (nullable; nunca contiene secretos)';
COMMENT ON COLUMN notificaciones_outbox.created_at IS 'Marca temporal de encolado';
COMMENT ON COLUMN notificaciones_outbox.sent_at IS 'Marca temporal del envío exitoso (nullable)';

-- Índices
CREATE UNIQUE INDEX IF NOT EXISTS uq_notificaciones_outbox_alerta_usuario_canal
    ON notificaciones_outbox USING btree (alerta_id, usuario_id, canal);
CREATE INDEX IF NOT EXISTS idx_notificaciones_outbox_pendientes
    ON notificaciones_outbox USING btree (proximo_intento_at)
    WHERE estado = 'PENDIENTE'::text;

COMMIT;
