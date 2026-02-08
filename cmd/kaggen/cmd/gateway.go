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

	tmemory "trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	trpcsession "trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"

	kaggenAgent "github.com/yourusername/kaggen/internal/agent"
	"github.com/yourusername/kaggen/internal/backlog"
	"github.com/yourusername/kaggen/internal/browser"
	"github.com/yourusername/kaggen/internal/config"
	"github.com/yourusername/kaggen/internal/embedding"
	kaggenChannel "github.com/yourusername/kaggen/internal/channel"
	"github.com/yourusername/kaggen/internal/gateway"
	"github.com/yourusername/kaggen/internal/memory"
	"github.com/yourusername/kaggen/internal/model/anthropic"
	"github.com/yourusername/kaggen/internal/model/gemini"
	"github.com/yourusername/kaggen/internal/model/zai"
	kaggenModel "github.com/yourusername/kaggen/internal/model"
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
	// Check for API keys (priority: ZAI > Gemini > Anthropic)
	zaiKey := config.ZaiAPIKey()
	geminiKey := config.GeminiAPIKey()
	anthropicKey := config.AnthropicAPIKey()

	if zaiKey == "" && geminiKey == "" && anthropicKey == "" {
		return fmt.Errorf("none of ZAI_API_KEY, GEMINI_API_KEY, or ANTHROPIC_API_KEY environment variables are set")
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

	// Handle signals: SIGINT/SIGTERM for shutdown, SIGHUP for skill reload.
	// SIGHUP handler is wired after factory is created below.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

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

	// Declare variables for Tier 2 model (used by reasoning and deliberation tools)
	var tier2ModelLimited model.Model
	var tier2ModelString string

	// Create Tier 2 model for reasoning escalation if enabled
	if cfg.Reasoning.Enabled {
		tier2ModelString = cfg.ReasoningTier2Model(configModel)
		tier2Model := createTier2ModelForGateway(tier2ModelString, logger)
		if tier2Model != nil {
			tier2ModelLimited = kaggenModel.NewRateLimitedModel(tier2Model, 1) // Conservative rate limit for deep thinking
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

	// Initialize web search if enabled
	if webSearchTool := tools.WebSearchTool(cfg.WebSearch); webSearchTool != nil {
		toolList = append(toolList, webSearchTool)
		logger.Info("web search enabled", "provider", cfg.WebSearch.Provider)
	}

	// Initialize persistent work backlog
	backlogStore, err := backlog.NewStore(cfg.BacklogDBPath())
	if err != nil {
		return fmt.Errorf("open backlog store: %w", err)
	}
	defer backlogStore.Close()
	toolList = append(toolList, tools.BacklogTools(backlogStore)...)
	cronTS, cronTools := tools.NewCronToolSet(cfg)
	toolList = append(toolList, cronTools...)
	logger.Info("backlog store enabled", "db", cfg.BacklogDBPath())

	// Register deliberation tool if Tier 2 model is available
	if tier2ModelLimited != nil {
		deliberationTool := tools.NewDeliberationTool(tier2ModelLimited, backlogStore, cfg.Reasoning, tier2ModelString, logger)
		if deliberationTool != nil {
			toolList = append(toolList, deliberationTool)
			logger.Info("plan_deliberate enabled")
		}
	}

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

	// Register creativity tools if enabled
	if cfg.Creativity.Enabled && tier2ModelLimited != nil {
		explorationTool := tools.NewExplorationTool(tier2ModelLimited, cfg.Creativity, tier2ModelString, logger)
		if explorationTool != nil {
			toolList = append(toolList, explorationTool)
			logger.Info("explore_approaches enabled")
		}

		// Analogy tool requires memory service
		if memService != nil {
			maxAnalogies := cfg.Creativity.MaxAnalogies
			if maxAnalogies <= 0 {
				maxAnalogies = 5
			}
			analogyTool := tools.NewAnalogyTool(memService, tier2ModelLimited, tier2ModelString, maxAnalogies, logger)
			if analogyTool != nil {
				toolList = append(toolList, analogyTool)
				logger.Info("find_analogies enabled")
			}
		}

		synthesisTool := tools.NewSynthesisTool(tier2ModelLimited, cfg.Creativity, tier2ModelString, logger)
		if synthesisTool != nil {
			toolList = append(toolList, synthesisTool)
			logger.Info("synthesize_solution enabled")
		}
	}

	// Create file memory for bootstrap loading
	fileMemory := memory.NewFileMemory(workspace)

	// Skill directories to scan.
	skillDirs := []string{
		filepath.Join(workspace, "skills"),
		config.ExpandPath("~/.kaggen/skills"),
	}

	// Create supervisor if enabled.
	var agentOpts []kaggenAgent.AgentOption
	supervisor := kaggenAgent.NewSupervisor(cfg.Agent.Supervisor, logger)
	if supervisor != nil {
		agentOpts = append(agentOpts, kaggenAgent.WithSupervisor(supervisor))
		logger.Info("agent supervisor enabled",
			"ollama_model", cfg.Agent.Supervisor.OllamaModel,
			"check_interval", cfg.Agent.Supervisor.CheckInterval)
	}

	// Create approval audit store.
	auditStore, err := kaggenAgent.NewAuditStore(cfg.AuditDBPath())
	if err != nil {
		logger.Warn("failed to open audit store", "error", err)
	} else {
		agentOpts = append(agentOpts, kaggenAgent.WithAuditStore(auditStore))
		defer auditStore.Close()
	}

	// Compile auto-approval rules from config.
	if len(cfg.Approval.AutoApprove) > 0 {
		rules := make([]kaggenAgent.AutoApproveRule, len(cfg.Approval.AutoApprove))
		for i, r := range cfg.Approval.AutoApprove {
			rules[i] = kaggenAgent.AutoApproveRule{Tool: r.Tool, Pattern: r.Pattern}
		}
		compiled, err := kaggenAgent.CompileAutoRules(rules)
		if err != nil {
			logger.Warn("failed to compile auto-approve rules", "error", err)
		} else {
			agentOpts = append(agentOpts, kaggenAgent.WithAutoRules(compiled))
			logger.Info("auto-approve rules loaded", "count", len(compiled))
		}
	}

	// Build the initial agent via the factory/provider pattern.
	// This enables hot-reload of skills on SIGHUP without restarting.
	initialAgent, err := kaggenAgent.BuildInitialAgentWithOpts(modelAdapter, toolList, fileMemory, skillDirs, memService, backlogStore, logger, []int{cfg.Agent.MaxHistoryRuns, cfg.Agent.PreloadMemory, cfg.Agent.MaxTurnsPerTask}, agentOpts...)
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}
	provider := kaggenAgent.NewAgentProvider(initialAgent)
	factory := kaggenAgent.NewAgentFactory(modelAdapter, toolList, fileMemory, memService, backlogStore, skillDirs, provider, logger, cfg.Agent.MaxHistoryRuns, cfg.Agent.PreloadMemory, cfg.Agent.MaxTurnsPerTask)
	if supervisor != nil {
		factory.SetSupervisor(supervisor)
	}
	if auditStore != nil {
		factory.SetAuditStore(auditStore)
	}

	// Create session service with appropriate backend
	sessionService, err := createSessionService(cfg)
	if err != nil {
		return fmt.Errorf("create session service: %w", err)
	}
	defer sessionService.Close()

	// Wire LLM model and session namer into the file service.
	if fs, ok := sessionService.(*kaggenSession.FileService); ok {
		fs.SetModel(modelAdapter)
		fs.SetLogger(logger)
		namer := kaggenSession.NewSessionNamer(
			cfg.Agent.Supervisor.OllamaBaseURL,
			cfg.Agent.Supervisor.OllamaModel,
			logger,
		)
		if namer != nil {
			fs.SetNamer(namer)
			logger.Info("session auto-naming enabled via Ollama",
				"url", cfg.Agent.Supervisor.OllamaBaseURL,
				"model", cfg.Agent.Supervisor.OllamaModel)
		}
		// Wire up pre-compaction memory extraction hook.
		// This ensures memories are extracted before events are deleted during /compact.
		if fms, ok := memService.(*memory.FileMemoryService); ok {
			fs.SetCompactionHook(fms)
		}
	}

	// Wrap session service to strip binary data (images, files) from history.
	sanitizedSession := kaggenSession.NewSanitizeWrapper(sessionService)

	// Create log streamer for dashboard SSE logs.
	logStreamer := gateway.NewLogStreamer()
	wrappedHandler := gateway.NewStreamerHandler(logStreamer, logger.Handler())
	logger = slog.New(wrappedHandler)

	// Create dashboard API (client count wired after server creation).
	dashboardAPI := gateway.NewDashboardAPI(provider, backlogStore, sanitizedSession, cfg, logStreamer, nil)

	// Create gateway server (with optional memory service)
	server := gateway.NewServer(cfg, sanitizedSession, provider, logger, dashboardAPI, memService)

	// Wire client count and task broadcast now that we have the server.
	dashboardAPI.SetClientCountFunc(server.ClientCount)
	dashboardAPI.SetBroadcastFunc(server.Broadcast)
	dashboardAPI.SetP2PNodeFunc(server.P2PNode)
	dashboardAPI.WireTaskEvents()

	// Mount callback handler for external tasks.
	server.MountCallbacks(provider.InFlightStore())

	// Wire external task tools into the agent's tool set and coordinator.
	externalTS := tools.NewExternalTaskToolSet(provider.InFlightStore(), server.CallbackBaseURL)
	if cfg.Gateway.PubSub.Enabled && cfg.Gateway.PubSub.Topic != "" {
		externalTS.SetPubSubTopic(cfg.Gateway.PubSub.Topic)
	}
	toolList = append(toolList, externalTS.Tools()...)

	// Build external delivery config so the coordinator knows how external
	// systems should send results back (Pub/Sub topic, callback URL, etc.).
	var extConfig *kaggenAgent.ExternalDeliveryConfig
	if cfg.Gateway.PubSub.Enabled || cfg.Gateway.Tunnel.Enabled {
		extConfig = &kaggenAgent.ExternalDeliveryConfig{
			PubSubProject:   cfg.PubSubProjectID(),
			PubSubTopic:     cfg.Gateway.PubSub.Topic,
			TunnelEnabled:   cfg.Gateway.Tunnel.Enabled,
			CallbackBaseURL: cfg.Gateway.CallbackBaseURL,
		}
	}

	// Give the coordinator external task tools + reload_skills + config awareness, then rebuild.
	coordTools := externalTS.Tools()
	if reloadTool := tools.ReloadSkillsTool(factory); reloadTool != nil {
		coordTools = append(coordTools, reloadTool)
		logger.Info("reload_skills tool enabled for autonomous skill acquisition")
	}
	factory.SetExtraCoordinatorTools(coordTools...)
	if extConfig != nil {
		factory.SetExternalConfig(extConfig)
	}
	if err := factory.Rebuild(); err != nil {
		logger.Warn("failed to rebuild agent with external config", "error", err)
	}

	// Start the external task timeout reaper.
	provider.InFlightStore().StartExternalReaper(ctx, func(taskID string, state *kaggenAgent.TaskState) {
		logger.Warn("external task timed out", "task_id", taskID, "name", state.Task)
		// Log approval timeout to audit store.
		if state.ApprovalRequest != nil {
			if as := provider.AuditStore(); as != nil {
				_ = as.RecordResolution(taskID, "timed_out", "system", time.Now())
			}
		}
		// Inject timeout notification into the originating session.
		if err := server.Handler().InjectCompletion(
			context.Background(), state.SessionID, state.UserID, taskID, "external",
			fmt.Sprintf("External task timed out: %s", state.Task),
		); err != nil {
			logger.Warn("failed to inject timeout notification", "task_id", taskID, "error", err)
		}
	})

	// Wire proactive engine to cron tools for live reload.
	cronTS.SetEngine(server.ProactiveEngine())

	// Wire up async completion: when a sub-agent finishes, inject the result
	// back into the coordinator's session so it can synthesize and notify the user.
	factory.SetCompletionFunc(func(taskID, result string, taskErr error, policy kaggenAgent.TriggerPolicy) {
		// For errors, always inject immediately (even for TriggerQueue).
		// For successful TriggerQueue results, defer to next user message.
		if policy != kaggenAgent.TriggerAuto && taskErr == nil {
			return
		}

		content := result
		if taskErr != nil {
			content = fmt.Sprintf("Error: %v", taskErr)
		}

		state, ok := provider.InFlightStore().Get(taskID)
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

	// Wire approval notifications: when a guarded tool is invoked, notify the client.
	provider.SetApprovalNotifyFunc(func(sessionID string, resp *kaggenChannel.Response) error {
		// Try to route through the session's respond callback.
		if respond, meta, ok := server.Handler().Responders().Get(sessionID); ok {
			if resp.Metadata == nil {
				resp.Metadata = make(map[string]any)
			}
			// Merge channel-specific metadata (chat_id, etc.) so Telegram can route.
			for k, v := range meta {
				if _, exists := resp.Metadata[k]; !exists {
					resp.Metadata[k] = v
				}
			}
			return respond(resp)
		}
		return nil
	})

	// Start watchdog to detect stale tasks and alert via Telegram.
	watchdog := kaggenAgent.NewWatchdog(provider.InFlightStore(), 30*time.Minute, func(task *kaggenAgent.TaskState, dur time.Duration) {
		msg := fmt.Sprintf("Task stuck: %s (%s) has been running for %s",
			task.AgentName, task.ID[:8], dur.Round(time.Minute))
		logger.Warn(msg, "task_id", task.ID, "agent", task.AgentName)
		for _, chatID := range cfg.Channels.Telegram.AllowedChats {
			server.SendTelegramAlert(chatID, msg)
		}
	}, logger)
	go watchdog.Start(ctx)

	// Start signal handler goroutine (SIGHUP reloads skills, others shut down).
	// Also listen on factory.ReloadCh() for programmatic reload requests.
	go func() {
		for {
			select {
			case sig := <-sigCh:
				if sig == syscall.SIGHUP {
					logger.Info("SIGHUP received, reloading skills...")
					if err := factory.Rebuild(); err != nil {
						logger.Error("skill reload failed", "error", err)
					}
					continue
				}
				fmt.Println("\nShutting down gateway server...")
				cancel()
				return
			case <-factory.ReloadCh():
				logger.Info("programmatic reload requested, reloading skills...")
				err := factory.Rebuild()
				if err != nil {
					logger.Error("skill reload failed", "error", err)
				}
				factory.SignalReloadDone(err)
			}
		}
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
	if cfg.Memory.Search.Enabled {
		fmt.Println("Memory Search: enabled")
	} else {
		fmt.Println("Memory Search: disabled")
	}
	if cfg.Browser.Enabled {
		fmt.Printf("Browser Control: enabled (%d profiles)\n", len(cfg.Browser.Profiles))
	} else {
		fmt.Println("Browser Control: disabled")
	}
	if cfg.WebSearch.Provider != "" {
		fmt.Printf("Web Search: enabled (%s)\n", cfg.WebSearch.Provider)
	} else {
		fmt.Println("Web Search: disabled")
	}
	if n := len(provider.SubAgents()); n > 0 {
		fmt.Printf("Skills: %d sub-agents\n", n)
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
	if cfg.Gateway.Tunnel.Enabled {
		fmt.Printf("Tunnel: enabled (%s)\n", server.CallbackBaseURL())
	}
	if cfg.Gateway.PubSub.Enabled {
		fmt.Printf("Pub/Sub: enabled (project=%s, sub=%s)\n", cfg.PubSubProjectID(), cfg.Gateway.PubSub.Subscription)
		if cfg.Gateway.PubSub.Topic != "" {
			fmt.Printf("Pub/Sub Topic: %s\n", cfg.Gateway.PubSub.Topic)
		}
	}
	if cfg.P2P.Enabled {
		fmt.Println("P2P: enabled")
	}
	fmt.Printf("Callbacks: %s/callbacks/\n", server.CallbackBaseURL())
	fmt.Println()

	// Determine protocol based on TLS config
	httpProto := "http"
	wsProto := "ws"
	if cfg.Gateway.TLS.Enabled {
		httpProto = "https"
		wsProto = "wss"
		fmt.Printf("TLS: enabled (cert=%s)\n", cfg.Gateway.TLS.CertFile)
	}
	fmt.Printf("Dashboard: %s://localhost:%d/\n", httpProto, cfg.Gateway.Port)
	fmt.Printf("WebSocket endpoint: %s://localhost:%d/ws\n", wsProto, cfg.Gateway.Port)
	fmt.Printf("Health check: %s://localhost:%d/health\n", httpProto, cfg.Gateway.Port)
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

// createTier2ModelForGateway creates a model adapter from a "provider/model" string.
// Used for creating the Tier 2 (deep thinking) model for reasoning escalation.
// Returns a trpc model.Model interface that implements GenerateContent.
func createTier2ModelForGateway(modelString string, logger *slog.Logger) model.Model {
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
