package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/metricashorno"
)

// MetricasHornoPostgresRepository implementa metricashorno.Repository sobre la
// tabla metricas_horno (append-only, una fila por heartbeat de la Pi ENTRADA).
type MetricasHornoPostgresRepository struct {
	db *pgxpool.Pool
}

// NewMetricasHornoPostgresRepository crea un repositorio real conectado a PostgreSQL.
func NewMetricasHornoPostgresRepository(db *pgxpool.Pool) *MetricasHornoPostgresRepository {
	return &MetricasHornoPostgresRepository{db: db}
}

const insertMetricaHornoSQL = `
INSERT INTO metricas_horno (
	lote_id, dispositivo_id, sector_id,
	temp_horno_1, temp_horno_2,
	temp_comb_1, temp_comb_2,
	humedad_horno, temp_ambiente, humedad_ambiente,
	velocidad_cinta, temp_objetivo, vel_objetivo,
	confianza_deteccion, received_at
) VALUES (
	$1, $2, $3,
	$4, $5,
	$6, $7,
	$8, $9, $10,
	$11, $12, $13,
	$14, COALESCE($15, now())
)
RETURNING id, received_at`

// Save persiste una métrica. El servidor completa received_at si el cliente
// no lo mandó (clock local de la Pi poco confiable en LAN industrial).
func (r *MetricasHornoPostgresRepository) Save(ctx context.Context, m *metricashorno.MetricaHorno) error {
	err := r.db.QueryRow(ctx, insertMetricaHornoSQL,
		m.LoteID, m.DispositivoID, m.SectorID,
		m.TempHorno1, m.TempHorno2,
		m.TempComb1, m.TempComb2,
		m.HumedadHorno, m.TempAmbiente, m.HumedadAmbiente,
		m.VelocidadCinta, m.TempObjetivo, m.VelObjetivo,
		m.ConfianzaDeteccion, m.ReceivedAt,
	).Scan(&m.ID, &m.ReceivedAt)
	if err != nil {
		// 23503 (foreign_key_violation) si lote_id o dispositivo_id no existen.
		// Se traduce a ErrLoteNoAbierto como mensaje de negocio neutro; el
		// handler lo mapea a 404.
		if isInvalidTextRepresentation(err) {
			return metricashorno.ErrLoteNoAbierto
		}
		return fmt.Errorf("insert metricas_horno: %w", err)
	}
	return nil
}

const selectMetricasHornoColumns = `
id, lote_id, dispositivo_id, sector_id,
temp_horno_1, temp_horno_2,
temp_comb_1, temp_comb_2,
humedad_horno, temp_ambiente, humedad_ambiente,
velocidad_cinta, temp_objetivo, vel_objetivo,
confianza_deteccion, received_at`

// GetUltimasParaLote devuelve las últimas N métricas, de la más reciente a
// la más antigua. Un lote sin telemetría devuelve slice vacío (no error).
func (r *MetricasHornoPostgresRepository) GetUltimasParaLote(ctx context.Context, loteID string, limite int) ([]metricashorno.MetricaHorno, error) {
	if limite <= 0 {
		limite = 100
	}
	rows, err := r.db.Query(ctx, `
		SELECT `+selectMetricasHornoColumns+`
		FROM metricas_horno
		WHERE lote_id = $1
		ORDER BY received_at DESC
		LIMIT $2`, loteID, limite)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return []metricashorno.MetricaHorno{}, nil
		}
		return nil, fmt.Errorf("query metricas_horno by lote: %w", err)
	}
	defer rows.Close()

	out := []metricashorno.MetricaHorno{}
	for rows.Next() {
		var m metricashorno.MetricaHorno
		if err := rows.Scan(
			&m.ID, &m.LoteID, &m.DispositivoID, &m.SectorID,
			&m.TempHorno1, &m.TempHorno2,
			&m.TempComb1, &m.TempComb2,
			&m.HumedadHorno, &m.TempAmbiente, &m.HumedadAmbiente,
			&m.VelocidadCinta, &m.TempObjetivo, &m.VelObjetivo,
			&m.ConfianzaDeteccion, &m.ReceivedAt,
		); err != nil {
			return nil, fmt.Errorf("scan metricas_horno: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration metricas_horno: %w", err)
	}
	return out, nil
}