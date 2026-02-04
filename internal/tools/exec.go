package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"

	"github.com/yourusername/kaggen/internal/security"
)

const (
	defaultTimeout = 30 * time.Second
	maxTimeout     = 30 * time.Minute
)

// ExecArgs defines the input arguments for the exec tool.
type ExecArgs struct {
	Command        string `json:"command" jsonschema:"required,description=The shell command to execute."`
	TimeoutSeconds *int   `json:"timeout_seconds,omitempty" jsonschema:"description=Maximum time in seconds to wait for the command to complete. Defaults to 30 max 300."`
}

// ExecResult defines the output of the exec tool.
type ExecResult struct {
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code,omitempty"`
	Message  string `json:"message"`
}

// newExecTool creates a new exec tool using trpc-agent-go's function tool.
func newExecTool(workspace string) tool.CallableTool {
	return newExecToolWithSandbox(workspace, nil)
}

// newExecToolWithSandbox creates a new exec tool with optional command sandbox.
func newExecToolWithSandbox(workspace string, sandbox *security.CommandSandbox) tool.CallableTool {
	return function.NewFunctionTool(
		func(ctx context.Context, args ExecArgs) (*ExecResult, error) {
			return executeExec(ctx, workspace, args, sandbox)
		},
		function.WithName("exec"),
		function.WithDescription("Execute a shell command and return its output. Commands run in a bash shell with the workspace as the working directory. Use this for running programs, listing files, or other system operations."),
	)
}

// executeExec performs the actual command execution.
func executeExec(ctx context.Context, workspace string, args ExecArgs, sandbox *security.CommandSandbox) (*ExecResult, error) {
	result := &ExecResult{}

	if args.Command == "" {
		result.Message = "Error: command is required"
		return result, fmt.Errorf("command is required")
	}

	// Validate command against security sandbox
	if sandbox != nil {
		validation := sandbox.Validate(args.Command)
		if !validation.Allowed {
			result.Message = fmt.Sprintf("Command blocked: %s", validation.Reason)
			result.Output = fmt.Sprintf("Security: command blocked by sandbox policy\nPattern: %s", validation.Pattern)
			result.ExitCode = -2
			return result, fmt.Errorf("command blocked by security policy")
		}
	}

	timeout := defaultTimeout
	if args.TimeoutSeconds != nil {
		timeout = time.Duration(*args.TimeoutSeconds) * time.Second
		if timeout > maxTimeout {
			timeout = maxTimeout
		}
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Execute command using bash
	cmd := exec.CommandContext(ctx, "bash", "-c", args.Command)
	cmd.Dir = workspace

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	// Build output
	var output strings.Builder

	if stdout.Len() > 0 {
		output.WriteString(stdout.String())
	}

	if stderr.Len() > 0 {
		if output.Len() > 0 {
			output.WriteString("\n")
		}
		output.WriteString("STDERR:\n")
		output.WriteString(stderr.String())
	}

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			result.Output = output.String() + "\nError: command timed out"
			result.Message = "Command timed out"
			result.ExitCode = -1
			return result, nil
		}
		if output.Len() > 0 {
			output.WriteString("\n")
		}
		output.WriteString(fmt.Sprintf("Exit error: %v", err))
		result.ExitCode = 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		}
	}

	outputStr := output.String()
	if outputStr == "" {
		outputStr = "(no output)"
	}

	result.Output = outputStr
	result.Message = fmt.Sprintf("Command executed: %s", args.Command)
	return result, nil
}
