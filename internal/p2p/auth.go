package p2p

import (
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/yourusername/kaggen/internal/auth"
	pb "github.com/yourusername/kaggen/internal/p2p/proto"
)

// StreamAuthenticator handles token-based authentication for P2P streams.
// It validates authentication tokens from the existing TokenStore.
type StreamAuthenticator struct {
	tokenStore   *auth.TokenStore
	authRequired bool
	logger       *slog.Logger
}

// NewStreamAuthenticator creates a new stream authenticator.
// If tokenStore is nil or authRequired is false, authentication is skipped.
func NewStreamAuthenticator(tokenStore *auth.TokenStore, authRequired bool, logger *slog.Logger) *StreamAuthenticator {
	return &StreamAuthenticator{
		tokenStore:   tokenStore,
		authRequired: authRequired,
		logger:       logger,
	}
}

// AuthResult contains the result of stream authentication.
type AuthResult struct {
	SessionID string
	UserID    string
}

// AuthenticateStream performs token handshake on a stream.
// Returns auth result on success, error on failure.
// If auth is not required or not configured, returns nil result (no error).
func (a *StreamAuthenticator) AuthenticateStream(stream network.Stream) (*AuthResult, error) {
	peerID := stream.Conn().RemotePeer()

	// Skip auth if not required
	if !a.authRequired {
		a.logger.Debug("P2P auth skipped (not required)", "peer", peerID)
		return nil, nil
	}

	// Skip auth if no token store configured
	if a.tokenStore == nil {
		a.logger.Debug("P2P auth skipped (no token store)", "peer", peerID)
		return nil, nil
	}

	// Skip auth if no tokens exist
	if !a.tokenStore.HasTokens() {
		a.logger.Debug("P2P auth skipped (no tokens configured)", "peer", peerID)
		return nil, nil
	}

	// Read auth handshake from client
	var handshake pb.AuthHandshake
	if err := ReadMessage(stream, &handshake); err != nil {
		a.logger.Warn("P2P auth: failed to read handshake",
			"peer", peerID,
			"error", err)
		return nil, fmt.Errorf("read auth handshake: %w", err)
	}

	// Validate token
	if !a.tokenStore.ValidateToken(handshake.Token) {
		a.logger.Warn("P2P auth: invalid token", "peer", peerID)

		// Send failure response
		resp := &pb.AuthResponse{
			Success: false,
			Error:   "invalid or expired authentication token",
		}
		if err := WriteMessage(stream, resp); err != nil {
			a.logger.Debug("P2P auth: failed to send error response", "error", err)
		}

		return nil, fmt.Errorf("invalid authentication token")
	}

	// Generate session info
	result := &AuthResult{
		SessionID: generateSessionID(peerID),
		UserID:    generateUserID(peerID),
	}

	// Send success response
	resp := &pb.AuthResponse{
		Success:   true,
		SessionId: result.SessionID,
		UserId:    result.UserID,
	}
	if err := WriteMessage(stream, resp); err != nil {
		return nil, fmt.Errorf("write auth response: %w", err)
	}

	a.logger.Info("P2P auth: success",
		"peer", peerID,
		"session_id", result.SessionID)

	return result, nil
}

// IsAuthRequired returns whether authentication is required.
func (a *StreamAuthenticator) IsAuthRequired() bool {
	return a.authRequired && a.tokenStore != nil && a.tokenStore.HasTokens()
}

// generateSessionID creates a session ID for the authenticated peer.
func generateSessionID(peerID peer.ID) string {
	// Use first 16 chars of peer ID for readability
	peerIDStr := peerID.String()
	if len(peerIDStr) > 16 {
		peerIDStr = peerIDStr[:16]
	}
	return peerIDStr
}

// generateUserID creates a user ID for the authenticated peer.
func generateUserID(peerID peer.ID) string {
	// Use UUID for uniqueness, prefixed with peer info
	return fmt.Sprintf("p2p-%s", uuid.New().String()[:8])
}
