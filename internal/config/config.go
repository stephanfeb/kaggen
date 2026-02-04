// Package config handles configuration loading and management.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Config represents the top-level configuration.
type Config struct {
	Agent     AgentConfig     `json:"agent"`
	Gateway   GatewayConfig   `json:"gateway"`
	Session   SessionConfig   `json:"session"`
	Channels  ChannelsConfig  `json:"channels"`
	Memory    MemoryConfig    `json:"memory"`
	Browser   BrowserConfig   `json:"browser,omitempty"`
	Proactive ProactiveConfig `json:"proactive,omitempty"`
	Telemetry TelemetryConfig `json:"telemetry,omitempty"`
	STT       STTConfig       `json:"stt,omitempty"`
	Approval  ApprovalConfig  `json:"approval,omitempty"`
	Security  SecurityConfig  `json:"security,omitempty"`
}

// SecurityConfig configures security hardening features.
type SecurityConfig struct {
	Auth           AuthConfig           `json:"auth,omitempty"`
	CommandSandbox CommandSandboxConfig `json:"command_sandbox,omitempty"`
}

// AuthConfig configures gateway authentication.
type AuthConfig struct {
	Enabled   bool   `json:"enabled"`              // Enable token authentication for WebSocket/API
	TokenFile string `json:"token_file,omitempty"` // Path to token store (default: ~/.kaggen/tokens.json)
}

// TokenFilePath returns the expanded path to the token store file.
func (c *Config) TokenFilePath() string {
	if c.Security.Auth.TokenFile != "" {
		return ExpandPath(c.Security.Auth.TokenFile)
	}
	return ExpandPath("~/.kaggen/tokens.json")
}

// CommandSandboxConfig configures the command execution sandbox.
type CommandSandboxConfig struct {
	Enabled         bool     `json:"enabled"`                    // Enable command validation (default: false for backwards compat)
	BlockedPatterns []string `json:"blocked_patterns,omitempty"` // Additional blocked patterns (regex)
}

// ApprovalConfig configures the maker-checker approval system.
type ApprovalConfig struct {
	AuditDBPath string             `json:"audit_db_path,omitempty"` // default ~/.kaggen/audit.db
	AutoApprove []AutoApproveRule  `json:"auto_approve,omitempty"`  // rules that bypass approval
}

// AutoApproveRule defines a pattern for auto-approving guarded tool calls.
type AutoApproveRule struct {
	Tool    string `json:"tool"`              // tool name, e.g. "Bash"
	Pattern string `json:"pattern,omitempty"` // regex matched against description; empty = match all
}

// STTConfig configures speech-to-text transcription for voice messages.
type STTConfig struct {
	Enabled bool   `json:"enabled"`
	BaseURL string `json:"base_url,omitempty"` // default http://localhost:8000
}

// BrowserConfig configures browser control via Chrome DevTools Protocol.
type BrowserConfig struct {
	Enabled  bool             `json:"enabled"`
	Profiles []BrowserProfile `json:"profiles,omitempty"`
}

// BrowserProfile configures a browser connection profile.
type BrowserProfile struct {
	Name        string   `json:"name"`                    // profile name, e.g. "default"
	Type        string   `json:"type"`                    // "managed" or "remote"
	ExecPath    string   `json:"exec_path,omitempty"`     // managed: path to chrome binary
	RemoteURL   string   `json:"remote_url,omitempty"`    // remote: ws:// or wss:// CDP URL
	Headless    bool     `json:"headless"`                // managed: run headless
	UserDataDir string   `json:"user_data_dir,omitempty"` // managed: chrome user data directory
	Flags       []string `json:"flags,omitempty"`         // extra chrome flags
}

// TelemetryConfig configures observability (tracing, metrics).
type TelemetryConfig struct {
	Enabled        bool   `json:"enabled"`
	JaegerEndpoint string `json:"jaeger_endpoint,omitempty"` // OTLP endpoint, default "localhost:4317"
	Protocol       string `json:"protocol,omitempty"`        // "grpc" (default) or "http"
	ServiceName    string `json:"service_name,omitempty"`    // default "kaggen"
}

