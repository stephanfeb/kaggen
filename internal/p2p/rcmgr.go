package p2p

import (
	"log/slog"

	"github.com/libp2p/go-libp2p/core/network"
	rcmgr "github.com/libp2p/go-libp2p/p2p/host/resource-manager"

	"github.com/yourusername/kaggen/internal/config"
)

// createResourceManager creates a libp2p resource manager with limits from config.
// If no limits are configured, it uses sensible defaults with auto-scaling.
func createResourceManager(cfg *config.P2PConfig, logger *slog.Logger) (network.ResourceManager, error) {
	// Start with default scaling limits
	scalingLimits := rcmgr.DefaultLimits

	// Apply custom connection limits
	if cfg.MaxConnections > 0 {
		scalingLimits.SystemBaseLimit.Conns = cfg.MaxConnections
		scalingLimits.SystemBaseLimit.ConnsInbound = cfg.MaxConnections / 2
		scalingLimits.SystemBaseLimit.ConnsOutbound = cfg.MaxConnections / 2
	}

	// Apply custom stream limits
	if cfg.MaxConcurrentStreams > 0 {
		scalingLimits.SystemBaseLimit.Streams = cfg.MaxConcurrentStreams
		scalingLimits.SystemBaseLimit.StreamsInbound = cfg.MaxConcurrentStreams / 2
		scalingLimits.SystemBaseLimit.StreamsOutbound = cfg.MaxConcurrentStreams / 2
	}

	// Apply per-peer stream limits
	if cfg.MaxStreamsPerPeer > 0 {
		scalingLimits.PeerBaseLimit.Streams = cfg.MaxStreamsPerPeer
		scalingLimits.PeerBaseLimit.StreamsInbound = cfg.MaxStreamsPerPeer / 2
		scalingLimits.PeerBaseLimit.StreamsOutbound = cfg.MaxStreamsPerPeer / 2
	}

	// AutoScale adjusts limits based on system resources (memory, FDs)
	limiter := rcmgr.NewFixedLimiter(scalingLimits.AutoScale())

	rm, err := rcmgr.NewResourceManager(limiter)
	if err != nil {
		return nil, err
	}

	logger.Info("P2P resource manager configured",
		"max_connections", scalingLimits.SystemBaseLimit.Conns,
		"max_streams", scalingLimits.SystemBaseLimit.Streams,
		"max_streams_per_peer", scalingLimits.PeerBaseLimit.Streams)

	return rm, nil
}
