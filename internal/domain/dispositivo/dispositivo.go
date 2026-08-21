package dispositivo

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Estados de salud posibles de un dispositivo según su último heartbeat.
const (
	EstadoOnline  = "online"
	EstadoOffline = "offline"
)

// Límites de largo de los campos de un dispositivo, coherentes con los
// VARCHAR(100) de la tabla dispositivos (database/schema.sql).
const (
	maxNombreLength    = 100
	maxUbicacionLength = 100
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ValidationError agrupa todos los errores de validación de un request entrante.
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
	ID                         string    `json:"id"                              db:"id"`
	DispositivoID              string    `json:"dispositivoId"                   db:"dispositivo_id"`
	CpuPct                     float64   `json:"cpuPct"                          db:"cpu_pct"`
	MemRamDisponibleMb         float64   `json:"memRamDisponibleMb"              db:"mem_ram_disponible_mb"`
	MemRamTotalMb              *float64  `json:"memRamTotalMb,omitempty"          db:"mem_ram_total_mb"`
	AlmacenamientoDisponibleMb *float64  `json:"almacenamientoDisponibleMb,omitempty" db:"almacenamiento_disponible_mb"`
	AlmacenamientoTotalMb      *float64  `json:"almacenamientoTotalMb,omitempty" db:"almacenamiento_total_mb"`
	TempChip                   float64   `json:"tempChip"                        db:"temp_chip"`
	AiProcessorPct             float64   `json:"aiProcessorPct"                  db:"ai_processor_pct"`
	ReceivedAt                 time.Time `json:"receivedAt"                      db:"received_at"`
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
	DispositivoID              string   `json:"dispositivoId"`
	CpuPct                     float64  `json:"cpuPct"`
	MemRamDisponibleMb         float64  `json:"memRamDisponibleMb"`
	MemRamTotalMb              *float64 `json:"memRamTotalMb,omitempty"`
	AlmacenamientoDisponibleMb *float64 `json:"almacenamientoDisponibleMb,omitempty"`
	AlmacenamientoTotalMb      *float64 `json:"almacenamientoTotalMb,omitempty"`
	TempChip                   float64  `json:"tempChip"`
	AiProcessorPct             float64  `json:"aiProcessorPct"`
}

// Validate verifica las reglas de negocio del PingRequest.
// Retorna un *ValidationError con el listado completo de campos inválidos,
// o nil si el request es válido.
func (req PingRequest) Validate() error {
	var errs []string

	// Campo obligatorio: dispositivo_id
	if strings.TrimSpace(req.DispositivoID) == "" {
		errs = append(errs, "dispositivoId: es requerido")
	} else if !uuidPattern.MatchString(req.DispositivoID) {
		errs = append(errs, "dispositivoId: debe ser un UUID válido")
	}

	// Uso de CPU en porcentaje (0-100)
	if req.CpuPct < 0 || req.CpuPct > 100 {
		errs = append(errs, fmt.Sprintf("cpuPct: valor '%.2f' fuera de rango, debe estar entre 0 y 100", req.CpuPct))
	}

	// Memoria RAM disponible no puede ser negativa
	if req.MemRamDisponibleMb < 0 {
		errs = append(errs, "memRamDisponibleMb: no puede ser negativo")
	}

	// Los totales de memoria y almacenamiento son opcionales para mantener
	// compatibilidad con las Raspberry Pi que todavía no los reportan. Los dos
	// valores de almacenamiento forman un único par opcional.
	if req.MemRamTotalMb != nil {
		if *req.MemRamTotalMb < 0 {
			errs = append(errs, "memRamTotalMb: no puede ser negativo")
		} else if req.MemRamDisponibleMb > *req.MemRamTotalMb {
			errs = append(errs, "memRamDisponibleMb: no puede superar memRamTotalMb")
		}
	}
	if (req.AlmacenamientoDisponibleMb == nil) != (req.AlmacenamientoTotalMb == nil) {
		errs = append(errs, "almacenamientoDisponibleMb y almacenamientoTotalMb: deben informarse juntos")
	}
	if req.AlmacenamientoDisponibleMb != nil && *req.AlmacenamientoDisponibleMb < 0 {
		errs = append(errs, "almacenamientoDisponibleMb: no puede ser negativo")
	}
	if req.AlmacenamientoTotalMb != nil {
		if *req.AlmacenamientoTotalMb < 0 {
			errs = append(errs, "almacenamientoTotalMb: no puede ser negativo")
		} else if req.AlmacenamientoDisponibleMb != nil && *req.AlmacenamientoDisponibleMb > *req.AlmacenamientoTotalMb {
			errs = append(errs, "almacenamientoDisponibleMb: no puede superar almacenamientoTotalMb")
		}
	}

	// Temperatura del chip dentro de un rango físico razonable
	if req.TempChip < -40 || req.TempChip > 120 {
		errs = append(errs, fmt.Sprintf("tempChip: valor '%.2f' fuera de rango razonable (-40 a 120)", req.TempChip))
	}

	// Uso del procesador de IA (NPU) en porcentaje (0-100)
	if req.AiProcessorPct < 0 || req.AiProcessorPct > 100 {
		errs = append(errs, fmt.Sprintf("aiProcessorPct: valor '%.2f' fuera de rango, debe estar entre 0 y 100", req.AiProcessorPct))
	}

	if len(errs) > 0 {
		return &ValidationError{Fields: errs}
	}
	return nil
}

