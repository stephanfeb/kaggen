package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFormatApprovalDescription(t *testing.T) {
	tests := []struct {
		name     string
		tool     string
		args     string
		wantPfx  string
	}{
		{"bash command", "Bash", `{"command":"kubectl apply -f deploy.yaml"}`, "Run command: kubectl apply"},
		{"bash empty", "Bash", `{}`, "Bash: "},
		{"write file", "Write", `{"file_path":"/tmp/foo.go","content":"x"}`, "Write file: /tmp/foo.go"},
		{"edit file", "Edit", `{"file_path":"/tmp/bar.go","old_string":"a","new_string":"b"}`, "Edit file: /tmp/bar.go"},
		{"read file", "Read", `{"file_path":"/tmp/baz.go"}`, "Read file: /tmp/baz.go"},
		{"unknown tool", "CustomTool", `{"key":"val"}`, "CustomTool: "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatApprovalDescription(tt.tool, json.RawMessage(tt.args))
			if !strings.HasPrefix(got, tt.wantPfx) {
				t.Errorf("got %q, want prefix %q", got, tt.wantPfx)
			}
		})
	}
}

func TestFormatApprovalDescriptionTruncation(t *testing.T) {
	long := `{"command":"` + strings.Repeat("x", 300) + `"}`
	got := formatApprovalDescription("Bash", json.RawMessage(long))
	if len(got) > 203 { // 200 + "..."
		t.Errorf("expected truncation, got len %d", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Error("expected ... suffix")
	}
}
