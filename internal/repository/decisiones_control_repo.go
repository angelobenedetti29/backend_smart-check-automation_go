package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/decisiones_control"
)

// DecisionesControlPostgresRepository implementa decisiones_control.Repository.
type DecisionesControlPostgresRepository struct {
	db *pgxpool.Pool
}

// NewDecisionesControlPostgresRepository crea un repositorio real conectado a PostgreSQL.
func NewDecisionesControlPostgresRepository(db *pgxpool.Pool) *DecisionesControlPostgresRepository {
	return &DecisionesControlPostgresRepository{db: db}
}

const insertDecisionSQL = `
INSERT INTO decisiones_control (
	lote_id, ventana_inicio, ventana_fin, muestras,
	temp_actual, vel_actual,
	pct_crudos_obs, pct_ok_obs, pct_quemados_obs,
	conf_promedio,
	temp_propuesto, vel_propuesto,
	temp_aplicado, vel_aplicado,
	prediccion, z_observado, z_predicho,
	razon_skipped, aplicado_por
) VALUES (
	$1, $2, $3, $4,
	$5, $6,
	$7, $8, $9,
	$10,
	$11, $12,
	$13, $14,
	$15, $16, $17,
	$18, $19
)
RETURNING id, created_at`

// Save persiste una decisión del MPC y completa ID/CreatedAt con el servidor.
func (r *DecisionesControlPostgresRepository) Save(ctx context.Context, d *decisiones_control.DecisionControl) error {
	prediccion := d.Prediccion
	if len(prediccion) == 0 {
		prediccion = []byte("{}")
	}
	err := r.db.QueryRow(ctx, insertDecisionSQL,
		d.LoteID, d.VentanaInicio, d.VentanaFin, d.Muestras,
		d.TempActual, d.VelActual,
		d.PctCrudosObs, d.PctOkObs, d.PctQuemadosObs,
		d.ConfPromedio,
		d.TempPropuesto, d.VelPropuesto,
		d.TempAplicado, d.VelAplicado,
		prediccion, d.ZObservado, d.ZPredicho,
		d.RazonSkipped, d.AplicadoPor,
	).Scan(&d.ID, &d.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert decisiones_control: %w", err)
	}
	return nil
}

const selectDecisionesColumns = `
id, lote_id, ventana_inicio, ventana_fin, muestras,
temp_actual, vel_actual,
pct_crudos_obs, pct_ok_obs, pct_quemados_obs, conf_promedio,
temp_propuesto, vel_propuesto,
temp_aplicado, vel_aplicado,
prediccion, z_observado, z_predicho,
razon_skipped, aplicado_por, created_at`

// ListByLote devuelve las últimas N decisiones del lote, ordenadas por
// created_at DESC. Un lote sin decisiones devuelve slice vacío.
//
// Adicionalmente decodifica `prediccion` (JSONB crudo persistido por el
// Loop) en `PrediccionView`, que es el subset que viaja al cliente.
func (r *DecisionesControlPostgresRepository) ListByLote(ctx context.Context, loteID string, limite int) ([]decisiones_control.DecisionControl, error) {
	if limite <= 0 {
		limite = 50
	}
	rows, err := r.db.Query(ctx, `
		SELECT `+selectDecisionesColumns+`
		FROM decisiones_control
		WHERE lote_id = $1
		ORDER BY created_at DESC
		LIMIT $2`, loteID, limite)
	if err != nil {
		return nil, fmt.Errorf("query decisiones_control by lote: %w", err)
	}
	defer rows.Close()

	out := []decisiones_control.DecisionControl{}
	for rows.Next() {
		var d decisiones_control.DecisionControl
		if err := rows.Scan(
			&d.ID, &d.LoteID, &d.VentanaInicio, &d.VentanaFin, &d.Muestras,
			&d.TempActual, &d.VelActual,
			&d.PctCrudosObs, &d.PctOkObs, &d.PctQuemadosObs, &d.ConfPromedio,
			&d.TempPropuesto, &d.VelPropuesto,
			&d.TempAplicado, &d.VelAplicado,
			&d.Prediccion, &d.ZObservado, &d.ZPredicho,
			&d.RazonSkipped, &d.AplicadoPor, &d.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan decisiones_control: %w", err)
		}
		if len(d.Prediccion) > 0 {
			var view decisiones_control.PrediccionResumen
			if err := json.Unmarshal(d.Prediccion, &view); err == nil {
				d.PrediccionView = &view
			}
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration decisiones_control: %w", err)
	}
	return out, nil
}