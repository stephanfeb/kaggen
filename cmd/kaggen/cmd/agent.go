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
	"trpc.group/trpc-go/trpc-agent-go/skill"

	kaggenAgent "github.com/yourusername/kaggen/internal/agent"
	"github.com/yourusername/kaggen/internal/config"
	"github.com/yourusername/kaggen/internal/embedding"
	"github.com/yourusername/kaggen/internal/memory"
	"github.com/yourusername/kaggen/internal/model/anthropic"
	"github.com/yourusername/kaggen/internal/model/gemini"
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
	agentCmd.Flags().StringVarP(&sessionID, "session", "s", "main", "Session ID to use")
	agentCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose logging")
}

func runAgent(cmd *cobra.Command, args []string) error {
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

	if geminiKey != "" {
		// Use Gemini if key is present
		var modelName string
		if strings.HasPrefix(configModel, "gemini/") {
			modelName = strings.TrimPrefix(configModel, "gemini/")
		} else {
			// Default to Gemini 1.5 Pro if config is not explicitly gemini
			// (e.g. default config is anthropic/claude...)
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

	// Load skills via framework repository
	skillsRepo, err := skill.NewFSRepository(
		filepath.Join(workspace, "skills"),
		config.ExpandPath("~/.kaggen/skills"),
	)
	if err != nil {
		logger.Warn("failed to load skills", "error", err)
	}
	if skillsRepo != nil {
		summaries := skillsRepo.Summaries()
		if len(summaries) > 0 {
			logger.Info("skills loaded", "count", len(summaries))
		}
	}

	// Create file memory for bootstrap loading
	fileMemory := memory.NewFileMemory(workspace)

	// Build specialist sub-agents from skills.
	var subAgents []trpcagent.Agent
	if skillsRepo != nil {
		subAgents, err = kaggenAgent.BuildSubAgents(modelAdapter, skillsRepo, toolList, logger)
		if err != nil {
			logger.Warn("failed to build sub-agents, falling back to single agent", "error", err)
		}
	}

	// Create the Kaggen agent (Coordinator Team pattern).
	// CLI mode doesn't use async completion injection, so pass nil.
	kaggen, err := kaggenAgent.NewAgent(modelAdapter, toolList, fileMemory, subAgents, nil, memService, logger)
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}

	// Create file-backed session service for CLI persistence
	sessionService := kaggenSession.NewFileService(cfg.SessionsPath())
	defer sessionService.Close()

	// Wrap session service to strip binary data (images, files) from history.
	sanitizedSession := kaggenSession.NewSanitizeWrapper(sessionService)

	// Create runner
	runnerOpts = append(runnerOpts, runner.WithSessionService(sanitizedSession))
	r := runner.NewRunner("kaggen", kaggen, runnerOpts...)
	defer func() {
		if closer, ok := r.(interface{ Close() error }); ok {
			closer.Close()
		}
	}()

	// Handle interrupt signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nInterrupted. Goodbye!")
		cancel()
		os.Exit(0)
	}()

	// Print welcome message
	fmt.Println("Kaggen Agent")
	fmt.Println("============")
	fmt.Printf("Session: %s\n", sessionID)
	fmt.Printf("Workspace: %s\n", workspace)
	if cfg.Memory.Search.Enabled {
		fmt.Println("Memory Search: enabled")
	}
	if skillsRepo != nil {
		if n := len(skillsRepo.Summaries()); n > 0 {
			fmt.Printf("Skills: %d loaded\n", n)
		}
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
