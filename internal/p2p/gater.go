package p2p

import (
	"fmt"
	"log/slog"

	"github.com/libp2p/go-libp2p/core/control"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

// PeerAllowlistGater implements ConnectionGater with PeerID allowlist support.
// When the allowlist is empty, all peers are allowed (backwards compatible).
type PeerAllowlistGater struct {
	allowedPeers map[peer.ID]struct{}
	logger       *slog.Logger
}

// NewPeerAllowlistGater creates a connection gater that restricts connections
// to the specified peer IDs. If peerIDs is empty, all peers are allowed.
func NewPeerAllowlistGater(peerIDs []string, logger *slog.Logger) (*PeerAllowlistGater, error) {
	g := &PeerAllowlistGater{
		allowedPeers: make(map[peer.ID]struct{}),
		logger:       logger,
	}

	for _, idStr := range peerIDs {
		pid, err := peer.Decode(idStr)
		if err != nil {
			return nil, fmt.Errorf("invalid peer ID %q: %w", idStr, err)
		}
		g.allowedPeers[pid] = struct{}{}
	}

	if len(g.allowedPeers) > 0 {
		logger.Info("P2P connection gater configured", "allowed_peers", len(g.allowedPeers))
	}

	return g, nil
}

// InterceptPeerDial checks if we're allowed to dial the given peer.
func (g *PeerAllowlistGater) InterceptPeerDial(p peer.ID) bool {
	if len(g.allowedPeers) == 0 {
		return true // No allowlist = allow all
	}
	_, ok := g.allowedPeers[p]
	return ok
}

// InterceptAddrDial checks if we're allowed to dial the given address.
func (g *PeerAllowlistGater) InterceptAddrDial(p peer.ID, addr ma.Multiaddr) bool {
	return g.InterceptPeerDial(p)
}

// InterceptAccept checks if we should accept an incoming connection.
// We allow at this stage since we don't know the peer ID yet.
func (g *PeerAllowlistGater) InterceptAccept(n network.ConnMultiaddrs) bool {
	return true // Allow connection, check peer after Noise handshake
}

// InterceptSecured is called after the security handshake completes.
// This is where we verify the peer ID against our allowlist.
func (g *PeerAllowlistGater) InterceptSecured(dir network.Direction, p peer.ID, addrs network.ConnMultiaddrs) bool {
	if len(g.allowedPeers) == 0 {
		return true // No allowlist = allow all
	}

	_, ok := g.allowedPeers[p]
	if !ok {
		g.logger.Warn("P2P connection rejected: peer not in allowlist",
			"peer", p,
			"direction", dir)
	}
	return ok
}

// InterceptUpgraded is called after the connection is fully upgraded.
func (g *PeerAllowlistGater) InterceptUpgraded(conn network.Conn) (bool, control.DisconnectReason) {
	return true, 0
}

// AddPeer adds a peer to the allowlist.
func (g *PeerAllowlistGater) AddPeer(p peer.ID) {
	g.allowedPeers[p] = struct{}{}
	g.logger.Info("P2P peer added to allowlist", "peer", p)
}

// RemovePeer removes a peer from the allowlist.
func (g *PeerAllowlistGater) RemovePeer(p peer.ID) {
	delete(g.allowedPeers, p)
	g.logger.Info("P2P peer removed from allowlist", "peer", p)
}

// IsAllowed checks if a peer is in the allowlist.
func (g *PeerAllowlistGater) IsAllowed(p peer.ID) bool {
	if len(g.allowedPeers) == 0 {
		return true
	}
	_, ok := g.allowedPeers[p]
	return ok
}
