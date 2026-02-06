package cmd

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	trpcagent "trpc.group/trpc-go/trpc-agent-go/agent"
	trpcmemory "trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	trpcsession "trpc.group/trpc-go/trpc-agent-go/session"

	kaggenAgent "github.com/yourusername/kaggen/internal/agent"
	"github.com/yourusername/kaggen/internal/browser"
	"github.com/yourusername/kaggen/internal/config"
	"github.com/yourusername/kaggen/internal/embedding"
	"github.com/yourusername/kaggen/internal/memory"
	"github.com/yourusername/kaggen/internal/model/anthropic"
	"github.com/yourusername/kaggen/internal/model/gemini"
	"github.com/yourusername/kaggen/internal/model/zai"
	kaggenModel "github.com/yourusername/kaggen/internal/model"
	kaggenSession "github.com/yourusername/kaggen/internal/session"
	"github.com/yourusername/kaggen/internal/tools"
)

var (
	sessionID string
	verbose   bool
)

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Start an interactive agent session",
	Long:  `Start an interactive CLI session with the Kaggen AI agent.`,
	RunE:  runAgent,
}

func init() {
	agentCmd.Flags().StringVarP(&sessionID, "session", "s", "", "Session ID to use (defaults to new UUID)")
	agentCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose logging")
}

