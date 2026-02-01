package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/yourusername/kaggen/internal/config"
)

// Verdict represents the supervisor's decision about an agent's execution state.
type Verdict struct {
	Action     string // "continue", "correct", "abort"
	Reason     string
	Correction string // corrective prompt for resume (when Action == "correct")
}

// monitorState tracks per-task monitoring data for heuristic evaluation.
type monitorState struct {
	task            string
	corrections     int
	recentTools     []string // last N tool names for repetition detection
	consecutiveErrs int
	lastActivity    time.Time
	turnCount       int
}

// Supervisor monitors ClaudeAgent execution and can trigger corrections.
// Layer 1: deterministic heuristics (instant, zero cost).
// Layer 2: local Ollama model checkpoints (periodic, zero API cost).
type Supervisor struct {
	config     config.SupervisorConfig
	httpClient *http.Client
	logger     *slog.Logger
	mu         sync.Mutex
	tasks      map[string]*monitorState
}

// NewSupervisor creates a supervisor from config. Returns nil if not enabled.
func NewSupervisor(cfg config.SupervisorConfig, logger *slog.Logger) *Supervisor {
	if !cfg.Enabled {
		return nil
	}
	if cfg.OllamaBaseURL == "" {
		cfg.OllamaBaseURL = "http://localhost:11434"
	}
	if cfg.OllamaModel == "" {
		cfg.OllamaModel = "qwen2.5:1.5b"
	}
	if cfg.CheckInterval <= 0 {
		cfg.CheckInterval = 10
	}
	if cfg.MaxCorrections <= 0 {
		cfg.MaxCorrections = 2
	}
	if cfg.StallTimeoutSec <= 0 {
		cfg.StallTimeoutSec = 300
	}
	return &Supervisor{
		config:     cfg,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		logger:     logger,
		tasks:      make(map[string]*monitorState),
	}
}

// StartTask registers a task for monitoring.
func (s *Supervisor) StartTask(taskID, task string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[taskID] = &monitorState{
		task:         task,
		lastActivity: time.Now(),
	}
}

// EndTask removes a task from monitoring.
func (s *Supervisor) EndTask(taskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tasks, taskID)
}

// IncrementCorrections increments the correction count for a task.
// Returns false if the max corrections limit has been reached.
func (s *Supervisor) IncrementCorrections(taskID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.tasks[taskID]
	if !ok {
		return false
	}
	st.corrections++
	return st.corrections <= s.config.MaxCorrections
}

// Evaluate checks a task event against Layer 1 heuristics and periodically Layer 2 Ollama.
// Returns a Verdict indicating whether execution should continue, be corrected, or aborted.
func (s *Supervisor) Evaluate(taskID string, evt *TaskEvent, task string) Verdict {
	s.mu.Lock()
	st, ok := s.tasks[taskID]
	if !ok {
		st = &monitorState{task: task, lastActivity: time.Now()}
		s.tasks[taskID] = st
	}

	// Update state from event.
	st.lastActivity = time.Now()
	st.turnCount++

	if evt.Type == "error" {
		st.consecutiveErrs++
	} else {
		st.consecutiveErrs = 0
	}

	if evt.Type == "tool_call" && len(evt.Tools) > 0 {
		st.recentTools = append(st.recentTools, evt.Tools...)
		// Keep only last 20 tool calls.
		if len(st.recentTools) > 20 {
			st.recentTools = st.recentTools[len(st.recentTools)-20:]
		}
	}

	// Snapshot state for evaluation outside lock.
	taskDesc := st.task
	consErrs := st.consecutiveErrs
	tools := make([]string, len(st.recentTools))
	copy(tools, st.recentTools)
	turnCount := st.turnCount
	lastActivity := st.lastActivity
	s.mu.Unlock()

	// Layer 1: Deterministic heuristics.
	if v := s.evaluateHeuristics(taskID, consErrs, tools, lastActivity, evt); v.Action != "continue" {
		return v
	}

	// Layer 2: Periodic Ollama checkpoint.
	if turnCount > 0 && turnCount%s.config.CheckInterval == 0 {
		if v := s.evaluateOllama(taskID, taskDesc, tools, turnCount); v.Action != "continue" {
			return v
		}
	}

	return Verdict{Action: "continue"}
}

