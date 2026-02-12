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
	WebSearch WebSearchConfig `json:"web_search,omitempty"`
	Proactive ProactiveConfig `json:"proactive,omitempty"`
	Telemetry TelemetryConfig `json:"telemetry,omitempty"`
	STT       STTConfig       `json:"stt,omitempty"`
	Approval  ApprovalConfig  `json:"approval,omitempty"`
	Security  SecurityConfig  `json:"security,omitempty"`
	Reasoning  ReasoningConfig  `json:"reasoning,omitempty"`
	Creativity CreativityConfig `json:"creativity,omitempty"`
	P2P        P2PConfig        `json:"p2p,omitempty"`
	Trust      TrustConfig      `json:"trust,omitempty"`
	OAuth      OAuthConfig      `json:"oauth,omitempty"`
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

// WebSearchConfig configures web search for the researcher skill.
type WebSearchConfig struct {
	Provider   string `json:"provider,omitempty"`    // "searxng" | "brave" | "google"
	BaseURL    string `json:"base_url,omitempty"`    // SearXNG instance URL (e.g. "http://localhost:8888")
	APIKey     string `json:"api_key,omitempty"`     // API key for Brave or Google
	NumResults int    `json:"num_results,omitempty"` // Max results to return (default 5)
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

	// Context management - token-aware pruning to prevent context overflow
	MaxInputTokens     int     `json:"max_input_tokens,omitempty"`      // Override provider's default max input tokens (0 = use provider default)
	TokenSafetyMargin  float64 `json:"token_safety_margin,omitempty"`   // Safety margin as fraction of limit (0.1 = 10%, default 0.1)
	ToolOutputMaxChars int     `json:"tool_output_max_chars,omitempty"` // Max chars for tool outputs before truncation (default 8000)
	EnableContextPrune bool    `json:"enable_context_prune,omitempty"`  // Enable automatic context pruning (default true when max_input_tokens > 0)
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

// ReasoningConfig configures the tiered reasoning architecture.
// When enabled, complex tasks can be escalated to a deeper model (e.g., Opus)
// for multi-approach analysis and strategic planning.
type ReasoningConfig struct {
	Enabled              bool     `json:"enabled"`                              // Enable reasoning escalation
	Tier2Model           string   `json:"tier2_model,omitempty"`                // Model for deep reasoning (default: claude-opus-4-5-20251101)
	EscalationThreshold  float64  `json:"escalation_threshold,omitempty"`       // Confidence below this triggers escalation (0-1, default 0.5)
	MaxSubtasksTrigger   int      `json:"max_subtasks_trigger,omitempty"`       // Subtask count that triggers escalation (default 5)
	AutoEscalateKeywords []string `json:"auto_escalate_keywords,omitempty"`     // Keywords that trigger escalation
	MaxTokens            int      `json:"max_tokens,omitempty"`                 // Max tokens for Tier 2 calls (default 8192)
}

// CreativityConfig configures tools for creative problem-solving.
// Enables exploration of multiple approaches, analogy search, and solution synthesis.
type CreativityConfig struct {
	Enabled         bool    `json:"enabled"`                    // Enable creativity tools
	ExplorationTemp float64 `json:"exploration_temp,omitempty"` // Temperature for exploration (default 0.95)
	MaxAnalogies    int     `json:"max_analogies,omitempty"`    // Max analogies to return (default 5)
}

// P2PConfig configures libp2p connectivity for mobile clients.
type P2PConfig struct {
	Enabled        bool     `json:"enabled"`                   // Enable libp2p networking
	Port           int      `json:"port,omitempty"`            // Listen port (default 4001)
	IdentityPath   string   `json:"identity_path,omitempty"`   // Path to identity key (default ~/.kaggen/p2p/identity.key)
	Transports     []string `json:"transports,omitempty"`      // Transports to enable: "udx", "tcp" (default ["udx"])
	DHTMode        string   `json:"dht_mode,omitempty"`        // DHT mode: "server" or "client" (default "server")
	BootstrapPeers []string `json:"bootstrap_peers,omitempty"` // Bootstrap peer multiaddrs
	Topics         []string `json:"topics,omitempty"`          // GossipSub topics to join
	RelayEnabled   bool     `json:"relay_enabled,omitempty"`   // Enable circuit relay v2
}

// TrustConfig configures the trust-tier security system.
type TrustConfig struct {
	// Owner tier - full access including send_* tools
	OwnerPhones   []string `json:"owner_phones,omitempty"`   // Phone numbers with full access (e.g. ["+1234567890"])
	OwnerTelegram []int64  `json:"owner_telegram,omitempty"` // Telegram user IDs with full access

	// Third-party settings
	ThirdParty ThirdPartyConfig `json:"third_party"`
}

// ThirdPartyConfig configures handling of third-party (unknown sender) messages.
type ThirdPartyConfig struct {
	Enabled          bool   `json:"enabled"`                     // Allow third-party messages (default: false, reject unknown)
	UseLocalLLM      bool   `json:"use_local_llm"`               // Route to local Ollama instead of frontier model
	LocalLLMModel    string `json:"local_llm_model,omitempty"`   // Ollama model (e.g. "llama3.2:3b")
	MaxSessionLength int    `json:"max_session_length,omitempty"` // Max messages per session (0 = no limit)
	AllowRelay       bool   `json:"allow_relay"`                 // Allow relay requests to owner
	SystemPrompt     string `json:"system_prompt,omitempty"`     // Custom sandboxed system prompt
}

// OAuthConfig configures OAuth 2.0 providers for skill integrations.
type OAuthConfig struct {
	Providers    map[string]OAuthProvider `json:"providers,omitempty"`
	CallbackPath string                   `json:"callback_path,omitempty"` // default "/api/oauth/callback"
	TokenDBPath  string                   `json:"token_db_path,omitempty"` // default "~/.kaggen/oauth_tokens.db"
}

// OAuthProvider defines an OAuth 2.0 provider configuration.
type OAuthProvider struct {
	ClientID     string            `json:"client_id"`               // secret:key reference supported
	ClientSecret string            `json:"client_secret"`           // secret:key reference supported
	AuthURL      string            `json:"auth_url,omitempty"`      // auto-populated for known providers
	TokenURL     string            `json:"token_url,omitempty"`     // auto-populated for known providers
	Scopes       []string          `json:"scopes,omitempty"`        // OAuth scopes to request
	PKCE         bool              `json:"pkce,omitempty"`          // auto-set for known providers
	RedirectURI  string            `json:"redirect_uri,omitempty"`  // override callback URI (rare)
	AuthParams   map[string]string `json:"auth_params,omitempty"`   // extra params for auth URL (e.g., access_type=offline)
	SMTP         *EmailServer      `json:"smtp,omitempty"`          // SMTP server for sending emails
	IMAP         *EmailServer      `json:"imap,omitempty"`          // IMAP server for reading emails
}

// EmailServer configures SMTP or IMAP server connection details.
type EmailServer struct {
	Host     string `json:"host"`               // Server hostname (e.g., smtp.gmail.com)
	Port     int    `json:"port"`               // Server port (e.g., 587 for SMTP, 993 for IMAP)
	TLS      bool   `json:"tls,omitempty"`      // Use implicit TLS (typically port 465/993)
	StartTLS bool   `json:"starttls,omitempty"` // Use STARTTLS (typically port 587)
}

// KnownOAuthProvider contains the pre-configured settings for known providers.
type KnownOAuthProvider struct {
	AuthURL    string
	TokenURL   string
	PKCE       bool
	AuthParams map[string]string // Default auth params (e.g., access_type=offline)
	SMTP       *EmailServer      // Default SMTP server (if provider supports email)
	IMAP       *EmailServer      // Default IMAP server (if provider supports email)
}

// knownOAuthProviders contains default configurations for well-known OAuth providers.
var knownOAuthProviders = map[string]KnownOAuthProvider{
	"google": {
		AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL: "https://oauth2.googleapis.com/token",
		PKCE:     true,
		AuthParams: map[string]string{
			"access_type": "offline", // Required to get a refresh token
			"prompt":      "consent", // Force consent to ensure refresh token is returned
		},
		SMTP: &EmailServer{Host: "smtp.gmail.com", Port: 587, StartTLS: true},
		IMAP: &EmailServer{Host: "imap.gmail.com", Port: 993, TLS: true},
	},
	"github": {
		AuthURL:  "https://github.com/login/oauth/authorize",
		TokenURL: "https://github.com/login/oauth/access_token",
		PKCE:     false,
		// GitHub doesn't provide email servers
	},
	"microsoft": {
		AuthURL:  "https://login.microsoftonline.com/common/oauth2/v2.0/authorize",
		TokenURL: "https://login.microsoftonline.com/common/oauth2/v2.0/token",
		PKCE:     true,
		SMTP:     &EmailServer{Host: "smtp.office365.com", Port: 587, StartTLS: true},
		IMAP:     &EmailServer{Host: "outlook.office365.com", Port: 993, TLS: true},
	},
}

// GetOAuthProvider returns the resolved OAuth provider configuration.
// For known providers (google, github, microsoft), it fills in default auth/token URLs and email servers.
func (c *Config) GetOAuthProvider(name string) (OAuthProvider, bool) {
	provider, ok := c.OAuth.Providers[name]
	if !ok {
		return OAuthProvider{}, false
	}

	// Fill in defaults for known providers
	if known, isKnown := knownOAuthProviders[name]; isKnown {
		if provider.AuthURL == "" {
			provider.AuthURL = known.AuthURL
		}
		if provider.TokenURL == "" {
			provider.TokenURL = known.TokenURL
		}
		// Only override PKCE if not explicitly set (Go zero value is false)
		// For known providers that require PKCE, we always enable it
		if known.PKCE {
			provider.PKCE = true
		}
		// Fill in email server defaults if not explicitly configured
		if provider.SMTP == nil && known.SMTP != nil {
			provider.SMTP = known.SMTP
		}
		if provider.IMAP == nil && known.IMAP != nil {
			provider.IMAP = known.IMAP
		}
		// Merge auth params (known defaults + user overrides)
		if len(known.AuthParams) > 0 {
			if provider.AuthParams == nil {
				provider.AuthParams = make(map[string]string)
			}
			for k, v := range known.AuthParams {
				if _, exists := provider.AuthParams[k]; !exists {
					provider.AuthParams[k] = v
				}
			}
		}
	}

	return provider, true
}

// OAuthCallbackPath returns the OAuth callback path.
func (c *Config) OAuthCallbackPath() string {
	if c.OAuth.CallbackPath != "" {
		return c.OAuth.CallbackPath
	}
	return "/api/oauth/callback"
}

// OAuthTokenDBPath returns the expanded path to the OAuth token database.
func (c *Config) OAuthTokenDBPath() string {
	if c.OAuth.TokenDBPath != "" {
		return ExpandPath(c.OAuth.TokenDBPath)
	}
	return ExpandPath("~/.kaggen/oauth_tokens.db")
}

// TLSConfig configures TLS/SSL for secure connections.
type TLSConfig struct {
	Enabled  bool   `json:"enabled"`            // Enable TLS (wss:// and https://)
	CertFile string `json:"cert_file"`          // Path to PEM-encoded certificate file
	KeyFile  string `json:"key_file"`           // Path to PEM-encoded private key file
}

// GatewayConfig configures the gateway server.
type GatewayConfig struct {
	Bind            string       `json:"bind"`
	Port            int          `json:"port"`
	TLS             TLSConfig    `json:"tls,omitempty"`               // TLS configuration for wss:// support
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
	WhatsApp WhatsAppConfig `json:"whatsapp"`
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

// WhatsAppConfig configures the WhatsApp channel.
type WhatsAppConfig struct {
	Enabled          bool     `json:"enabled"`
	DeviceName       string   `json:"device_name,omitempty"`
	SessionDBPath    string   `json:"session_db_path,omitempty"`
	AllowedPhones    []string `json:"allowed_phones,omitempty"`
	AllowedGroups    []string `json:"allowed_groups,omitempty"`
	RejectMessage    string   `json:"reject_message,omitempty"`
	UserRateLimit    int      `json:"user_rate_limit,omitempty"`
	UserRateWindow   int      `json:"user_rate_window,omitempty"`
	RateLimitMessage string   `json:"rate_limit_message,omitempty"`
}

// WhatsAppSessionDBPath returns the session database path, with a default.
func (c *Config) WhatsAppSessionDBPath() string {
	if c.Channels.WhatsApp.SessionDBPath != "" {
		return ExpandPath(c.Channels.WhatsApp.SessionDBPath)
	}
	return ExpandPath("~/.kaggen/whatsapp.db")
}

// WhatsAppDeviceName returns the device name for WhatsApp linked devices list.
// Falls back to reading the bot name from IDENTITY.md, or "Kaggen" as default.
func (c *Config) WhatsAppDeviceName() string {
	if c.Channels.WhatsApp.DeviceName != "" {
		return c.Channels.WhatsApp.DeviceName
	}
	// Try to read from IDENTITY.md
	if name := c.readBotNameFromIdentity(); name != "" {
		return name + " Bot"
	}
	return "Kaggen Bot"
}

// readBotNameFromIdentity attempts to extract the bot name from IDENTITY.md.
func (c *Config) readBotNameFromIdentity() string {
	identityPath := filepath.Join(c.WorkspacePath(), "IDENTITY.md")
	data, err := os.ReadFile(identityPath)
	if err != nil {
		return ""
	}
	// Look for **Name:** pattern
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "**Name:**") {
			name := strings.TrimPrefix(line, "**Name:**")
			return strings.TrimSpace(name)
		}
	}
	return ""
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

// P2PIdentityPath returns the expanded path to the P2P identity key.
func (c *Config) P2PIdentityPath() string {
	if c.P2P.IdentityPath != "" {
		return ExpandPath(c.P2P.IdentityPath)
	}
	return ExpandPath("~/.kaggen/p2p/identity.key")
}

// ReasoningTier2Model returns the Tier 2 model for deep reasoning.
// If an explicit tier2_model is configured, it returns that.
// Otherwise, it auto-selects based on the coordinator model's family:
//   - Anthropic coordinator -> anthropic/claude-opus-4-5-20251101
//   - Gemini coordinator -> gemini/gemini-2.5-pro-preview-06-05
//   - ZAI coordinator -> zai/glm-4.7
func (c *Config) ReasoningTier2Model(coordinatorModel string) string {
	if c.Reasoning.Tier2Model != "" {
		return c.Reasoning.Tier2Model // explicit override
	}
	// Auto-select based on coordinator family
	// Import is done inline to avoid circular dependency
	if strings.HasPrefix(coordinatorModel, "gemini/") {
		return "gemini/gemini-2.5-pro-preview-06-05"
	}
	if strings.HasPrefix(coordinatorModel, "zai/") {
		return "zai/glm-4.7"
	}
	return "anthropic/claude-opus-4-5-20251101"
}

// ReasoningThreshold returns the escalation threshold or the default (0.5).
func (c *Config) ReasoningThreshold() float64 {
	if c.Reasoning.EscalationThreshold > 0 && c.Reasoning.EscalationThreshold <= 1.0 {
		return c.Reasoning.EscalationThreshold
	}
	return 0.5
}

// ReasoningMaxSubtasks returns the max subtasks trigger or the default (5).
func (c *Config) ReasoningMaxSubtasks() int {
	if c.Reasoning.MaxSubtasksTrigger > 0 {
		return c.Reasoning.MaxSubtasksTrigger
	}
	return 5
}

// ReasoningMaxTokens returns the max tokens for Tier 2 calls or the default (8192).
func (c *Config) ReasoningMaxTokens() int {
	if c.Reasoning.MaxTokens > 0 {
		return c.Reasoning.MaxTokens
	}
	return 8192
}

// DefaultAutoEscalateKeywords returns the default keywords that trigger escalation.
func DefaultAutoEscalateKeywords() []string {
	return []string{
		"design",
		"architect",
		"evaluate",
		"analyze",
		"compare",
		"trade-off",
		"tradeoff",
		"strategic",
		"thoroughly",
		"comprehensively",
	}
}

// ReasoningKeywords returns the configured escalation keywords or defaults.
func (c *Config) ReasoningKeywords() []string {
	if len(c.Reasoning.AutoEscalateKeywords) > 0 {
		return c.Reasoning.AutoEscalateKeywords
	}
	return DefaultAutoEscalateKeywords()
}

// ContextPruneEnabled returns whether automatic context pruning is enabled.
func (c *Config) ContextPruneEnabled() bool {
	// Enabled by default if max_input_tokens is set, otherwise respect explicit config
	if c.Agent.MaxInputTokens > 0 {
		return true
	}
	return c.Agent.EnableContextPrune
}

// ContextTokenSafetyMargin returns the safety margin as a fraction (0.0-1.0).
func (c *Config) ContextTokenSafetyMargin() float64 {
	if c.Agent.TokenSafetyMargin > 0 && c.Agent.TokenSafetyMargin < 1.0 {
		return c.Agent.TokenSafetyMargin
	}
	return 0.1 // default 10%
}

// ContextToolOutputMaxChars returns the max chars for tool output truncation.
func (c *Config) ContextToolOutputMaxChars() int {
	if c.Agent.ToolOutputMaxChars > 0 {
		return c.Agent.ToolOutputMaxChars
	}
	return 8000 // default
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