// ProactiveConfig configures the proactive engine (cron, webhooks, heartbeats).
type ProactiveConfig struct {
	Jobs          []CronJobConfig   `json:"jobs,omitempty"`
	Webhooks      []WebhookConfig   `json:"webhooks,omitempty"`
	Heartbeats    []HeartbeatConfig `json:"heartbeats,omitempty"`
	HistoryDBPath string            `json:"history_db_path,omitempty"` // default ~/.kaggen/proactive.db
}

// CronJobConfig configures a scheduled proactive job.
type CronJobConfig struct {
	Name       string         `json:"name"`
	Schedule   string         `json:"schedule"` // crontab e.g. "0 9 * * 1-5"
	Prompt     string         `json:"prompt"`
	UserID     string         `json:"user_id"`
	SessionID  string         `json:"session_id,omitempty"`  // default: "proactive-{name}"
	Channel    string         `json:"channel"`               // "telegram" or "websocket"
	Metadata   map[string]any `json:"metadata,omitempty"`    // e.g. {"chat_id": "123456"}
	Timeout    string         `json:"timeout,omitempty"`     // Go duration, default "5m"
	MaxRetries int            `json:"max_retries,omitempty"` // default 0 (no retries)
}

// WebhookConfig configures an HTTP webhook trigger.
type WebhookConfig struct {
	Name       string         `json:"name"`
	Path       string         `json:"path"`   // e.g. "/hooks/github"
	Prompt     string         `json:"prompt"` // {{.Payload}} replaced with POST body
	UserID     string         `json:"user_id"`
	SessionID  string         `json:"session_id,omitempty"`
	Channel    string         `json:"channel"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	Secret     string         `json:"secret,omitempty"`  // HMAC-SHA256 secret for signature verification
	Timeout    string         `json:"timeout,omitempty"` // Go duration, default "5m"
	MaxRetries int            `json:"max_retries,omitempty"`
}

// HeartbeatConfig configures a periodic heartbeat check.
type HeartbeatConfig struct {
	Name       string         `json:"name"`
	Interval   string         `json:"interval"` // Go duration: "5m", "1h"
	Prompt     string         `json:"prompt"`
	UserID     string         `json:"user_id"`
	SessionID  string         `json:"session_id,omitempty"`
	Channel    string         `json:"channel"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	Timeout    string         `json:"timeout,omitempty"` // Go duration, default "2m"
	MaxRetries int            `json:"max_retries,omitempty"`
}

// MemoryConfig configures the semantic memory search system.
type MemoryConfig struct {
	Search    SearchConfig     `json:"search"`
	Embedding EmbeddingConfig  `json:"embedding"`
	Indexing  IndexingConfig   `json:"indexing"`
	Auto      AutoMemoryConfig `json:"auto"`
}

// AutoMemoryConfig configures auto-memory extraction behavior.
type AutoMemoryConfig struct {
	Timeout string `json:"timeout,omitempty"` // Go duration, default "2m"
}

// SearchConfig configures memory search.
type SearchConfig struct {
	Enabled bool   `json:"enabled"`
	DBPath  string `json:"db_path,omitempty"` // default ~/.kaggen/memory.db
}

// EmbeddingConfig configures the embedding provider.
type EmbeddingConfig struct {
	Provider string `json:"provider"`           // "ollama"
	Model    string `json:"model"`              // e.g. "nomic-embed-text"
	BaseURL  string `json:"base_url,omitempty"` // default http://localhost:11434
}

// IndexingConfig configures memory chunk indexing.
type IndexingConfig struct {
	ChunkSize    int `json:"chunk_size,omitempty"`    // default 400
	ChunkOverlap int `json:"chunk_overlap,omitempty"` // default 80
}

