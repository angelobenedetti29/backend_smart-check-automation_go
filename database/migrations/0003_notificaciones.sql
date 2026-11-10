-- 0003_notificaciones.sql
-- Preferencias de notificación por usuario y outbox persistente de envíos.
-- Idempotente: seguro sobre instalaciones limpias (schema.sql ya creó las
-- tablas) y sobre bases existentes.

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

CREATE TABLE IF NOT EXISTS notificaciones_outbox (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    alerta_id uuid NOT NULL,
    usuario_id uuid NOT NULL,
    canal text NOT NULL,
    destino text NOT NULL,
    asunto text NOT NULL,
    cuerpo text NOT NULL,
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

CREATE UNIQUE INDEX IF NOT EXISTS uq_notificaciones_outbox_alerta_usuario_canal
    ON notificaciones_outbox USING btree (alerta_id, usuario_id, canal);
CREATE INDEX IF NOT EXISTS idx_notificaciones_outbox_pendientes
    ON notificaciones_outbox USING btree (proximo_intento_at)
    WHERE estado = 'PENDIENTE'::text;
