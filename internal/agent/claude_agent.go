package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// ClaudeAgent implements agent.Agent by running `claude -p` as a subprocess
// instead of making an LLM API call. This eliminates the intermediate LLM
// round-trip that was previously used just to construct a CLI command.
//
// It uses --output-format stream-json to get real-time events, enabling
// the supervisor to monitor and intervene during execution.
type ClaudeAgent struct {
	name        string
	description string
	claudeModel string        // --model flag (e.g. "sonnet")
	workDir     string        // --add-dir path
	tools       string        // --allowed-tools value
	instruction string        // skill instruction prepended to task prompt
	timeout     time.Duration // subprocess timeout
	logger      *slog.Logger

	// Mutable state for active subprocess (protected by mu).
	mu        sync.Mutex
	activeCmd *exec.Cmd
	cancelFn  context.CancelFunc
	sessionID string
}

// ClaudeAgentOption configures a ClaudeAgent.
type ClaudeAgentOption func(*ClaudeAgent)

// WithClaudeModel sets the --model flag for the claude CLI.
func WithClaudeModel(m string) ClaudeAgentOption {
	return func(a *ClaudeAgent) { a.claudeModel = m }
}

// WithClaudeWorkDir sets the --add-dir path.
func WithClaudeWorkDir(dir string) ClaudeAgentOption {
	return func(a *ClaudeAgent) { a.workDir = dir }
}

// WithClaudeTools sets the --allowed-tools value.
func WithClaudeTools(t string) ClaudeAgentOption {
	return func(a *ClaudeAgent) { a.tools = t }
}

// WithClaudeInstruction sets the skill instruction prepended to each task.
func WithClaudeInstruction(inst string) ClaudeAgentOption {
	return func(a *ClaudeAgent) { a.instruction = inst }
}

// WithClaudeTimeout sets the subprocess timeout.
func WithClaudeTimeout(d time.Duration) ClaudeAgentOption {
	return func(a *ClaudeAgent) { a.timeout = d }
}

// WithClaudeLogger sets the logger.
func WithClaudeLogger(l *slog.Logger) ClaudeAgentOption {
	return func(a *ClaudeAgent) { a.logger = l }
}

// NewClaudeAgent creates a ClaudeAgent that dispatches tasks via `claude -p`.
func NewClaudeAgent(name, description string, opts ...ClaudeAgentOption) *ClaudeAgent {
	a := &ClaudeAgent{
		name:        name,
		description: description,
		claudeModel: "sonnet",
		tools:       "Bash,Read,Edit,Write,Glob,Grep",
		timeout:     30 * time.Minute,
		logger:      slog.Default(),
	}
	for _, o := range opts {
		o(a)
	}
	return a
}

// Stream-JSON event types from `claude -p --output-format stream-json --verbose`.

type claudeStreamEvent struct {
	Type      string          `json:"type"`    // "system", "assistant", "user", "result"
	Subtype   string          `json:"subtype"` // e.g. "init", "success"
	SessionID string          `json:"session_id"`
	Message   *claudeMessage  `json:"message,omitempty"`
	Result    string          `json:"result,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	TotalCost float64         `json:"total_cost_usd,omitempty"`
}

type claudeMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"` // array of claudeContent
	Usage   *claudeUsage    `json:"usage,omitempty"`
}

type claudeContent struct {
	Type  string          `json:"type"` // "text", "tool_use", "tool_result"
	Text  string          `json:"text,omitempty"`
	Name  string          `json:"name,omitempty"`  // tool name for tool_use
	ID    string          `json:"id,omitempty"`    // tool call ID
	Input json.RawMessage `json:"input,omitempty"` // tool args
}

type claudeUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// buildArgs constructs the CLI arguments for a claude -p invocation.
func (a *ClaudeAgent) buildArgs(prompt string) []string {
	args := []string{"-p", prompt, "--output-format", "stream-json", "--verbose", "--dangerously-skip-permissions"}
	if a.claudeModel != "" {
		args = append(args, "--model", a.claudeModel)
	}
	if a.workDir != "" {
		args = append(args, "--add-dir", a.workDir)
	}
	if a.tools != "" {
		args = append(args, "--allowed-tools", a.tools)
	}
	return args
}

