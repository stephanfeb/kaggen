package p2p

import (
	"fmt"
	"log/slog"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/p2p/muxer/yamux"
	"github.com/libp2p/go-libp2p/p2p/security/noise"
	"github.com/libp2p/go-libp2p/p2p/transport/tcp"
	udxtransport "github.com/stephanfeb/go-libp2p-udx-transport"

	"github.com/yourusername/kaggen/internal/config"
)

// CreateHost creates a libp2p host with the specified configuration.
func CreateHost(cfg *config.P2PConfig, priv crypto.PrivKey, logger *slog.Logger) (host.Host, error) {
	opts := []libp2p.Option{
		libp2p.Identity(priv),
		libp2p.Security(noise.ID, noise.New),
		libp2p.Muxer("/yamux/1.0.0", yamux.DefaultTransport),
	}

	// Configure transports
	transports := cfg.Transports
	if len(transports) == 0 {
		transports = []string{"udx"} // Default to UDX for mobile clients
	}

	port := cfg.Port
	if port == 0 {
		port = 4001 // Default libp2p port
	}

	hasTransport := false
	for _, t := range transports {
		switch t {
		case "udx":
			opts = append(opts,
				libp2p.NoTransports,
				libp2p.Transport(udxtransport.NewTransport),
				libp2p.ListenAddrStrings(fmt.Sprintf("/ip4/0.0.0.0/udp/%d/udx", port)),
				libp2p.ResourceManager(&network.NullResourceManager{}),
			)
			hasTransport = true
			logger.Info("configured UDX transport", "port", port)
		case "tcp":
			if !hasTransport {
				// Only add TCP transport if UDX wasn't added
				opts = append(opts,
					libp2p.Transport(tcp.NewTCPTransport),
				)
			} else {
				// Add TCP as additional transport
				opts = append(opts, libp2p.Transport(tcp.NewTCPTransport))
			}
			opts = append(opts, libp2p.ListenAddrStrings(fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", port)))
			hasTransport = true
			logger.Info("configured TCP transport", "port", port)
		default:
			logger.Warn("unknown transport, skipping", "transport", t)
		}
	}

	// Relay configuration
	if cfg.RelayEnabled {
		opts = append(opts, libp2p.EnableRelay())
		logger.Info("relay enabled")
	} else {
		opts = append(opts, libp2p.DisableRelay())
	}

	return libp2p.New(opts...)
}
