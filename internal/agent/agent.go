// Package agent implements the Kaggen AI agent using trpc-agent-go.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	trpcmemory "trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/team"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"

	"github.com/yourusername/kaggen/internal/backlog"
	"github.com/yourusername/kaggen/internal/config"
	"github.com/yourusername/kaggen/internal/memory"
	"github.com/yourusername/kaggen/internal/pipeline"
)

const (
	// AgentName is the name of the Kaggen agent.
	AgentName = "kaggen"
)

// ExternalDeliveryConfig describes how external systems can send results back
// to Kaggen. When set, this information is injected into the coordinator's
// system prompt so it can instruct external systems (GCP instances, CI
// pipelines, etc.) on where and how to publish callbacks.
type ExternalDeliveryConfig struct {
	PubSubProject   string // GCP project ID for Pub/Sub delivery
	PubSubTopic     string // Pub/Sub topic name
	TunnelEnabled   bool   // whether a reverse tunnel is active
	CallbackBaseURL string // public callback URL (from tunnel or manual config)
}

// Agent wraps a trpc-agent-go Team for Kaggen's coordinator pattern.
type Agent struct {
	team          *team.Team
	memory        *memory.FileMemory
	tools         []tool.Tool
	model         model.Model
	logger        *slog.Logger
	inFlightStore *InFlightStore
	dispatcher    *asyncDispatcher
	pipelines     []pipeline.Pipeline
}

// AgentOption configures optional aspects of the Agent.
type AgentOption func(*agentOptions)

type agentOptions struct {
	extConfig           *ExternalDeliveryConfig
	extraCoordTools     []tool.Tool
}

// WithExternalConfig injects external delivery configuration into the
// coordinator's system prompt so it can instruct external systems on how
// to send results back.
func WithExternalConfig(cfg *ExternalDeliveryConfig) AgentOption {
	return func(o *agentOptions) { o.extConfig = cfg }
}

// WithExtraCoordinatorTools adds additional tools to the coordinator's
// tool set (e.g. external_task_register, external_task_list).
func WithExtraCoordinatorTools(tools ...tool.Tool) AgentOption {
	return func(o *agentOptions) { o.extraCoordTools = append(o.extraCoordTools, tools...) }
}

