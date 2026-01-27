package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	tmemory "trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	trpcsession "trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"

	kaggenAgent "github.com/yourusername/kaggen/internal/agent"
	"github.com/yourusername/kaggen/internal/config"
	"github.com/yourusername/kaggen/internal/embedding"
	"github.com/yourusername/kaggen/internal/gateway"
	"github.com/yourusername/kaggen/internal/memory"
	"github.com/yourusername/kaggen/internal/model/anthropic"
	kaggenSession "github.com/yourusername/kaggen/internal/session"
	"github.com/yourusername/kaggen/internal/skills"
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

	// Create components
	workspace := cfg.WorkspacePath()

	// Create Anthropic adapter (implements model.Model)
	modelAdapter := anthropic.NewAdapter(apiKey, modelName)

	// Create tools
	toolList := tools.DefaultTools(workspace)

	// Memory service (passed to server if available)
	var memService tmemory.Service

	// Initialize memory service if enabled
	if cfg.Memory.Search.Enabled {
		embedder := embedding.NewOllamaEmbedder(
			cfg.Memory.Embedding.BaseURL,
			cfg.Memory.Embedding.Model,
		)

		dim := embedder.Dimension()
		if dim == 0 {
			logger.Warn("memory search: failed to probe embedding dimension, disabling")
		} else {
			dbPath := cfg.MemoryDBPath()
			vecIndex, err := memory.NewVectorIndex(dbPath, dim)
			if err != nil {
				logger.Warn("memory search: failed to open vector index", "error", err)
			} else {
				defer vecIndex.Close()

				chunkSize := cfg.Memory.Indexing.ChunkSize
				chunkOverlap := cfg.Memory.Indexing.ChunkOverlap
				indexer := memory.NewIndexer(vecIndex, embedder, workspace, chunkSize, chunkOverlap, logger)
				if err := indexer.Start(ctx); err != nil {
					logger.Warn("memory search: indexer start failed", "error", err)
				}

				// Create memory service with auto-extraction
				memExtractor := extractor.NewExtractor(modelAdapter, extractor.WithPrompt(memory.ExtractorPrompt))
				svc, err := memory.NewFileMemoryService(
					vecIndex.DB(), vecIndex, embedder, workspace, logger,
					memory.WithExtractor(memExtractor),
					memory.WithAsyncMemoryNum(1),
					memory.WithMemoryQueueSize(10),
					memory.WithMemoryJobTimeout(30*time.Second),
					memory.WithModel(modelAdapter),
					memory.WithSynthesisInterval(1*time.Hour),
				)
				if err != nil {
					logger.Warn("memory service: init failed", "error", err)
				} else {
					defer svc.Close()
					memService = svc
					toolList = append(toolList, svc.Tools()...)
					logger.Info("memory service enabled", "db", dbPath, "dimension", dim)
				}
			}
		}
	}

	// Load skills
	skillLoader := skills.NewLoader(
		filepath.Join(workspace, "skills"),
		config.ExpandPath("~/.kaggen/skills"),
	)
	loadedSkills, err := skillLoader.Load()
	if err != nil {
		logger.Warn("failed to load skills", "error", err)
	}
	skillsXML := skills.FormatXML(loadedSkills)
	if len(loadedSkills) > 0 {
		logger.Info("skills loaded", "count", len(loadedSkills))
	}

	// Create file memory for bootstrap loading
	fileMemory := memory.NewFileMemory(workspace)

	// Create the Kaggen agent
	kaggen, err := kaggenAgent.NewAgent(modelAdapter, toolList, fileMemory, skillsXML, logger)
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}

	// Create session service with appropriate backend
	sessionService, err := createSessionService(cfg)
	if err != nil {
		return fmt.Errorf("create session service: %w", err)
	}
	defer sessionService.Close()

	// Create gateway server (with optional memory service)
	server := gateway.NewServer(cfg, sessionService, kaggen, logger, memService)

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
	if cfg.Memory.Search.Enabled {
		fmt.Println("Memory Search: enabled")
	} else {
		fmt.Println("Memory Search: disabled")
	}
	if len(loadedSkills) > 0 {
		fmt.Printf("Skills: %d loaded\n", len(loadedSkills))
	}
	if len(cfg.Proactive.Jobs) > 0 {
		fmt.Printf("Proactive Jobs: %d\n", len(cfg.Proactive.Jobs))
	}
	if len(cfg.Proactive.Webhooks) > 0 {
		fmt.Printf("Webhooks: %d\n", len(cfg.Proactive.Webhooks))
	}
	if len(cfg.Proactive.Heartbeats) > 0 {
		fmt.Printf("Heartbeats: %d\n", len(cfg.Proactive.Heartbeats))
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
