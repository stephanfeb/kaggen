package oauth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/crypto/argon2"

	"github.com/yourusername/kaggen/internal/secrets"
)

const (
	// Argon2 parameters for key derivation (same as secrets package)
	argon2Time    = 1
	argon2Memory  = 64 * 1024 // 64 MB
	argon2Threads = 4
	argon2KeyLen  = 32 // AES-256

	// Salt and nonce sizes
	saltSize  = 16
	nonceSize = 12

	// Auto-refresh tokens this long before expiry
	refreshBuffer = 5 * time.Minute
)

// SQLiteStore implements TokenStore using SQLite with AES-256-GCM encryption.
type SQLiteStore struct {
	db          *sql.DB
	masterKey   []byte
	salt        []byte // derived once at store creation
	derivedKey  []byte // cached derived key
	refreshFunc RefreshFunc
	mu          sync.RWMutex
}

// NewSQLiteStore creates a new SQLite-backed token store.
// The dbPath is the path to the SQLite database file.
// Tokens are encrypted using the master key from ~/.kaggen/master.key.
func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	masterKey, err := secrets.LoadMasterKey()
	if err != nil {
		return nil, fmt.Errorf("master key required for OAuth token encryption: %w", err)
	}

	return NewSQLiteStoreWithKey(dbPath, masterKey)
}

// NewSQLiteStoreWithKey creates a new SQLite-backed token store with an explicit key.
func NewSQLiteStoreWithKey(dbPath string, masterKey []byte) (*SQLiteStore, error) {
	// Ensure directory exists
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("oauth: failed to create token db directory: %w", err)
	}

	// Open database
	db, err := sql.Open("sqlite3", dbPath+"?_busy_timeout=5000&_journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("oauth: failed to open token db: %w", err)
	}

	// Set file permissions
	if err := os.Chmod(dbPath, 0600); err != nil && !os.IsNotExist(err) {
		db.Close()
		return nil, fmt.Errorf("oauth: failed to set db permissions: %w", err)
	}

	// Create schema
	if err := createSchema(db); err != nil {
		db.Close()
		return nil, err
	}

	// Generate or load salt
	salt, err := getOrCreateSalt(db)
	if err != nil {
		db.Close()
		return nil, err
	}

	// Derive encryption key
	derivedKey := argon2.IDKey(masterKey, salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)

	return &SQLiteStore{
		db:         db,
		masterKey:  masterKey,
		salt:       salt,
		derivedKey: derivedKey,
	}, nil
}

func createSchema(db *sql.DB) error {
	schema := `
		CREATE TABLE IF NOT EXISTS oauth_tokens (
			user_id TEXT NOT NULL,
			provider TEXT NOT NULL,
			access_token_enc BLOB NOT NULL,
			refresh_token_enc BLOB,
			token_type TEXT NOT NULL DEFAULT 'Bearer',
			expires_at DATETIME NOT NULL,
			scopes TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (user_id, provider)
		);

		CREATE TABLE IF NOT EXISTS oauth_meta (
			key TEXT PRIMARY KEY,
			value BLOB
		);

		CREATE INDEX IF NOT EXISTS idx_oauth_tokens_expires
			ON oauth_tokens(expires_at);
	`
	_, err := db.Exec(schema)
	if err != nil {
		return fmt.Errorf("oauth: failed to create schema: %w", err)
	}
	return nil
}

