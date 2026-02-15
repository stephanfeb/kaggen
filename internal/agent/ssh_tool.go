// Package agent provides the SSH tool for remote command execution.
package agent

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// SSH command limits.
const (
	sshDefaultExecTimeout = 60 * time.Second
	sshMaxExecTimeout     = 5 * time.Minute
	sshMaxOutputSize      = 1024 * 1024 // 1MB
)

// dangerousCommandPatterns are patterns that should trigger warnings.
var dangerousCommandPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\brm\s+-rf\s+/`),      // rm -rf /
	regexp.MustCompile(`\bdd\s+.*of=/dev/`),   // dd to devices
	regexp.MustCompile(`:\(\)\{.*:\|:.*\};:`), // Fork bomb
	regexp.MustCompile(`\bshutdown\b`),
	regexp.MustCompile(`\breboot\b`),
	regexp.MustCompile(`\bmkfs\b`),
	regexp.MustCompile(`\bfdisk\b`),
	regexp.MustCompile(`\b>\s*/etc/`),            // Overwriting /etc
	regexp.MustCompile(`\bchmod\s+-R\s+777\s+/`), // chmod 777 /
}

// sensitiveOutputPatterns are patterns to redact from output.
var sensitiveOutputPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(password|passwd|pwd)[:=]\s*\S+`),
	regexp.MustCompile(`(?i)(api[_-]?key|token)[:=]\s*\S+`),
	regexp.MustCompile(`(?i)Authorization:\s*\S+`),
	regexp.MustCompile(`(?i)(secret|credential)[:=]\s*\S+`),
}

// SSHToolArgs defines the input arguments for the ssh tool.
type SSHToolArgs struct {
	Action       string            `json:"action" jsonschema:"required,description=Action to perform: connect exec disconnect list_connections,enum=connect,enum=exec,enum=disconnect,enum=list_connections"`
	Host         string            `json:"host,omitempty" jsonschema:"description=Host name from config. Required for connect action."`
	ConnectionID string            `json:"connection_id,omitempty" jsonschema:"description=Connection ID from connect. Required for exec and disconnect."`
	Command      string            `json:"command,omitempty" jsonschema:"description=Command to execute. Required for exec action."`
	Stdin        string            `json:"stdin,omitempty" jsonschema:"description=Input to send to command stdin."`
	TimeoutSecs  int               `json:"timeout_seconds,omitempty" jsonschema:"description=Command timeout in seconds (default: 60 max: 300)."`
	PTY          bool              `json:"pty,omitempty" jsonschema:"description=Request pseudo-terminal for interactive commands."`
	Env          map[string]string `json:"env,omitempty" jsonschema:"description=Environment variables to set for the command."`
}

// SSHToolResult is the result of an SSH operation.
type SSHToolResult struct {
	Success      bool          `json:"success"`
	Message      string        `json:"message"`
	ConnectionID string        `json:"connection_id,omitempty"`
	Stdout       string        `json:"stdout,omitempty"`
	Stderr       string        `json:"stderr,omitempty"`
	ExitCode     int           `json:"exit_code,omitempty"`
	Warning      string        `json:"warning,omitempty"`
	Connections  []SSHConnInfo `json:"connections,omitempty"`
}

// NewSSHTool creates a new SSH tool.
func NewSSHTool(manager *SSHConnectionManager) tool.CallableTool {
	return function.NewFunctionTool(
		func(ctx context.Context, args SSHToolArgs) (*SSHToolResult, error) {
			return executeSSHTool(ctx, args, manager)
		},
		function.WithName("ssh"),
		function.WithDescription(`Execute commands on remote servers via SSH.

Actions:
- connect: Establish SSH connection to a configured host, returns connection_id
- exec: Execute a command on an established connection
- disconnect: Close an SSH connection
- list_connections: List all active SSH connections

Authentication is handled automatically based on host configuration:
- ssh-agent (if use_agent: true)
- Private key (with optional passphrase from secrets store)
- Password (from secrets store)

Host key verification follows the configured mode (strict/accept-new/none).
Jump hosts are supported via proxy_jump configuration.`),
	)
}

// executeSSHTool handles SSH tool actions.
func executeSSHTool(ctx context.Context, args SSHToolArgs, manager *SSHConnectionManager) (*SSHToolResult, error) {
	switch args.Action {
	case "connect":
		return sshConnect(ctx, args, manager)
	case "exec":
		return sshExec(ctx, args, manager)
	case "disconnect":
		return sshDisconnect(args, manager)
	case "list_connections":
		return sshListConnections(manager)
	default:
		return &SSHToolResult{
			Success: false,
			Message: fmt.Sprintf("unknown action: %s", args.Action),
		}, nil
	}
}

// sshConnect establishes an SSH connection.
func sshConnect(ctx context.Context, args SSHToolArgs, manager *SSHConnectionManager) (*SSHToolResult, error) {
	if args.Host == "" {
		return &SSHToolResult{
			Success: false,
			Message: "host is required for connect action",
		}, nil
	}

	connID, err := manager.Connect(ctx, args.Host)
	if err != nil {
		return &SSHToolResult{
			Success: false,
			Message: fmt.Sprintf("failed to connect to %s: %s", args.Host, err),
		}, nil
	}

	return &SSHToolResult{
		Success:      true,
		Message:      fmt.Sprintf("connected to %s", args.Host),
		ConnectionID: connID,
	}, nil
}