// CreateDispositivoRequest representa el payload JSON entrante para el alta
// (POST) de un dispositivo Raspberry Pi desde el panel del operador.
type CreateDispositivoRequest struct {
	Nombre    string `json:"nombre"`
	Ubicacion string `json:"ubicacion"`
}

// Validate verifica las reglas de negocio del CreateDispositivoRequest,
// replicando en el servidor los constraints de la tabla dispositivos.
// Retorna un *ValidationError con el listado completo de campos inválidos,
// o nil si el request es válido.
func (req CreateDispositivoRequest) Validate() error {
	errs := validateNombreUbicacion(req.Nombre, req.Ubicacion)

	if len(errs) > 0 {
		return &ValidationError{Fields: errs}
	}
	return nil
}

// validateNombreUbicacion valida las reglas de negocio de nombre y ubicación
// compartidas entre el alta (CreateDispositivoRequest) y la modificación
// (UpdateDispositivoRequest). Retorna el listado de campos inválidos.
func validateNombreUbicacion(nombre, ubicacion string) []string {
	var errs []string

	// Campo obligatorio: nombre
	if strings.TrimSpace(nombre) == "" {
		errs = append(errs, "nombre: es requerido")
	} else if len([]rune(strings.TrimSpace(nombre))) > maxNombreLength {
		errs = append(errs, fmt.Sprintf("nombre: no puede superar los %d caracteres", maxNombreLength))
	}

	// Ubicación opcional, con límite de largo coherente con VARCHAR(100)
	if len([]rune(strings.TrimSpace(ubicacion))) > maxUbicacionLength {
		errs = append(errs, fmt.Sprintf("ubicacion: no puede superar los %d caracteres", maxUbicacionLength))
	}

	return errs
}

// UpdateDispositivoRequest representa el payload JSON entrante para la
// modificación (PUT) de un dispositivo existente desde el panel del operador.
type UpdateDispositivoRequest struct {
	DispositivoID string `json:"dispositivoId"`
	Nombre        string `json:"nombre"`
	Ubicacion     string `json:"ubicacion"`
}

// Validate verifica las reglas de negocio del UpdateDispositivoRequest: exige un
// dispositivoId presente y aplica las mismas reglas de nombre/ubicación que el
// alta. Retorna un *ValidationError con el listado completo de campos inválidos,
// o nil si el request es válido.
func (req UpdateDispositivoRequest) Validate() error {
	var errs []string

	// Campo obligatorio: dispositivo_id
	if strings.TrimSpace(req.DispositivoID) == "" {
		errs = append(errs, "dispositivoId: es requerido")
	}

	errs = append(errs, validateNombreUbicacion(req.Nombre, req.Ubicacion)...)

	if len(errs) > 0 {
		return &ValidationError{Fields: errs}
	}
	return nil
}

// Repository define el contrato de persistencia de dispositivos y métricas.
// Las implementaciones viven en la capa de infraestructura (internal/repository).
type Repository interface {
	Create(ctx context.Context, d *Dispositivo) error
	Update(ctx context.Context, d *Dispositivo) error
	Delete(ctx context.Context, id string) error
	InsertMetrica(ctx context.Context, m *MetricaDispositivo) error
	GetDispositivosConUltimaMetrica(ctx context.Context) ([]DispositivoConUltimaMetrica, error)
	GetDispositivoByID(ctx context.Context, id string) (*Dispositivo, error)
	GetMetricasByDispositivo(ctx context.Context, dispositivoID string, page, pageSize int) (*PaginatedResult, error)
}

// StateStore define el contrato del caché en memoria del estado actual de los dispositivos.
type StateStore interface {
	Register(d Dispositivo)
	UpdateDispositivo(d Dispositivo)
	Remove(dispositivoID string)
	Hydrate(items []DispositivoConUltimaMetrica, umbral time.Duration, now time.Time)
	Update(d Dispositivo, m MetricaDispositivo) (prevState, currState string, estado *EstadoDispositivo)
	Get(dispositivoID string) (*EstadoDispositivo, bool)
	GetAll() []EstadoDispositivo
	MarkOfflineIfStale(umbral time.Duration, now time.Time) []string
}

// Service define las operaciones de negocio para dispositivos.
type Service interface {
	Create(ctx context.Context, req CreateDispositivoRequest) (*EstadoDispositivo, error)
	Update(ctx context.Context, req UpdateDispositivoRequest) (*EstadoDispositivo, error)
	Delete(ctx context.Context, id string) error
	ProcessPing(ctx context.Context, req PingRequest) (*EstadoDispositivo, error)
	GetAllEstados() []EstadoDispositivo
	GetMetricas(ctx context.Context, dispositivoID string, page, pageSize int) (*PaginatedResult, error)
}
