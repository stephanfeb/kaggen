// Package auth provides authentication utilities for kaggen.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

const (
	// TokenLength is the number of random bytes in a generated token.
	TokenLength = 32

	// Argon2 parameters
	argon2Time    = 1
	argon2Memory  = 64 * 1024
	argon2Threads = 4
	argon2KeyLen  = 32
	saltLength    = 16
)

// Token represents a stored authentication token.
type Token struct {
	ID        string    `json:"id"`
	Hash      []byte    `json:"hash"`
	Salt      []byte    `json:"salt"`
	Name      string    `json:"name,omitempty"` // Optional friendly name
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at,omitempty"` // Zero means no expiration
}

// TokenStore manages authentication tokens.
type TokenStore struct {
	tokens   map[string]*Token // keyed by ID
	mu       sync.RWMutex
	filepath string
}

// NewTokenStore creates a new token store.
// If filepath is provided, tokens are persisted to disk.
func NewTokenStore(filepath string) (*TokenStore, error) {
	store := &TokenStore{
		tokens:   make(map[string]*Token),
		filepath: filepath,
	}

	if filepath != "" {
		if err := store.load(); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("load tokens: %w", err)
		}
	}

	return store, nil
}

// GenerateToken creates a new random token and stores it.
// Returns the plaintext token (only shown once) and the token ID.
func (s *TokenStore) GenerateToken(name string, expiresIn time.Duration) (plaintext, id string, err error) {
	// Generate random token bytes
	tokenBytes := make([]byte, TokenLength)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", "", fmt.Errorf("generate random bytes: %w", err)
	}
	plaintext = base64.URLEncoding.EncodeToString(tokenBytes)

	// Generate salt
	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", "", fmt.Errorf("generate salt: %w", err)
	}

	// Hash the token
	hash := argon2.IDKey([]byte(plaintext), salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)

	// Generate ID
	idBytes := make([]byte, 8)
	rand.Read(idBytes)
	id = base64.URLEncoding.EncodeToString(idBytes)[:11]

	// Create token record
	token := &Token{
		ID:        id,
		Hash:      hash,
		Salt:      salt,
		Name:      name,
		CreatedAt: time.Now(),
	}
	if expiresIn > 0 {
		token.ExpiresAt = time.Now().Add(expiresIn)
	}

	// Store token
	s.mu.Lock()
	s.tokens[id] = token
	s.mu.Unlock()

	// Persist if filepath configured
	if s.filepath != "" {
		if err := s.save(); err != nil {
			return "", "", fmt.Errorf("save tokens: %w", err)
		}
	}

	return plaintext, id, nil
}

// ValidateToken checks if a plaintext token is valid.
// Uses constant-time comparison to prevent timing attacks.
func (s *TokenStore) ValidateToken(plaintext string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, token := range s.tokens {
		// Check expiration
		if !token.ExpiresAt.IsZero() && time.Now().After(token.ExpiresAt) {
			continue
		}

		// Compute hash of provided token
		computedHash := argon2.IDKey([]byte(plaintext), token.Salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)

		// Constant-time comparison
		if subtle.ConstantTimeCompare(computedHash, token.Hash) == 1 {
			return true
		}
	}

	return false
}

// RevokeToken removes a token by ID.
func (s *TokenStore) RevokeToken(id string) error {
	s.mu.Lock()
	delete(s.tokens, id)
	s.mu.Unlock()

	if s.filepath != "" {
		return s.save()
	}
	return nil
}

// ListTokens returns metadata about all tokens (not the hashes).
func (s *TokenStore) ListTokens() []TokenInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var infos []TokenInfo
	for _, t := range s.tokens {
		infos = append(infos, TokenInfo{
			ID:        t.ID,
			Name:      t.Name,
			CreatedAt: t.CreatedAt,
			ExpiresAt: t.ExpiresAt,
			Expired:   !t.ExpiresAt.IsZero() && time.Now().After(t.ExpiresAt),
		})
	}
	return infos
}

// TokenInfo contains non-sensitive token metadata.
type TokenInfo struct {
	ID        string    `json:"id"`
	Name      string    `json:"name,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	Expired   bool      `json:"expired"`
}

// HasTokens returns true if any tokens are configured.
func (s *TokenStore) HasTokens() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.tokens) > 0
}

// load reads tokens from the configured filepath.
func (s *TokenStore) load() error {
	data, err := os.ReadFile(s.filepath)
	if err != nil {
		return err
	}

	var tokens []*Token
	if err := json.Unmarshal(data, &tokens); err != nil {
		return err
	}

	s.mu.Lock()
	for _, t := range tokens {
		s.tokens[t.ID] = t
	}
	s.mu.Unlock()

	return nil
}

// save writes tokens to the configured filepath.
func (s *TokenStore) save() error {
	s.mu.RLock()
	var tokens []*Token
	for _, t := range s.tokens {
		tokens = append(tokens, t)
	}
	s.mu.RUnlock()

	data, err := json.MarshalIndent(tokens, "", "  ")
	if err != nil {
		return err
	}

	// Write with secure permissions
	return os.WriteFile(s.filepath, data, 0600)
}
