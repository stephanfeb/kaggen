// Package agent provides the coordinator agent and sub-agent orchestration.
package agent

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// TestStatus represents the current test/build state.
type TestStatus string

const (
	TestStatusUnknown TestStatus = "unknown"
	TestStatusPassing TestStatus = "passing"
	TestStatusFailing TestStatus = "failing"
)

// ToolCallRecord captures a single tool invocation for decision support.
type ToolCallRecord struct {
	Timestamp  time.Time      `json:"timestamp"`
	Tool       string         `json:"tool"`
	Args       map[string]any `json:"args,omitempty"`
	Success    bool           `json:"success"`
	Duration   time.Duration  `json:"duration"`
	ErrorMsg   string         `json:"error_msg,omitempty"`
	ResultSize int            `json:"result_size"` // bytes of result
}

// ErrorRecord captures error details for decision support.
type ErrorRecord struct {
	Timestamp time.Time `json:"timestamp"`
	Tool      string    `json:"tool"`
	Message   string    `json:"message"`
	Args      string    `json:"args,omitempty"`
}

// StateTransition captures significant state changes for temporal reasoning.
type StateTransition struct {
	Timestamp time.Time `json:"timestamp"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Trigger   string    `json:"trigger"` // what caused the transition
}

// ExecutionSummary provides context for reasoning escalation.
type ExecutionSummary struct {
	SessionID     string        `json:"session_id"`
	Elapsed       time.Duration `json:"elapsed"`
	TotalCalls    int           `json:"total_calls"`
	SuccessRate   float64       `json:"success_rate"`
	FilesModified int           `json:"files_modified"`
	FilesList     []string      `json:"files_list,omitempty"`
	UniqueTools   []string      `json:"unique_tools"`
	TestStatus    string        `json:"test_status"`
	BuildStatus   string        `json:"build_status"`
	ErrorStreak   int           `json:"error_streak"`
	LastError     string        `json:"last_error,omitempty"`
}

// String returns a human-readable summary for inclusion in reasoning prompts.
func (s ExecutionSummary) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Session running for %s\n", s.Elapsed.Round(time.Second))
	fmt.Fprintf(&b, "- %d tool calls (%.0f%% success rate)\n", s.TotalCalls, s.SuccessRate*100)
	fmt.Fprintf(&b, "- %d files modified\n", s.FilesModified)
	fmt.Fprintf(&b, "- Tests: %s, Build: %s\n", s.TestStatus, s.BuildStatus)
	if s.ErrorStreak > 0 {
		fmt.Fprintf(&b, "- Error streak: %d consecutive failures\n", s.ErrorStreak)
	}
	if s.LastError != "" {
		fmt.Fprintf(&b, "- Last error: %s\n", s.LastError)
	}
	return b.String()
}

// WorldModel tracks session-scoped execution state for decision support.
// This is DISTINCT from Epistemic Memory (long-term knowledge about the user).
// WorldModel is ephemeral, session-scoped, and consulted constantly.
//
// Key differences from Epistemic Memory:
// - Lifecycle: Ephemeral (session-scoped) vs Persistent (across sessions)
// - Access: Consulted before every decision vs Searched when relevant
// - Update: After every tool call vs After conversations
// - Data: Structured state machine vs Unstructured text + metadata
// - Question: "What's happening right now?" vs "What do I know about this user?"
type WorldModel struct {
	mu        sync.RWMutex
	sessionID string
	startedAt time.Time

	// Execution state
	filesModified map[string]time.Time // path -> last modified timestamp
	toolCalls     []ToolCallRecord     // chronological log of all calls
	taskStatus    map[string]TaskStatus // task_id -> current status

	// Decision support state
	errorStreak   int          // consecutive errors (same tool category)
	lastError     *ErrorRecord // most recent error details
	lastErrorTool string       // tool category that caused last error (for streak tracking)
	stateChanges  []StateTransition

	// Test/build status
	testStatus   TestStatus
	lastTestRun  time.Time
	buildStatus  TestStatus
	lastBuildRun time.Time

	// Pending tool calls (for duration tracking)
	pendingCalls map[string]time.Time // callID -> start time
}

// NewWorldModel creates a new session-scoped WorldModel.
func NewWorldModel(sessionID string) *WorldModel {
	return &WorldModel{
		sessionID:     sessionID,
		startedAt:     time.Now(),
		filesModified: make(map[string]time.Time),
		toolCalls:     make([]ToolCallRecord, 0, 100),
		taskStatus:    make(map[string]TaskStatus),
		testStatus:    TestStatusUnknown,
		buildStatus:   TestStatusUnknown,
		pendingCalls:  make(map[string]time.Time),
	}
}

// SessionID returns the session ID this WorldModel is tracking.
func (w *WorldModel) SessionID() string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.sessionID
}

// RecordToolCallStart marks the beginning of a tool call.
func (w *WorldModel) RecordToolCallStart(callID, toolName string, args map[string]any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pendingCalls[callID] = time.Now()
}

// RecordToolCallEnd records a completed tool call.
func (w *WorldModel) RecordToolCallEnd(callID, toolName string, success bool, errMsg string, resultSize int) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Calculate duration
	var duration time.Duration
	if startTime, ok := w.pendingCalls[callID]; ok {
		duration = time.Since(startTime)
		delete(w.pendingCalls, callID)
	}

	record := ToolCallRecord{
		Timestamp:  time.Now(),
		Tool:       toolName,
		Success:    success,
		Duration:   duration,
		ErrorMsg:   errMsg,
		ResultSize: resultSize,
	}
	w.toolCalls = append(w.toolCalls, record)

	// Update error streak
	if !success {
		toolCategory := categorizeToolName(toolName)
		if w.lastErrorTool == toolCategory {
			w.errorStreak++
		} else {
			w.errorStreak = 1
			w.lastErrorTool = toolCategory
		}
		w.lastError = &ErrorRecord{
			Timestamp: time.Now(),
			Tool:      toolName,
			Message:   errMsg,
		}
	} else {
		w.errorStreak = 0
	}
}

// RecordFileModified tracks a file modification.
func (w *WorldModel) RecordFileModified(path string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.filesModified[path] = time.Now()
}

// UpdateTestStatus updates the test pass/fail status.
func (w *WorldModel) UpdateTestStatus(status TestStatus) {
	w.mu.Lock()
	defer w.mu.Unlock()

	oldStatus := w.testStatus
	w.testStatus = status
	w.lastTestRun = time.Now()

	// Record state transition
	if oldStatus != status {
		w.stateChanges = append(w.stateChanges, StateTransition{
			Timestamp: time.Now(),
			From:      string(oldStatus),
			To:        string(status),
			Trigger:   "test_run",
		})
	}
}

// UpdateBuildStatus updates the build pass/fail status.
func (w *WorldModel) UpdateBuildStatus(status TestStatus) {
	w.mu.Lock()
	defer w.mu.Unlock()

	oldStatus := w.buildStatus
	w.buildStatus = status
	w.lastBuildRun = time.Now()

	// Record state transition
	if oldStatus != status {
		w.stateChanges = append(w.stateChanges, StateTransition{
			Timestamp: time.Now(),
			From:      string(oldStatus),
			To:        string(status),
			Trigger:   "build_run",
		})
	}
}

// RecordTaskStatus updates the status of a tracked task.
func (w *WorldModel) RecordTaskStatus(taskID string, status TaskStatus) {
	w.mu.Lock()
	defer w.mu.Unlock()

	oldStatus := w.taskStatus[taskID]
	w.taskStatus[taskID] = status

	if oldStatus != status {
		w.stateChanges = append(w.stateChanges, StateTransition{
			Timestamp: time.Now(),
			From:      string(oldStatus),
			To:        string(status),
			Trigger:   fmt.Sprintf("task:%s", taskID),
		})
	}
}

// ShouldPivot returns true if the agent should try a different approach.
// Returns (shouldPivot, reason).
func (w *WorldModel) ShouldPivot() (bool, string) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	// Trigger: consecutive errors with same tool type
	if w.errorStreak >= 3 {
		return true, fmt.Sprintf("failed %d consecutive times with same approach (%s)",
			w.errorStreak, w.lastErrorTool)
	}

	// Trigger: repeated failures on same file (check last 5 calls)
	if len(w.toolCalls) >= 5 {
		recentFailures := 0
		for i := len(w.toolCalls) - 1; i >= 0 && i >= len(w.toolCalls)-5; i-- {
			if !w.toolCalls[i].Success {
				recentFailures++
			}
		}
		if recentFailures >= 4 {
			return true, "4+ failures in last 5 tool calls suggests approach isn't working"
		}
	}

	return false, ""
}

// ShouldEscalate returns true if we should invoke deeper reasoning.
// Returns (shouldEscalate, reason).
func (w *WorldModel) ShouldEscalate() (bool, string) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	// Trigger: minimal progress after significant time
	elapsed := time.Since(w.startedAt)
	if elapsed > 10*time.Minute && len(w.stateChanges) < 2 {
		return true, fmt.Sprintf("minimal progress after %s", elapsed.Round(time.Minute))
	}

	// Trigger: high error rate in recent calls
	if len(w.toolCalls) >= 10 {
		recent := w.toolCalls[len(w.toolCalls)-10:]
		failures := 0
		for _, tc := range recent {
			if !tc.Success {
				failures++
			}
		}
		if failures >= 6 {
			return true, fmt.Sprintf("%d failures in last 10 tool calls", failures)
		}
	}

	return false, ""
}

// IsProgressingWell returns true if the current approach is working.
func (w *WorldModel) IsProgressingWell() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if len(w.toolCalls) < 5 {
		return true // not enough data yet
	}

	// Check last 5 tool calls
	recent := w.recentToolCallsLocked(5)
	successes := 0
	for _, tc := range recent {
		if tc.Success {
			successes++
		}
	}
	return successes >= 3 // at least 3 of last 5 succeeded
}

// GetExecutionSummary returns a structured summary for reasoning escalation.
func (w *WorldModel) GetExecutionSummary() ExecutionSummary {
	w.mu.RLock()
	defer w.mu.RUnlock()

	summary := ExecutionSummary{
		SessionID:     w.sessionID,
		Elapsed:       time.Since(w.startedAt),
		TotalCalls:    len(w.toolCalls),
		FilesModified: len(w.filesModified),
		TestStatus:    string(w.testStatus),
		BuildStatus:   string(w.buildStatus),
		ErrorStreak:   w.errorStreak,
	}

	// Calculate success rate
	if len(w.toolCalls) > 0 {
		successes := 0
		for _, tc := range w.toolCalls {
			if tc.Success {
				successes++
			}
		}
		summary.SuccessRate = float64(successes) / float64(len(w.toolCalls))
	}

	// Get unique tools used
	toolSet := make(map[string]bool)
	for _, tc := range w.toolCalls {
		toolSet[tc.Tool] = true
	}
	summary.UniqueTools = make([]string, 0, len(toolSet))
	for t := range toolSet {
		summary.UniqueTools = append(summary.UniqueTools, t)
	}

	// Get recent errors
	if w.lastError != nil {
		summary.LastError = w.lastError.Message
	}

	// Get files touched
	summary.FilesList = make([]string, 0, len(w.filesModified))
	for f := range w.filesModified {
		summary.FilesList = append(summary.FilesList, f)
	}

	return summary
}

// recentToolCallsLocked returns the N most recent tool calls (caller must hold lock).
func (w *WorldModel) recentToolCallsLocked(n int) []ToolCallRecord {
	if len(w.toolCalls) <= n {
		return w.toolCalls
	}
	return w.toolCalls[len(w.toolCalls)-n:]
}

// categorizeToolName groups tools by category for error streak tracking.
func categorizeToolName(toolName string) string {
	toolLower := strings.ToLower(toolName)
	switch {
	case strings.HasPrefix(toolLower, "bash"), strings.HasPrefix(toolLower, "exec"):
		return "execution"
	case strings.HasPrefix(toolLower, "read"), strings.HasPrefix(toolLower, "glob"), strings.HasPrefix(toolLower, "grep"):
		return "filesystem_read"
	case strings.HasPrefix(toolLower, "write"), strings.HasPrefix(toolLower, "edit"):
		return "filesystem_write"
	case strings.HasPrefix(toolLower, "browser"), strings.HasPrefix(toolLower, "web"):
		return "web"
	case strings.HasPrefix(toolLower, "dispatch"), strings.HasPrefix(toolLower, "backlog"):
		return "orchestration"
	default:
		return toolName
	}
}
