package lote_sector

import (
	"testing"
	"time"
)

// locArTest es la zona horaria del negocio usada para construir los instantes
// de los límites de turno.
func locArTest(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Argentina/Buenos_Aires")
	if err != nil {
		t.Fatalf("no se pudo cargar la location de Argentina: %v", err)
	}
	return loc
}

// TestTurnoDeLimites verifica los bordes de cada franja horaria con instantes
// construidos directamente en la zona de Argentina.
func TestTurnoDeLimites(t *testing.T) {
	loc := locArTest(t)
	casos := []struct {
		nombre string
		hora   int
		minuto int
		want   string
	}{
		{"05:59 es noche", 5, 59, TurnoNoche},
		{"06:00 es mañana", 6, 0, TurnoManana},
		{"13:59 es mañana", 13, 59, TurnoManana},
		{"14:00 es tarde", 14, 0, TurnoTarde},
		{"21:59 es tarde", 21, 59, TurnoTarde},
		{"22:00 es noche", 22, 0, TurnoNoche},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			instante := time.Date(2026, time.January, 15, c.hora, c.minuto, 0, 0, loc)
			if got := TurnoDe(instante); got != c.want {
				t.Fatalf("TurnoDe(%s) = %q, se esperaba %q", instante, got, c.want)
			}
		})
	}
}

// TestTurnoDeConvierteDesdeUTC comprueba que un instante en UTC se clasifique
// según la hora local de Argentina (UTC-3), no según su hora UTC.
func TestTurnoDeConvierteDesdeUTC(t *testing.T) {
	casos := []struct {
		nombre    string
		instante  time.Time
		want      string
		horaLocal int
	}{
		{"09:00 UTC = 06:00 ART es mañana", time.Date(2026, time.January, 15, 9, 0, 0, 0, time.UTC), TurnoManana, 6},
		{"01:00 UTC = 22:00 ART del día previo es noche", time.Date(2026, time.January, 15, 1, 0, 0, 0, time.UTC), TurnoNoche, 22},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			loc := locArTest(t)
			if h := c.instante.In(loc).Hour(); h != c.horaLocal {
				t.Fatalf("hora local = %d, se esperaba %d", h, c.horaLocal)
			}
			if got := TurnoDe(c.instante); got != c.want {
				t.Fatalf("TurnoDe(%s) = %q, se esperaba %q", c.instante, got, c.want)
			}
		})
	}
}

// TestValoresDeTurno verifica que los valores persistidos coincidan con el
// CHECK del schema.
func TestValoresDeTurno(t *testing.T) {
	for _, turno := range []string{TurnoManana, TurnoTarde, TurnoNoche} {
		switch turno {
		case "mañana", "tarde", "noche":
		default:
			t.Fatalf("valor de turno inesperado: %q", turno)
		}
	}
}
