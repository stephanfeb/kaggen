package cmd

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/yourusername/kaggen/internal/agent"
	"github.com/yourusername/kaggen/internal/channel"
	"github.com/yourusername/kaggen/internal/config"
)

var testLocalCmd = &cobra.Command{
	Use:   "test-local [message]",
	Short: "Test local LLM (Ollama) routing for third-party messages",
	Long: `Test the local LLM routing that handles third-party (unknown sender) messages.

This simulates what happens when someone not in your allowlist messages you.
The message is processed by your local Ollama instance instead of the frontier model.

Examples:
  kaggen test-local "Hello, who are you?"
  kaggen test-local  # Interactive mode`,
	RunE: runTestLocal,
}

func init() {
	rootCmd.AddCommand(testLocalCmd)
}

func runTestLocal(cmd *cobra.Command, args []string) error {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Check third-party config
	if !cfg.Trust.ThirdParty.Enabled {
		return fmt.Errorf("third_party.enabled is false in config.json")
	}
	if !cfg.Trust.ThirdParty.UseLocalLLM {
		return fmt.Errorf("third_party.use_local_llm is false in config.json")
	}

	// Setup logger
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Create local agent
	localAgent := agent.NewLocalAgent(&cfg.Trust.ThirdParty, logger)

	// Check if Ollama is available
	ctx := context.Background()
	if !localAgent.IsAvailable(ctx) {
		return fmt.Errorf("Ollama is not running at http://localhost:11434\nStart it with: ollama serve")
	}

	fmt.Println("Local LLM Test")
	fmt.Println("==============")
	fmt.Printf("Model: %s\n", localAgent.Model())
	fmt.Printf("System prompt: %s\n", truncate(cfg.Trust.ThirdParty.SystemPrompt, 60))
	fmt.Println()

	// Single message mode
	if len(args) > 0 {
		message := strings.Join(args, " ")
		return sendTestMessage(ctx, localAgent, message)
	}

	// Interactive mode
	fmt.Println("Interactive mode. Type 'exit' or 'quit' to end.")
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)
	sessionID := uuid.New().String()

	for {
		fmt.Print("You: ")
		input, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("read input: %w", err)
		}

		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}

		switch strings.ToLower(input) {
		case "exit", "quit":
			fmt.Println("Goodbye!")
			return nil
		case "/clear":
			localAgent.ClearSession(sessionID)
			fmt.Println("Session cleared.")
			continue
		}

		msg := &channel.Message{
			ID:        uuid.New().String(),
			SessionID: sessionID,
			UserID:    "test-user",
			Content:   input,
			Channel:   "test",
		}

		resp, err := localAgent.HandleMessage(ctx, msg)
		if err != nil {
			fmt.Printf("Error: %v\n\n", err)
			continue
		}

		fmt.Printf("Local LLM: %s\n\n", resp.Content)
	}
}

func sendTestMessage(ctx context.Context, localAgent *agent.LocalAgent, message string) error {
	fmt.Printf("You: %s\n\n", message)

	msg := &channel.Message{
		ID:        uuid.New().String(),
		SessionID: uuid.New().String(),
		UserID:    "test-user",
		Content:   message,
		Channel:   "test",
	}

	resp, err := localAgent.HandleMessage(ctx, msg)
	if err != nil {
		return fmt.Errorf("local agent error: %w", err)
	}

	fmt.Printf("Local LLM: %s\n", resp.Content)
	return nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
