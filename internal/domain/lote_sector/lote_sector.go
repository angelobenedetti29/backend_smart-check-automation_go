// Package lote_sector define el modelo de dominio del ciclo de vida de un lote
// coordinado entre dos dispositivos (ENTRADA_HORNO/SALIDA_HORNO) de un mismo
// sector: apertura idempotente, reporte de eventos en vivo y cierre con conteo
// final autoritativo. Persiste sobre la tabla lotes_productivos y eventos_lote.
package lote_sector

import (
	"context"
	"errors"
	"time"
	// Embebe la base tzdata para que time.LoadLocation("America/Argentina/
	// Buenos_Aires") funcione también fuera de la imagen Docker (que ya la trae).
	_ "time/tzdata"
)

// Estados del ciclo de vida de un lote, coherentes con el CHECK de la columna
// lotes_productivos.estado.
const (
	EstadoAbierto = "ABIERTO"
	EstadoCerrado = "CERRADO"
)

// Turnos de producción, coherentes con el CHECK de lotes_productivos.turno.
const (
	TurnoManana = "mañana"
	TurnoTarde  = "tarde"
	TurnoNoche  = "noche"
)

// Estados de calidad de una detección, coherentes con el CHECK de
// eventos_lote.estado. Un estado que el modelo no produce viaja null.
const (
	EstadoOK      = "ok"
	EstadoCrudo   = "crudo"
	EstadoQuemado = "quemado"
)

// Errores centinela — comparar con errors.Is() en el service y el handler.
var (
	// ErrNotFound se retorna cuando el lote_id no existe.
	ErrNotFound = errors.New("lote_sector: lote no encontrado")

	// ErrCerrado se retorna al registrar eventos sobre un lote ya cerrado.
	ErrCerrado = errors.New("lote_sector: el lote ya está cerrado")

	// ErrAjeno se retorna cuando el lote pertenece a otro sector que el del
	// dispositivo autenticado.
	ErrAjeno = errors.New("lote_sector: el lote pertenece a otro sector")

	// ErrProductoInconsistente se retorna cuando algún evento del batch no
	// coincide con el producto del lote; se rechaza el batch completo.
	ErrProductoInconsistente = errors.New("lote_sector: el producto del evento no coincide con el lote")

	// ErrDemasiadosEventos se retorna cuando el batch supera el máximo admitido.
	ErrDemasiadosEventos = errors.New("lote_sector: demasiados eventos en el batch")

	// ErrIdempotencyKeyConflicto se retorna cuando la clave de apertura ya fue
	// usada por un lote de otro sector: la clave es globalmente única, pero el
	// lote resultante no pertenece al sector solicitante.
	ErrIdempotencyKeyConflicto = errors.New("lote_sector: la clave de idempotencia ya fue usada en otro sector")

	// ErrPayloadInvalido se retorna cuando el cuerpo de la petición no cumple
	// las reglas de negocio del contrato: evento_id que no es UUID, motivo de
	// cierre fuera del conjunto admitido, conteos inconsistentes o cursor de
	// paginación malformado.
	ErrPayloadInvalido = errors.New("lote_sector: payload inválido")

	// ErrSectorActivo se retorna al intentar cerrar un lote cuando el criterio
	// de inactividad del sector todavía no se cumple; la Pi debe esperar y
	// reintentar.
	ErrSectorActivo = errors.New("lote_sector: el sector todavía está activo")
)

// Conteos agrupa los totales de un lote. OK/Crudo/Quemado son punteros para
// representar un estado que el modelo no produce como null, nunca como 0.
// Total es la suma de los buckets no nulos.
type Conteos struct {
	OK      *int `json:"ok"`
	Crudo   *int `json:"crudo"`
	Quemado *int `json:"quemado"`
	Total   int  `json:"total"`
}

// AbiertoPor identifica al dispositivo que abrió el lote y su tipo funcional.
// Un tipo distinto de ENTRADA_HORNO señala un lote degradado.
type AbiertoPor struct {
	DeviceID string `json:"device_id"`
	Tipo     string `json:"type"`
}