func getOrCreateSalt(db *sql.DB) ([]byte, error) {
	var salt []byte
	err := db.QueryRow("SELECT value FROM oauth_meta WHERE key = 'salt'").Scan(&salt)
	if err == sql.ErrNoRows {
		// Generate new salt
		salt = make([]byte, saltSize)
		if _, err := io.ReadFull(rand.Reader, salt); err != nil {
			return nil, fmt.Errorf("oauth: failed to generate salt: %w", err)
		}
		_, err = db.Exec("INSERT INTO oauth_meta (key, value) VALUES ('salt', ?)", salt)
		if err != nil {
			return nil, fmt.Errorf("oauth: failed to store salt: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("oauth: failed to get salt: %w", err)
	}
	return salt, nil
}

// SetRefresher sets the function used to refresh expired tokens.
func (s *SQLiteStore) SetRefresher(fn RefreshFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshFunc = fn
}

// Get retrieves a token for the given user and provider.
func (s *SQLiteStore) Get(userID, provider string) (*Token, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var accessEnc, refreshEnc []byte
	var tokenType, scopesJSON string
	var expiresAt time.Time

	err := s.db.QueryRow(`
		SELECT access_token_enc, refresh_token_enc, token_type, expires_at, scopes
		FROM oauth_tokens
		WHERE user_id = ? AND provider = ?
	`, userID, provider).Scan(&accessEnc, &refreshEnc, &tokenType, &expiresAt, &scopesJSON)

	if err == sql.ErrNoRows {
		return nil, ErrTokenNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("oauth: failed to get token: %w", err)
	}

	// Decrypt access token
	accessToken, err := s.decrypt(accessEnc)
	if err != nil {
		return nil, fmt.Errorf("oauth: failed to decrypt access token: %w", err)
	}

	// Decrypt refresh token (may be nil)
	var refreshToken string
	if refreshEnc != nil {
		refreshToken, err = s.decrypt(refreshEnc)
		if err != nil {
			return nil, fmt.Errorf("oauth: failed to decrypt refresh token: %w", err)
		}
	}

	// Parse scopes
	var scopes []string
	if scopesJSON != "" {
		if err := json.Unmarshal([]byte(scopesJSON), &scopes); err != nil {
			// Ignore parse errors, scopes are optional
			scopes = nil
		}
	}

	token := &Token{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    tokenType,
		ExpiresAt:    expiresAt,
		Scopes:       scopes,
	}

	// Check if token needs refresh
	if token.ExpiresWithin(refreshBuffer) && token.CanRefresh() && s.refreshFunc != nil {
		s.mu.RUnlock()
		newToken, err := s.tryRefresh(userID, provider, token)
		s.mu.RLock()
		if err == nil {
			return newToken, nil
		}
		// If refresh failed but token not yet expired, return it anyway
		if !token.IsExpired() {
			return token, nil
		}
		return nil, ErrTokenExpired
	}

	if token.IsExpired() {
		return nil, ErrTokenExpired
	}

	return token, nil
}

// tryRefresh attempts to refresh the token and store the result.
func (s *SQLiteStore) tryRefresh(userID, provider string, current *Token) (*Token, error) {
	s.mu.Lock()
	refreshFunc := s.refreshFunc
	s.mu.Unlock()

	if refreshFunc == nil {
		return nil, ErrNoRefreshToken
	}

	newToken, err := refreshFunc(userID, provider, current)
	if err != nil {
		return nil, err
	}

	// Store the refreshed token
	if err := s.Store(userID, provider, newToken); err != nil {
		// Log but don't fail - we have the new token
		return newToken, nil
	}

	return newToken, nil
}

// Store saves a token for the given user and provider.
func (s *SQLiteStore) Store(userID, provider string, token *Token) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Encrypt access token
	accessEnc, err := s.encrypt(token.AccessToken)
	if err != nil {
		return fmt.Errorf("oauth: failed to encrypt access token: %w", err)
	}

	// Encrypt refresh token (may be empty)
	var refreshEnc []byte
	if token.RefreshToken != "" {
		refreshEnc, err = s.encrypt(token.RefreshToken)
		if err != nil {
			return fmt.Errorf("oauth: failed to encrypt refresh token: %w", err)
		}
	}

	// Serialize scopes
	var scopesJSON string
	if len(token.Scopes) > 0 {
		data, _ := json.Marshal(token.Scopes)
		scopesJSON = string(data)
	}

	_, err = s.db.Exec(`
		INSERT INTO oauth_tokens (user_id, provider, access_token_enc, refresh_token_enc, token_type, expires_at, scopes, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(user_id, provider) DO UPDATE SET
			access_token_enc = excluded.access_token_enc,
			refresh_token_enc = excluded.refresh_token_enc,
			token_type = excluded.token_type,
			expires_at = excluded.expires_at,
			scopes = excluded.scopes,
			updated_at = CURRENT_TIMESTAMP
	`, userID, provider, accessEnc, refreshEnc, token.TokenType, token.ExpiresAt, scopesJSON)

	if err != nil {
		return fmt.Errorf("oauth: failed to store token: %w", err)
	}

	return nil
}

// Delete removes a token for the given user and provider.
func (s *SQLiteStore) Delete(userID, provider string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
		DELETE FROM oauth_tokens WHERE user_id = ? AND provider = ?
	`, userID, provider)

	if err != nil {
		return fmt.Errorf("oauth: failed to delete token: %w", err)
	}

	return nil
}

// List returns all provider names with stored tokens for a user.
func (s *SQLiteStore) List(userID string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT provider FROM oauth_tokens WHERE user_id = ?
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("oauth: failed to list tokens: %w", err)
	}
	defer rows.Close()

	var providers []string
	for rows.Next() {
		var provider string
		if err := rows.Scan(&provider); err != nil {
			return nil, fmt.Errorf("oauth: failed to scan provider: %w", err)
		}
		providers = append(providers, provider)
	}

	return providers, rows.Err()
}

// NeedsAuth returns which of the required providers lack valid tokens.
func (s *SQLiteStore) NeedsAuth(userID string, requiredProviders []string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var missing []string
	for _, provider := range requiredProviders {
		var expiresAt time.Time
		err := s.db.QueryRow(`
			SELECT expires_at FROM oauth_tokens WHERE user_id = ? AND provider = ?
		`, userID, provider).Scan(&expiresAt)

		if err == sql.ErrNoRows || time.Now().After(expiresAt) {
			missing = append(missing, provider)
		}
	}

	return missing
}

// Close closes the database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// encrypt encrypts data using AES-256-GCM.
func (s *SQLiteStore) encrypt(plaintext string) ([]byte, error) {
	block, err := aes.NewCipher(s.derivedKey)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return ciphertext, nil
}

// decrypt decrypts data using AES-256-GCM.
func (s *SQLiteStore) decrypt(data []byte) (string, error) {
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	block, err := aes.NewCipher(s.derivedKey)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}
