package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
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
	razon_skipped, aplicado_por, decision_origen_id
) VALUES (
	$1, $2, $3, $4,
	$5, $6,
	$7, $8, $9,
	$10,
	$11, $12,
	$13, $14,
	$15, $16, $17,
	$18, $19, $20
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
		d.RazonSkipped, d.AplicadoPor, d.DecisionOrigenID,
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
	razon_skipped, aplicado_por, decision_origen_id, aprobada_por, aprobada_at, created_at`

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
		if err := scanDecision(rows, &d); err != nil {
			return nil, fmt.Errorf("scan decisiones_control: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration decisiones_control: %w", err)
	}
	return out, nil
}

const selectUltimaPendienteSQL = `
	SELECT ` + selectDecisionesColumns + `
	FROM decisiones_control
	WHERE lote_id = $1 AND aplicado_por = $2 AND razon_skipped IS NULL
	ORDER BY created_at DESC
	LIMIT 1`

// GetUltimaPendiente devuelve la propuesta de SUPERVISION más reciente que
// todavía no fue aplicada (aplicado_por='NADIE' y sin razon_skipped). Si el
// lote no tiene ninguna pendiente, devuelve (nil, nil) sin error.
func (r *DecisionesControlPostgresRepository) GetUltimaPendiente(ctx context.Context, loteID string) (*decisiones_control.DecisionControl, error) {
	row := r.db.QueryRow(ctx, selectUltimaPendienteSQL, loteID, decisiones_control.AplicadoPorNadie)
	var d decisiones_control.DecisionControl
	if err := scanDecision(row, &d); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("query ultima decision pendiente: %w", err)
	}
	return &d, nil
}

// GetByID devuelve una decisión por id, o (nil, nil) si no existe.
func (r *DecisionesControlPostgresRepository) GetByID(ctx context.Context, decisionID string) (*decisiones_control.DecisionControl, error) {
	row := r.db.QueryRow(ctx, `
		SELECT `+selectDecisionesColumns+`
		FROM decisiones_control
		WHERE id = $1`, decisionID)
	var d decisiones_control.DecisionControl
	if err := scanDecision(row, &d); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("query decision por id: %w", err)
	}
	return &d, nil
}

const updateAprobarSQL = `
	UPDATE decisiones_control
	SET aprobada_por = $2, aprobada_at = now()
	WHERE id = $1 AND aplicado_por = 'NADIE' AND aprobada_por IS NULL`

// Aprobar registra la aprobación de una propuesta pendiente. Idempotente:
// sólo toca filas aún no aplicadas ni aprobadas; si no afectó ninguna fila
// devuelve ErrSinPendiente.
func (r *DecisionesControlPostgresRepository) Aprobar(ctx context.Context, decisionID, usuario string) error {
	tag, err := r.db.Exec(ctx, updateAprobarSQL, decisionID, usuario)
	if err != nil {
		return fmt.Errorf("update decision aprobada: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return decisiones_control.ErrSinPendiente
	}
	return nil
}

const updateMarcarAplicadaSQL = `
	UPDATE decisiones_control
	SET aplicado_por = $1, temp_aplicado = $2, vel_aplicado = $3, decision_origen_id = $4
	WHERE id = $5`

// MarcarAplicada marca una decisión como aplicada con los setpoints
// efectivamente despachados. Setea decision_origen_id = id (self-ref): en el
// flujo actual la fila promovida es su propio origen. Idempotente.
func (r *DecisionesControlPostgresRepository) MarcarAplicada(ctx context.Context, decisionID string, tempAplicada, velAplicada float64, aplicadoPor string) error {
	tag, err := r.db.Exec(ctx, updateMarcarAplicadaSQL, aplicadoPor, tempAplicada, velAplicada, decisionID, decisionID)
	if err != nil {
		return fmt.Errorf("update decision aplicada: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("MarcarAplicada: decisión %s no encontrada", decisionID)
	}
	return nil
}

// rowScanner abstrae el Scan común a pgx.Row y pgx.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanDecision escanea las columnas canónicas de decisiones_control y decodifica
// el JSONB de prediccion en PrediccionView.
func scanDecision(s rowScanner, d *decisiones_control.DecisionControl) error {
	if err := s.Scan(
		&d.ID, &d.LoteID, &d.VentanaInicio, &d.VentanaFin, &d.Muestras,
		&d.TempActual, &d.VelActual,
		&d.PctCrudosObs, &d.PctOkObs, &d.PctQuemadosObs, &d.ConfPromedio,
		&d.TempPropuesto, &d.VelPropuesto,
		&d.TempAplicado, &d.VelAplicado,
		&d.Prediccion, &d.ZObservado, &d.ZPredicho,
		&d.RazonSkipped, &d.AplicadoPor, &d.DecisionOrigenID, &d.AprobadaPor, &d.AprobadaAt, &d.CreatedAt,
	); err != nil {
		return err
	}
	if len(d.Prediccion) > 0 {
		var view decisiones_control.PrediccionResumen
		if err := json.Unmarshal(d.Prediccion, &view); err == nil {
			d.PrediccionView = &view
		}
	}
	return nil
}
