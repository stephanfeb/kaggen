package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

const secretsDisabledMsg = `Secrets management is now dashboard-only for security.

Open the Kaggen dashboard to manage secrets:
  1. Start kaggen with: kaggen agent
  2. Open http://localhost:<port>/ in your browser
  3. Log in and navigate to Settings > Secrets

This change prevents secrets from being exposed via shell history,
process lists, or agents with exec access.`

var secretsCmd = &cobra.Command{
	Use:   "secrets",
	Short: "Manage secure credential storage (dashboard-only)",
	Long:  secretsDisabledMsg,
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf(secretsDisabledMsg)
	},
}

var secretsSetCmd = &cobra.Command{
	Use:   "set <key>",
	Short: "Store a secret (disabled - use dashboard)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf(secretsDisabledMsg)
	},
}

var secretsGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Retrieve a secret (disabled - use dashboard)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf(secretsDisabledMsg)
	},
}

var secretsDeleteCmd = &cobra.Command{
	Use:   "delete <key>",
	Short: "Delete a secret (disabled - use dashboard)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf(secretsDisabledMsg)
	},
}

var secretsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all stored secret keys (disabled - use dashboard)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf(secretsDisabledMsg)
	},
}

var secretsImportEnvCmd = &cobra.Command{
	Use:   "import-env <ENV_VAR>",
	Short: "Import a secret from environment (disabled - use dashboard)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf(secretsDisabledMsg)
	},
}

func init() {
	secretsCmd.AddCommand(secretsSetCmd)
	secretsCmd.AddCommand(secretsGetCmd)
	secretsCmd.AddCommand(secretsDeleteCmd)
	secretsCmd.AddCommand(secretsListCmd)
	secretsCmd.AddCommand(secretsImportEnvCmd)

	rootCmd.AddCommand(secretsCmd)
}
