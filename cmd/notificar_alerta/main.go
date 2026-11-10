// Comando notificar_alerta encola una alerta existente a destinatarios
// concretos. NO envía directo: inserta filas EMAIL en notificaciones_outbox y
// el worker del backend las envía con el proveedor configurado.
//
// Uso:
//
//	go run ./cmd/notificar_alerta -emails "a@x.com,b@y.com"
//	go run ./cmd/notificar_alerta -alerta-id <uuid> -emails "a@x.com" -panel-url "https://panel.example"
//
// Sin -alerta-id se toma la última alerta ACTIVA PORCENTAJE_QUEMADOS_ALTO.
// El encolado es idempotente por el unique (alerta_id, usuario_id, canal).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/alerta"
	domainnotif "github.com/angelobenedetti29/smart-check-automation/internal/domain/notificacion"
	"github.com/angelobenedetti29/smart-check-automation/internal/repository"
	notifsvc "github.com/angelobenedetti29/smart-check-automation/internal/service/notificacion"
)

func main() {
	alertaID := flag.String("alerta-id", "", "UUID de la alerta (vacío = última ACTIVA PORCENTAJE_QUEMADOS_ALTO)")
	emails := flag.String("emails", "", "Emails destino separados por coma (obligatorio)")
	panelURL := flag.String("panel-url", "", "URL base del panel (default NOTIFICACIONES_PANEL_URL)")
	flag.Parse()

	_ = godotenv.Load()
	if *panelURL == "" {
		*panelURL = os.Getenv("NOTIFICACIONES_PANEL_URL")
	}
	destinos := parseEmails(*emails)
	if len(destinos) == 0 {
		log.Fatal("notificar_alerta: -emails es obligatorio")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("notificar_alerta: DATABASE_URL no configurada")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("notificar_alerta: conectar DB: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("notificar_alerta: ping DB: %v", err)
	}

	a, err := cargarAlerta(ctx, pool, *alertaID)
	if err != nil {
		log.Fatalf("notificar_alerta: cargar alerta: %v", err)
	}

	usuarios, err := usuariosActivos(ctx, pool, destinos)
	if err != nil {
		log.Fatalf("notificar_alerta: buscar usuarios: %v", err)
	}
	if len(usuarios) == 0 {
		log.Fatalf("notificar_alerta: ningún usuario activo coincide con %s", strings.Join(destinos, ", "))
	}

	asunto, texto, htmlBody := notifsvc.RenderizarAlerta(a, *panelURL)
	items := make([]domainnotif.Notificacion, 0, len(usuarios))
	for _, u := range usuarios {
		items = append(items, domainnotif.Notificacion{
			AlertaID:   a.ID,
			UsuarioID:  u.ID,
			Canal:      domainnotif.CanalEmail,
			Destino:    u.Email,
			Asunto:     asunto,
			Cuerpo:     texto,
			CuerpoHTML: htmlBody,
		})
	}

	repo := repository.NewNotificacionPostgresRepository(pool)
	if err := repo.Encolar(ctx, items); err != nil {
		log.Fatalf("notificar_alerta: encolar: %v", err)
	}

	fmt.Printf("Alerta %s\n", a.ID)
	fmt.Printf("  tipo=%s severidad=%s estado=%s\n", a.Tipo, a.Severidad, a.Estado)
	fmt.Printf("  mensaje=%s\n", a.Mensaje)
	fmt.Printf("Destinatarios activos: %s\n", strings.Join(destinos, ", "))
	for _, u := range usuarios {
		fmt.Printf("  EMAIL -> %s (usuario %s)\n", u.Email, u.ID)
	}
	fmt.Println("El worker del backend envía las filas PENDIENTE; el unique evita duplicados.")
}

type usuarioEmail struct {
	ID    string
	Email string
}

func parseEmails(raw string) []string {
	var out []string
	for _, e := range strings.Split(raw, ",") {
		if e = strings.TrimSpace(e); e != "" {
			out = append(out, e)
		}
	}
	return out
}

func cargarAlerta(ctx context.Context, pool *pgxpool.Pool, alertaID string) (*alerta.Alerta, error) {
	const columnas = `id::text, COALESCE(sector_id::text, ''), COALESCE(lote_id::text, ''),
		COALESCE(dispositivo_id::text, ''), COALESCE(horno_id, ''), tipo, severidad, estado, mensaje, created_at`

	query := `SELECT ` + columnas + ` FROM alertas WHERE id = $1`
	args := []any{alertaID}
	if alertaID == "" {
		query = `SELECT ` + columnas + ` FROM alertas
			WHERE estado = 'ACTIVA' AND tipo = $1
			ORDER BY created_at DESC LIMIT 1`
		args = []any{alerta.TipoPorcentajeQuemadosAlto}
	}

	var a alerta.Alerta
	var sectorID, loteID, dispositivoID string
	if err := pool.QueryRow(ctx, query, args...).Scan(
		&a.ID, &sectorID, &loteID, &dispositivoID, &a.HornoID,
		&a.Tipo, &a.Severidad, &a.Estado, &a.Mensaje, &a.CreadaEn,
	); err != nil {
		return nil, err
	}
	if sectorID != "" {
		a.SectorID = &sectorID
	}
	if loteID != "" {
		a.LoteID = &loteID
	}
	if dispositivoID != "" {
		a.DispositivoID = &dispositivoID
	}
	return &a, nil
}

func usuariosActivos(ctx context.Context, pool *pgxpool.Pool, emails []string) ([]usuarioEmail, error) {
	rows, err := pool.Query(ctx,
		`SELECT id::text, email FROM usuarios WHERE email = ANY($1) AND activo ORDER BY email`, emails)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []usuarioEmail
	for rows.Next() {
		var u usuarioEmail
		if err := rows.Scan(&u.ID, &u.Email); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