// buildPrompt combines the skill instruction with the task text.
func (a *ClaudeAgent) buildPrompt(taskText string) string {
	if a.instruction != "" {
		return a.instruction + "\n\n---\n\n" + taskText
	}
	return taskText
}

// runSubprocess starts a claude subprocess and streams events on the returned channel.
// It sets activeCmd, cancelFn, and sessionID on the agent for Kill/Resume support.
func (a *ClaudeAgent) runSubprocess(ctx context.Context, invocation *agent.Invocation, args []string) (<-chan *event.Event, error) {
	subCtx, cancel := context.WithTimeout(ctx, a.timeout)
	cmd := exec.CommandContext(subCtx, "claude", args...)

	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("claude subprocess stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("claude subprocess start: %w", err)
	}

	// Store active state for Kill().
	a.mu.Lock()
	a.activeCmd = cmd
	a.cancelFn = cancel
	a.sessionID = ""
	a.mu.Unlock()

	invID := ""
	if invocation != nil {
		invID = invocation.InvocationID
	}

	ch := make(chan *event.Event, 8)
	go func() {
		defer close(ch)
		defer func() {
			a.mu.Lock()
			a.activeCmd = nil
			a.cancelFn = nil
			a.mu.Unlock()
		}()
		defer cancel()

		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}

			var streamEvt claudeStreamEvent
			if err := json.Unmarshal([]byte(line), &streamEvt); err != nil {
				a.logger.Debug("claude stream: unparseable line", "line", line[:min(len(line), 200)])
				continue
			}

			// Capture session_id from any event.
			if streamEvt.SessionID != "" {
				a.mu.Lock()
				if a.sessionID == "" {
					a.sessionID = streamEvt.SessionID
					a.logger.Info("claude session started", "agent", a.name, "session_id", a.sessionID)
				}
				a.mu.Unlock()
			}

			switch streamEvt.Type {
			case "assistant":
				if streamEvt.Message == nil {
					continue
				}
				evt := a.parseAssistantEvent(invID, &streamEvt)
				if evt != nil {
					ch <- evt
				}

			case "result":
				// Final result event.
				finishReason := "stop"
				content := streamEvt.Result
				if streamEvt.IsError {
					ch <- event.NewResponseEvent(invID, a.name, &model.Response{
						Error: &model.ResponseError{Message: content},
					})
				} else {
					ch <- event.NewResponseEvent(invID, a.name, &model.Response{
						Choices: []model.Choice{{
							Index: 0,
							Message: model.Message{
								Role:    model.RoleAssistant,
								Content: content,
							},
							FinishReason: &finishReason,
						}},
					})
				}
				a.logger.Info("claude subprocess completed",
					"agent", a.name,
					"session_id", streamEvt.SessionID,
					"cost_usd", streamEvt.TotalCost,
					"result_len", len(content))
			}
		}

		// Wait for process to finish.
		waitErr := cmd.Wait()
		if waitErr != nil {
			// Check if it was killed intentionally.
			a.mu.Lock()
			wasKilled := a.activeCmd == nil
			a.mu.Unlock()
			if wasKilled {
				return // Kill() was called, don't emit error
			}

			errMsg := fmt.Sprintf("claude subprocess failed: %v", waitErr)
			if stderr := strings.TrimSpace(stderrBuf.String()); stderr != "" {
				errMsg += "\nstderr: " + stderr
			}
			a.logger.Error("claude subprocess failed", "agent", a.name, "error", waitErr)
			ch <- event.NewResponseEvent(invID, a.name, &model.Response{
				Error: &model.ResponseError{Message: errMsg},
			})
		}
	}()

	return ch, nil
}