// AgentConfig configures the AI agent.
type AgentConfig struct {
	Model            string           `json:"model"`              // e.g., "anthropic/claude-sonnet-4-20250514"
	Workspace        string           `json:"workspace"`          // e.g., "~/.kaggen/workspace"
	MaxHistoryRuns   int              `json:"max_history_runs"`   // Max conversation messages to keep in context (0 = unlimited, default 40)
	PreloadMemory    int              `json:"preload_memory"`     // Memories to inject into system prompt each turn (0 = disabled, -1 = all, default 20)
	MaxTurnsPerTask  int              `json:"max_turns_per_task"` // Max LLM turns per async task before circuit breaker (0 = default 75)
	MaxConcurrentLLM int              `json:"max_concurrent_llm"` // Max concurrent LLM API calls (0 = unlimited, default 4)
	ClaudeModel      string           `json:"claude_model,omitempty"`  // Default Claude model for sub-agent subprocess dispatch (e.g. "sonnet"), default "sonnet"
	ClaudeTools      string           `json:"claude_tools,omitempty"`  // Default --allowed-tools for Claude sub-agents, default "Bash,Read,Edit,Write,Glob,Grep"
	Supervisor       SupervisorConfig `json:"supervisor,omitempty"`    // Agent execution supervisor
}

// SupervisorConfig configures the agent execution supervisor that monitors
// ClaudeAgent subprocesses and can intervene when agents go off-track.
type SupervisorConfig struct {
	Enabled         bool   `json:"enabled"`
	OllamaBaseURL   string `json:"ollama_base_url,omitempty"`   // default "http://localhost:11434"
	OllamaModel     string `json:"ollama_model,omitempty"`      // default "qwen2.5:1.5b"
	CheckInterval   int    `json:"check_interval,omitempty"`    // turns between Ollama checks, default 10
	MaxCorrections  int    `json:"max_corrections,omitempty"`   // max resume attempts before abort, default 2
	StallTimeoutSec int    `json:"stall_timeout_sec,omitempty"` // seconds of inactivity before stall detection, default 300
}

// GatewayConfig configures the gateway server.
type GatewayConfig struct {
	Bind            string       `json:"bind"`
	Port            int          `json:"port"`
	CallbackBaseURL string       `json:"callback_base_url,omitempty"` // manual override for callback URLs (e.g. "https://kaggen.example.com")
	AllowedOrigins  []string     `json:"allowed_origins,omitempty"`   // WebSocket/CORS allowed origins (default: localhost variants)
	Tunnel          TunnelConfig `json:"tunnel,omitempty"`
	PubSub          PubSubConfig `json:"pubsub,omitempty"`
}

// DefaultAllowedOrigins returns the default list of allowed origins for CORS/WebSocket.
func DefaultAllowedOrigins() []string {
	return []string{
		"http://localhost",
		"https://localhost",
		"http://127.0.0.1",
		"https://127.0.0.1",
	}
}

// GetAllowedOrigins returns the configured allowed origins, or defaults if none configured.
func (c *GatewayConfig) GetAllowedOrigins() []string {
	if len(c.AllowedOrigins) > 0 {
		return c.AllowedOrigins
	}
	return DefaultAllowedOrigins()
}

// PubSubConfig configures the GCP Pub/Sub bridge for receiving external task callbacks.
type PubSubConfig struct {
	Enabled      bool   `json:"enabled"`
	ProjectID    string `json:"project_id,omitempty"`    // GCP project ID (or GOOGLE_CLOUD_PROJECT env)
	Topic        string `json:"topic,omitempty"`         // topic name (informational, for agent to reference)
	Subscription string `json:"subscription,omitempty"` // subscription name (required when enabled)
}

// TunnelConfig configures a reverse tunnel for exposing the gateway through NAT.
type TunnelConfig struct {
	Enabled     bool   `json:"enabled"`
	Provider    string `json:"provider,omitempty"`      // "cloudflare" (only option for now)
	NamedTunnel string `json:"named_tunnel,omitempty"` // empty = quick tunnel (random URL each restart)
}

