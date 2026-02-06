// Package p2p provides libp2p connectivity for mobile clients.
package p2p

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/yourusername/kaggen/internal/config"
)

// DefaultIdentityPath is the default path for the P2P identity key.
const DefaultIdentityPath = "~/.kaggen/p2p/identity.key"

// LoadOrCreateIdentity loads an existing Ed25519 private key from disk,
// or generates and persists a new one if none exists.
func LoadOrCreateIdentity(path string) (crypto.PrivKey, error) {
	path = config.ExpandPath(path)

	// Try to load existing key
	if data, err := os.ReadFile(path); err == nil {
		priv, err := crypto.UnmarshalEd25519PrivateKey(data)
		if err != nil {
			return nil, fmt.Errorf("unmarshal identity: %w", err)
		}
		return priv, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read identity: %w", err)
	}

	// Generate new Ed25519 key
	priv, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}

	// Ensure directory exists with secure permissions
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create p2p dir: %w", err)
	}

	// Get raw key bytes for storage
	raw, err := priv.Raw()
	if err != nil {
		return nil, fmt.Errorf("marshal key: %w", err)
	}

	// Persist key with secure permissions (owner-only read/write)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		return nil, fmt.Errorf("save identity: %w", err)
	}

	return priv, nil
}

// PeerIDFromIdentity derives the peer ID from a private key.
func PeerIDFromIdentity(priv crypto.PrivKey) (peer.ID, error) {
	return peer.IDFromPrivateKey(priv)
}
