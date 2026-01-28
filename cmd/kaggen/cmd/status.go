package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/yourusername/kaggen/internal/config"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show Kaggen status and configuration",
	Long:  `Display the current configuration, workspace status, and health information.`,
	RunE:  runStatus,
}

func runStatus(cmd *cobra.Command, args []string) error {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	fmt.Println("Kaggen Status")
	fmt.Println("=============")
	fmt.Println()

	// Configuration
	fmt.Println("Configuration:")
	fmt.Printf("  Model:     %s\n", cfg.Agent.Model)
	fmt.Printf("  Workspace: %s\n", cfg.WorkspacePath())
	fmt.Printf("  Sessions:  %s\n", cfg.SessionsPath())
	fmt.Printf("  Gateway:   %s:%d\n", cfg.Gateway.Bind, cfg.Gateway.Port)
	fmt.Println()

	// API Key status
	apiKey := config.AnthropicAPIKey()
	fmt.Println("API Key:")
	if apiKey == "" {
		fmt.Println("  ANTHROPIC_API_KEY: NOT SET")
	} else {
		// Show only first and last 4 characters
		masked := apiKey[:4] + "..." + apiKey[len(apiKey)-4:]
		fmt.Printf("  ANTHROPIC_API_KEY: %s\n", masked)
	}
	fmt.Println()

	// Workspace status
	workspace := cfg.WorkspacePath()
	fmt.Println("Workspace Files:")

	files := []string{"SOUL.md", "IDENTITY.md", "AGENTS.md", "TOOLS.md", "USER.md", "MEMORY.md"}
	for _, f := range files {
		path := workspace + "/" + f
		if _, err := os.Stat(path); err == nil {
			info, _ := os.Stat(path)
			fmt.Printf("  %s: exists (%d bytes)\n", f, info.Size())
		} else {
			fmt.Printf("  %s: missing\n", f)
		}
	}
	fmt.Println()

	// Sessions
	sessDir := cfg.SessionsPath()
	if entries, err := os.ReadDir(sessDir); err == nil {
		fmt.Printf("Sessions: %d found\n", len(entries))
		for _, e := range entries {
			if !e.IsDir() {
				info, _ := e.Info()
				fmt.Printf("  - %s (%d bytes)\n", e.Name(), info.Size())
			}
		}
	} else {
		fmt.Println("Sessions: directory not found")
	}

	return nil
}