// SessionConfig configures session storage.
type SessionConfig struct {
	Backend  string      `json:"backend"` // "file", "redis", "postgres", "memory"
	Redis    RedisConfig `json:"redis,omitempty"`
	Postgres PGConfig    `json:"postgres,omitempty"`
	AppName  string      `json:"app_name,omitempty"` // App name for trpc backends
	UserID   string      `json:"user_id,omitempty"`  // Default user ID for trpc backends
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
	Enabled          bool    `json:"enabled"`
	BotToken         string  `json:"bot_token"`
	AllowedUsers     []int64 `json:"allowed_users,omitempty"`
	AllowedChats     []int64 `json:"allowed_chats,omitempty"`
	RejectMessage    string  `json:"reject_message,omitempty"`
	UserRateLimit    int     `json:"user_rate_limit,omitempty"`
	UserRateWindow   int     `json:"user_rate_window,omitempty"`
	RateLimitMessage string  `json:"rate_limit_message,omitempty"`
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
			Model:     "anthropic/claude-haiku-4-5",
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

	// Ensure directory exists with secure permissions
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}

	// Secure: owner-only file (config may contain sensitive data)
	return os.WriteFile(configPath, data, 0600)
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

// MemoryDBPath returns the expanded path to the memory database.
func (c *Config) MemoryDBPath() string {
	if c.Memory.Search.DBPath != "" {
		return ExpandPath(c.Memory.Search.DBPath)
	}
	return ExpandPath("~/.kaggen/memory.db")
}

// ProactiveDBPath returns the expanded path to the proactive history database.
func (c *Config) ProactiveDBPath() string {
	if c.Proactive.HistoryDBPath != "" {
		return ExpandPath(c.Proactive.HistoryDBPath)
	}
	return ExpandPath("~/.kaggen/proactive.db")
}

// AuditDBPath returns the expanded path to the approval audit database.
func (c *Config) AuditDBPath() string {
	if c.Approval.AuditDBPath != "" {
		return ExpandPath(c.Approval.AuditDBPath)
	}
	return ExpandPath("~/.kaggen/audit.db")
}

// BacklogDBPath returns the expanded path to the backlog database.
func (c *Config) BacklogDBPath() string {
	return ExpandPath("~/.kaggen/backlog.db")
}

// AnthropicAPIKey returns the Anthropic API key from environment.
func AnthropicAPIKey() string {
	return os.Getenv("ANTHROPIC_API_KEY")
}

func GeminiAPIKey() string {
	return os.Getenv("GEMINI_API_KEY")
}

func ZaiAPIKey() string {
	return os.Getenv("ZAI_API_KEY")
}

// PubSubProjectID returns the GCP project ID from config, falling back to
// the GOOGLE_CLOUD_PROJECT environment variable.
func (c *Config) PubSubProjectID() string {
	if c.Gateway.PubSub.ProjectID != "" {
		return c.Gateway.PubSub.ProjectID
	}
	return os.Getenv("GOOGLE_CLOUD_PROJECT")
}

// ResolveSecret resolves a configuration value that may be a secret reference.
// Secret references have the format "secret:key-name".
// If the value is not a secret reference, it is returned unchanged.
// If the secret cannot be resolved, the original value is returned.
func ResolveSecret(value string) string {
	if !strings.HasPrefix(value, "secret:") {
		return value
	}

	key := strings.TrimPrefix(value, "secret:")
	if key == "" {
		return value
	}

	// Import secrets package dynamically to avoid circular imports
	// This is a simple resolution - actual implementation uses the secrets package
	resolved, err := resolveSecretFromStore(key)
	if err != nil {
		return value // Return original on error
	}
	return resolved
}

// secretResolver is a function type for resolving secrets.
// This allows the secrets package to register itself without circular imports.
type secretResolver func(key string) (string, error)

var (
	registeredSecretResolver secretResolver
	secretResolverMu         sync.RWMutex
)

// RegisterSecretResolver registers a secret resolution function.
// This is called by the secrets package during initialization.
func RegisterSecretResolver(resolver secretResolver) {
	secretResolverMu.Lock()
	defer secretResolverMu.Unlock()
	registeredSecretResolver = resolver
}

// resolveSecretFromStore resolves a secret using the registered resolver.
func resolveSecretFromStore(key string) (string, error) {
	secretResolverMu.RLock()
	resolver := registeredSecretResolver
	secretResolverMu.RUnlock()

	if resolver == nil {
		return "", nil // No resolver registered
	}
	return resolver(key)
}
