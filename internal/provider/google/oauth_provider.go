package google

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/api/idtoken"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/user"
)

// OAuthProvider verifica Google ID Tokens usando la librería oficial de Google.
// Implementa la interfaz user.GoogleVerifier.
type OAuthProvider struct {
	clientID string // GOOGLE_CLIENT_ID leído desde variables de entorno
}

// NewOAuthProvider crea un nuevo proveedor con el Client ID de la aplicación Google.
func NewOAuthProvider(clientID string) *OAuthProvider {
	return &OAuthProvider{clientID: clientID}
}

// Verify valida el Google ID Token verificando:
//   - Firma criptográfica de Google
//   - Que el token no haya expirado
//   - Que el audience coincida con el GOOGLE_CLIENT_ID configurado
//
// Retorna user.ErrInvalidToken ante cualquier falla, sin exponer detalles internos.
func (p *OAuthProvider) Verify(ctx context.Context, idToken string) (*user.GoogleClaims, error) {
	payload, err := idtoken.Validate(ctx, idToken, p.clientID)
	if err != nil {
		// Preservar deadline/cancelación del contexto: permiten al service y al
		// handler distinguir un timeout de red de Google de un token inválido.
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, err
		}
		// Nunca propagar el error interno de Google hacia arriba:
		// podría contener información sensible sobre la causa del fallo.
		return nil, fmt.Errorf("%w: %v", user.ErrInvalidToken, "verificación fallida")
	}

	email, ok := payload.Claims["email"].(string)
	if !ok || email == "" {
		return nil, fmt.Errorf("%w: email ausente en el token", user.ErrInvalidToken)
	}

	// email_verified verifica que Google haya confirmado la propiedad del email
	emailVerified, _ := payload.Claims["email_verified"].(bool)
	if !emailVerified {
		return nil, fmt.Errorf("%w: email no verificado por Google", user.ErrInvalidToken)
	}

	name, _ := payload.Claims["name"].(string)

	return &user.GoogleClaims{
		Email: email,
		Name:  name,
	}, nil
}
