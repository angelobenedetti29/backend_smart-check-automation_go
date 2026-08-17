package google

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/user"
)

func TestOAuthProvider_Verify_TokenInvalido(t *testing.T) {
	provider := NewOAuthProvider("test-client-id")

	// Token vacío o inválido debe fallar con ErrInvalidToken
	claims, err := provider.Verify(context.Background(), "invalid-token-string")

	assert.Error(t, err)
	assert.ErrorIs(t, err, user.ErrInvalidToken)
	assert.Nil(t, claims)
}
