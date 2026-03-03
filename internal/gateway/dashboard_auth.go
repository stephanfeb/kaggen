package gateway

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/zalando/go-keyring"
	"golang.org/x/crypto/argon2"

	"github.com/yourusername/kaggen/internal/secrets"
)

const (
	// Keychain identifiers
	keychainService     = "kaggen"
	keychainPasswordKey = "dashboard-password"

	// Fallback key in encrypted secret store
	dashboardPasswordSecretKey = "__dashboard_password__"

	// Session settings
	sessionCookieName = "kaggen_session"
	sessionLength     = 32

	// Argon2 parameters (same as token.go)
	argon2Time    = 1
	argon2Memory  = 64 * 1024
	argon2Threads = 4
	argon2KeyLen  = 32
	saltLength    = 16
)

// DashboardAuth manages dashboard authentication with password and sessions.
type DashboardAuth struct {
	sessions    map[string]*Session
	secretStore secrets.SecretStore // fallback when keychain is unavailable
	mu          sync.RWMutex
}

// Session represents an authenticated dashboard session.
type Session struct {
	ID        string
	CreatedAt time.Time
}

// storedPassword holds the hashed password in keychain.
type storedPassword struct {
	Hash []byte `json:"hash"`
	Salt []byte `json:"salt"`
}

// NewDashboardAuth creates a new dashboard authentication manager.
// The secretStore is used as a fallback when the OS keychain is unavailable
// (e.g. headless/SSH sessions on macOS).
func NewDashboardAuth(secretStore secrets.SecretStore) *DashboardAuth {
	return &DashboardAuth{
		sessions:    make(map[string]*Session),
		secretStore: secretStore,
	}
}

// IsPasswordSet checks if a dashboard password has been configured.
func (a *DashboardAuth) IsPasswordSet() bool {
	// Try keychain first
	_, err := keyring.Get(keychainService, keychainPasswordKey)
	if err == nil {
		slog.Debug("dashboard password found in keychain")
		return true
	}
	if err == keyring.ErrNotFound {
		slog.Debug("dashboard password not found in keychain")
		// Keychain works but no password — check fallback too
		return a.isPasswordInFallback()
	}
	// Keychain inaccessible (headless) — check fallback
	slog.Debug("keychain inaccessible, checking fallback store", "error", err)
	return a.isPasswordInFallback()
}

// isPasswordInFallback checks the encrypted secret store for the password hash.
func (a *DashboardAuth) isPasswordInFallback() bool {
	if a.secretStore == nil || !a.secretStore.Available() {
		return false
	}
	_, err := a.secretStore.Get(dashboardPasswordSecretKey)
	return err == nil
}

// SetPassword stores a new dashboard password (hashed).
// Tries the OS keychain first; falls back to the encrypted secret store.
func (a *DashboardAuth) SetPassword(password string) error {
	// Generate salt
	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return err
	}

	// Hash password
	hash := argon2.IDKey([]byte(password), salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)

	// Serialize
	stored := storedPassword{Hash: hash, Salt: salt}
	data, err := json.Marshal(stored)
	if err != nil {
		return err
	}

	// Try keychain first
	err = keyring.Set(keychainService, keychainPasswordKey, string(data))
	if err == nil {
		return nil
	}

	// Keychain failed — use fallback
	slog.Warn("keychain unavailable for password storage, using encrypted file fallback", "error", err)
	if a.secretStore == nil || !a.secretStore.Available() {
		return fmt.Errorf("no available store for dashboard password (keychain error: %w)", err)
	}
	return a.secretStore.Set(dashboardPasswordSecretKey, string(data))
}

// ValidatePassword checks if the provided password matches the stored hash.
func (a *DashboardAuth) ValidatePassword(password string) bool {
	data := a.loadPasswordData()
	if data == "" {
		return false
	}

	var stored storedPassword
	if err := json.Unmarshal([]byte(data), &stored); err != nil {
		slog.Warn("failed to parse stored password", "error", err)
		return false
	}

	// Compute hash of provided password
	computedHash := argon2.IDKey([]byte(password), stored.Salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)

	// Constant-time comparison
	return subtle.ConstantTimeCompare(computedHash, stored.Hash) == 1
}

// loadPasswordData retrieves the serialized password hash from keychain or fallback.
func (a *DashboardAuth) loadPasswordData() string {
	// Try keychain
	data, err := keyring.Get(keychainService, keychainPasswordKey)
	if err == nil {
		return data
	}

	// Try fallback
	if a.secretStore != nil && a.secretStore.Available() {
		data, err = a.secretStore.Get(dashboardPasswordSecretKey)
		if err == nil {
			return data
		}
	}

	slog.Warn("dashboard password not found in keychain or fallback", "error", err)
	return ""
}

// CreateSession creates a new authenticated session and returns the session token.
func (a *DashboardAuth) CreateSession() (string, error) {
	// Generate random session token
	tokenBytes := make([]byte, sessionLength)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", err
	}
	token := base64.URLEncoding.EncodeToString(tokenBytes)

	session := &Session{
		ID:        token,
		CreatedAt: time.Now(),
	}

	a.mu.Lock()
	a.sessions[token] = session
	a.mu.Unlock()

	return token, nil
}

// ValidateSession checks if a session token is valid.
func (a *DashboardAuth) ValidateSession(token string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()

	_, exists := a.sessions[token]
	return exists
}

// DestroySession removes a session.
func (a *DashboardAuth) DestroySession(token string) {
	a.mu.Lock()
	delete(a.sessions, token)
	a.mu.Unlock()
}

// RequireAuth is middleware that checks for valid session cookie.
func (a *DashboardAuth) RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || !a.ValidateSession(cookie.Value) {
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, `{"error":"unauthorized","message":"Please log in to access this resource"}`, http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// SetSessionCookie sets the session cookie on the response.
func (a *DashboardAuth) SetSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	cookie := &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	}

	// Set Secure flag if request is over HTTPS
	if r.TLS != nil {
		cookie.Secure = true
	}

	http.SetCookie(w, cookie)
}

// ClearSessionCookie clears the session cookie.
func (a *DashboardAuth) ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})
}
