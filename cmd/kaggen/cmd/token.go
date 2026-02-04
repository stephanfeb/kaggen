package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/yourusername/kaggen/internal/auth"
	"github.com/yourusername/kaggen/internal/config"
)

var tokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Manage authentication tokens",
	Long:  `Generate, list, and revoke authentication tokens for secure access to Kaggen.`,
}

var tokenGenerateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate a new authentication token",
	Long: `Generate a new authentication token for client access.

The token is displayed only once - save it securely.

Examples:
  kaggen token generate                    # Generate token, never expires
  kaggen token generate -n "iPhone"        # Named token
  kaggen token generate -e 24h             # Expires in 24 hours
  kaggen token generate -e 7d -n "iPad"    # Named token, expires in 7 days
`,
	RunE: runTokenGenerate,
}

var tokenListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all configured tokens",
	RunE:  runTokenList,
}

var tokenRevokeCmd = &cobra.Command{
	Use:   "revoke <token-id>",
	Short: "Revoke a token by ID",
	Args:  cobra.ExactArgs(1),
	RunE:  runTokenRevoke,
}

var (
	tokenName      string
	tokenExpiresIn string
)

func init() {
	tokenGenerateCmd.Flags().StringVarP(&tokenName, "name", "n", "", "Friendly name for the token")
	tokenGenerateCmd.Flags().StringVarP(&tokenExpiresIn, "expires", "e", "", "Expiration duration (e.g., 24h, 7d, 30d)")

	tokenCmd.AddCommand(tokenGenerateCmd)
	tokenCmd.AddCommand(tokenListCmd)
	tokenCmd.AddCommand(tokenRevokeCmd)
}

func getTokenStore() (*auth.TokenStore, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	tokenFile := cfg.Security.Auth.TokenFile
	if tokenFile == "" {
		tokenFile = config.ExpandPath("~/.kaggen/tokens.json")
	}

	return auth.NewTokenStore(tokenFile)
}

func runTokenGenerate(cmd *cobra.Command, args []string) error {
	store, err := getTokenStore()
	if err != nil {
		return err
	}

	// Parse expiration duration
	var expiresIn time.Duration
	if tokenExpiresIn != "" {
		var parseErr error
		expiresIn, parseErr = time.ParseDuration(tokenExpiresIn)
		if parseErr != nil {
			// Try parsing as days (e.g., "7d")
			if len(tokenExpiresIn) > 1 && tokenExpiresIn[len(tokenExpiresIn)-1] == 'd' {
				var days int
				_, err := fmt.Sscanf(tokenExpiresIn[:len(tokenExpiresIn)-1], "%d", &days)
				if err == nil {
					expiresIn = time.Duration(days) * 24 * time.Hour
				}
			}
			if expiresIn == 0 {
				return fmt.Errorf("invalid expiration format: %s (use formats like 24h, 7d, 30d)", tokenExpiresIn)
			}
		}
	}

	plaintext, id, err := store.GenerateToken(tokenName, expiresIn)
	if err != nil {
		return fmt.Errorf("failed to generate token: %w", err)
	}

	fmt.Println("=== New Authentication Token ===")
	fmt.Println()
	fmt.Printf("Token ID: %s\n", id)
	if tokenName != "" {
		fmt.Printf("Name:     %s\n", tokenName)
	}
	if expiresIn > 0 {
		fmt.Printf("Expires:  %s\n", time.Now().Add(expiresIn).Format(time.RFC3339))
	} else {
		fmt.Println("Expires:  Never")
	}
	fmt.Println()
	fmt.Println("Token (save this - it cannot be retrieved later):")
	fmt.Println()
	fmt.Printf("  %s\n", plaintext)
	fmt.Println()
	fmt.Println("Use this token in the mobile app or with WebSocket connections:")
	fmt.Printf("  ws://YOUR_HOST:PORT/ws?token=%s\n", plaintext)
	fmt.Println()

	return nil
}

func runTokenList(cmd *cobra.Command, args []string) error {
	store, err := getTokenStore()
	if err != nil {
		return err
	}

	tokens := store.ListTokens()
	if len(tokens) == 0 {
		fmt.Println("No tokens configured.")
		fmt.Println()
		fmt.Println("Generate a new token with:")
		fmt.Println("  kaggen token generate")
		return nil
	}

	fmt.Printf("%-12s %-20s %-24s %-10s\n", "ID", "NAME", "EXPIRES", "STATUS")
	fmt.Printf("%-12s %-20s %-24s %-10s\n", "----", "----", "-------", "------")

	for _, t := range tokens {
		name := t.Name
		if name == "" {
			name = "(unnamed)"
		}
		if len(name) > 20 {
			name = name[:17] + "..."
		}

		expires := "Never"
		status := "Active"
		if !t.ExpiresAt.IsZero() {
			expires = t.ExpiresAt.Format("2006-01-02 15:04")
			if t.Expired {
				status = "Expired"
			}
		}

		fmt.Printf("%-12s %-20s %-24s %-10s\n", t.ID, name, expires, status)
	}

	return nil
}

func runTokenRevoke(cmd *cobra.Command, args []string) error {
	tokenID := args[0]

	store, err := getTokenStore()
	if err != nil {
		return err
	}

	if err := store.RevokeToken(tokenID); err != nil {
		return fmt.Errorf("failed to revoke token: %w", err)
	}

	fmt.Printf("Token %s has been revoked.\n", tokenID)
	return nil
}

// Add to root command
func init() {
	rootCmd.AddCommand(tokenCmd)
}

// Ensure ~/.kaggen directory exists when generating tokens
func init() {
	kaggenDir := config.ExpandPath("~/.kaggen")
	os.MkdirAll(kaggenDir, 0700)
}
