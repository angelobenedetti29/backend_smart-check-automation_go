-- 0006_notificaciones_html.sql
-- Cuerpo HTML opcional para emails del outbox. Idempotente: seguro sobre
-- instalaciones limpias (schema.sql ya creó la columna) y bases existentes.

ALTER TABLE notificaciones_outbox ADD COLUMN IF NOT EXISTS cuerpo_html text NOT NULL DEFAULT '';

COMMENT ON COLUMN notificaciones_outbox.cuerpo_html IS 'Cuerpo HTML del email (vacío = enviar sólo texto plano)';