func runAgent(cmd *cobra.Command, args []string) error {
	// Generate UUID session ID if not explicitly provided.
	if sessionID == "" {
		sessionID = uuid.New().String()
	}

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Check for API key
	// Check for API keys (priority: ZAI > Gemini > Anthropic)
	zaiKey := config.ZaiAPIKey()
	geminiKey := config.GeminiAPIKey()
	anthropicKey := config.AnthropicAPIKey()

	if zaiKey == "" && geminiKey == "" && anthropicKey == "" {
		return fmt.Errorf("none of ZAI_API_KEY, GEMINI_API_KEY, or ANTHROPIC_API_KEY environment variables are set")
	}

	// Setup logger
	logLevel := slog.LevelInfo
	if verbose {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))

	// Get model name from config
	configModel := cfg.Agent.Model

	// Create components
	workspace := cfg.WorkspacePath()

	// Create model adapter
	var modelAdapter model.Model

	if zaiKey != "" {
		modelName := "glm-4.7"
		if strings.HasPrefix(configModel, "zai/") {
			modelName = strings.TrimPrefix(configModel, "zai/")
		}
		modelAdapter = zai.NewAdapter(zaiKey, modelName)
		logger.Info("Using ZAI model", "model", modelName)
	} else if geminiKey != "" {
		var modelName string
		if strings.HasPrefix(configModel, "gemini/") {
			modelName = strings.TrimPrefix(configModel, "gemini/")
		} else {
			modelName = "gemini-3-pro-preview"
		}
		modelAdapter = gemini.NewAdapter(geminiKey, modelName)
		logger.Info("Using Google Gemini model", "model", modelName)
	} else {
		modelName := configModel
		if strings.HasPrefix(modelName, "anthropic/") {
			modelName = strings.TrimPrefix(modelName, "anthropic/")
		}
		modelAdapter = anthropic.NewAdapter(anthropicKey, modelName)
		logger.Info("Using Anthropic model", "model", modelName)
	}

	// Apply concurrency limit to LLM API calls.
	maxConc := cfg.Agent.MaxConcurrentLLM
	if maxConc == 0 {
		maxConc = 4
	}
	modelAdapter = kaggenModel.NewRateLimitedModel(modelAdapter, maxConc)
	logger.Info("LLM concurrency limit", "max", maxConc)

	// Create tools
	toolList := tools.DefaultTools(workspace)
	_, cronTools := tools.NewCronToolSet(cfg)
	toolList = append(toolList, cronTools...)

	// Create Tier 2 model for reasoning escalation if enabled
	if cfg.Reasoning.Enabled {
		tier2ModelString := cfg.ReasoningTier2Model(configModel)
		tier2Model := createTier2Model(tier2ModelString, logger)
		if tier2Model != nil {
			tier2ModelLimited := kaggenModel.NewRateLimitedModel(tier2Model, 1) // Conservative rate limit for deep thinking
			reasoningTool := tools.NewReasoningTool(tier2ModelLimited, nil, cfg.Reasoning, tier2ModelString, logger)
			if reasoningTool != nil {
				toolList = append(toolList, reasoningTool)
				logger.Info("reasoning escalation enabled", "tier2_model", tier2ModelString)
			}
		}
	}

	// Initialize browser control if enabled
	if cfg.Browser.Enabled {
		browserMgr := browser.NewManager(cfg.Browser, logger)
		defer browserMgr.Close()
		toolList = append(toolList, tools.BrowserTools(browserMgr)...)
		logger.Info("browser control enabled", "profiles", len(cfg.Browser.Profiles))
	}

	// Setup context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Runner options
	var runnerOpts []runner.Option
	var memService trpcmemory.Service

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
				memService, err = memory.NewFileMemoryService(
					vecIndex.DB(), vecIndex, embedder, workspace, logger,
					memory.WithExtractor(memExtractor),
					memory.WithAsyncMemoryNum(1),
					memory.WithMemoryQueueSize(10),
					func() memory.ServiceOpt {
						timeout := 2 * time.Minute
						if t, err := time.ParseDuration(cfg.Memory.Auto.Timeout); err == nil && t > 0 {
							timeout = t
						}
						return memory.WithMemoryJobTimeout(timeout)
					}(),
					memory.WithModel(modelAdapter),
					memory.WithSynthesisInterval(1*time.Hour),
				)
				if err != nil {
					logger.Warn("memory service: init failed", "error", err)
				} else {
					defer memService.Close()
					toolList = append(toolList, memService.Tools()...)
					runnerOpts = append(runnerOpts, runner.WithMemoryService(memService))
					logger.Info("memory service enabled", "db", dbPath, "dimension", dim)
				}
			}
		}
	}

	// Create file memory for bootstrap loading
	fileMemory := memory.NewFileMemory(workspace)

	// Skill directories to scan.
	skillDirs := []string{
		filepath.Join(workspace, "skills"),
		config.ExpandPath("~/.kaggen/skills"),
	}

	// Build the initial agent via the factory/provider pattern.
	// This enables hot-reload of skills on SIGHUP without restarting.
	initialAgent, err := kaggenAgent.BuildInitialAgent(modelAdapter, toolList, fileMemory, skillDirs, memService, nil, logger, cfg.Agent.MaxHistoryRuns, cfg.Agent.PreloadMemory, cfg.Agent.MaxTurnsPerTask)
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}
	provider := kaggenAgent.NewAgentProvider(initialAgent)
	factory := kaggenAgent.NewAgentFactory(modelAdapter, toolList, fileMemory, memService, nil, skillDirs, provider, logger, cfg.Agent.MaxHistoryRuns, cfg.Agent.PreloadMemory, cfg.Agent.MaxTurnsPerTask)

	// Create file-backed session service for CLI persistence
	sessionService := kaggenSession.NewFileService(cfg.SessionsPath())
	sessionService.SetModel(modelAdapter)
	defer sessionService.Close()

	// Wire up pre-compaction memory extraction hook.
	// This ensures memories are extracted before events are deleted during /compact.
	if fms, ok := memService.(*memory.FileMemoryService); ok {
		sessionService.SetCompactionHook(fms)
	}

	// Wrap session service to strip binary data (images, files) from history.
	sanitizedSession := kaggenSession.NewSanitizeWrapper(sessionService)

	// Create runner (provider implements agent.Agent, enabling hot-reload)
	runnerOpts = append(runnerOpts, runner.WithSessionService(sanitizedSession))
	r := runner.NewRunner("kaggen", provider, runnerOpts...)
	defer func() {
		if closer, ok := r.(interface{ Close() error }); ok {
			closer.Close()
		}
	}()

	// Handle signals: SIGINT/SIGTERM for shutdown, SIGHUP for skill reload.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		for sig := range sigCh {
			if sig == syscall.SIGHUP {
				logger.Info("SIGHUP received, reloading skills...")
				if err := factory.Rebuild(); err != nil {
					logger.Error("skill reload failed", "error", err)
				}
				continue
			}
			fmt.Println("\nInterrupted. Goodbye!")
			cancel()
			os.Exit(0)
		}
	}()

	// Print welcome message
	fmt.Println("Kaggen Agent")
	fmt.Println("============")
	fmt.Printf("Session: %s\n", sessionID)
	fmt.Printf("Workspace: %s\n", workspace)
	if cfg.Memory.Search.Enabled {
		fmt.Println("Memory Search: enabled")
	}
	if n := len(provider.SubAgents()); n > 0 {
		fmt.Printf("Skills: %d sub-agents\n", n)
	}
	fmt.Println()
	fmt.Println("Type your message and press Enter. Type 'exit' or 'quit' to end.")
	fmt.Println("Commands: /clear (clear session)")
	fmt.Println()

	// Interactive loop
	reader := bufio.NewReader(os.Stdin)
	userID := "cli-user"

	for {
		fmt.Print("You: ")
		input, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("read input: %w", err)
		}

		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}

		// Handle commands
		switch strings.ToLower(input) {
		case "exit", "quit":
			fmt.Println("Goodbye!")
			return nil
		case "/clear":
			// Delete the current session from the file backend
			clearKey := trpcsession.Key{AppName: "kaggen", UserID: userID, SessionID: sessionID}
			_ = sessionService.DeleteSession(ctx, clearKey)
			fmt.Println("Session cleared.")
			continue
		case "/compact":
			compactKey := trpcsession.Key{AppName: "kaggen", UserID: userID, SessionID: sessionID}
			sess, err := sessionService.GetSession(ctx, compactKey)
			if err != nil || sess == nil {
				fmt.Println("No session to compact.")
				continue
			}
			fmt.Println("Compacting session...")
			if err := sessionService.CreateSessionSummary(ctx, sess, "", true); err != nil {
				fmt.Printf("Failed to compact: %v\n", err)
				continue
			}
			fmt.Println("Session compacted. Kept last 20 messages with a summary of prior history.")
			continue
		}

		// Run agent
		fmt.Println()
		events, err := r.Run(
			ctx,
			userID,
			sessionID,
			model.NewUserMessage(input),
			trpcagent.WithRequestID(uuid.New().String()),
		)
		if err != nil {
			if ctx.Err() != nil {
				return nil // Context cancelled, exit gracefully
			}
			fmt.Printf("Error: %v\n\n", err)
			continue
		}

		// Process events and display response
		var finalContent string
		for evt := range events {
			if evt == nil || evt.Response == nil {
				continue
			}

			// Handle errors
			if evt.Response.Error != nil {
				fmt.Printf("Error: %s\n", evt.Response.Error.Message)
				continue
			}

			// Handle tool calls (show progress)
			if len(evt.Response.Choices) > 0 {
				choice := evt.Response.Choices[0]

				// Show tool calls
				if len(choice.Message.ToolCalls) > 0 {
					for _, tc := range choice.Message.ToolCalls {
						if verbose {
							fmt.Printf("[Tool: %s]\n", tc.Function.Name)
						}
					}
				}

				// Capture final content
				if choice.Message.Content != "" {
					finalContent = choice.Message.Content
				}
			}

			// Handle runner completion
			if evt.IsRunnerCompletion() && len(evt.Response.Choices) > 0 {
				finalContent = evt.Response.Choices[0].Message.Content
			}
		}

		if finalContent != "" {
			fmt.Printf("Kaggen: %s\n\n", finalContent)
		} else {
			fmt.Println()
		}
	}
}

