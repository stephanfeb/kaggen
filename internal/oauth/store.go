package oauth

// TokenStore manages OAuth tokens per user and provider.
// Implementations must be safe for concurrent use.
type TokenStore interface {
	// Get retrieves a token for the given user and provider.
	// Returns ErrTokenNotFound if no token exists.
	// Implementations may automatically refresh tokens that are near expiration.
	Get(userID, provider string) (*Token, error)

	// Store saves a token for the given user and provider.
	// If a token already exists, it is replaced.
	Store(userID, provider string, token *Token) error

	// Delete removes a token for the given user and provider.
	// Returns nil if no token exists (idempotent).
	Delete(userID, provider string) error

	// List returns all provider names with stored tokens for a user.
	List(userID string) ([]string, error)

	// NeedsAuth returns which of the required providers lack valid tokens.
	// This is useful for checking if a skill can proceed or needs authorization.
	NeedsAuth(userID string, requiredProviders []string) []string

	// Close releases any resources held by the store.
	Close() error
}

// Refresher is an optional interface for token stores that support automatic refresh.
type Refresher interface {
	// SetRefresher sets the function used to refresh expired tokens.
	// The refresher is called when Get() encounters a token that is expired
	// or near expiration but has a refresh token.
	SetRefresher(fn RefreshFunc)
}

// RefreshFunc is called to refresh an expired token.
// It receives the current (expired) token and returns a new token.
type RefreshFunc func(userID, provider string, current *Token) (*Token, error)
