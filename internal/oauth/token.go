package oauth

import (
	"errors"
	"time"
)

// Token represents an OAuth 2.0 access token with metadata.
type Token struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type"` // typically "Bearer"
	ExpiresAt    time.Time `json:"expires_at"`
	Scopes       []string  `json:"scopes,omitempty"`
}

// IsExpired returns true if the token has expired.
func (t *Token) IsExpired() bool {
	return time.Now().After(t.ExpiresAt)
}

// ExpiresWithin returns true if the token expires within the given duration.
func (t *Token) ExpiresWithin(d time.Duration) bool {
	return time.Now().Add(d).After(t.ExpiresAt)
}

// CanRefresh returns true if the token has a refresh token.
func (t *Token) CanRefresh() bool {
	return t.RefreshToken != ""
}

// Common errors returned by OAuth operations.
var (
	// ErrTokenNotFound indicates no token exists for the user/provider.
	ErrTokenNotFound = errors.New("oauth: token not found")

	// ErrTokenExpired indicates the token has expired and cannot be refreshed.
	ErrTokenExpired = errors.New("oauth: token expired")

	// ErrNoRefreshToken indicates the token cannot be refreshed (no refresh token).
	ErrNoRefreshToken = errors.New("oauth: no refresh token available")

	// ErrProviderNotConfigured indicates the OAuth provider is not in config.
	ErrProviderNotConfigured = errors.New("oauth: provider not configured")

	// ErrInvalidState indicates the OAuth state parameter was invalid or expired.
	ErrInvalidState = errors.New("oauth: invalid or expired state")

	// ErrAuthorizationFailed indicates the OAuth authorization failed.
	ErrAuthorizationFailed = errors.New("oauth: authorization failed")

	// ErrMasterKeyRequired indicates encryption requires a master key.
	ErrMasterKeyRequired = errors.New("oauth: master key required for token encryption")
)

// TokenResponse represents the OAuth token endpoint response.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
	Error        string `json:"error,omitempty"`
	ErrorDesc    string `json:"error_description,omitempty"`
}

// ToToken converts a TokenResponse to a Token.
func (r *TokenResponse) ToToken() *Token {
	expiresAt := time.Now().Add(time.Duration(r.ExpiresIn) * time.Second)
	return &Token{
		AccessToken:  r.AccessToken,
		RefreshToken: r.RefreshToken,
		TokenType:    r.TokenType,
		ExpiresAt:    expiresAt,
	}
}
