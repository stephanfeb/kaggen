package secrets

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

const (
	masterKeyFileName = "master.key"
	masterKeyLength   = 32 // 256-bit key
)

var (
	resolvedMasterKey     []byte
	resolvedMasterKeyOnce sync.Once
	resolvedMasterKeyErr  error
)

// LoadMasterKey resolves the master key once, with this priority:
//  1. ~/.kaggen/master.key file (hex-encoded, 0600)
//  2. KAGGEN_MASTER_KEY env var (deprecated, logs warning)
//  3. Auto-generate and write to ~/.kaggen/master.key
func LoadMasterKey() ([]byte, error) {
	resolvedMasterKeyOnce.Do(func() {
		resolvedMasterKey, resolvedMasterKeyErr = loadMasterKeyInner()
	})
	return resolvedMasterKey, resolvedMasterKeyErr
}

func masterKeyFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".kaggen", masterKeyFileName), nil
}

func loadMasterKeyInner() ([]byte, error) {
	// 1. Try file first
	keyPath, err := masterKeyFilePath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(keyPath)
	if err == nil {
		// Warn if permissions are too open
		info, statErr := os.Stat(keyPath)
		if statErr == nil && info.Mode().Perm() != 0600 {
			slog.Warn("master key file has loose permissions, should be 0600",
				"path", keyPath, "mode", fmt.Sprintf("%04o", info.Mode().Perm()))
		}

		// Try hex-encoded first (preferred format)
		trimmed := bytes.TrimSpace(data)
		if key, decErr := hex.DecodeString(string(trimmed)); decErr == nil && len(key) == masterKeyLength {
			slog.Info("master key loaded from file", "path", keyPath)
			return key, nil
		}

		// Fall back to raw bytes (backwards compat if someone wrote raw content)
		if len(trimmed) > 0 {
			slog.Info("master key loaded from file (raw)", "path", keyPath)
			return trimmed, nil
		}
	}

	// 2. Env var fallback (deprecated)
	envKey := os.Getenv("KAGGEN_MASTER_KEY")
	if envKey != "" {
		slog.Warn("KAGGEN_MASTER_KEY env var is deprecated; migrate to ~/.kaggen/master.key")
		return []byte(envKey), nil
	}

	// 3. Auto-generate
	key := make([]byte, masterKeyLength)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("failed to generate master key: %w", err)
	}

	// Ensure directory exists
	dir := filepath.Dir(keyPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create config directory: %w", err)
	}

	// Write hex-encoded key with strict permissions
	if err := os.WriteFile(keyPath, []byte(hex.EncodeToString(key)), 0600); err != nil {
		return nil, fmt.Errorf("failed to write master key file: %w", err)
	}

	slog.Info("generated new master key", "path", keyPath)
	return key, nil
}
