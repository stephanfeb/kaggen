package gateway

import (
	"context"
	"fmt"
	"log/slog"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"

	kaggenAgent "github.com/yourusername/kaggen/internal/agent"
	"github.com/yourusername/kaggen/internal/auth"
	"github.com/yourusername/kaggen/internal/channel"
	"github.com/yourusername/kaggen/internal/config"
	"github.com/yourusername/kaggen/internal/p2p"
	"github.com/yourusername/kaggen/internal/proactive"
	"github.com/yourusername/kaggen/internal/pubsub"
	"github.com/yourusername/kaggen/internal/tunnel"
)

const (
	// AppName is the name of the application for the runner.
	AppName = "kaggen"
)

// channelMap implements proactive.ChannelResolver.
type channelMap struct {
	channels map[string]channel.Channel
}

func (m *channelMap) Channel(name string) channel.Channel {
	return m.channels[name]
}

// Server is the gateway server that routes messages between channels and the agent.
type Server struct {
	config       *config.Config
	router       *channel.Router
	handler      *Handler
	wsChannel    *channel.WebSocketChannel
	tgChannel    *channel.TelegramChannel
	proactive    *proactive.Engine
	dashboard    *DashboardAPI
	tunnel       *tunnel.CloudflareTunnel
	pubsubBridge *pubsub.Bridge
	p2pNode      *p2p.Node
	callbackURL  string // resolved callback base URL
	logger       *slog.Logger
}

// NewServer creates a new gateway server.
func NewServer(cfg *config.Config, sessionService session.Service, ag agent.Agent, logger *slog.Logger, dashboard *DashboardAPI, memService ...memory.Service) *Server {
	// Extract ThreadForker from the session service if it supports forking.
	var forker ThreadForker
	if f, ok := sessionService.(ThreadForker); ok {
		forker = f
	}
	// Extract InFlightStore, AuditStore, and GuardedRunner from the agent if available.
	var inFlight *kaggenAgent.InFlightStore
	var auditStore *kaggenAgent.AuditStore
	var guardedRunner *kaggenAgent.GuardedSkillRunner
	if ap, ok := ag.(*kaggenAgent.AgentProvider); ok {
		inFlight = ap.InFlightStore()
		auditStore = ap.AuditStore()
		guardedRunner = ap.GuardedRunner()
	}
	handler := NewHandler(AppName, ag, sessionService, logger, forker, inFlight, auditStore, guardedRunner, memService...)
	router := channel.NewRouter(handler)

	addr := fmt.Sprintf("%s:%d", cfg.Gateway.Bind, cfg.Gateway.Port)

	// Configure WebSocket channel with auth and TLS if enabled
	wsOpts := channel.WebSocketChannelOptions{
		AllowedOrigins: cfg.Gateway.GetAllowedOrigins(),
		AuthRequired:   cfg.Security.Auth.Enabled,
		TLSCertFile:    cfg.Gateway.TLS.CertFile,
		TLSKeyFile:     cfg.Gateway.TLS.KeyFile,
	}

	// Initialize token validator if auth is enabled
	if cfg.Security.Auth.Enabled {
		tokenFile := cfg.Security.Auth.TokenFile
		if tokenFile == "" {
			tokenFile = config.ExpandPath("~/.kaggen/tokens.json")
		}
		tokenStore, err := auth.NewTokenStore(tokenFile)
		if err != nil {
			logger.Warn("failed to initialize token store, auth disabled", "error", err)
			wsOpts.AuthRequired = false
		} else if !tokenStore.HasTokens() {
			logger.Warn("auth enabled but no tokens configured - generate tokens with 'kaggen token generate'")
		} else {
			wsOpts.TokenValidator = tokenStore.ValidateToken
			logger.Info("websocket authentication enabled", "token_file", tokenFile)
		}
	}

	wsChannel := channel.NewWebSocketChannelWithOptions(addr, logger, wsOpts)

	router.AddChannel(wsChannel)

	var tgChannel *channel.TelegramChannel
	if cfg.Channels.Telegram.Enabled {
		token := cfg.TelegramBotToken()
		if token != "" {
			var sttURL string
			if cfg.STT.Enabled {
				sttURL = cfg.STT.BaseURL
				if sttURL == "" {
					sttURL = "http://localhost:8000"
				}
			}
			tgChannel = channel.NewTelegramChannel(token, &cfg.Channels.Telegram, sessionService, logger, sttURL)
			router.AddChannel(tgChannel)
		} else {
			logger.Warn("telegram enabled but no bot token configured")
		}
	}

	// Register dashboard routes if provided.
	if dashboard != nil {
		dashboard.SetHandler(handler)
		dashboard.RegisterRoutes(wsChannel.HandleFunc)
	}

	s := &Server{
		config:      cfg,
		router:      router,
		handler:     handler,
		wsChannel:   wsChannel,
		tgChannel:   tgChannel,
		dashboard:   dashboard,
		logger:      logger,
	}

	// Build channel map and create proactive engine if configured
	if len(cfg.Proactive.Jobs) > 0 || len(cfg.Proactive.Webhooks) > 0 || len(cfg.Proactive.Heartbeats) > 0 {
		chMap := &channelMap{channels: map[string]channel.Channel{
			"websocket": wsChannel,
		}}
		if tgChannel != nil {
			chMap.channels["telegram"] = tgChannel
		}
		var history *proactive.HistoryStore
		historyDBPath := cfg.ProactiveDBPath()
		if h, err := proactive.NewHistoryStore(historyDBPath); err != nil {
			logger.Warn("proactive history: failed to open", "path", historyDBPath, "error", err)
		} else {
			history = h
		}
		s.proactive = proactive.New(&cfg.Proactive, handler, chMap, logger, history)

		// Mount webhook routes on the WebSocket channel's HTTP server
		if len(cfg.Proactive.Webhooks) > 0 {
			mux := s.proactive.Mux()
			for _, wh := range cfg.Proactive.Webhooks {
				wsChannel.HandleFunc(wh.Path, mux.ServeHTTP)
			}
		}
	}

	return s
}