// NewAgent creates a new Kaggen agent using the Coordinator Team pattern.
// When subAgents is non-empty, a Team is created with the coordinator delegating
// to specialist sub-agents. When subAgents is empty, a single-agent team is
// created with a general-purpose member as a fallback.
//
// completeFn is called when an async sub-agent finishes. It may be nil during
// construction and set later via SetCompletionFunc (to break circular deps).
func NewAgent(m model.Model, tools []tool.Tool, mem *memory.FileMemory, subAgents []agent.Agent, completeFn CompletionFunc, memSvc trpcmemory.Service, bStore *backlog.Store, logger *slog.Logger, maxHistoryRuns []int, opts ...AgentOption) (*Agent, error) {
	var ao agentOptions
	for _, o := range opts {
		o(&ao)
	}

	// Build instruction from bootstrap files.
	instruction, err := buildInstruction(mem, subAgents, ao.extConfig)
	if err != nil {
		return nil, fmt.Errorf("build instruction: %w", err)
	}

	// If no sub-agents were provided, create a minimal general-purpose member.
	if len(subAgents) == 0 {
		gp := llmagent.New("general",
			llmagent.WithModel(m),
			llmagent.WithTools(tools),
			llmagent.WithInstruction("You are a general-purpose assistant. Use the available tools to complete tasks."),
			llmagent.WithDescription("General-purpose agent with standard tools."),
			llmagent.WithMaxLLMCalls(25),
			llmagent.WithMaxToolIterations(30),
		)
		subAgents = []agent.Agent{gp}
	}

	// Build the async dispatch infrastructure.
	store := NewInFlightStore()
	agentMap := make(map[string]agent.Agent, len(subAgents))
	for _, sa := range subAgents {
		agentMap[sa.Info().Name] = sa
	}

	// Use a no-op completion func if none provided yet.
	if completeFn == nil {
		completeFn = func(taskID, result string, err error, policy TriggerPolicy) {}
	}

	// Default to 40 history messages if not specified (prevents unbounded context growth).
	historyLimit := 40
	if len(maxHistoryRuns) > 0 && maxHistoryRuns[0] > 0 {
		historyLimit = maxHistoryRuns[0]
	}

	// Default to 20 preloaded memories in the system prompt (survives context pruning).
	// Can be overridden via maxHistoryRuns[1] (0 = disabled, -1 = all).
	preloadMemory := 20
	if len(maxHistoryRuns) > 1 {
		preloadMemory = maxHistoryRuns[1]
	}

	// Default to 75 max turns per async task (circuit breaker).
	// Can be overridden via maxHistoryRuns[2].
	maxTurnsPerTask := 75
	if len(maxHistoryRuns) > 2 && maxHistoryRuns[2] > 0 {
		maxTurnsPerTask = maxHistoryRuns[2]
	}

	// Load pipeline definitions for dispatch-time stage gating.
	pipelinesDir := config.ExpandPath("~/.kaggen/pipelines")
	dispatchPipelines, _ := pipeline.LoadAll(pipelinesDir)
	dispatchPipelineAgents := pipeline.AgentSet(dispatchPipelines)

	dispatchTool, dispatcher := NewAsyncDispatchTool(agentMap, store, completeFn, m, memSvc, logger, dispatchPipelines, dispatchPipelineAgents, bStore, maxTurnsPerTask)
	statusTool := NewTaskStatusTool(store)

	pipelineStatusTool := NewPipelineStatusTool(store, dispatchPipelines)
	cancelTaskTool := NewCancelTaskTool(store)

	// Coordinator gets routing tools plus read-only investigation tools.
	// Write and exec remain on sub-agents to prevent the coordinator from
	// bypassing specialists for mutating operations.
	readTool := newCoordinatorReadTool(mem.Workspace())
	coordinatorTools := []tool.Tool{dispatchTool, statusTool, pipelineStatusTool, cancelTaskTool, readTool}
	coordinatorTools = append(coordinatorTools, ao.extraCoordTools...)

	// Build tool callbacks to track synchronous Team delegations in InFlightStore.
	// This makes sync member-agent calls visible in the dashboard alongside async dispatch_task calls.
	memberNames := make(map[string]bool, len(subAgents))
	for _, sa := range subAgents {
		memberNames[sa.Info().Name] = true
	}

	// Log registered member names for debugging.
	for name := range memberNames {
		logger.Info("registered member agent", "name", name)
	}

	// Infrastructure tools that should not create tasks (internal plumbing).
	infraTools := map[string]bool{
		"dispatch_task":   true,
		"task_status":     true,
		"pipeline_status": true,
		"cancel_task":     true,
	}

	// Per-invocation counters for coordinator tool calls (used for turn numbering).
	var coordMu sync.Mutex
	coordCallCount := make(map[string]int)    // invID -> call count
	coordToolNames := make(map[string][]string) // invID -> unique tool names used

	callbacks := &tool.Callbacks{}
	callbacks.RegisterBeforeTool(tool.BeforeToolCallbackStructured(
		func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
			isMember := memberNames[args.ToolName]
			isInfra := infraTools[args.ToolName]

			logger.Info("BeforeTool callback",
				"tool", args.ToolName,
				"call_id", args.ToolCallID,
				"is_member", isMember,
				"is_infra", isInfra,
				"args_len", len(args.Arguments))

			if isInfra {
				return &tool.BeforeToolResult{}, nil
			}

			var sessionID, userID, invID string
			if inv, ok := agent.InvocationFromContext(ctx); ok {
				invID = inv.InvocationID
				if inv.Session != nil {
					sessionID = inv.Session.ID
					userID = inv.Session.UserID
				}
			}

			if isMember {
				// Sync delegation to a sub-agent — register as its own task.
				taskID := args.ToolCallID
				taskDesc := string(args.Arguments)
				var ma struct {
					Message string `json:"message"`
				}
				if json.Unmarshal(args.Arguments, &ma) == nil && ma.Message != "" {
					taskDesc = ma.Message
				}
				store.Register(taskID, args.ToolName, taskDesc, TriggerAuto, sessionID, userID)
				logger.Info("sync member task registered",
					"task_id", taskID, "agent", args.ToolName, "invocation_id", invID)
			} else {
				// Coordinator direct tool call — group by invocation ID.
				coordTaskID := "coord-" + invID

				coordMu.Lock()
				coordCallCount[invID]++
				callNum := coordCallCount[invID]
				// Track unique tool names for task description.
				seen := false
				for _, n := range coordToolNames[invID] {
					if n == args.ToolName {
						seen = true
						break
					}
				}
				if !seen {
					coordToolNames[invID] = append(coordToolNames[invID], args.ToolName)
				}
				// Build description: prefer task content from dispatch args over tool names.
				desc := "Coordinator: " + strings.Join(coordToolNames[invID], ", ")
				var dispatchArgs struct {
					Task      string `json:"task"`
					AgentName string `json:"agent_name"`
				}
				if json.Unmarshal(args.Arguments, &dispatchArgs) == nil && dispatchArgs.Task != "" {
					taskPreview := dispatchArgs.Task
					if len(taskPreview) > 100 {
						taskPreview = taskPreview[:100] + "..."
					}
					desc = "Coordinator: " + taskPreview
				}
				coordMu.Unlock()

				if _, exists := store.Get(coordTaskID); !exists {
					store.Register(coordTaskID, "coordinator", desc, TriggerAuto, sessionID, userID)
					logger.Info("coordinator task registered",
						"task_id", coordTaskID, "invocation_id", invID)
				} else {
					store.UpdateTask(coordTaskID, desc)
				}
				// Extract input preview for dashboard visibility.
				inputPreview := string(args.Arguments)
				if len(inputPreview) > 200 {
					inputPreview = inputPreview[:200] + "..."
				}
				store.AddEvent(coordTaskID, &TaskEvent{
					Timestamp: time.Now(),
					Type:      "tool_call",
					Turn:      callNum,
					Tools:     []string{args.ToolName},
					Content:   inputPreview,
				})
			}
			return &tool.BeforeToolResult{}, nil
		},
	))
	callbacks.RegisterAfterTool(tool.AfterToolCallbackStructured(
		func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
			isMember := memberNames[args.ToolName]
			isInfra := infraTools[args.ToolName]

			logger.Info("AfterTool callback",
				"tool", args.ToolName,
				"call_id", args.ToolCallID,
				"is_member", isMember,
				"is_infra", isInfra,
				"has_error", args.Error != nil)

			if isInfra {
				return &tool.AfterToolResult{}, nil
			}

			if isMember {
				taskID := args.ToolCallID
				if args.Error != nil {
					store.Fail(taskID, args.Error.Error())
					logger.Info("sync member task failed", "task_id", taskID, "agent", args.ToolName)
				} else {
					result := resultToString(args.Result)
					store.Complete(taskID, result)
					logger.Info("sync member task completed", "task_id", taskID, "agent", args.ToolName, "result_len", len(result))
				}
			} else {
				// Coordinator direct tool call — add result event.
				invID := ""
				if inv, ok := agent.InvocationFromContext(ctx); ok {
					invID = inv.InvocationID
				}
				coordTaskID := "coord-" + invID

				coordMu.Lock()
				callNum := coordCallCount[invID]
				coordMu.Unlock()

				resultPreview := resultToString(args.Result)
				if len(resultPreview) > 200 {
					resultPreview = resultPreview[:200] + "..."
				}
				evtType := "response"
				if args.Error != nil {
					evtType = "error"
					resultPreview = args.Error.Error()
				}
				store.AddEvent(coordTaskID, &TaskEvent{
					Timestamp: time.Now(),
					Type:      evtType,
					Turn:      callNum,
					Tools:     []string{args.ToolName},
					Content:   resultPreview,
				})
			}
			return &tool.AfterToolResult{}, nil
		},
	))

	coordinatorOpts := []llmagent.Option{
		llmagent.WithModel(m),
		llmagent.WithTools(coordinatorTools),
		llmagent.WithInstruction(instruction),
		llmagent.WithDescription("Kaggen personal AI assistant coordinator"),
		llmagent.WithMaxHistoryRuns(historyLimit),
		llmagent.WithPreloadMemory(preloadMemory),
		llmagent.WithToolCallbacks(callbacks),
	}

	coordinator := llmagent.New(AgentName, coordinatorOpts...)

	t, err := team.New(
		coordinator,
		subAgents,
		team.WithMemberToolConfig(team.MemberToolConfig{
			HistoryScope: team.HistoryScopeIsolated,
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("create team: %w", err)
	}

	return &Agent{
		team:          t,
		memory:        mem,
		tools:         tools,
		model:         m,
		logger:        logger,
		inFlightStore: store,
		dispatcher:    dispatcher,
		pipelines:     dispatchPipelines,
	}, nil
}

// InFlightStore returns the async task store for external components
// (e.g. the handler) to query task state.
func (a *Agent) InFlightStore() *InFlightStore {
	return a.inFlightStore
}

// Pipelines returns the loaded pipeline definitions.
func (a *Agent) Pipelines() []pipeline.Pipeline {
	return a.pipelines
}

// SetCompletionFunc updates the async dispatch completion callback.
// Call this after handler construction to wire up completion event injection.
func (a *Agent) SetCompletionFunc(fn CompletionFunc) {
	a.dispatcher.SetCompletionFunc(fn)
}

// Run executes the agent with the given invocation.
// After the event stream closes, any open coordinator task is marked completed.
func (a *Agent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	inner, err := a.team.Run(ctx, invocation)
	if err != nil {
		return nil, err
	}

	// Wrap the channel to detect when the turn ends.
	out := make(chan *event.Event)
	go func() {
		defer close(out)
		for evt := range inner {
			out <- evt
		}
		// Turn finished — complete any open coordinator task for this invocation.
		coordTaskID := "coord-" + invocation.InvocationID
		if ts, ok := a.inFlightStore.Get(coordTaskID); ok && ts.Status == TaskRunning {
			// Build a summary from the events: count tool calls and list unique tools.
			var toolSet []string
			seen := make(map[string]bool)
			calls := 0
			for _, evt := range ts.Events {
				if evt.Type == "tool_call" {
					calls++
					for _, t := range evt.Tools {
						if !seen[t] {
							seen[t] = true
							toolSet = append(toolSet, t)
						}
					}
				}
			}
			summary := fmt.Sprintf("Completed %d tool call(s): %s", calls, strings.Join(toolSet, ", "))
			a.inFlightStore.Complete(coordTaskID, summary)
			a.logger.Info("coordinator task completed", "task_id", coordTaskID, "summary", summary)
		}
	}()
	return out, nil
}

// Tools returns the list of tools available to this agent.
func (a *Agent) Tools() []tool.Tool {
	return a.team.Tools()
}

// Info returns basic information about this agent.
func (a *Agent) Info() agent.Info {
	return a.team.Info()
}

// SubAgents returns the list of sub-agents available to this agent.
func (a *Agent) SubAgents() []agent.Agent {
	return a.team.SubAgents()
}

// FindSubAgent finds a sub-agent by name.
func (a *Agent) FindSubAgent(name string) agent.Agent {
	return a.team.FindSubAgent(name)
}

// buildInstruction constructs the system instruction from bootstrap files.
func buildInstruction(mem *memory.FileMemory, subAgents []agent.Agent, extConfig *ExternalDeliveryConfig) (string, error) {
	bootstrap, err := mem.LoadBootstrap()
	if err != nil {
		return "", fmt.Errorf("load bootstrap: %w", err)
	}

	var instruction string
	instruction = "You are Kaggen, a personal AI assistant.\n\n"

	if bootstrap != "" {
		instruction += "## Context & Instructions\n\n"
		instruction += bootstrap
		instruction += "\n\n"
	}

	instruction += "## Operating Guidelines\n\n"
	instruction += "- Be helpful, direct, and concise\n"
	instruction += "- Use tools when needed to accomplish tasks\n"
	instruction += "- Ask for clarification if a request is ambiguous\n"
	instruction += "- When you complete a task, summarize what you did\n"
	instruction += "- To send a file to the user (e.g. show an image, deliver a document), include [send_file: /path/to/file] in your response. The file will be delivered through the chat channel.\n"
	instruction += "\n"
	instruction += "## Task Orchestration\n\n"
	instruction += "You have access to specialist sub-agents via `dispatch_task` (async) and team member tools (sync).\n\n"

	instruction += "### Guidelines\n"
	instruction += "1. You are a COORDINATOR — you delegate tasks to sub-agents and synthesize their results. You have `read` access for investigation, but delegate all write/exec work to sub-agents.\n"
	instruction += "2. For simple questions you can answer from your knowledge, respond directly without delegating.\n"
	instruction += "3. Use `read` to investigate files, logs, and outputs before deciding how to act. For any task requiring writes, code changes, or commands: delegate to the appropriate sub-agent.\n"
	instruction += "4. Use async dispatch (`dispatch_task`) for long-running agents. Pipelines are optional — for small fixes or single-agent tasks, dispatch directly without a pipeline. Use pipelines only when the task genuinely benefits from a structured multi-stage process.\n"
	instruction += "5. After dispatching an async task, STOP and tell the user it's in progress. Do NOT poll `task_status` in a loop — you will be notified automatically via a [Task Completed] message when the task finishes.\n"
	instruction += "6. Notify the user when you start long-running work and when it completes.\n"
	instruction += "7. Synthesize results from sub-agents into a coherent response for the user.\n"
	instruction += "8. If a sub-agent fails, attempt one round of autonomous diagnosis (read logs, check errors) and retry or adjust the task. If the second attempt also fails, inform the user with a summary of what you tried.\n"
	instruction += "9. When you receive a [Task Completed] message, summarize the result for the user.\n"

	instruction += "\n### Task Decomposition\n\n"
	instruction += "For complex tasks requiring multiple steps or agents:\n"
	instruction += "1. Use `backlog_decompose` to create a plan with subtasks\n"
	instruction += "2. Report the plan to the user\n"
	instruction += "3. For each subtask, dispatch the appropriate agent with `dispatch_task`, setting `backlog_item_id` to the subtask ID\n"
	instruction += "4. Use `backlog_plan_status` to check overall progress\n"
	instruction += "5. When all subtasks complete, synthesize results and report to the user\n\n"
	instruction += "Use decomposition when: 3+ distinct steps are needed, multiple specialist agents are involved, or the work will span significant time.\n"
	instruction += "Do NOT decompose: simple single-agent tasks, direct questions, or tasks already well-scoped for one specialist.\n"

	// Load pipeline definitions for optional workflow section.
	pipelinesDir := config.ExpandPath("~/.kaggen/pipelines")
	pipelines, _ := pipeline.LoadAll(pipelinesDir)

	if len(pipelines) > 0 {
		instruction += "\n### Available Workflows\n\n"
		instruction += "Multi-agent workflows are available for complex projects. Use them when the user explicitly requests a structured process (e.g. \"build this from scratch\", \"run the full dev pipeline\") or when you judge a multi-stage workflow is appropriate.\n"
		instruction += "To activate a workflow, dispatch each stage sequentially with the `pipeline` field set to the pipeline name on each `dispatch_task` call.\n"
		instruction += pipeline.BuildInstruction(pipelines)
		instruction += "\nWhen running a pipeline: after a [Task Completed] message for a pipeline stage, dispatch the next stage.\n"
		instruction += "On failure or cancellation, re-dispatch the failed stage — do NOT restart from stage 1.\n"
	}

	instruction += "\nWhen dispatching project tasks, always include the full project directory path in the task description. "
	instruction += "Sub-agents will automatically load project-specific instructions from AGENTS.md in the project directory.\n"

	// List available sub-agents.
	if len(subAgents) > 0 {
		instruction += "\n### Available Sub-Agents\n\n"
		for _, sa := range subAgents {
			info := sa.Info()
			instruction += fmt.Sprintf("- **%s**: %s\n", info.Name, info.Description)
		}
	}

	// Build agent name list for dispatch_task reference.
	if len(subAgents) > 0 {
		names := make([]string, len(subAgents))
		for i, sa := range subAgents {
			names[i] = sa.Info().Name
		}
		instruction += fmt.Sprintf("\nValid agent_name values for dispatch_task: %s\n", strings.Join(names, ", "))
	}

	// Inject external delivery config so the coordinator knows how external
	// systems should send results back.
	if extConfig != nil {
		instruction += "\n## External Task Delivery\n\n"
		instruction += "You can register external tasks using `external_task_register` and monitor them with `external_task_list`.\n"
		instruction += "When you register a task, you receive a `task_id` that the external system uses to correlate its results.\n\n"

		if extConfig.PubSubProject != "" && extConfig.PubSubTopic != "" {
			instruction += "### GCP Pub/Sub (primary)\n\n"
			instruction += fmt.Sprintf("- **Project:** `%s`\n", extConfig.PubSubProject)
			instruction += fmt.Sprintf("- **Topic:** `%s`\n", extConfig.PubSubTopic)
			instruction += "- **Message format:** Set `task_id` as a Pub/Sub message attribute. Body is JSON: `{\"status\": \"success\"|\"error\", \"result\": {...}}`\n\n"
			instruction += "When instructing external systems (GCP instances, CI pipelines, etc.) to report back, provide them with:\n"
			instruction += fmt.Sprintf("1. The `task_id` from `external_task_register`\n")
			instruction += fmt.Sprintf("2. Pub/Sub project `%s` and topic `%s`\n", extConfig.PubSubProject, extConfig.PubSubTopic)
			instruction += "3. The message format above\n\n"
		}

		if extConfig.TunnelEnabled && extConfig.CallbackBaseURL != "" {
			instruction += "### Direct HTTP Callback\n\n"
			instruction += fmt.Sprintf("- **Callback URL:** `%s/callbacks/{task_id}`\n", extConfig.CallbackBaseURL)
			instruction += "- **Method:** POST with JSON body: `{\"status\": \"success\"|\"error\", \"result\": {...}}`\n\n"
		}
	}

	return instruction, nil
}

// coordinatorReadArgs defines input for the coordinator's read tool.
type coordinatorReadArgs struct {
	Path     string `json:"path" jsonschema:"required,description=The path to the file to read. Can be absolute or relative to the workspace."`
	MaxLines *int   `json:"max_lines,omitempty" jsonschema:"description=Maximum number of lines to read. Defaults to 1000 if not specified."`
}

type coordinatorReadResult struct {
	Content string `json:"content"`
	Message string `json:"message"`
}

// newCoordinatorReadTool creates a read-only file tool for the coordinator.
func newCoordinatorReadTool(workspace string) tool.Tool {
	return function.NewFunctionTool(
		func(_ context.Context, args coordinatorReadArgs) (*coordinatorReadResult, error) {
			path := args.Path
			if path == "" {
				return nil, fmt.Errorf("path is required")
			}
			maxLines := 1000
			if args.MaxLines != nil {
				maxLines = *args.MaxLines
			}
			// Resolve path.
			resolved := path
			if !filepath.IsAbs(path) {
				if strings.HasPrefix(path, "~/") {
					if home, err := os.UserHomeDir(); err == nil {
						resolved = filepath.Join(home, path[2:])
					}
				} else {
					resolved = filepath.Join(workspace, path)
				}
			}
			data, err := os.ReadFile(resolved)
			if err != nil {
				return nil, fmt.Errorf("failed to read file: %w", err)
			}
			content := string(data)
			lines := strings.Split(content, "\n")
			total := len(lines)
			if total > maxLines {
				lines = lines[:maxLines]
				content = strings.Join(lines, "\n") + fmt.Sprintf("\n... (truncated, showing %d of %d lines)", maxLines, total)
			}
			shown := total
			if shown > maxLines {
				shown = maxLines
			}
			return &coordinatorReadResult{
				Content: content,
				Message: fmt.Sprintf("Read %s (%d lines)", args.Path, shown),
			}, nil
		},
		function.WithName("read"),
		function.WithDescription("Read the contents of a file for investigation. Use this to examine logs, config files, or task outputs before deciding whether to delegate."),
	)
}

// resultToString extracts a readable string from a tool result.
// The Result field is `any` — it may be a string, a struct with a String() method,
// or an arbitrary type. This avoids the `&{...}` output from fmt.Sprintf("%v").
func resultToString(r any) string {
	if r == nil {
		return ""
	}
	switch v := r.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	case error:
		return v.Error()
	default:
		// Try JSON marshaling for structured results.
		if b, err := json.Marshal(v); err == nil {
			return string(b)
		}
		return fmt.Sprintf("%v", v)
	}
}
