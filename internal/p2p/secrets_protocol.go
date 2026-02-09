package p2p

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/libp2p/go-libp2p/core/network"

	"github.com/yourusername/kaggen/internal/auth"
)

// SecretsProtocol handles the /kaggen/secrets/1.0.0 protocol.
type SecretsProtocol struct {
	*APIHandler
	tokenStore *auth.TokenStore
}

// NewSecretsProtocol creates a new secrets protocol handler.
func NewSecretsProtocol(tokenStore *auth.TokenStore, logger *slog.Logger) *SecretsProtocol {
	h := &SecretsProtocol{
		APIHandler: NewAPIHandler(SecretsProtocolID, logger),
		tokenStore: tokenStore,
	}

	// Secrets methods
	h.RegisterMethod("list", h.listSecrets)
	h.RegisterMethod("set", h.setSecret)
	h.RegisterMethod("delete", h.deleteSecret)

	// Token methods
	h.RegisterMethod("tokens", h.listTokens)
	h.RegisterMethod("generate_token", h.generateToken)
	h.RegisterMethod("revoke_token", h.revokeToken)

	return h
}

// StreamHandler returns the stream handler for this protocol.
func (p *SecretsProtocol) StreamHandler() network.StreamHandler {
	return p.HandleStream
}

func (p *SecretsProtocol) listSecrets(params json.RawMessage) (any, error) {
	// Secrets management is now dashboard-only for security
	return nil, fmt.Errorf("secrets management disabled via P2P - use the dashboard instead")
}

func (p *SecretsProtocol) setSecret(params json.RawMessage) (any, error) {
	// Secrets management is now dashboard-only for security
	return nil, fmt.Errorf("secrets management disabled via P2P - use the dashboard instead")
}

func (p *SecretsProtocol) deleteSecret(params json.RawMessage) (any, error) {
	// Secrets management is now dashboard-only for security
	return nil, fmt.Errorf("secrets management disabled via P2P - use the dashboard instead")
}

func (p *SecretsProtocol) listTokens(params json.RawMessage) (any, error) {
	if p.tokenStore == nil {
		return map[string]any{"tokens": []any{}}, nil
	}

	tokens := p.tokenStore.ListTokens()
	return map[string]any{"tokens": tokens}, nil
}

type generateTokenParams struct {
	Name      string `json:"name"`
	ExpiresIn string `json:"expires_in,omitempty"`
}

func (p *SecretsProtocol) generateToken(params json.RawMessage) (any, error) {
	if p.tokenStore == nil {
		return nil, fmt.Errorf("token store not configured")
	}

	var args generateTokenParams
	if len(params) > 0 {
		json.Unmarshal(params, &args)
	}

	var expiresIn time.Duration
	if args.ExpiresIn != "" {
		var err error
		expiresIn, err = time.ParseDuration(args.ExpiresIn)
		if err != nil {
			// Try parsing as days
			if len(args.ExpiresIn) > 1 && args.ExpiresIn[len(args.ExpiresIn)-1] == 'd' {
				days := args.ExpiresIn[:len(args.ExpiresIn)-1]
				var n int
				fmt.Sscanf(days, "%d", &n)
				expiresIn = time.Duration(n) * 24 * time.Hour
			}
		}
	}

	plaintext, id, err := p.tokenStore.GenerateToken(args.Name, expiresIn)
	if err != nil {
		return nil, fmt.Errorf("failed to generate token: %w", err)
	}

	return map[string]any{
		"success": true,
		"id":      id,
		"token":   plaintext,
		"name":    args.Name,
		"message": "Save this token now - it cannot be retrieved again!",
	}, nil
}

type revokeTokenParams struct {
	ID string `json:"id"`
}

func (p *SecretsProtocol) revokeToken(params json.RawMessage) (any, error) {
	if p.tokenStore == nil {
		return nil, fmt.Errorf("token store not configured")
	}

	var args revokeTokenParams
	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if args.ID == "" {
		return nil, fmt.Errorf("id is required")
	}

	if err := p.tokenStore.RevokeToken(args.ID); err != nil {
		return nil, fmt.Errorf("failed to revoke token: %w", err)
	}

	return map[string]any{"success": true, "id": args.ID}, nil
}
