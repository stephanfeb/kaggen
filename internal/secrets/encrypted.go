package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/crypto/argon2"
)

const (
	// Argon2 parameters for key derivation
	argon2Time    = 1
	argon2Memory  = 64 * 1024 // 64 MB
	argon2Threads = 4
	argon2KeyLen  = 32 // AES-256

	// Salt size for key derivation
	saltSize = 16

	// Nonce size for AES-GCM
	nonceSize = 12
)

// EncryptedStore implements SecretStore using an encrypted file.
type EncryptedStore struct {
	filePath  string
	masterKey []byte
	available bool
	mu        sync.RWMutex
}

// encryptedData is the structure stored in the encrypted file.
type encryptedData struct {
	Salt       []byte            `json:"salt"`
	Nonce      []byte            `json:"nonce"`
	Ciphertext []byte            `json:"ciphertext"`
	Secrets    map[string]string `json:"-"` // Decrypted in memory only
}

// NewEncryptedStore creates a new encrypted file-backed secret store.
// If filePath is empty, uses ~/.kaggen/secrets.enc
func NewEncryptedStore(filePath string) (*EncryptedStore, error) {
	if filePath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get home directory: %w", err)
		}
		filePath = filepath.Join(home, ".kaggen", "secrets.enc")
	}

	store := &EncryptedStore{
		filePath: filePath,
	}

	// Load master key from file or env var (deprecated)
	masterKey, err := LoadMasterKey()
	if err != nil {
		store.available = false
		return store, nil
	}
	store.masterKey = masterKey
	store.available = true

	return store, nil
}

func (e *EncryptedStore) Set(key, value string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.available {
		return ErrMasterKeyRequired
	}

	// Load existing secrets
	secrets, salt, err := e.loadSecrets()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to load secrets: %w", err)
	}
	if secrets == nil {
		secrets = make(map[string]string)
	}

	// Generate new salt if this is a new file
	if salt == nil {
		salt = make([]byte, saltSize)
		if _, err := io.ReadFull(rand.Reader, salt); err != nil {
			return fmt.Errorf("failed to generate salt: %w", err)
		}
	}

	// Update secrets
	secrets[key] = value

	// Save back
	return e.saveSecrets(secrets, salt)
}

func (e *EncryptedStore) Get(key string) (string, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if !e.available {
		return "", ErrMasterKeyRequired
	}

	secrets, _, err := e.loadSecrets()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrSecretNotFound
		}
		return "", err
	}

	value, ok := secrets[key]
	if !ok {
		return "", ErrSecretNotFound
	}
	return value, nil
}

func (e *EncryptedStore) Delete(key string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.available {
		return ErrMasterKeyRequired
	}

	secrets, salt, err := e.loadSecrets()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // Nothing to delete
		}
		return err
	}

	delete(secrets, key)

	if len(secrets) == 0 {
		// Remove the file if no secrets left
		return os.Remove(e.filePath)
	}

	return e.saveSecrets(secrets, salt)
}

func (e *EncryptedStore) List() ([]string, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if !e.available {
		return nil, ErrMasterKeyRequired
	}

	secrets, _, err := e.loadSecrets()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []string{}, nil
		}
		return nil, err
	}

	keys := make([]string, 0, len(secrets))
	for k := range secrets {
		keys = append(keys, k)
	}
	return keys, nil
}

func (e *EncryptedStore) Available() bool {
	return e.available
}

func (e *EncryptedStore) Name() string {
	return "encrypted-file"
}

// loadSecrets decrypts and loads secrets from the file.
func (e *EncryptedStore) loadSecrets() (map[string]string, []byte, error) {
	data, err := os.ReadFile(e.filePath)
	if err != nil {
		return nil, nil, err
	}

	var enc encryptedData
	if err := json.Unmarshal(data, &enc); err != nil {
		return nil, nil, fmt.Errorf("failed to parse encrypted file: %w", err)
	}

	// Derive key from master key and salt
	derivedKey := argon2.IDKey(e.masterKey, enc.Salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)

	// Decrypt
	block, err := aes.NewCipher(derivedKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	plaintext, err := gcm.Open(nil, enc.Nonce, enc.Ciphertext, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to decrypt (wrong master key?): %w", err)
	}

	var secrets map[string]string
	if err := json.Unmarshal(plaintext, &secrets); err != nil {
		return nil, nil, fmt.Errorf("failed to parse decrypted secrets: %w", err)
	}

	return secrets, enc.Salt, nil
}

// saveSecrets encrypts and saves secrets to the file.
func (e *EncryptedStore) saveSecrets(secrets map[string]string, salt []byte) error {
	// Ensure directory exists
	dir := filepath.Dir(e.filePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create secrets directory: %w", err)
	}

	// Serialize secrets
	plaintext, err := json.Marshal(secrets)
	if err != nil {
		return fmt.Errorf("failed to serialize secrets: %w", err)
	}

	// Derive key from master key and salt
	derivedKey := argon2.IDKey(e.masterKey, salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)

	// Create cipher
	block, err := aes.NewCipher(derivedKey)
	if err != nil {
		return fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("failed to create GCM: %w", err)
	}

	// Generate nonce
	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	// Create encrypted data structure
	enc := encryptedData{
		Salt:       salt,
		Nonce:      nonce,
		Ciphertext: ciphertext,
	}

	// Serialize to JSON
	data, err := json.Marshal(enc)
	if err != nil {
		return fmt.Errorf("failed to serialize encrypted data: %w", err)
	}

	// Write to file with secure permissions
	if err := os.WriteFile(e.filePath, data, 0600); err != nil {
		return fmt.Errorf("failed to write secrets file: %w", err)
	}

	return nil
}
