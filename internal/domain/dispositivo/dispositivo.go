package dispositivo

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Estados de salud posibles de un dispositivo según su último heartbeat.
const (
	EstadoOnline  = "online"
	EstadoOffline = "offline"
)

// TipoDispositivo identifica la ubicación funcional del nodo dentro de la línea
// de horneado. Es el enum canónico del dominio: entrada o salida del horno.
type TipoDispositivo string

// Tipos de dispositivo admitidos por el contrato.
const (
	TipoEntradaHorno TipoDispositivo = "ENTRADA_HORNO"
	TipoSalidaHorno  TipoDispositivo = "SALIDA_HORNO"
)

// ParseTipoDispositivo normaliza (trim + upper) y valida un tipo de dispositivo.
// Devuelve ok=false si el valor está vacío o no corresponde a un tipo conocido.
func ParseTipoDispositivo(raw string) (TipoDispositivo, bool) {
	switch TipoDispositivo(strings.ToUpper(strings.TrimSpace(raw))) {
	case TipoEntradaHorno:
		return TipoEntradaHorno, true
	case TipoSalidaHorno:
		return TipoSalidaHorno, true
	default:
		return "", false
	}
}

// Límites de largo de los campos de un dispositivo, coherentes con los
// VARCHAR(100) de la tabla dispositivos (database/schema.sql).
const (
	maxNombreLength  = 100
	maxWhepURLLength = 500
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

// ErrSectorNotFound indica que el sectorId informado no existe en el catálogo
// de sectores. Se traduce a un 400 en el handler.
var ErrSectorNotFound = errors.New("el sector indicado no existe")

// ErrSectorTipoDuplicado indica que el sector ya tiene un dispositivo del mismo
// tipo funcional (violación de uq_dispositivos_sector_tipo). Se traduce a 409.
var ErrSectorTipoDuplicado = errors.New("el sector ya tiene un dispositivo de ese tipo")

// Dispositivo representa un nodo Raspberry Pi del catálogo de monitoreo.
type Dispositivo struct {
	ID        string    `json:"id"        db:"id"`
	Nombre    string    `json:"nombre"    db:"nombre"`
	SectorID  *string   `json:"sectorId,omitempty" db:"sector_id"`
	WhepURL   string    `json:"whepUrl,omitempty" db:"whep_url"`
	Tipo      *string   `json:"type,omitempty" db:"tipo"`
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
	SectorID      *string             `json:"sectorId,omitempty"`
	WhepURL       string              `json:"whepUrl,omitempty"`
	Tipo          *string             `json:"type,omitempty"`
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

	// Campo opcional: dispositivo_id. Un dispositivo registrado se autentica con
	// su Bearer secret y el servidor inyecta su id; si se informa, debe ser UUID.
	if trimmed := strings.TrimSpace(req.DispositivoID); trimmed != "" && !uuidPattern.MatchString(trimmed) {
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
	Nombre   string  `json:"nombre"`
	SectorID *string `json:"sectorId,omitempty"`
	WhepURL  string  `json:"whepUrl,omitempty"`
	Tipo     string  `json:"type"`
}

// Validate verifica las reglas de negocio del CreateDispositivoRequest,
// replicando en el servidor los constraints de la tabla dispositivos.
// Retorna un *ValidationError con el listado completo de campos inválidos,
// o nil si el request es válido.
func (req CreateDispositivoRequest) Validate() error {
	errs := validateNombre(req.Nombre)
	errs = append(errs, validateWhepURL(req.WhepURL)...)

	// Campo obligatorio: tipo, enum canónico ENTRADA_HORNO/SALIDA_HORNO.
	if _, ok := ParseTipoDispositivo(req.Tipo); !ok {
		errs = append(errs, "type: debe ser ENTRADA_HORNO o SALIDA_HORNO")
	}

	if len(errs) > 0 {
		return &ValidationError{Fields: errs}
	}
	return nil
}

// validateNombre valida las reglas de negocio del nombre, compartidas entre el
// alta (CreateDispositivoRequest) y el rename autenticado de la propia Raspberry
// (RenameDispositivoRequest). Retorna el listado de campos inválidos.
func validateNombre(nombre string) []string {
	var errs []string

	// Campo obligatorio: nombre
	if strings.TrimSpace(nombre) == "" {
		errs = append(errs, "nombre: es requerido")
	} else if len([]rune(strings.TrimSpace(nombre))) > maxNombreLength {
		errs = append(errs, fmt.Sprintf("nombre: no puede superar los %d caracteres", maxNombreLength))
	}

	return errs
}

// validateWhepURL valida el campo opcional whepUrl de un dispositivo: si no está
// vacío, debe ser una URL absoluta con esquema http/https y un host, y no puede
// superar maxWhepURLLength runes (coherente con VARCHAR(500)). Retorna el listado
// de campos inválidos (vacío si el valor es válido u omitido).
func validateWhepURL(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}

	var errs []string
	if len([]rune(trimmed)) > maxWhepURLLength {
		errs = append(errs, fmt.Sprintf("whepUrl: no puede superar los %d caracteres", maxWhepURLLength))
	}

	u, err := url.Parse(trimmed)
	if err != nil || !u.IsAbs() || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		errs = append(errs, "whepUrl: debe ser una URL absoluta http o https")
	}

	return errs
}

// UpdateDispositivoRequest representa el payload JSON entrante para la
// modificación (PUT) de un dispositivo existente desde el panel del operador.
// El nombre NO es modificable desde el panel: solo el rename autenticado del nodo
// (RenameDispositivoRequest) puede cambiarlo. SectorID nil desasigna el
// dispositivo de su sector.
type UpdateDispositivoRequest struct {
	DispositivoID string  `json:"dispositivoId"`
	SectorID      *string `json:"sectorId,omitempty"`
	WhepURL       string  `json:"whepUrl,omitempty"`
}

// Validate verifica las reglas de negocio del UpdateDispositivoRequest: exige un
// dispositivoId presente y valida el whepUrl opcional. El nombre no participa
// porque es inmutable desde el panel. Retorna un *ValidationError con el listado
// completo de campos inválidos, o nil si el request es válido.
func (req UpdateDispositivoRequest) Validate() error {
	var errs []string

	// Campo obligatorio: dispositivo_id (UUID). Validarlo evita que un id
	// malformado llegue a la query y derive en un 500 (22P02).
	if trimmed := strings.TrimSpace(req.DispositivoID); trimmed == "" {
		errs = append(errs, "dispositivoId: es requerido")
	} else if !uuidPattern.MatchString(trimmed) {
		errs = append(errs, "dispositivoId: debe ser un UUID válido")
	}

	errs = append(errs, validateWhepURL(req.WhepURL)...)

	if len(errs) > 0 {
		return &ValidationError{Fields: errs}
	}
	return nil
}

// RenameDispositivoRequest representa el payload del rename que emite la
// propia Raspberry autenticada con su secret.
type RenameDispositivoRequest struct {
	Nombre string `json:"nombre"`
}

// Validate aplica las reglas de nombre compartidas con el alta/modificación.
func (req RenameDispositivoRequest) Validate() error {
	errs := validateNombre(req.Nombre)
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
	Revoke(ctx context.Context, actorEmail, id string) error
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
	Rename(ctx context.Context, deviceID, nombre string) (*EstadoDispositivo, error)
	Revoke(ctx context.Context, actorEmail, id string) error
	ProcessPing(ctx context.Context, req PingRequest) (*EstadoDispositivo, error)
	GetAllEstados() []EstadoDispositivo
	GetMetricas(ctx context.Context, dispositivoID string, page, pageSize int) (*PaginatedResult, error)
}
