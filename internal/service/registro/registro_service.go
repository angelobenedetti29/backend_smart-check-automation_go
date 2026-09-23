// Package service implementa la lógica de negocio del flujo de registro de
// nodos: emisión de solicitudes, aprobación/rechazo y entrega única del secret.
package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"time"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/registro"
)

const (
	// requestTTL es la vigencia de una solicitud PENDING (ventana de aprobación).
	requestTTL = 15 * time.Minute
	// pickupTTL es la vigencia de la ventana de entrega del secret tras aprobar.
	pickupTTL = 10 * time.Minute
)

// requestIDPattern valida el formato de un request_id antes de tocar la base:
// "req_" + base64url sin padding de 32 bytes (43 chars).
var requestIDPattern = regexp.MustCompile(`^req_[A-Za-z0-9_-]{43}$`)

// Service orquesta el ciclo de vida de las solicitudes de registro.
type Service struct {
	repo registro.Repository
}

// NewService instancia el servicio inyectando el repositorio de registro.
func NewService(repo registro.Repository) *Service {
	return &Service{repo: repo}
}

// Issue valida el hostname y el tipo de dispositivo, genera un request_id de un
// solo uso y crea la solicitud PENDING con vencimiento a requestTTL.
func (s *Service) Issue(ctx context.Context, req registro.CreateRequest) (*registro.IssueResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	token, err := generateToken()
	if err != nil {
		return nil, fmt.Errorf("generar request_id: %w", err)
	}
	return s.repo.Issue(ctx, req.Hostname, req.Type, "req_"+token, time.Now().Add(requestTTL))
}

// List devuelve las solicitudes pendientes vigentes para el panel.
func (s *Service) List(ctx context.Context) ([]registro.RegistrationRequest, error) {
	return s.repo.ListPending(ctx)
}

// Approve genera un secret nuevo (nunca reutilizado), persiste solo su hash
// SHA-256 y entrega el device_id creado. Validar el formato del request_id
// evita filtrar existencia antes de tocar la base.
func (s *Service) Approve(ctx context.Context, actorEmail, requestID string) (*registro.Approval, error) {
	if !requestIDPattern.MatchString(requestID) {
		return nil, registro.ErrRequestNotFound
	}
	secret, err := generateToken()
	if err != nil {
		return nil, fmt.Errorf("generar secret: %w", err)
	}
	sum := sha256.Sum256([]byte(secret))
	secretHash := hex.EncodeToString(sum[:])
	return s.repo.Approve(ctx, actorEmail, requestID, secret, secretHash, time.Now().Add(pickupTTL))
}

// Reject rechaza una solicitud PENDING. Valida el formato del request_id.
func (s *Service) Reject(ctx context.Context, actorEmail, requestID string) error {
	if !requestIDPattern.MatchString(requestID) {
		return registro.ErrRequestNotFound
	}
	return s.repo.Reject(ctx, actorEmail, requestID)
}

// Pickup entrega el secret una única vez o el estado actual de la solicitud.
// Valida el formato del request_id.
func (s *Service) Pickup(ctx context.Context, requestID string) (*registro.Pickup, error) {
	if !requestIDPattern.MatchString(requestID) {
		return nil, registro.ErrRequestNotFound
	}
	return s.repo.Pickup(ctx, requestID)
}

// generateToken devuelve 32 bytes de crypto/rand en base64url sin padding.
func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
