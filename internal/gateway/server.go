package gateway

import (
	"context"
	"fmt"
	"log/slog"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/session"

	"github.com/yourusername/kaggen/internal/channel"
	"github.com/yourusername/kaggen/internal/config"
)

const (
	// AppName is the name of the application for the runner.
	AppName = "kaggen"
)

// Server is the gateway server that routes messages between channels and the agent.
type Server struct {
	config    *config.Config
	router    *channel.Router
	handler   *Handler
	wsChannel *channel.WebSocketChannel
	tgChannel *channel.TelegramChannel
	logger    *slog.Logger
}

// NewServer creates a new gateway server.
func NewServer(cfg *config.Config, sessionService session.Service, ag agent.Agent, logger *slog.Logger) *Server {
	handler := NewHandler(AppName, ag, sessionService, logger)
	router := channel.NewRouter(handler)

	addr := fmt.Sprintf("%s:%d", cfg.Gateway.Bind, cfg.Gateway.Port)
	wsChannel := channel.NewWebSocketChannel(addr, logger)

	router.AddChannel(wsChannel)

	var tgChannel *channel.TelegramChannel
	if cfg.Channels.Telegram.Enabled {
		token := cfg.TelegramBotToken()
		if token != "" {
			tgChannel = channel.NewTelegramChannel(token, logger)
			router.AddChannel(tgChannel)
		} else {
			logger.Warn("telegram enabled but no bot token configured")
		}
	}

	return &Server{
		config:    cfg,
		router:    router,
		handler:   handler,
		wsChannel: wsChannel,
		tgChannel: tgChannel,
		logger:    logger,
	}
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

	// Start the WebSocket channel (this blocks)
	return s.wsChannel.Start(ctx)
}

// Stop gracefully shuts down the gateway server.
func (s *Server) Stop(ctx context.Context) error {
	s.logger.Info("stopping gateway server")

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
