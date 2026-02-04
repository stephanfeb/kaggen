package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yourusername/kaggen/internal/config"
)

var fixPermissions bool

var securityAuditCmd = &cobra.Command{
	Use:   "security-audit",
	Short: "Run security audit on Kaggen configuration and files",
	Long: `Performs a security audit of your Kaggen installation, checking:
- File and directory permissions
- Gateway network binding
- CORS configuration
- Command sandbox settings
- Credential storage

Use --fix to automatically fix file permission issues.`,
	RunE: runSecurityAudit,
}

func init() {
	securityAuditCmd.Flags().BoolVar(&fixPermissions, "fix", false, "Automatically fix file permission issues")
}

type auditIssue struct {
	Severity    string // "critical", "high", "medium", "low"
	Component   string
	Description string
	Remediation string
	Fixed       bool
}

func runSecurityAudit(cmd *cobra.Command, args []string) error {
	fmt.Println("=== Kaggen Security Audit ===")
	fmt.Println()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	var issues []auditIssue

	// 1. Check file permissions
	permIssues := auditFilePermissions(fixPermissions)
	issues = append(issues, permIssues...)

	// 2. Check gateway binding
	bindIssues := auditGatewayBinding(cfg)
	issues = append(issues, bindIssues...)

	// 3. Check CORS configuration
	corsIssues := auditCORSConfig(cfg)
	issues = append(issues, corsIssues...)

	// 4. Check command sandbox
	sandboxIssues := auditCommandSandbox(cfg)
	issues = append(issues, sandboxIssues...)

	// 5. Check credential storage
	credIssues := auditCredentialStorage(cfg)
	issues = append(issues, credIssues...)

	// Print results
	printAuditResults(issues)

	return nil
}

func auditFilePermissions(fix bool) []auditIssue {
	var issues []auditIssue
	baseDir := config.ExpandPath("~/.kaggen")

	fmt.Println("Checking file permissions...")

	// Check if base directory exists
	if _, err := os.Stat(baseDir); os.IsNotExist(err) {
		return issues
	}

	err := filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip files we can't access
		}

		mode := info.Mode().Perm()
		relPath, _ := filepath.Rel(baseDir, path)
		if relPath == "" {
			relPath = "."
		}

		// Check for world-readable files
		if mode&0004 != 0 {
			issue := auditIssue{
				Severity:    "high",
				Component:   "Permissions",
				Description: fmt.Sprintf("World-readable: ~/.kaggen/%s (mode %04o)", relPath, mode),
				Remediation: "Remove world-read permission",
			}

			if fix {
				var newMode os.FileMode
				if info.IsDir() {
					newMode = 0700
				} else {
					newMode = 0600
				}
				if err := os.Chmod(path, newMode); err == nil {
					issue.Fixed = true
				}
			}
			issues = append(issues, issue)
		}

		// Check for group-readable sensitive files
		if mode&0040 != 0 {
			// Only flag certain sensitive files/directories
			if isSensitivePath(relPath) {
				issue := auditIssue{
					Severity:    "medium",
					Component:   "Permissions",
					Description: fmt.Sprintf("Group-readable: ~/.kaggen/%s (mode %04o)", relPath, mode),
					Remediation: "Remove group-read permission",
				}

				if fix {
					var newMode os.FileMode
					if info.IsDir() {
						newMode = 0700
					} else {
						newMode = 0600
					}
					if err := os.Chmod(path, newMode); err == nil {
						issue.Fixed = true
					}
				}
				issues = append(issues, issue)
			}
		}

		return nil
	})

	if err != nil {
		issues = append(issues, auditIssue{
			Severity:    "low",
			Component:   "Permissions",
			Description: fmt.Sprintf("Error scanning directory: %v", err),
		})
	}

	return issues
}

func isSensitivePath(path string) bool {
	sensitive := []string{
		"config.json",
		"sessions",
		"audit.db",
		"memory.db",
		"downloads",
	}
	for _, s := range sensitive {
		if strings.Contains(path, s) {
			return true
		}
	}
	return false
}

func auditGatewayBinding(cfg *config.Config) []auditIssue {
	var issues []auditIssue

	fmt.Println("Checking gateway binding...")

	bind := cfg.Gateway.Bind
	if bind == "" {
		bind = "127.0.0.1"
	}

	// Check if bound to all interfaces
	if bind == "0.0.0.0" || bind == "::" {
		issues = append(issues, auditIssue{
			Severity:    "critical",
			Component:   "Gateway",
			Description: fmt.Sprintf("Gateway bound to all interfaces (%s)", bind),
			Remediation: "Set gateway.bind to '127.0.0.1' unless remote access is required with authentication",
		})
	} else if bind != "127.0.0.1" && bind != "localhost" {
		issues = append(issues, auditIssue{
			Severity:    "medium",
			Component:   "Gateway",
			Description: fmt.Sprintf("Gateway bound to non-loopback address (%s)", bind),
			Remediation: "Consider binding to '127.0.0.1' for local-only access",
		})
	}

	return issues
}

