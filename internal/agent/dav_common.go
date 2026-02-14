package agent

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"

	"github.com/yourusername/kaggen/internal/config"
	"github.com/yourusername/kaggen/internal/oauth"
)

const (
	davTimeout         = 30 * time.Second
	davDiscoveryTimout = 10 * time.Second
)

// DAVAuthType specifies the authentication method for DAV operations.
type DAVAuthType string

const (
	DAVAuthOAuth DAVAuthType = "oauth"
	DAVAuthBasic DAVAuthType = "basic"
)

// DAVTokenGetter retrieves OAuth tokens for DAV authentication.
type DAVTokenGetter func(userID, provider string) (*oauth.Token, error)

// DAVProviderGetter retrieves OAuth provider configuration.
type DAVProviderGetter func(provider string) (config.OAuthProvider, bool)

// DAVClientConfig holds configuration for creating DAV clients.
type DAVClientConfig struct {
	ServerURL string
	AuthType  DAVAuthType

	// OAuth auth
	UserID     string
	Provider   string
	OAuthToken string // Bearer token

	// Basic auth
	Username string
	Password string
}

// davAuthTransport is an http.RoundTripper that injects authentication headers.
type davAuthTransport struct {
	base     http.RoundTripper
	authType DAVAuthType
	token    string
	username string
	password string
}

// RoundTrip implements http.RoundTripper.
func (t *davAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req2 := req.Clone(req.Context())
	switch t.authType {
	case DAVAuthOAuth:
		req2.Header.Set("Authorization", "Bearer "+t.token)
	case DAVAuthBasic:
		auth := base64.StdEncoding.EncodeToString([]byte(t.username + ":" + t.password))
		req2.Header.Set("Authorization", "Basic "+auth)
	}
	return t.base.RoundTrip(req2)
}

// NewDAVHTTPClient creates an HTTP client with appropriate authentication.
func NewDAVHTTPClient(cfg DAVClientConfig) *http.Client {
	transport := &davAuthTransport{
		base:     http.DefaultTransport,
		authType: cfg.AuthType,
		token:    cfg.OAuthToken,
		username: cfg.Username,
		password: cfg.Password,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   davTimeout,
	}
}

// DiscoverDAVServer performs .well-known discovery for CalDAV/CardDAV (RFC 6764).
// The protocol parameter should be "caldav" or "carddav".
// Returns the discovered server URL or an error if discovery fails.
func DiscoverDAVServer(ctx context.Context, domain, protocol string) (string, error) {
	wellKnownURL := fmt.Sprintf("https://%s/.well-known/%s", domain, protocol)

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // Don't follow redirects automatically
		},
		Timeout: davDiscoveryTimout,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wellKnownURL, nil)
	if err != nil {
		return "", fmt.Errorf("create discovery request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("discovery request failed: %w", err)
	}
	defer resp.Body.Close()

	// Check for redirect responses (301, 302, 307, 308)
	if resp.StatusCode == http.StatusMovedPermanently ||
		resp.StatusCode == http.StatusFound ||
		resp.StatusCode == http.StatusTemporaryRedirect ||
		resp.StatusCode == http.StatusPermanentRedirect {
		location := resp.Header.Get("Location")
		if location != "" {
			return location, nil
		}
		return "", fmt.Errorf("redirect response without Location header")
	}

	// If we get a 200 OK, the well-known URL itself is the server
	if resp.StatusCode == http.StatusOK {
		return wellKnownURL, nil
	}

	return "", fmt.Errorf("discovery failed: status %d", resp.StatusCode)
}

// ResolveDAVServerURL resolves the DAV server URL for a provider.
// It first checks the provider configuration, then falls back to .well-known discovery.
func ResolveDAVServerURL(ctx context.Context, provider config.OAuthProvider, email, protocol string) (string, error) {
	// Check provider configuration first
	var configuredURL string
	switch protocol {
	case "caldav":
		if provider.CalDAV != nil && provider.CalDAV.URL != "" {
			configuredURL = provider.CalDAV.URL
		}
	case "carddav":
		if provider.CardDAV != nil && provider.CardDAV.URL != "" {
			configuredURL = provider.CardDAV.URL
		}
	}

	if configuredURL != "" {
		return configuredURL, nil
	}

	// Fall back to .well-known discovery using email domain
	domain := extractDomainFromEmail(email)
	if domain == "" {
		return "", fmt.Errorf("cannot extract domain from email %q", email)
	}

	return DiscoverDAVServer(ctx, domain, protocol)
}

// extractDomainFromEmail extracts the domain part from an email address.
func extractDomainFromEmail(email string) string {
	for i := len(email) - 1; i >= 0; i-- {
		if email[i] == '@' {
			return email[i+1:]
		}
	}
	return ""
}

// DAVError represents a DAV operation error with categorization.
type DAVError struct {
	Type    DAVErrorType
	Message string
	Cause   error
}

// DAVErrorType categorizes DAV errors.
type DAVErrorType string

const (
	DAVErrorAuth       DAVErrorType = "auth"       // Authentication/authorization error
	DAVErrorConnection DAVErrorType = "connection" // Connection error
	DAVErrorTimeout    DAVErrorType = "timeout"    // Operation timed out
	DAVErrorNotFound   DAVErrorType = "not_found"  // Resource not found
	DAVErrorConflict   DAVErrorType = "conflict"   // Resource conflict (e.g., ETag mismatch)
	DAVErrorProtocol   DAVErrorType = "protocol"   // Protocol-level error
)

func (e *DAVError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Type, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Type, e.Message)
}

// NewDAVError creates a new DAVError.
func NewDAVError(errType DAVErrorType, message string, cause error) *DAVError {
	return &DAVError{
		Type:    errType,
		Message: message,
		Cause:   cause,
	}
}
