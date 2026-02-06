package agent

import (
	"testing"
	"time"
)

func TestNewWorldModel(t *testing.T) {
	wm := NewWorldModel("test-session")
	if wm == nil {
		t.Fatal("NewWorldModel returned nil")
	}
	if wm.SessionID() != "test-session" {
		t.Errorf("expected session ID 'test-session', got %q", wm.SessionID())
	}
}

func TestRecordToolCalls(t *testing.T) {
	wm := NewWorldModel("test-session")

	// Record a tool call start
	wm.RecordToolCallStart("call-1", "read", map[string]any{"path": "/tmp/test.txt"})

	// Record successful completion
	wm.RecordToolCallEnd("call-1", "read", true, "", 100)

	summary := wm.GetExecutionSummary()
	if summary.TotalCalls != 1 {
		t.Errorf("expected 1 tool call, got %d", summary.TotalCalls)
	}
	if summary.SuccessRate != 1.0 {
		t.Errorf("expected success rate 1.0, got %f", summary.SuccessRate)
	}
}

func TestErrorStreak(t *testing.T) {
	wm := NewWorldModel("test-session")

	// Record 3 consecutive failures with same tool category
	for i := 0; i < 3; i++ {
		callID := "call-" + string(rune('a'+i))
		wm.RecordToolCallStart(callID, "bash", nil)
		wm.RecordToolCallEnd(callID, "bash", false, "command failed", 0)
	}

	shouldPivot, reason := wm.ShouldPivot()
	if !shouldPivot {
		t.Error("expected ShouldPivot to be true after 3 consecutive errors")
	}
	if reason == "" {
		t.Error("expected a reason for pivoting")
	}
}

func TestErrorStreakResets(t *testing.T) {
	wm := NewWorldModel("test-session")

	// Record 2 failures
	wm.RecordToolCallStart("call-1", "bash", nil)
	wm.RecordToolCallEnd("call-1", "bash", false, "error", 0)
	wm.RecordToolCallStart("call-2", "bash", nil)
	wm.RecordToolCallEnd("call-2", "bash", false, "error", 0)

	// Record a success - should reset streak
	wm.RecordToolCallStart("call-3", "bash", nil)
	wm.RecordToolCallEnd("call-3", "bash", true, "", 100)

	shouldPivot, _ := wm.ShouldPivot()
	if shouldPivot {
		t.Error("expected ShouldPivot to be false after success")
	}
}

func TestShouldEscalate_MinimalProgress(t *testing.T) {
	wm := NewWorldModel("test-session")

	// Simulate 10+ minutes with no state changes
	wm.mu.Lock()
	wm.startedAt = time.Now().Add(-11 * time.Minute)
	wm.mu.Unlock()

	shouldEscalate, reason := wm.ShouldEscalate()
	if !shouldEscalate {
		t.Error("expected ShouldEscalate to be true after 10+ minutes with no progress")
	}
	if reason == "" {
		t.Error("expected a reason for escalation")
	}
}

func TestShouldEscalate_HighErrorRate(t *testing.T) {
	wm := NewWorldModel("test-session")

	// Record 10 calls with 6 failures
	for i := 0; i < 10; i++ {
		callID := "call-" + string(rune('a'+i))
		wm.RecordToolCallStart(callID, "read", nil)
		success := i < 4 // first 4 succeed, last 6 fail
		errMsg := ""
		if !success {
			errMsg = "error"
		}
		wm.RecordToolCallEnd(callID, "read", success, errMsg, 50)
	}

	shouldEscalate, reason := wm.ShouldEscalate()
	if !shouldEscalate {
		t.Error("expected ShouldEscalate to be true with 60% failure rate")
	}
	if reason == "" {
		t.Error("expected a reason for escalation")
	}
}

func TestIsProgressingWell(t *testing.T) {
	wm := NewWorldModel("test-session")

	// Not enough data
	if !wm.IsProgressingWell() {
		t.Error("expected IsProgressingWell to be true with insufficient data")
	}

	// Record 5 successful calls
	for i := 0; i < 5; i++ {
		callID := "call-" + string(rune('a'+i))
		wm.RecordToolCallStart(callID, "read", nil)
		wm.RecordToolCallEnd(callID, "read", true, "", 50)
	}

	if !wm.IsProgressingWell() {
		t.Error("expected IsProgressingWell to be true with all successes")
	}

	// Add 3 failures
	for i := 0; i < 3; i++ {
		callID := "fail-" + string(rune('a'+i))
		wm.RecordToolCallStart(callID, "write", nil)
		wm.RecordToolCallEnd(callID, "write", false, "error", 0)
	}

	// Last 5: 2 successes, 3 failures
	if wm.IsProgressingWell() {
		t.Error("expected IsProgressingWell to be false with <3 successes in last 5")
	}
}

func TestRecordFileModified(t *testing.T) {
	wm := NewWorldModel("test-session")

	wm.RecordFileModified("/path/to/file1.txt")
	wm.RecordFileModified("/path/to/file2.txt")

	summary := wm.GetExecutionSummary()
	if summary.FilesModified != 2 {
		t.Errorf("expected 2 files modified, got %d", summary.FilesModified)
	}
}

func TestUpdateTestStatus(t *testing.T) {
	wm := NewWorldModel("test-session")

	wm.UpdateTestStatus(TestStatusPassing)
	summary := wm.GetExecutionSummary()
	if summary.TestStatus != "passing" {
		t.Errorf("expected test status 'passing', got %q", summary.TestStatus)
	}

	wm.UpdateTestStatus(TestStatusFailing)
	summary = wm.GetExecutionSummary()
	if summary.TestStatus != "failing" {
		t.Errorf("expected test status 'failing', got %q", summary.TestStatus)
	}
}

func TestExecutionSummaryString(t *testing.T) {
	wm := NewWorldModel("test-session")

	// Record some activity
	wm.RecordToolCallStart("call-1", "read", nil)
	wm.RecordToolCallEnd("call-1", "read", true, "", 100)
	wm.RecordFileModified("/path/to/file.txt")
	wm.UpdateTestStatus(TestStatusPassing)

	summary := wm.GetExecutionSummary()
	str := summary.String()

	if str == "" {
		t.Error("expected non-empty summary string")
	}
	if len(str) < 50 {
		t.Errorf("expected summary string with content, got %q", str)
	}
}

func TestCategorizeToolName(t *testing.T) {
	tests := []struct {
		tool     string
		expected string
	}{
		{"bash", "execution"},
		{"Bash", "execution"},
		{"exec", "execution"},
		{"read", "filesystem_read"},
		{"Read", "filesystem_read"},
		{"glob", "filesystem_read"},
		{"grep", "filesystem_read"},
		{"write", "filesystem_write"},
		{"Write", "filesystem_write"},
		{"edit", "filesystem_write"},
		{"browser", "web"},
		{"web_search", "web"},
		{"dispatch_task", "orchestration"},
		{"backlog_add", "orchestration"},
		{"custom_tool", "custom_tool"},
	}

	for _, tc := range tests {
		t.Run(tc.tool, func(t *testing.T) {
			result := categorizeToolName(tc.tool)
			if result != tc.expected {
				t.Errorf("categorizeToolName(%q) = %q, expected %q", tc.tool, result, tc.expected)
			}
		})
	}
}