func auditCORSConfig(cfg *config.Config) []auditIssue {
	var issues []auditIssue

	fmt.Println("Checking CORS configuration...")

	origins := cfg.Gateway.AllowedOrigins
	if len(origins) == 0 {
		// Using defaults (localhost only) - good
		return issues
	}

	// Check for wildcard or overly permissive origins
	for _, origin := range origins {
		if origin == "*" {
			issues = append(issues, auditIssue{
				Severity:    "critical",
				Component:   "CORS",
				Description: "Wildcard origin (*) configured in allowed_origins",
				Remediation: "Remove wildcard and specify explicit allowed origins",
			})
		} else if !strings.HasPrefix(origin, "http://localhost") &&
			!strings.HasPrefix(origin, "https://localhost") &&
			!strings.HasPrefix(origin, "http://127.0.0.1") &&
			!strings.HasPrefix(origin, "https://127.0.0.1") {
			issues = append(issues, auditIssue{
				Severity:    "low",
				Component:   "CORS",
				Description: fmt.Sprintf("Non-localhost origin configured: %s", origin),
				Remediation: "Verify this origin is intended and trusted",
			})
		}
	}

	return issues
}

func auditCommandSandbox(cfg *config.Config) []auditIssue {
	var issues []auditIssue

	fmt.Println("Checking command sandbox...")

	if !cfg.Security.CommandSandbox.Enabled {
		issues = append(issues, auditIssue{
			Severity:    "medium",
			Component:   "Sandbox",
			Description: "Command execution sandbox is disabled",
			Remediation: "Enable security.command_sandbox.enabled in config for production use",
		})
	}

	return issues
}

func auditCredentialStorage(cfg *config.Config) []auditIssue {
	var issues []auditIssue

	fmt.Println("Checking credential storage...")

	// Check if Telegram bot token is in config file (should use env var)
	if cfg.Channels.Telegram.BotToken != "" {
		issues = append(issues, auditIssue{
			Severity:    "medium",
			Component:   "Credentials",
			Description: "Telegram bot token stored in config file",
			Remediation: "Use TELEGRAM_BOT_TOKEN environment variable instead",
		})
	}

	// Check PostgreSQL password in config
	if cfg.Session.Postgres.Password != "" {
		issues = append(issues, auditIssue{
			Severity:    "medium",
			Component:   "Credentials",
			Description: "PostgreSQL password stored in config file",
			Remediation: "Use environment variable or secrets manager for database credentials",
		})
	}

	// Check Redis password in config
	if cfg.Session.Redis.Password != "" {
		issues = append(issues, auditIssue{
			Severity:    "medium",
			Component:   "Credentials",
			Description: "Redis password stored in config file",
			Remediation: "Use environment variable or secrets manager for database credentials",
		})
	}

	// Check PostgreSQL SSL mode
	if cfg.Session.Backend == "postgres" && cfg.Session.Postgres.SSLMode == "disable" {
		issues = append(issues, auditIssue{
			Severity:    "high",
			Component:   "Credentials",
			Description: "PostgreSQL SSL is disabled - credentials transmitted in plaintext",
			Remediation: "Set session.postgres.ssl_mode to 'require' or higher",
		})
	}

	return issues
}

func printAuditResults(issues []auditIssue) {
	fmt.Println()
	fmt.Println("=== Audit Results ===")
	fmt.Println()

	if len(issues) == 0 {
		fmt.Println("No security issues detected.")
		return
	}

	critical, high, medium, low := 0, 0, 0, 0
	fixed := 0

	for _, issue := range issues {
		switch issue.Severity {
		case "critical":
			critical++
			fmt.Printf("CRITICAL: %s\n", issue.Description)
		case "high":
			high++
			fmt.Printf("HIGH: %s\n", issue.Description)
		case "medium":
			medium++
			fmt.Printf("MEDIUM: %s\n", issue.Description)
		case "low":
			low++
			fmt.Printf("LOW: %s\n", issue.Description)
		}

		fmt.Printf("  Component: %s\n", issue.Component)
		if issue.Remediation != "" {
			fmt.Printf("  Fix: %s\n", issue.Remediation)
		}
		if issue.Fixed {
			fmt.Printf("  Status: FIXED\n")
			fixed++
		}
		fmt.Println()
	}

	fmt.Println("--- Summary ---")
	fmt.Printf("Critical: %d, High: %d, Medium: %d, Low: %d\n", critical, high, medium, low)
	if fixed > 0 {
		fmt.Printf("Issues fixed: %d\n", fixed)
	}

	if critical > 0 || high > 0 {
		fmt.Println()
		fmt.Println("Your installation has security issues that should be addressed!")
	}
}