// createTier2Model creates a model adapter from a "provider/model" string.
// Used for creating the Tier 2 (deep thinking) model for reasoning escalation.
// Returns a trpc model.Model interface that implements GenerateContent.
func createTier2Model(modelString string, logger *slog.Logger) model.Model {
	if strings.HasPrefix(modelString, "gemini/") {
		modelName := strings.TrimPrefix(modelString, "gemini/")
		apiKey := config.GeminiAPIKey()
		if apiKey == "" {
			logger.Warn("Gemini API key not set for Tier 2 model")
			return nil
		}
		return gemini.NewAdapter(apiKey, modelName)
	}
	if strings.HasPrefix(modelString, "zai/") {
		modelName := strings.TrimPrefix(modelString, "zai/")
		apiKey := config.ZaiAPIKey()
		if apiKey == "" {
			logger.Warn("ZAI API key not set for Tier 2 model")
			return nil
		}
		return zai.NewAdapter(apiKey, modelName)
	}
	// Default: Anthropic
	modelName := strings.TrimPrefix(modelString, "anthropic/")
	apiKey := config.AnthropicAPIKey()
	if apiKey == "" {
		logger.Warn("Anthropic API key not set for Tier 2 model")
		return nil
	}
	return anthropic.NewAdapter(apiKey, modelName)
}
