-- 0002_legacy_ubicacion.sql
-- Migración recuperada de ab59e9c: ubicacion (legacy) -> sectores.
-- Idempotente: sólo actúa si dispositivos.ubicacion todavía existe.

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
