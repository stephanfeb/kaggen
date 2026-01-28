package gateway

import (
	"context"
	"fmt"
	"log/slog"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"

	"github.com/yourusername/kaggen/internal/channel"
	"github.com/yourusername/kaggen/internal/config"
	"github.com/yourusername/kaggen/internal/proactive"
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
	config    *config.Config
	router    *channel.Router
	handler   *Handler
	wsChannel *channel.WebSocketChannel
	tgChannel *channel.TelegramChannel
	proactive *proactive.Engine
	logger    *slog.Logger
}

// NewServer creates a new gateway server.
func NewServer(cfg *config.Config, sessionService session.Service, ag agent.Agent, logger *slog.Logger, memService ...memory.Service) *Server {
	handler := NewHandler(AppName, ag, sessionService, logger, memService...)
	router := channel.NewRouter(handler)

	addr := fmt.Sprintf("%s:%d", cfg.Gateway.Bind, cfg.Gateway.Port)
	wsChannel := channel.NewWebSocketChannel(addr, logger)

	router.AddChannel(wsChannel)

	var tgChannel *channel.TelegramChannel
	if cfg.Channels.Telegram.Enabled {
		token := cfg.TelegramBotToken()
		if token != "" {
			tgChannel = channel.NewTelegramChannel(token, &cfg.Channels.Telegram, sessionService, logger)
			router.AddChannel(tgChannel)
		} else {
			logger.Warn("telegram enabled but no bot token configured")
		}
	}

	s := &Server{
		config:    cfg,
		router:    router,
		handler:   handler,
		wsChannel: wsChannel,
		tgChannel: tgChannel,
		logger:    logger,
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

// Start begins the gateway server.
func (s *Server) Start(ctx context.Context) error {
	s.logger.Info("starting gateway server",
		"bind", s.config.Gateway.Bind,
		"port", s.config.Gateway.Port)

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

// Stop gracefully shuts down the gateway server.
func (s *Server) Stop(ctx context.Context) error {
	s.logger.Info("stopping gateway server")

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

// ClientCount returns the number of connected WebSocket clients.
func (s *Server) ClientCount() int {
	return s.wsChannel.ClientCount()
}
