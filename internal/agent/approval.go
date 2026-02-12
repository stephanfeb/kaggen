package agent

import (
	"encoding/json"
	"fmt"
	"strings"
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
	case "email":
		var v struct {
			Action  string   `json:"action"`
			To      []string `json:"to"`
			Subject string   `json:"subject"`
		}
		if json.Unmarshal(rawArgs, &v) == nil && v.Action == "send" {
			return fmt.Sprintf("Send email to %s: %s", strings.Join(v.To, ", "), v.Subject)
		}
	}
	// Fallback: tool name + truncated raw args.
	return truncate(fmt.Sprintf("%s: %s", toolName, string(rawArgs)), 200)
}

// extractEmailPreview extracts an EmailPreview if the tool is email with action=send.
// Returns nil for non-email tools or non-send actions.
func extractEmailPreview(toolName string, rawArgs json.RawMessage) *EmailPreview {
	if toolName != "email" {
		return nil
	}

	var v struct {
		Action  string   `json:"action"`
		To      []string `json:"to"`
		CC      []string `json:"cc"`
		Subject string   `json:"subject"`
		Body    string   `json:"body"`
	}
	if err := json.Unmarshal(rawArgs, &v); err != nil || v.Action != "send" {
		return nil
	}

	// Truncate body for preview
	body := v.Body
	if len(body) > 1000 {
		body = body[:1000] + "..."
	}

	return &EmailPreview{
		To:      v.To,
		CC:      v.CC,
		Subject: v.Subject,
		Body:    body,
	}
}

func truncate(s string, max int) string {
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