// parseAssistantEvent converts a claude stream assistant event into a trpc event.
func (a *ClaudeAgent) parseAssistantEvent(invID string, streamEvt *claudeStreamEvent) *event.Event {
	msg := streamEvt.Message
	if msg == nil {
		return nil
	}

	var contents []claudeContent
	if err := json.Unmarshal(msg.Content, &contents); err != nil {
		return nil
	}

	var toolCalls []model.ToolCall
	var textContent string

	for _, c := range contents {
		switch c.Type {
		case "tool_use":
			toolCalls = append(toolCalls, model.ToolCall{
				ID: c.ID,
				Function: model.FunctionDefinitionParam{
					Name:      c.Name,
					Arguments: c.Input,
				},
			})
		case "text":
			textContent += c.Text
		}
	}

	// Build usage if available.
	var usage *model.Usage
	if msg.Usage != nil {
		usage = &model.Usage{
			PromptTokens:     msg.Usage.InputTokens,
			CompletionTokens: msg.Usage.OutputTokens,
			TotalTokens:      msg.Usage.InputTokens + msg.Usage.OutputTokens,
		}
	}

	resp := &model.Response{Usage: usage}

	if len(toolCalls) > 0 || textContent != "" {
		resp.Choices = []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role:      model.RoleAssistant,
				Content:   textContent,
				ToolCalls: toolCalls,
			},
		}}
	} else {
		return nil
	}

	return event.NewResponseEvent(invID, a.name, resp)
}

// Run executes the task by spawning a `claude -p` subprocess with stream-json output.
func (a *ClaudeAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	taskText := invocation.Message.Content
	prompt := a.buildPrompt(taskText)
	args := a.buildArgs(prompt)

	a.logger.Info("claude subprocess starting",
		"agent", a.name,
		"model", a.claudeModel,
		"work_dir", a.workDir,
		"prompt_len", len(prompt))

	return a.runSubprocess(ctx, invocation, args)
}

// Kill terminates the active subprocess and returns its session_id for potential resume.
// Returns empty string if no subprocess is active or session_id was not captured.
func (a *ClaudeAgent) Kill() string {
	a.mu.Lock()
	defer a.mu.Unlock()

	sid := a.sessionID
	if a.activeCmd != nil && a.activeCmd.Process != nil {
		_ = a.activeCmd.Process.Kill()
		a.activeCmd = nil
	}
	if a.cancelFn != nil {
		a.cancelFn()
		a.cancelFn = nil
	}

	a.logger.Info("claude subprocess killed", "agent", a.name, "session_id", sid)
	return sid
}

// SessionID returns the session ID of the active or last subprocess.
func (a *ClaudeAgent) SessionID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sessionID
}

// Resume starts a new subprocess that continues from a previous session with a corrective prompt.
func (a *ClaudeAgent) Resume(ctx context.Context, invocation *agent.Invocation, sessionID, correction string) (<-chan *event.Event, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("no session_id for resume")
	}

	args := []string{
		"-p", correction,
		"--resume", sessionID,
		"--output-format", "stream-json",
		"--verbose",
		"--dangerously-skip-permissions",
	}

	a.logger.Info("claude subprocess resuming",
		"agent", a.name,
		"session_id", sessionID,
		"correction_len", len(correction))

	return a.runSubprocess(ctx, invocation, args)
}

// Tools returns an empty slice — ClaudeAgent doesn't expose tools to the coordinator.
func (a *ClaudeAgent) Tools() []tool.Tool {
	return nil
}

// Info returns the agent's name and description.
func (a *ClaudeAgent) Info() agent.Info {
	return agent.Info{
		Name:        a.name,
		Description: a.description,
	}
}

// SubAgents returns nil — ClaudeAgent has no sub-agents.
func (a *ClaudeAgent) SubAgents() []agent.Agent {
	return nil
}

// FindSubAgent returns nil — ClaudeAgent has no sub-agents.
func (a *ClaudeAgent) FindSubAgent(name string) agent.Agent {
	return nil
}
