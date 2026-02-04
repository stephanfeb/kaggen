package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/yourusername/kaggen/internal/secrets"
)

var secretsCmd = &cobra.Command{
	Use:   "secrets",
	Short: "Manage secure credential storage",
	Long: `Securely store and manage credentials like API keys, tokens, and passwords.

Secrets are stored using OS keychain when available, or an encrypted file
as a fallback for headless/server environments.

For encrypted file storage, set the KAGGEN_MASTER_KEY environment variable
or you will be prompted for the master key interactively.`,
}

var secretsSetCmd = &cobra.Command{
	Use:   "set <key>",
	Short: "Store a secret",
	Long: `Store a secret value securely.

Examples:
  kaggen secrets set anthropic-key          # Prompts for value
  kaggen secrets set telegram-token --value="bot123:abc"  # Inline value
  echo "secret" | kaggen secrets set my-key # From stdin`,
	Args: cobra.ExactArgs(1),
	RunE: runSecretsSet,
}

var secretsGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Retrieve a secret",
	Long: `Retrieve a stored secret value.

Examples:
  kaggen secrets get anthropic-key
  export API_KEY=$(kaggen secrets get anthropic-key)`,
	Args: cobra.ExactArgs(1),
	RunE: runSecretsGet,
}

var secretsDeleteCmd = &cobra.Command{
	Use:   "delete <key>",
	Short: "Delete a secret",
	Args:  cobra.ExactArgs(1),
	RunE:  runSecretsDelete,
}

var secretsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all stored secret keys",
	RunE:  runSecretsList,
}

var secretsImportEnvCmd = &cobra.Command{
	Use:   "import-env <ENV_VAR>",
	Short: "Import a secret from an environment variable",
	Long: `Import a secret from an environment variable.

Examples:
  kaggen secrets import-env ANTHROPIC_API_KEY --as=anthropic-key
  kaggen secrets import-env TELEGRAM_BOT_TOKEN  # Uses lowercase name`,
	Args: cobra.ExactArgs(1),
	RunE: runSecretsImportEnv,
}

var (
	secretValue string
	secretAs    string
)

func init() {
	secretsSetCmd.Flags().StringVar(&secretValue, "value", "", "Secret value (omit for interactive prompt)")
	secretsImportEnvCmd.Flags().StringVar(&secretAs, "as", "", "Name to store the secret as (default: lowercase env var name)")

	secretsCmd.AddCommand(secretsSetCmd)
	secretsCmd.AddCommand(secretsGetCmd)
	secretsCmd.AddCommand(secretsDeleteCmd)
	secretsCmd.AddCommand(secretsListCmd)
	secretsCmd.AddCommand(secretsImportEnvCmd)

	rootCmd.AddCommand(secretsCmd)
}

func getSecretStore() (secrets.SecretStore, error) {
	store := secrets.DefaultStore()

	// If encrypted store and needs master key, try to prompt
	if enc, ok := store.(*secrets.EncryptedStore); ok && !enc.Available() {
		// Try to prompt for master key interactively
		if term.IsTerminal(int(syscall.Stdin)) {
			fmt.Print("Enter master key for encrypted secrets: ")
			key, err := term.ReadPassword(int(syscall.Stdin))
			fmt.Println()
			if err != nil {
				return nil, fmt.Errorf("failed to read master key: %w", err)
			}
			enc.SetMasterKey(string(key))
		} else {
			return nil, fmt.Errorf("encrypted store requires KAGGEN_MASTER_KEY environment variable")
		}
	}

	if !store.Available() {
		return nil, fmt.Errorf("no secret store available")
	}

	return store, nil
}

func runSecretsSet(cmd *cobra.Command, args []string) error {
	key := args[0]

	store, err := getSecretStore()
	if err != nil {
		return err
	}

	var value string
	if secretValue != "" {
		value = secretValue
	} else if !term.IsTerminal(int(syscall.Stdin)) {
		// Read from stdin
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			value = scanner.Text()
		}
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("failed to read from stdin: %w", err)
		}
	} else {
		// Prompt for value
		fmt.Printf("Enter value for %q: ", key)
		valueBytes, err := term.ReadPassword(int(syscall.Stdin))
		fmt.Println()
		if err != nil {
			return fmt.Errorf("failed to read value: %w", err)
		}
		value = string(valueBytes)
	}

	if value == "" {
		return fmt.Errorf("secret value cannot be empty")
	}

	if err := store.Set(key, value); err != nil {
		return fmt.Errorf("failed to store secret: %w", err)
	}

	fmt.Printf("Secret %q stored in %s.\n", key, store.Name())
	return nil
}

func runSecretsGet(cmd *cobra.Command, args []string) error {
	key := args[0]

	store, err := getSecretStore()
	if err != nil {
		return err
	}

	value, err := store.Get(key)
	if err != nil {
		if err == secrets.ErrSecretNotFound {
			return fmt.Errorf("secret %q not found", key)
		}
		return fmt.Errorf("failed to retrieve secret: %w", err)
	}

	fmt.Println(value)
	return nil
}

func runSecretsDelete(cmd *cobra.Command, args []string) error {
	key := args[0]

	store, err := getSecretStore()
	if err != nil {
		return err
	}

	if err := store.Delete(key); err != nil {
		return fmt.Errorf("failed to delete secret: %w", err)
	}

	fmt.Printf("Secret %q deleted.\n", key)
	return nil
}

func runSecretsList(cmd *cobra.Command, args []string) error {
	store, err := getSecretStore()
	if err != nil {
		return err
	}

	keys, err := store.List()
	if err != nil {
		return fmt.Errorf("failed to list secrets: %w", err)
	}

	if len(keys) == 0 {
		fmt.Println("No secrets stored.")
		fmt.Println()
		fmt.Println("Store a secret with:")
		fmt.Println("  kaggen secrets set <key-name>")
		return nil
	}

	fmt.Printf("Secrets stored in %s:\n\n", store.Name())
	for _, key := range keys {
		fmt.Printf("  - %s\n", key)
	}
	fmt.Printf("\nTotal: %d secret(s)\n", len(keys))

	return nil
}

func runSecretsImportEnv(cmd *cobra.Command, args []string) error {
	envVar := args[0]

	value := os.Getenv(envVar)
	if value == "" {
		return fmt.Errorf("environment variable %s is not set or empty", envVar)
	}

	key := secretAs
	if key == "" {
		// Convert to lowercase and replace underscores with dashes
		key = strings.ToLower(strings.ReplaceAll(envVar, "_", "-"))
	}

	store, err := getSecretStore()
	if err != nil {
		return err
	}

	if err := store.Set(key, value); err != nil {
		return fmt.Errorf("failed to store secret: %w", err)
	}

	fmt.Printf("Imported %s as %q into %s.\n", envVar, key, store.Name())
	return nil
}
