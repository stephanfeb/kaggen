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
	"trpc.group/trpc-go/trpc-agent-go/model"

	"github.com/spf13/cobra"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	tmemory "trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	trpcsession "trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"

	kaggenAgent "github.com/yourusername/kaggen/internal/agent"
	"github.com/yourusername/kaggen/internal/backlog"
	"github.com/yourusername/kaggen/internal/config"
	"github.com/yourusername/kaggen/internal/embedding"
	"github.com/yourusername/kaggen/internal/gateway"
	"github.com/yourusername/kaggen/internal/memory"
	"github.com/yourusername/kaggen/internal/model/anthropic"
	"github.com/yourusername/kaggen/internal/model/gemini"
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
	// Check for Gemini API key first
	geminiKey := config.GeminiAPIKey()
	anthropicKey := config.AnthropicAPIKey()

	if geminiKey == "" && anthropicKey == "" {
		return fmt.Errorf("neither GEMINI_API_KEY nor ANTHROPIC_API_KEY environment variable is set")
	}

	// Setup logger
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Initialize telemetry/tracing if enabled
	if cfg.Telemetry.Enabled {
		endpoint := cfg.Telemetry.JaegerEndpoint
		if endpoint == "" {
			endpoint = "localhost:4317"
		}
		serviceName := cfg.Telemetry.ServiceName
		if serviceName == "" {
			serviceName = "kaggen"
		}
		protocol := cfg.Telemetry.Protocol
		if protocol == "" {
			protocol = "grpc"
		}

		traceOpts := []atrace.Option{
			atrace.WithEndpoint(endpoint),
			atrace.WithServiceName(serviceName),
			atrace.WithProtocol(protocol),
		}

		shutdownTrace, err := atrace.Start(context.Background(), traceOpts...)
		if err != nil {
			logger.Warn("failed to initialize tracing", "error", err)
		} else {
			defer shutdownTrace()
			logger.Info("tracing enabled", "endpoint", endpoint, "protocol", protocol, "service", serviceName)
		}
	}

	// Get model name from config
	configModel := cfg.Agent.Model

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

	// Create model adapter
	var modelAdapter model.Model

	if geminiKey != "" {
		// Use Gemini if key is present
		var modelName string
		if strings.HasPrefix(configModel, "gemini/") {
			modelName = strings.TrimPrefix(configModel, "gemini/")
		} else {
			// Default to Gemini 1.5 Pro if config is not explicitly gemini
			modelName = "gemini-3-pro-preview"
		}
		modelAdapter = gemini.NewAdapter(geminiKey, modelName)
		logger.Info("Using Google Gemini model", "model", modelName)
	} else {
		// Fallback to Anthropic
		modelName := configModel
		if strings.HasPrefix(modelName, "anthropic/") {
			modelName = strings.TrimPrefix(modelName, "anthropic/")
		}
		modelAdapter = anthropic.NewAdapter(anthropicKey, modelName)
		logger.Info("Using Anthropic model", "model", modelName)
	}

	// Create tools
	toolList := tools.DefaultTools(workspace)

	// Initialize persistent work backlog
	backlogStore, err := backlog.NewStore(cfg.BacklogDBPath())
	if err != nil {
		return fmt.Errorf("open backlog store: %w", err)
	}
	defer backlogStore.Close()
	toolList = append(toolList, tools.BacklogTools(backlogStore)...)
	logger.Info("backlog store enabled", "db", cfg.BacklogDBPath())

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
					memory.WithAsyncMemoryNum(2),
					memory.WithMemoryQueueSize(50),
					memory.WithMemoryJobTimeout(120*time.Second),
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

	// Load skills via framework repository
	var skillsRepo skill.Repository
	fsRepo, err := skill.NewFSRepository(
		filepath.Join(workspace, "skills"),
		config.ExpandPath("~/.kaggen/skills"),
	)
	if err != nil {
		logger.Warn("failed to load skills", "error", err)
	}
	if fsRepo != nil {
		// Wrap with case-insensitive lookup to handle LLMs that capitalize skill names
		skillsRepo = kaggenAgent.NewCaseInsensitiveRepository(fsRepo)
		summaries := skillsRepo.Summaries()
		if len(summaries) > 0 {
			logger.Info("skills loaded", "count", len(summaries))
		}
	}

	// Create file memory for bootstrap loading
	fileMemory := memory.NewFileMemory(workspace)

	// Build specialist sub-agents from skills.
	var subAgents []agent.Agent
	if skillsRepo != nil {
		subAgents, err = kaggenAgent.BuildSubAgents(modelAdapter, skillsRepo, toolList, logger)
		if err != nil {
			logger.Warn("failed to build sub-agents, falling back to single agent", "error", err)
		}
	}

	// Create the Kaggen agent (Coordinator Team pattern).
	// Pass nil for completeFn; it's wired up after the handler is created.
	kaggen, err := kaggenAgent.NewAgent(modelAdapter, toolList, fileMemory, subAgents, nil, memService, logger)
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}

	// Create session service with appropriate backend
	sessionService, err := createSessionService(cfg)
	if err != nil {
		return fmt.Errorf("create session service: %w", err)
	}
	defer sessionService.Close()

	// Wrap session service to strip binary data (images, files) from history.
	sanitizedSession := kaggenSession.NewSanitizeWrapper(sessionService)

	// Create gateway server (with optional memory service)
	server := gateway.NewServer(cfg, sanitizedSession, kaggen, logger, memService)

	// Wire up async completion: when a sub-agent finishes, inject the result
	// back into the coordinator's session so it can synthesize and notify the user.
	kaggen.SetCompletionFunc(func(taskID, result string, taskErr error, policy kaggenAgent.TriggerPolicy) {
		// For errors, always inject immediately (even for TriggerQueue).
		// For successful TriggerQueue results, defer to next user message.
		if policy != kaggenAgent.TriggerAuto && taskErr == nil {
			return
		}

		content := result
		if taskErr != nil {
			content = fmt.Sprintf("Error: %v", taskErr)
		}

		state, ok := kaggen.InFlightStore().Get(taskID)
		if !ok {
			logger.Warn("completion for unknown task", "task_id", taskID)
			return
		}

		// Route completion back to the originating session.
		sid := state.SessionID
		uid := state.UserID
		if sid == "" {
			sid = "tg-dm-system"
		}
		if uid == "" {
			uid = "system"
		}

		if err := server.Handler().InjectCompletion(
			context.Background(), sid, uid, taskID, state.AgentName, content,
		); err != nil {
			logger.Warn("failed to inject completion", "task_id", taskID, "error", err)
		}
	})

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
	if skillsRepo != nil {
		if n := len(skillsRepo.Summaries()); n > 0 {
			fmt.Printf("Skills: %d loaded\n", n)
		}
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
	if cfg.Telemetry.Enabled {
		endpoint := cfg.Telemetry.JaegerEndpoint
		if endpoint == "" {
			endpoint = "localhost:4317"
		}
		fmt.Printf("Tracing: enabled (Jaeger @ %s)\n", endpoint)
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
