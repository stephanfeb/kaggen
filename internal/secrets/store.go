// Package secrets provides secure credential storage for kaggen.
package secrets

import (
	"errors"
	"sync"

	"github.com/yourusername/kaggen/internal/config"
)

// Common errors
var (
	ErrSecretNotFound = errors.New("secret not found")
	ErrStoreNotAvailable = errors.New("secret store not available")
	ErrMasterKeyRequired = errors.New("master key required")
)

// SecretStore defines the interface for credential storage backends.
type SecretStore interface {
	// Set stores a secret value for the given key.
	Set(key, value string) error

	// Get retrieves a secret value by key.
	// Returns ErrSecretNotFound if the key doesn't exist.
	Get(key string) (string, error)

	// Delete removes a secret by key.
	Delete(key string) error

	// List returns all stored secret keys (not values).
	List() ([]string, error)

	// Available returns true if this store backend is usable.
	Available() bool

	// Name returns the backend name for logging/display.
	Name() string
}

// defaultStore is the singleton default secret store.
var (
	defaultStore     SecretStore
	defaultStoreOnce sync.Once
	defaultStoreMu   sync.RWMutex
)

// DefaultStore returns the default secret store, initializing it if needed.
// It tries keychain first, falling back to encrypted file.
func DefaultStore() SecretStore {
	defaultStoreOnce.Do(func() {
		initDefaultStore()
	})
	defaultStoreMu.RLock()
	defer defaultStoreMu.RUnlock()
	return defaultStore
}

// SetDefaultStore allows overriding the default store (useful for testing).
func SetDefaultStore(store SecretStore) {
	defaultStoreMu.Lock()
	defer defaultStoreMu.Unlock()
	defaultStore = store
}

// EncryptedFallbackStore returns an encrypted file store directly, bypassing
// the keychain availability check. Use this when you need a guaranteed
// non-keychain backend (e.g. for dashboard password storage on headless systems).
func EncryptedFallbackStore() SecretStore {
	store, err := NewEncryptedStore("")
	if err == nil && store.Available() {
		return store
	}
	return NewMemoryStore()
}

// initDefaultStore initializes the default store with the best available backend.
func initDefaultStore() {
	defaultStoreMu.Lock()
	defer defaultStoreMu.Unlock()

	// Try keychain first (best security, native UX)
	keychain := NewKeychainStore()
	if keychain.Available() {
		defaultStore = keychain
		return
	}

	// Fall back to encrypted file
	encrypted, err := NewEncryptedStore("")
	if err == nil && encrypted.Available() {
		defaultStore = encrypted
		return
	}

	// Last resort: in-memory store (not persistent, for testing only)
	defaultStore = NewMemoryStore()
}

// MemoryStore is a non-persistent in-memory store for testing.
type MemoryStore struct {
	secrets map[string]string
	mu      sync.RWMutex
}

// NewMemoryStore creates a new in-memory secret store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		secrets: make(map[string]string),
	}
}

func (m *MemoryStore) Set(key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.secrets[key] = value
	return nil
}

func (m *MemoryStore) Get(key string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if v, ok := m.secrets[key]; ok {
		return v, nil
	}
	return "", ErrSecretNotFound
}

func (m *MemoryStore) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.secrets, key)
	return nil
}

func (m *MemoryStore) List() ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	keys := make([]string, 0, len(m.secrets))
	for k := range m.secrets {
		keys = append(keys, k)
	}
	return keys, nil
}

func (m *MemoryStore) Available() bool {
	return true
}

func (m *MemoryStore) Name() string {
	return "memory"
}

// init registers the secret resolver with the config package.
func init() {
	config.RegisterSecretResolver(func(key string) (string, error) {
		store := DefaultStore()
		if store == nil || !store.Available() {
			return "", ErrStoreNotAvailable
		}
		return store.Get(key)
	})
}
