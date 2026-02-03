package agent

import (
	"encoding/json"
	"fmt"
)

// formatApprovalDescription produces a human-readable summary of a tool
// invocation for display in approval requests and audit logs.
func formatApprovalDescription(toolName string, rawArgs json.RawMessage) string {
	switch toolName {
	case "Bash":
		var v struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(rawArgs, &v) == nil && v.Command != "" {
			return truncate(fmt.Sprintf("Run command: %s", v.Command), 200)
		}
	case "Write":
		var v struct {
			FilePath string `json:"file_path"`
		}
		if json.Unmarshal(rawArgs, &v) == nil && v.FilePath != "" {
			return fmt.Sprintf("Write file: %s", v.FilePath)
		}
	case "Edit":
		var v struct {
			FilePath string `json:"file_path"`
		}
		if json.Unmarshal(rawArgs, &v) == nil && v.FilePath != "" {
			return fmt.Sprintf("Edit file: %s", v.FilePath)
		}
	case "Read":
		var v struct {
			FilePath string `json:"file_path"`
		}
		if json.Unmarshal(rawArgs, &v) == nil && v.FilePath != "" {
			return fmt.Sprintf("Read file: %s", v.FilePath)
		}
	}
	// Fallback: tool name + truncated raw args.
	return truncate(fmt.Sprintf("%s: %s", toolName, string(rawArgs)), 200)
}

func truncate(s string, max int) string {
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