// sshExec executes a command on an SSH connection.
func sshExec(ctx context.Context, args SSHToolArgs, manager *SSHConnectionManager) (*SSHToolResult, error) {
	if args.ConnectionID == "" {
		return &SSHToolResult{
			Success: false,
			Message: "connection_id is required for exec action",
		}, nil
	}
	if args.Command == "" {
		return &SSHToolResult{
			Success: false,
			Message: "command is required for exec action",
		}, nil
	}

	conn, err := manager.GetConnection(args.ConnectionID)
	if err != nil {
		return &SSHToolResult{
			Success: false,
			Message: fmt.Sprintf("connection not found: %s", args.ConnectionID),
		}, nil
	}

	// Update activity
	manager.UpdateActivity(args.ConnectionID)

	// Check for dangerous commands
	var warning string
	if isDestructiveCommand(args.Command) {
		warning = "WARNING: This command may be destructive. Proceed with caution."
	}

	// Determine timeout
	timeout := sshDefaultExecTimeout
	if args.TimeoutSecs > 0 {
		timeout = time.Duration(args.TimeoutSecs) * time.Second
		if timeout > sshMaxExecTimeout {
			timeout = sshMaxExecTimeout
		}
	}

	// Create context with timeout
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Create session
	session, err := conn.client.NewSession()
	if err != nil {
		return &SSHToolResult{
			Success: false,
			Message: fmt.Sprintf("failed to create session: %s", err),
		}, nil
	}
	defer session.Close()

	// Set environment variables
	for k, v := range args.Env {
		if err := session.Setenv(k, v); err != nil {
			// Some servers don't allow setting env vars - log but continue
			manager.logger.Debug("failed to set env var", "key", k, "error", err)
		}
	}

	// Request PTY if needed
	if args.PTY {
		modes := make(map[uint8]uint32)
		if err := session.RequestPty("xterm", 80, 40, modes); err != nil {
			return &SSHToolResult{
				Success: false,
				Message: fmt.Sprintf("failed to request PTY: %s", err),
			}, nil
		}
	}

	// Set up stdin if provided
	if args.Stdin != "" {
		session.Stdin = strings.NewReader(args.Stdin)
	}

	// Capture stdout and stderr
	var stdout, stderr bytes.Buffer
	session.Stdout = &limitedWriter{w: &stdout, limit: sshMaxOutputSize}
	session.Stderr = &limitedWriter{w: &stderr, limit: sshMaxOutputSize}

	// Run command with context
	done := make(chan error, 1)
	go func() {
		done <- session.Run(args.Command)
	}()

	var exitCode int
	select {
	case <-execCtx.Done():
		session.Signal(ssh.SIGINT)
		return &SSHToolResult{
			Success:  false,
			Message:  "command timed out",
			Stdout:   sanitizeOutput(stdout.String()),
			Stderr:   sanitizeOutput(stderr.String()),
			ExitCode: -1,
		}, nil
	case err := <-done:
		if err != nil {
			// Try to get exit code from ssh.ExitError
			if exitErr, ok := err.(*ssh.ExitError); ok {
				exitCode = exitErr.ExitStatus()
			} else {
				exitCode = 1
			}
		}
	}

	return &SSHToolResult{
		Success:  exitCode == 0,
		Message:  fmt.Sprintf("command completed with exit code %d", exitCode),
		Stdout:   sanitizeOutput(stdout.String()),
		Stderr:   sanitizeOutput(stderr.String()),
		ExitCode: exitCode,
		Warning:  warning,
	}, nil
}

// sshDisconnect closes an SSH connection.
func sshDisconnect(args SSHToolArgs, manager *SSHConnectionManager) (*SSHToolResult, error) {
	if args.ConnectionID == "" {
		return &SSHToolResult{
			Success: false,
			Message: "connection_id is required for disconnect action",
		}, nil
	}

	if err := manager.Disconnect(args.ConnectionID); err != nil {
		return &SSHToolResult{
			Success: false,
			Message: fmt.Sprintf("failed to disconnect: %s", err),
		}, nil
	}

	return &SSHToolResult{
		Success: true,
		Message: "disconnected",
	}, nil
}

// sshListConnections lists all active SSH connections.
func sshListConnections(manager *SSHConnectionManager) (*SSHToolResult, error) {
	conns := manager.ListConnections()
	return &SSHToolResult{
		Success:     true,
		Message:     fmt.Sprintf("%d active connection(s)", len(conns)),
		Connections: conns,
	}, nil
}

// isDestructiveCommand checks if a command matches dangerous patterns.
func isDestructiveCommand(cmd string) bool {
	for _, pattern := range dangerousCommandPatterns {
		if pattern.MatchString(cmd) {
			return true
		}
	}
	return false
}

// sanitizeOutput removes potential sensitive data from command output.
func sanitizeOutput(output string) string {
	result := output
	for _, pattern := range sensitiveOutputPatterns {
		result = pattern.ReplaceAllString(result, "[REDACTED]")
	}
	return result
}

// limitedWriter limits the amount of data written.
type limitedWriter struct {
	w       *bytes.Buffer
	limit   int
	written int
}

func (lw *limitedWriter) Write(p []byte) (n int, err error) {
	remaining := lw.limit - lw.written
	if remaining <= 0 {
		return len(p), nil // Discard but pretend we wrote it
	}
	if len(p) > remaining {
		p = p[:remaining]
	}
	n, err = lw.w.Write(p)
	lw.written += n
	return len(p), err // Return original length to satisfy caller
}
