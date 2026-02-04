package secrets

import (
	"encoding/json"
	"strings"

	"github.com/zalando/go-keyring"
)

const (
	// ServiceName is the keychain service identifier for kaggen.
	ServiceName = "kaggen"
	// KeyListKey is the special key used to store the list of all secret keys.
	KeyListKey = "__kaggen_keys__"
)

// KeychainStore implements SecretStore using the OS keychain.
type KeychainStore struct {
	available bool
}

// NewKeychainStore creates a new keychain-backed secret store.
func NewKeychainStore() *KeychainStore {
	store := &KeychainStore{}
	// Test if keychain is available by trying a benign operation
	store.available = store.testAvailability()
	return store
}

// testAvailability checks if the keychain is accessible.
func (k *KeychainStore) testAvailability() bool {
	// Try to get a non-existent key - if we get "not found" error,
	// the keychain is available. Other errors mean it's not accessible.
	_, err := keyring.Get(ServiceName, "__test_availability__")
	if err == nil {
		return true
	}
	// Check if error is "not found" (expected) vs access denied
	errStr := err.Error()
	if strings.Contains(errStr, "not found") ||
		strings.Contains(errStr, "does not exist") ||
		strings.Contains(errStr, "secret not found") {
		return true
	}
	// On Linux without a keyring service, we get specific errors
	if strings.Contains(errStr, "service not available") ||
		strings.Contains(errStr, "cannot open display") ||
		strings.Contains(errStr, "dbus") {
		return false
	}
	// Assume available if we can't determine otherwise
	return true
}

func (k *KeychainStore) Set(key, value string) error {
	if !k.available {
		return ErrStoreNotAvailable
	}

	// Store the secret
	if err := keyring.Set(ServiceName, key, value); err != nil {
		return err
	}

	// Update the key list
	return k.addToKeyList(key)
}

func (k *KeychainStore) Get(key string) (string, error) {
	if !k.available {
		return "", ErrStoreNotAvailable
	}

	value, err := keyring.Get(ServiceName, key)
	if err != nil {
		if strings.Contains(err.Error(), "not found") ||
			strings.Contains(err.Error(), "does not exist") {
			return "", ErrSecretNotFound
		}
		return "", err
	}
	return value, nil
}

func (k *KeychainStore) Delete(key string) error {
	if !k.available {
		return ErrStoreNotAvailable
	}

	// Delete the secret
	if err := keyring.Delete(ServiceName, key); err != nil {
		// Ignore "not found" errors on delete
		if !strings.Contains(err.Error(), "not found") &&
			!strings.Contains(err.Error(), "does not exist") {
			return err
		}
	}

	// Update the key list
	return k.removeFromKeyList(key)
}

func (k *KeychainStore) List() ([]string, error) {
	if !k.available {
		return nil, ErrStoreNotAvailable
	}

	// Get the key list
	data, err := keyring.Get(ServiceName, KeyListKey)
	if err != nil {
		if strings.Contains(err.Error(), "not found") ||
			strings.Contains(err.Error(), "does not exist") {
			return []string{}, nil
		}
		return nil, err
	}

	var keys []string
	if err := json.Unmarshal([]byte(data), &keys); err != nil {
		return nil, err
	}
	return keys, nil
}

func (k *KeychainStore) Available() bool {
	return k.available
}

func (k *KeychainStore) Name() string {
	return "keychain"
}

// addToKeyList adds a key to the stored key list.
func (k *KeychainStore) addToKeyList(key string) error {
	keys, err := k.List()
	if err != nil {
		keys = []string{}
	}

	// Check if key already exists
	for _, existing := range keys {
		if existing == key {
			return nil
		}
	}

	keys = append(keys, key)
	data, err := json.Marshal(keys)
	if err != nil {
		return err
	}
	return keyring.Set(ServiceName, KeyListKey, string(data))
}

// removeFromKeyList removes a key from the stored key list.
func (k *KeychainStore) removeFromKeyList(key string) error {
	keys, err := k.List()
	if err != nil {
		return nil // No list to update
	}

	newKeys := make([]string, 0, len(keys))
	for _, existing := range keys {
		if existing != key {
			newKeys = append(newKeys, existing)
		}
	}

	if len(newKeys) == 0 {
		// Delete the key list entry
		keyring.Delete(ServiceName, KeyListKey)
		return nil
	}

	data, err := json.Marshal(newKeys)
	if err != nil {
		return err
	}
	return keyring.Set(ServiceName, KeyListKey, string(data))
}