// evaluateHeuristics applies Layer 1 deterministic rules.
func (s *Supervisor) evaluateHeuristics(taskID string, consecutiveErrs int, recentTools []string, lastActivity time.Time, evt *TaskEvent) Verdict {
	// Error loop detection.
	if consecutiveErrs >= 3 {
		return Verdict{
			Action:     "correct",
			Reason:     fmt.Sprintf("error loop: %d consecutive errors", consecutiveErrs),
			Correction: "You have encountered multiple consecutive errors. Stop and take a different approach. Analyze what went wrong and try an alternative strategy.",
		}
	}

	// Repetition detection: same tool called 3+ times in recent history.
	if rep, count := detectRepetition(recentTools, 3); rep != "" {
		return Verdict{
			Action:     "correct",
			Reason:     fmt.Sprintf("repetition: tool %q called %d times recently", rep, count),
			Correction: fmt.Sprintf("You appear to be repeating the same action (%s) without making progress. Stop, reassess your approach, and try something different.", rep),
		}
	}

	// Stall detection.
	stallTimeout := time.Duration(s.config.StallTimeoutSec) * time.Second
	if time.Since(lastActivity) > stallTimeout {
		return Verdict{
			Action:     "correct",
			Reason:     "stalled: no activity for " + stallTimeout.String(),
			Correction: "You appear to have stalled. Summarize what you've done so far and continue with the next step.",
		}
	}

	// Content pattern detection.
	if evt != nil && evt.Content != "" {
		lower := strings.ToLower(evt.Content)
		apologyPatterns := []string{"i apologize", "i'm sorry", "let me try again", "let me start over"}
		for _, pat := range apologyPatterns {
			if strings.Contains(lower, pat) {
				return Verdict{
					Action:     "correct",
					Reason:     "detected apology/restart pattern in output",
					Correction: "Focus on completing the task. Do not apologize or restart. Identify the specific issue and fix it directly.",
				}
			}
		}
	}

	return Verdict{Action: "continue"}
}

// detectRepetition finds the most repeated tool in recent calls.
// Returns the tool name and count if any tool appears >= threshold times.
func detectRepetition(tools []string, threshold int) (string, int) {
	if len(tools) < threshold {
		return "", 0
	}
	counts := make(map[string]int)
	for _, t := range tools {
		counts[t]++
	}
	var maxTool string
	var maxCount int
	for t, c := range counts {
		if c > maxCount {
			maxTool = t
			maxCount = c
		}
	}
	if maxCount >= threshold {
		return maxTool, maxCount
	}
	return "", 0
}

// ollamaRequest is the Ollama /api/generate request body.
type ollamaRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
}

// ollamaResponse is the Ollama /api/generate response body.
type ollamaResponse struct {
	Response string `json:"response"`
}

// evaluateOllama calls the local Ollama model for a checkpoint assessment.
func (s *Supervisor) evaluateOllama(taskID, task string, recentTools []string, turnCount int) Verdict {
	// Build a digest of recent activity.
	digest := buildToolDigest(recentTools, 10)

	prompt := fmt.Sprintf(`You are monitoring an AI agent executing a task. Classify its current status.

Task: %s
Turn: %d
Recent tool calls: %s

Reply with exactly one word: on_track, spinning, stuck, or off_topic`, task, turnCount, digest)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	reqBody, _ := json.Marshal(ollamaRequest{
		Model:  s.config.OllamaModel,
		Prompt: prompt,
		Stream: false,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.config.OllamaBaseURL+"/api/generate", bytes.NewReader(reqBody))
	if err != nil {
		s.logger.Warn("supervisor: ollama request build failed", "error", err)
		return Verdict{Action: "continue"}
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		s.logger.Warn("supervisor: ollama request failed (degrading gracefully)", "error", err)
		return Verdict{Action: "continue"} // fail open
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		s.logger.Warn("supervisor: ollama non-200", "status", resp.StatusCode, "body", string(body))
		return Verdict{Action: "continue"}
	}

	var ollamaResp ollamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		s.logger.Warn("supervisor: ollama decode failed", "error", err)
		return Verdict{Action: "continue"}
	}

	classification := strings.TrimSpace(strings.ToLower(ollamaResp.Response))
	s.logger.Info("supervisor: ollama checkpoint",
		"task_id", taskID,
		"turn", turnCount,
		"classification", classification)

	switch {
	case strings.Contains(classification, "on_track"):
		return Verdict{Action: "continue"}
	case strings.Contains(classification, "spinning"):
		return Verdict{
			Action:     "correct",
			Reason:     "ollama checkpoint: agent is spinning",
			Correction: fmt.Sprintf("You are spinning without making progress on the task: %s. Stop repeating actions and take a different approach.", task),
		}
	case strings.Contains(classification, "stuck"):
		return Verdict{
			Action:     "correct",
			Reason:     "ollama checkpoint: agent is stuck",
			Correction: fmt.Sprintf("You appear stuck on the task: %s. Try breaking the problem into smaller steps or using a different tool.", task),
		}
	case strings.Contains(classification, "off_topic"):
		return Verdict{
			Action:     "correct",
			Reason:     "ollama checkpoint: agent is off-topic",
			Correction: fmt.Sprintf("You have gone off-topic. Refocus on the original task: %s", task),
		}
	default:
		// Unrecognized response — fail open.
		return Verdict{Action: "continue"}
	}
}

// buildToolDigest creates a short summary of recent tool calls for the Ollama prompt.
func buildToolDigest(tools []string, maxItems int) string {
	if len(tools) == 0 {
		return "(no tool calls)"
	}
	start := 0
	if len(tools) > maxItems {
		start = len(tools) - maxItems
	}
	return strings.Join(tools[start:], " → ")
}
