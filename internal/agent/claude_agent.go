package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// ClaudeAgent implements agent.Agent by running `claude -p` as a subprocess
// instead of making an LLM API call. This eliminates the intermediate LLM
// round-trip that was previously used just to construct a CLI command.
type ClaudeAgent struct {
	name        string
	description string
	claudeModel string        // --model flag (e.g. "sonnet")
	workDir     string        // --add-dir path
	tools       string        // --allowed-tools value
	instruction string        // skill instruction prepended to task prompt
	timeout     time.Duration // subprocess timeout
	logger      *slog.Logger
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

// claudeOutput is the JSON structure returned by `claude -p --output-format json`.
type claudeOutput struct {
	SessionID string `json:"session_id"`
	Result    string `json:"result"`
	Cost      any    `json:"cost_usd"`
}

// Run executes the task by spawning a `claude -p` subprocess.
// It combines the skill instruction with the task from the invocation message,
// streams output, and returns events compatible with the async dispatch loop.
func (a *ClaudeAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	// Extract task text from invocation message.
	taskText := invocation.Message.Content

	// Build the combined prompt: instruction context + task.
	var prompt string
	if a.instruction != "" {
		prompt = a.instruction + "\n\n---\n\n" + taskText
	} else {
		prompt = taskText
	}

	// Build CLI arguments.
	args := []string{"-p", prompt, "--output-format", "json", "--dangerously-skip-permissions"}
	if a.claudeModel != "" {
		args = append(args, "--model", a.claudeModel)
	}
	if a.workDir != "" {
		args = append(args, "--add-dir", a.workDir)
	}
	if a.tools != "" {
		args = append(args, "--allowed-tools", a.tools)
	}

	a.logger.Info("claude subprocess starting",
		"agent", a.name,
		"model", a.claudeModel,
		"work_dir", a.workDir,
		"prompt_len", len(prompt))

	// Create subprocess with timeout context.
	subCtx, cancel := context.WithTimeout(ctx, a.timeout)
	cmd := exec.CommandContext(subCtx, "claude", args...)

	// Capture stderr for error reporting.
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

	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		defer cancel()

		// Read all stdout (claude -p --output-format json writes a single JSON blob).
		var output strings.Builder
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024) // 10MB buffer
		for scanner.Scan() {
			output.WriteString(scanner.Text())
			output.WriteByte('\n')
		}

		waitErr := cmd.Wait()
		invID := ""
		if invocation != nil {
			invID = invocation.InvocationID
		}

		if waitErr != nil {
			// Subprocess failed.
			errMsg := fmt.Sprintf("claude subprocess failed: %v", waitErr)
			if stderr := strings.TrimSpace(stderrBuf.String()); stderr != "" {
				errMsg += "\nstderr: " + stderr
			}
			a.logger.Error("claude subprocess failed",
				"agent", a.name,
				"error", waitErr,
				"stderr", stderrBuf.String())

			ch <- event.NewResponseEvent(invID, a.name, &model.Response{
				Error: &model.ResponseError{Message: errMsg},
			})
			return
		}

		// Parse JSON output.
		rawOutput := strings.TrimSpace(output.String())
		var result string
		var parsed claudeOutput
		if json.Unmarshal([]byte(rawOutput), &parsed) == nil && parsed.Result != "" {
			result = parsed.Result
			a.logger.Info("claude subprocess completed",
				"agent", a.name,
				"session_id", parsed.SessionID,
				"result_len", len(result))
		} else {
			// Fallback: use raw output as result.
			result = rawOutput
			a.logger.Info("claude subprocess completed (raw output)",
				"agent", a.name,
				"result_len", len(result))
		}

		// Emit a single response event with the result.
		finishReason := "stop"
		ch <- event.NewResponseEvent(invID, a.name, &model.Response{
			Choices: []model.Choice{{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: result,
				},
				FinishReason: &finishReason,
			}},
		})
	}()

	return ch, nil
}

// Tools returns an empty slice — ClaudeAgent doesn't expose tools to the coordinator.
// The actual tools are specified via --allowed-tools on the subprocess.
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
