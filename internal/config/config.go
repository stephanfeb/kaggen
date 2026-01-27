// Package config handles configuration loading and management.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Config represents the top-level configuration.
type Config struct {
	Agent    AgentConfig    `json:"agent"`
	Gateway  GatewayConfig  `json:"gateway"`
	Session  SessionConfig  `json:"session"`
	Channels ChannelsConfig `json:"channels"`
}

// AgentConfig configures the AI agent.
type AgentConfig struct {
	Model     string `json:"model"`     // e.g., "anthropic/claude-sonnet-4-20250514"
	Workspace string `json:"workspace"` // e.g., "~/.kaggen/workspace"
}

// GatewayConfig configures the gateway server.
type GatewayConfig struct {
	Bind string `json:"bind"`
	Port int    `json:"port"`
}

// SessionConfig configures session storage.
type SessionConfig struct {
	Backend  string       `json:"backend"` // "file", "redis", "postgres", "memory"
	Redis    RedisConfig  `json:"redis,omitempty"`
	Postgres PGConfig     `json:"postgres,omitempty"`
	AppName  string       `json:"app_name,omitempty"` // App name for trpc backends
	UserID   string       `json:"user_id,omitempty"`  // Default user ID for trpc backends
}

// RedisConfig configures Redis session storage.
type RedisConfig struct {
	Addr     string `json:"addr"`               // e.g., "localhost:6379"
	Password string `json:"password,omitempty"` // Redis password
	DB       int    `json:"db,omitempty"`       // Redis database number
}

// PGConfig configures PostgreSQL session storage.
type PGConfig struct {
	Host     string `json:"host"`               // e.g., "localhost"
	Port     int    `json:"port"`               // e.g., 5432
	User     string `json:"user"`               // Database user
	Password string `json:"password,omitempty"` // Database password
	Database string `json:"database"`           // Database name
	SSLMode  string `json:"ssl_mode,omitempty"` // e.g., "disable", "require"
}

// ChannelsConfig configures communication channels.
type ChannelsConfig struct {
	Telegram TelegramConfig `json:"telegram"`
}

// TelegramConfig configures the Telegram bot channel.
type TelegramConfig struct {
	Enabled  bool   `json:"enabled"`
	BotToken string `json:"bot_token"`
}

// TelegramBotToken returns the bot token from config, falling back to the
// TELEGRAM_BOT_TOKEN environment variable.
func (c *Config) TelegramBotToken() string {
	if c.Channels.Telegram.BotToken != "" {
		return c.Channels.Telegram.BotToken
	}
	return os.Getenv("TELEGRAM_BOT_TOKEN")
}

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	return &Config{
		Agent: AgentConfig{
			Model:     "anthropic/claude-sonnet-4-20250514",
			Workspace: "~/.kaggen/workspace",
		},
		Gateway: GatewayConfig{
			Bind: "127.0.0.1",
			Port: 18789,
		},
		Session: SessionConfig{
			Backend: "file",
			AppName: "kaggen",
			UserID:  "default",
			Redis: RedisConfig{
				Addr: "localhost:6379",
			},
			Postgres: PGConfig{
				Host:     "localhost",
				Port:     5432,
				Database: "kaggen",
				SSLMode:  "disable",
			},
		},
	}
}

// Load reads configuration from the default location (~/.kaggen/config.json).
// If the file doesn't exist, returns the default configuration.
func Load() (*Config, error) {
	configPath := ExpandPath("~/.kaggen/config.json")

	data, err := os.ReadFile(configPath)
	if os.IsNotExist(err) {
		return DefaultConfig(), nil
	}
	if err != nil {
		return nil, err
	}

	cfg := DefaultConfig()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Save writes the configuration to the default location.
func (c *Config) Save() error {
	configPath := ExpandPath("~/.kaggen/config.json")

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(configPath, data, 0644)
}

// ExpandPath expands ~ to the user's home directory.
func ExpandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}

// WorkspacePath returns the expanded workspace path.
func (c *Config) WorkspacePath() string {
	return ExpandPath(c.Agent.Workspace)
}

// SessionsPath returns the path to the sessions directory.
func (c *Config) SessionsPath() string {
	return ExpandPath("~/.kaggen/sessions")
}

// APIKey returns the Anthropic API key from environment.
func APIKey() string {
	return os.Getenv("ANTHROPIC_API_KEY")
}
