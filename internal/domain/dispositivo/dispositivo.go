package dispositivo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Estados de salud posibles de un dispositivo según su último heartbeat.
const (
	EstadoOnline  = "online"
	EstadoOffline = "offline"
)

// ValidationError agrupa todos los errores de validación de un PingRequest.
type ValidationError struct {
	Fields []string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("errores de validación: %s", strings.Join(e.Fields, "; "))
}

// IsValidationError informa si un error es de tipo ValidationError.
func IsValidationError(err error) bool {
	var ve *ValidationError
	return errors.As(err, &ve)
}

// ErrDispositivoNotFound indica que el dispositivo no existe en el catálogo.
var ErrDispositivoNotFound = errors.New("dispositivo no encontrado")

// Dispositivo representa un nodo Raspberry Pi del catálogo de monitoreo.
type Dispositivo struct {
	ID        string    `json:"id"        db:"id"`
	Nombre    string    `json:"nombre"    db:"nombre"`
	Ubicacion string    `json:"ubicacion" db:"ubicacion"`
	CreatedAt time.Time `json:"createdAt" db:"created_at"`
}

// MetricaDispositivo representa un registro de telemetría recibido de un dispositivo.
type MetricaDispositivo struct {
	ID                 string    `json:"id"                 db:"id"`
	DispositivoID      string    `json:"dispositivoId"      db:"dispositivo_id"`
	CpuPct             float64   `json:"cpuPct"             db:"cpu_pct"`
	MemRamDisponibleMb float64   `json:"memRamDisponibleMb" db:"mem_ram_disponible_mb"`
	TempChip           float64   `json:"tempChip"           db:"temp_chip"`
	ReceivedAt         time.Time `json:"receivedAt"         db:"received_at"`
}

// EstadoDispositivo representa el estado de salud actual de un dispositivo,
// calculado a partir de su último heartbeat (last_seen).
type EstadoDispositivo struct {
	DispositivoID string              `json:"dispositivoId"`
	Nombre        string              `json:"nombre"`
	Ubicacion     string              `json:"ubicacion"`
	Estado        string              `json:"estado"`
	UltimaMetrica *MetricaDispositivo `json:"ultimaMetrica,omitempty"`
	LastSeen      *time.Time          `json:"lastSeen,omitempty"`
}

// DispositivoConUltimaMetrica vincula un dispositivo del catálogo con su última
// métrica registrada (si existe), usada para hidratar el estado actual al arrancar.
type DispositivoConUltimaMetrica struct {
	Dispositivo   Dispositivo
	UltimaMetrica *MetricaDispositivo
}

// PaginatedResult encapsula una página de métricas junto con metadatos de paginación.
type PaginatedResult struct {
	Items    []MetricaDispositivo
	Total    int
	Page     int
	PageSize int
}

// PingRequest representa el payload JSON enviado por la Raspberry Pi cada 10 segundos.
type PingRequest struct {
	DispositivoID      string  `json:"dispositivoId"`
	CpuPct             float64 `json:"cpuPct"`
	MemRamDisponibleMb float64 `json:"memRamDisponibleMb"`
	TempChip           float64 `json:"tempChip"`
}

// Validate verifica las reglas de negocio del PingRequest.
// Retorna un *ValidationError con el listado completo de campos inválidos,
// o nil si el request es válido.
func (req PingRequest) Validate() error {
	var errs []string

	// Campo obligatorio: dispositivo_id
	if strings.TrimSpace(req.DispositivoID) == "" {
		errs = append(errs, "dispositivoId: es requerido")
	}

	// Uso de CPU en porcentaje (0-100)
	if req.CpuPct < 0 || req.CpuPct > 100 {
		errs = append(errs, fmt.Sprintf("cpuPct: valor '%.2f' fuera de rango, debe estar entre 0 y 100", req.CpuPct))
	}

	// Memoria RAM disponible no puede ser negativa
	if req.MemRamDisponibleMb < 0 {
		errs = append(errs, "memRamDisponibleMb: no puede ser negativo")
	}

	// Temperatura del chip dentro de un rango físico razonable
	if req.TempChip < -40 || req.TempChip > 120 {
		errs = append(errs, fmt.Sprintf("tempChip: valor '%.2f' fuera de rango razonable (-40 a 120)", req.TempChip))
	}

	if len(errs) > 0 {
		return &ValidationError{Fields: errs}
	}
	return nil
}

// Repository define el contrato de persistencia de dispositivos y métricas.
// Las implementaciones viven en la capa de infraestructura (internal/repository).
type Repository interface {
	InsertMetrica(ctx context.Context, m *MetricaDispositivo) error
	GetDispositivosConUltimaMetrica(ctx context.Context) ([]DispositivoConUltimaMetrica, error)
	GetDispositivoByID(ctx context.Context, id string) (*Dispositivo, error)
	GetMetricasByDispositivo(ctx context.Context, dispositivoID string, page, pageSize int) (*PaginatedResult, error)
}

// StateStore define el contrato del caché en memoria del estado actual de los dispositivos.
type StateStore interface {
	Hydrate(items []DispositivoConUltimaMetrica, umbral time.Duration, now time.Time)
	Update(d Dispositivo, m MetricaDispositivo) (prevState, currState string, estado *EstadoDispositivo)
	Get(dispositivoID string) (*EstadoDispositivo, bool)
	GetAll() []EstadoDispositivo
	MarkOfflineIfStale(umbral time.Duration, now time.Time) []string
}

// Service define las operaciones de negocio para dispositivos.
type Service interface {
	ProcessPing(ctx context.Context, req PingRequest) (*EstadoDispositivo, error)
	GetAllEstados() []EstadoDispositivo
	GetMetricas(ctx context.Context, dispositivoID string, page, pageSize int) (*PaginatedResult, error)
}