// MountCallbacks registers the callback HTTP handler on the WebSocket channel's
// HTTP server. Call this after the InFlightStore is available.
func (s *Server) MountCallbacks(store *kaggenAgent.InFlightStore) {
	ch := NewCallbackHandler(store, s.handler, s.logger)
	s.wsChannel.HandleFunc("/callbacks/", ch.ServeHTTP)
	s.logger.Info("callback handler mounted at /callbacks/")
}

// Start begins the gateway server.
func (s *Server) Start(ctx context.Context) error {
	s.logger.Info("starting gateway server",
		"bind", s.config.Gateway.Bind,
		"port", s.config.Gateway.Port)

	// Initialize P2P node if configured.
	if s.config.P2P.Enabled {
		node, err := p2p.NewNode(ctx, &s.config.P2P, s.logger)
		if err != nil {
			s.logger.Warn("failed to initialize P2P node", "error", err)
		} else {
			s.p2pNode = node
			s.logger.Info("P2P node started",
				"peer_id", node.PeerID().String(),
				"addrs", node.Addrs())
			// Print PeerID to console for visibility
			fmt.Printf("PeerID: %s\n", node.PeerID().String())
			for _, addr := range node.Addrs() {
				fmt.Printf("P2P Listen: %s/p2p/%s\n", addr, node.PeerID())
			}
		}
	}

	// Start Cloudflare Tunnel if configured.
	if s.config.Gateway.Tunnel.Enabled {
		provider := s.config.Gateway.Tunnel.Provider
		if provider == "" {
			provider = "cloudflare"
		}
		if provider == "cloudflare" {
			t := tunnel.NewCloudflareTunnel(s.config.Gateway.Port, s.config.Gateway.Tunnel.NamedTunnel, s.logger)
			if err := t.Start(ctx); err != nil {
				s.logger.Warn("failed to start tunnel", "error", err)
			} else {
				s.tunnel = t
				// Discover the public URL (blocks briefly).
				if url, err := t.PublicURL(ctx); err != nil {
					s.logger.Warn("tunnel URL not available", "error", err)
				} else {
					s.callbackURL = url
					s.logger.Info("tunnel active", "url", url)
				}
			}
		}
	}

	// Start Pub/Sub bridge if configured.
	if s.config.Gateway.PubSub.Enabled {
		projectID := s.config.PubSubProjectID()
		sub := s.config.Gateway.PubSub.Subscription
		if projectID == "" || sub == "" {
			s.logger.Warn("pubsub enabled but project_id or subscription not configured")
		} else {
			localURL := fmt.Sprintf("http://127.0.0.1:%d", s.config.Gateway.Port)
			bridge := pubsub.NewBridge(projectID, sub, localURL, s.logger)
			s.pubsubBridge = bridge
			go func() {
				if err := bridge.Start(ctx); err != nil {
					s.logger.Error("pubsub bridge failed", "error", err)
				}
			}()
			s.logger.Info("pubsub bridge started", "project", projectID, "subscription", sub)
		}
	}

	// Start the Telegram channel if configured (non-blocking)
	if s.tgChannel != nil {
		if err := s.tgChannel.Start(ctx); err != nil {
			return fmt.Errorf("start telegram channel: %w", err)
		}
		s.logger.Info("telegram channel started")
	}

	// Start the router to begin processing messages
	if err := s.router.Start(ctx); err != nil {
		return fmt.Errorf("start router: %w", err)
	}

	// Start the proactive engine if configured
	if s.proactive != nil {
		if err := s.proactive.Start(ctx); err != nil {
			return fmt.Errorf("start proactive engine: %w", err)
		}
		s.logger.Info("proactive engine started")
	}

	// Start the WebSocket channel (this blocks)
	return s.wsChannel.Start(ctx)
}