// Lote es el objeto de contrato expuesto por GET /lotes/abierto y GET /lotes.
// InactividadSegundos lo calcula el service a partir del reloj del servidor;
// el repositorio lo deja en 0.
type Lote struct {
	ID                  string      `json:"id"`
	SectorID            string      `json:"sector_id"`
	Estado              string      `json:"estado"`
	Turno               *string     `json:"turno"`
	ProductoID          string      `json:"producto_id"`
	ProductoNombre      string      `json:"producto_nombre"`
	AbiertoEn           time.Time   `json:"abierto_en"`
	AbiertoPor          *AbiertoPor `json:"abierto_por"`
	Conteos             Conteos     `json:"conteos"`
	UltimoEventoEn      *time.Time  `json:"ultimo_evento_en"`
	InactividadSegundos float64     `json:"inactividad_segundos"`
	CerradoEn           *time.Time  `json:"cerrado_en,omitempty"`
	MotivoCierre        *string     `json:"motivo_cierre,omitempty"`
}

// TurnoDe clasifica el momento de apertura de un lote en el turno de
// producción (hora de Argentina): mañana [06,14), tarde [14,22), noche el resto.
func TurnoDe(t time.Time) string {
	loc, err := time.LoadLocation("America/Argentina/Buenos_Aires")
	if err != nil {
		// Argentina no aplica horario de verano: UTC-3 fijo como fallback.
		loc = time.FixedZone("ART", -3*60*60)
	}
	h := t.In(loc).Hour()
	switch {
	case h >= 6 && h < 14:
		return TurnoManana
	case h >= 14 && h < 22:
		return TurnoTarde
	default:
		return TurnoNoche
	}
}

// Evento es una detección reportada por la salida. Estado/Confianza/Pista/
// Frame/ModeloID/Momento son opcionales según lo que el modelo pueda emitir.
type Evento struct {
	EventoID   string     `json:"evento_id"`
	ProductoID string     `json:"producto_id"`
	Estado     *string    `json:"estado"`
	Confianza  *float64   `json:"confianza"`
	Pista      *int       `json:"pista"`
	Frame      *int64     `json:"frame"`
	ModeloID   *string    `json:"modelo_id"`
	Momento    *time.Time `json:"momento"`
}

// AbrirParams son los datos para abrir (o adjuntarse a) un lote de un sector.
type AbrirParams struct {
	SectorID       string
	ProductoID     string
	AbiertoPor     string
	IdempotencyKey string
	Turno          string
}

// CerrarParams son los datos del cierre autoritativo de un lote. Conteos trae
// los buckets finales (con null donde el modelo no produce ese estado); el
// total autoritativo a persistir es Conteos.Total.
type CerrarParams struct {
	LoteID         string
	SectorID       string
	Conteos        Conteos
	Motivo         string
	IdempotencyKey string
}

// HistorialParams filtran el historial de lotes de un sector. AntesDe y
// AntesDeID forman el cursor opaco (inicio_at, id) para paginar hacia atrás.
type HistorialParams struct {
	SectorID   string
	ProductoID string
	AntesDe    *time.Time
	AntesDeID  string
	Limite     int
}

// Repository define el contrato de persistencia del ciclo de vida de los lotes
// por sector. La implementación real vive en internal/repository/.
type Repository interface {
	// GetAbiertoBySector devuelve el lote ABIERTO del sector, o nil si no hay.
	GetAbiertoBySector(ctx context.Context, sectorID string) (*Lote, error)
	// GetByID busca un lote por id o devuelve ErrNotFound si no existe.
	GetByID(ctx context.Context, loteID string) (*Lote, error)
	// Abrir es get-or-create por sector; creado=false si ya existía (misma
	// clave de idempotencia o lote abierto del sector).
	Abrir(ctx context.Context, params AbrirParams) (*Lote, bool, error)
	// RegistrarEventos aplica un batch de eventos deduplicando por evento_id,
	// persiste la procedencia (deviceID, null si viene vacío) y devuelve
	// aceptados, duplicados y el lote actualizado.
	RegistrarEventos(ctx context.Context, loteID, sectorID, deviceID string, eventos []Evento) (aceptados int, duplicados int, lote *Lote, err error)
	// Cerrar fija los conteos finales autoritativos; yaCerrado=true si el lote
	// ya estaba cerrado (reintento idempotente).
	Cerrar(ctx context.Context, params CerrarParams) (*Lote, bool, error)
	// Historial devuelve una página de lotes (más nuevos primero) y el total
	// que matchea los filtros.
	Historial(ctx context.Context, params HistorialParams) ([]Lote, int, error)
}
