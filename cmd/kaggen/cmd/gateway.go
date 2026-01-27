package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	trpcsession "trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"

	kaggenAgent "github.com/yourusername/kaggen/internal/agent"
	"github.com/yourusername/kaggen/internal/config"
	"github.com/yourusername/kaggen/internal/gateway"
	"github.com/yourusername/kaggen/internal/memory"
	"github.com/yourusername/kaggen/internal/model/anthropic"
	kaggenSession "github.com/yourusername/kaggen/internal/session"
	"github.com/yourusername/kaggen/internal/tools"
)

var gatewayCmd = &cobra.Command{
	Use:   "gateway",
	Short: "Start the Kaggen gateway server",
	Long:  `Start the WebSocket gateway server for multi-channel communication.`,
	RunE:  runGateway,
}

func runGateway(cmd *cobra.Command, args []string) error {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Check for API key
	apiKey := config.APIKey()
	if apiKey == "" {
		return fmt.Errorf("ANTHROPIC_API_KEY environment variable not set")
	}

	// Setup logger
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Get model name (strip provider prefix if present)
	modelName := cfg.Agent.Model
	if strings.HasPrefix(modelName, "anthropic/") {
		modelName = strings.TrimPrefix(modelName, "anthropic/")
	}

	// Create components
	workspace := cfg.WorkspacePath()

	// Create Anthropic adapter (implements model.Model)
	modelAdapter := anthropic.NewAdapter(apiKey, modelName)

	// Create tools
	toolList := tools.DefaultTools(workspace)

	// Create file memory for bootstrap loading
	fileMemory := memory.NewFileMemory(workspace)

	// Create the Kaggen agent
	kaggen, err := kaggenAgent.NewAgent(modelAdapter, toolList, fileMemory, logger)
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}

	// Create session service with appropriate backend
	sessionService, err := createSessionService(cfg)
	if err != nil {
		return fmt.Errorf("create session service: %w", err)
	}
	defer sessionService.Close()

	// Create gateway server
	server := gateway.NewServer(cfg, sessionService, kaggen, logger)

	// Setup context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle interrupt signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nShutting down gateway server...")
		cancel()
	}()

	// Print startup message
	fmt.Println("Kaggen Gateway")
	fmt.Println("==============")
	fmt.Printf("Bind: %s:%d\n", cfg.Gateway.Bind, cfg.Gateway.Port)
	fmt.Printf("Session Backend: %s\n", cfg.Session.Backend)
	if cfg.Channels.Telegram.Enabled {
		fmt.Println("Telegram: enabled")
	} else {
		fmt.Println("Telegram: disabled")
	}
	fmt.Println()
	fmt.Println("WebSocket endpoint: ws://localhost:" + fmt.Sprint(cfg.Gateway.Port) + "/ws")
	fmt.Println("Health check: http://localhost:" + fmt.Sprint(cfg.Gateway.Port) + "/health")
	fmt.Println()
	fmt.Println("Press Ctrl+C to stop the server.")
	fmt.Println()

	// Start the server
	if err := server.Start(ctx); err != nil {
		if ctx.Err() != nil {
			// Context cancelled, graceful shutdown
			return nil
		}
		return fmt.Errorf("server error: %w", err)
	}

	return nil
}

// createSessionService creates a session service based on the configured backend.
func createSessionService(cfg *config.Config) (trpcsession.Service, error) {
	switch cfg.Session.Backend {
	case "file", "":
		return kaggenSession.NewFileService(cfg.SessionsPath()), nil

	case "memory":
		return inmemory.NewSessionService(), nil

	default:
		return nil, fmt.Errorf("unknown session backend: %s", cfg.Session.Backend)
	}
}