// CallbackBaseURL returns the resolved callback base URL for external tasks.
// Priority: explicit config > tunnel URL > local fallback.
func (s *Server) CallbackBaseURL() string {
	if s.config.Gateway.CallbackBaseURL != "" {
		return s.config.Gateway.CallbackBaseURL
	}
	if s.callbackURL != "" {
		return s.callbackURL
	}
	return fmt.Sprintf("http://%s:%d", s.config.Gateway.Bind, s.config.Gateway.Port)
}

// Stop gracefully shuts down the gateway server.
func (s *Server) Stop(ctx context.Context) error {
	s.logger.Info("stopping gateway server")

	// Stop the tunnel
	if s.tunnel != nil {
		if err := s.tunnel.Stop(); err != nil {
			s.logger.Warn("error stopping tunnel", "error", err)
		}
	}

	// Stop the pubsub bridge
	if s.pubsubBridge != nil {
		if err := s.pubsubBridge.Stop(); err != nil {
			s.logger.Warn("error stopping pubsub bridge", "error", err)
		}
	}

	// Stop the P2P node
	if s.p2pNode != nil {
		if err := s.p2pNode.Close(); err != nil {
			s.logger.Warn("error stopping P2P node", "error", err)
		}
	}

	// Stop the proactive engine
	if s.proactive != nil {
		s.proactive.Stop()
	}

	// Close the handler to release runner resources
	if err := s.handler.Close(); err != nil {
		s.logger.Warn("error closing handler", "error", err)
	}

	return s.router.Stop(ctx)
}

// Handler returns the server's message handler, allowing external components
// to wire up completion event injection.
func (s *Server) Handler() *Handler {
	return s.handler
}

// ProactiveEngine returns the proactive engine, or nil if not configured.
func (s *Server) ProactiveEngine() *proactive.Engine {
	return s.proactive
}

// ClientCount returns the number of connected WebSocket clients.
func (s *Server) ClientCount() int {
	return s.wsChannel.ClientCount()
}

// P2PNode returns the P2P node, or nil if P2P is disabled.
func (s *Server) P2PNode() *p2p.Node {
	return s.p2pNode
}

// Broadcast sends data to all connected WebSocket clients.
func (s *Server) Broadcast(data []byte) {
	s.wsChannel.Broadcast(data)
}

// SendTelegramAlert sends a direct text message to a Telegram chat.
// Used by alerting systems (watchdog) to notify users of stuck tasks.
func (s *Server) SendTelegramAlert(chatID int64, text string) {
	if s.tgChannel != nil {
		s.tgChannel.SendText(chatID, text)
	}
}
