// Package cmd implements the CLI commands for Kaggen.
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "kaggen",
	Short: "Kaggen - Personal AI Assistant",
	Long: `Kaggen is a personal AI assistant platform.

It provides an interactive CLI agent with file-based persistence,
Claude integration, and tool execution capabilities.`,
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(agentCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(gatewayCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(securityAuditCmd)
	rootCmd.AddCommand(evalCmd)
}
