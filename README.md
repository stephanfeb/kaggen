# Kaggen

Kaggen is a personal AI assistant platform with multi-model support (Anthropic Claude, Google Gemini, ZAI GLM). It provides an interactive CLI agent, a WebSocket gateway, and a Telegram bot interface -- all with persistent conversation sessions and tool execution capabilities.

Named after the mantis deity of the San people, associated with creativity and trickster wisdom.

## Features

- **Interactive CLI agent** with session persistence
- **WebSocket gateway** for real-time multi-client communication
- **Telegram bot** with access control and rate limiting
- **Tool execution** -- read/write files, run shell commands
- **File-backed sessions** for conversation history across restarts
- **Bootstrap memory** -- customizable personality, identity, and instructions via Markdown files
- **Semantic memory search** -- vector similarity search over stored memories using sqlite-vec and Ollama embeddings
- **External task orchestration** -- launch external work (GCP instances, CI pipelines) and receive async results via Cloudflare Tunnel or GCP Pub/Sub

## Prerequisites

- Go 1.23+
- At least one LLM API key (see [Supported Models](#supported-models))
- [Ollama](https://ollama.com/) (optional, for memory search)

## Installation

```bash
git clone https://github.com/yourusername/kaggen.git
cd kaggen
go build -tags "fts5" -o kaggen ./cmd/kaggen
```

## Quick Start

```bash
# Set an API key (at least one required -- see Supported Models below)
export ANTHROPIC_API_KEY="sk-ant-..."

# Initialize the workspace (creates config and bootstrap files)
./kaggen init

# Start an interactive CLI session
./kaggen agent
```

## Commands

| Command | Description |
|---------|-------------|
| `kaggen init` | Initialize workspace with default config and bootstrap files |
| `kaggen agent` | Start an interactive CLI agent session |
| `kaggen gateway` | Start the WebSocket + Telegram gateway server |
| `kaggen status` | Show current configuration and workspace status |
| `kaggen token generate` | Generate a new authentication token |
| `kaggen token list` | List configured authentication tokens |
| `kaggen token revoke` | Revoke an authentication token |
| `kaggen secrets set` | Store a secret securely |
| `kaggen secrets get` | Retrieve a stored secret |
| `kaggen secrets list` | List stored secret keys |
| `kaggen secrets delete` | Delete a stored secret |
| `kaggen secrets import-env` | Import a secret from an environment variable |
| `kaggen security-audit` | Run security checks on the installation |

### Agent flags

```
-s, --session string   Session ID to use (default "main")
-v, --verbose          Enable verbose logging
```

## Configuration

Configuration lives at `~/.kaggen/config.json`. It is created with defaults by `kaggen init`.

```json
{
  "agent": {
    "model": "anthropic/claude-sonnet-4-20250514",
    "workspace": "~/.kaggen/workspace"
  },
  "gateway": {
    "bind": "127.0.0.1",
    "port": 18789
  },
  "session": {
    "backend": "file"
  },
  "channels": {
    "telegram": {
      "enabled": false
    }
  },
  "memory": {
    "search": {
      "enabled": false
    }
  }
}
```

### Supported Models

Kaggen supports three LLM providers. Set the corresponding environment variable and Kaggen will use that provider automatically. When multiple keys are set, priority is **ZAI > Gemini > Anthropic**.

| Provider | Env Variable | Config model prefix | Default model |
|----------|-------------|---------------------|---------------|
| [ZAI (GLM)](https://docs.z.ai/) | `ZAI_API_KEY` | `zai/` | `glm-4.7` |
| [Google Gemini](https://ai.google.dev/) | `GEMINI_API_KEY` | `gemini/` | `gemini-3-pro-preview` |
| [Anthropic Claude](https://console.anthropic.com/) | `ANTHROPIC_API_KEY` | `anthropic/` | `claude-sonnet-4-20250514` |

To select a specific model, set `agent.model` in `~/.kaggen/config.json`:

```json
{
  "agent": {
    "model": "zai/glm-4.7"
  }
}
```

The prefix determines which provider is used regardless of which API keys are set.

### Environment variables

| Variable | Description |
|----------|-------------|
| `ZAI_API_KEY` | ZAI (GLM) API key. Highest priority when multiple keys are set. |
| `GEMINI_API_KEY` | Google Gemini API key. |
| `ANTHROPIC_API_KEY` | Anthropic API key. |
| `TELEGRAM_BOT_TOKEN` | Telegram bot token (alternative to config file). |
| `KAGGEN_MASTER_KEY` | Master key for encrypted secrets storage (required on headless servers). |

## Telegram Bot Setup

### 1. Create a bot

1. Open Telegram and message [@BotFather](https://t.me/BotFather)
2. Send `/newbot` and follow the prompts to choose a name and username
3. Copy the bot token BotFather gives you

### 2. Configure Kaggen

Edit `~/.kaggen/config.json`:

```json
{
  "channels": {
    "telegram": {
      "enabled": true,
      "bot_token": "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11"
    }
  }
}
```

Or use an environment variable instead of putting the token in the config file:

```bash
export TELEGRAM_BOT_TOKEN="123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11"
```

When using the environment variable you still need `"enabled": true` in the config.

### 3. Access control (optional)

Restrict which Telegram users or chats can talk to the bot. When both lists are empty (the default), all users are allowed.

```json
{
  "channels": {
    "telegram": {
      "enabled": true,
      "bot_token": "...",
      "allowed_users": [123456789, 987654321],
      "allowed_chats": [-1001234567890],
      "reject_message": "Sorry, this bot is private."
    }
  }
}
```

To find your Telegram user ID, message [@userinfobot](https://t.me/userinfobot).

### 4. Rate limiting (optional)

Per-user rate limiting prevents abuse. Defaults to 10 messages per 60 seconds.

```json
{
  "channels": {
    "telegram": {
      "enabled": true,
      "bot_token": "...",
      "user_rate_limit": 10,
      "user_rate_window": 60,
      "rate_limit_message": "Please slow down! Max 10 messages per minute."
    }
  }
}
```

### 5. Start the gateway

```bash
./kaggen gateway
```

You should see:

```
Kaggen Gateway
==============
Bind: 127.0.0.1:18789
Session Backend: file
Telegram: enabled

WebSocket endpoint: ws://localhost:18789/ws
Health check: http://localhost:18789/health
```

Open your bot in Telegram and send a message. The bot will respond using the same agent and tools available in the CLI.

### 6. Group chats

By default, Telegram bots in groups only see messages starting with `/` or messages that @mention the bot. To let the bot see all messages:

1. Message [@BotFather](https://t.me/BotFather)
2. Send `/setprivacy`
3. Select your bot
4. Choose **Disable**

### Session routing

- **DMs:** Session ID is `tg-dm-{user_id}` -- each user gets their own persistent session
- **Groups:** Session ID is `tg-group-{chat_id}` -- all members share one session per group

Sessions are stored as JSONL files under `~/.kaggen/sessions/`.

## Memory Search

Kaggen supports semantic memory search backed by [sqlite-vec](https://github.com/asg017/sqlite-vec) for vector storage and [Ollama](https://ollama.com/) for local embeddings. When enabled, the agent can recall past conversations and stored knowledge using the `memory_search` tool, and persist new memories with the `memory_write` tool.

### Setup

1. Install and start Ollama:

```bash
# macOS
brew install ollama
ollama serve
```

2. Pull an embedding model:

```bash
ollama pull nomic-embed-text
```

3. Enable memory search in `~/.kaggen/config.json`:

```json
{
  "memory": {
    "search": {
      "enabled": true,
      "db_path": "~/.kaggen/memory.db"
    },
    "embedding": {
      "provider": "ollama",
      "model": "nomic-embed-text",
      "base_url": "http://localhost:11434"
    },
    "indexing": {
      "chunk_size": 400,
      "chunk_overlap": 80
    }
  }
}
```

All fields except `search.enabled` have sensible defaults and can be omitted.

### How it works

- On startup, Kaggen indexes all Markdown files in `workspace/memory/` and `workspace/MEMORY.md` by chunking them, generating embeddings via Ollama, and storing vectors in a local SQLite database.
- A background poller re-indexes changed files every 30 seconds.
- The `memory_search` tool performs hybrid search (vector similarity + FTS5 keyword search merged with Reciprocal Rank Fusion) and returns the top-K matching chunks.
- The `memory_write` tool writes to memory files and triggers immediate re-indexing.

### Build note

FTS5 full-text search requires the `fts5` build tag:

```bash
go build -tags "fts5" -o kaggen ./cmd/kaggen
```

Without this tag the build succeeds but FTS5 tables won't be created, and hybrid search falls back to vector-only results.

## Secrets Management

Kaggen provides secure storage for sensitive credentials like API keys, database passwords, and tokens. Secrets are stored using the OS keychain when available, with an encrypted file fallback for headless/server environments.

### Storage Backends

| Backend | Platform | Security | Use Case |
|---------|----------|----------|----------|
| **Keychain** | macOS Keychain, Linux Secret Service, Windows Credential Manager | OS-level encryption, protected by user login | Desktop/development |
| **Encrypted File** | All platforms | AES-256-GCM + Argon2 key derivation | Servers, containers, CI/CD |
| **Memory** | All platforms | None (not persistent) | Testing only |

Kaggen automatically selects the best available backend. On servers without a keychain service, set the `KAGGEN_MASTER_KEY` environment variable to enable encrypted file storage.

### CLI Commands

```bash
# Store a secret (prompts for value securely)
kaggen secrets set anthropic-key

# Store with inline value (for scripts)
kaggen secrets set telegram-token --value="123456:ABC..."

# Import from environment variable
kaggen secrets import-env ANTHROPIC_API_KEY --as=anthropic-key

# List stored secret keys (values are never displayed)
kaggen secrets list

# Retrieve a secret (for use in scripts)
export API_KEY=$(kaggen secrets get anthropic-key)

# Delete a secret
kaggen secrets delete old-key
```

### Using Secrets in Config

Reference stored secrets in `~/.kaggen/config.json` using the `secret:` prefix:

```json
{
  "channels": {
    "telegram": {
      "bot_token": "secret:telegram-token"
    }
  },
  "session": {
    "redis": {
      "password": "secret:redis-password"
    },
    "postgres": {
      "password": "secret:postgres-password"
    }
  }
}
```

When Kaggen loads the config, `secret:key-name` values are automatically resolved from the secrets store.

### Dashboard UI

The web dashboard at `http://localhost:18789/` includes a Settings panel where you can:

- View secrets storage status and backend type
- Add new secrets via the web interface
- See which secrets are configured (keys only, values are never exposed)
- Delete secrets

### Server/Container Setup

For headless environments without a keychain service:

```bash
# Generate a strong master key
export KAGGEN_MASTER_KEY=$(openssl rand -base64 32)

# Store the master key securely (e.g., in your secrets manager, K8s secret, etc.)
# Then start kaggen
./kaggen gateway
```

Secrets are stored encrypted at `~/.kaggen/secrets.enc`. The file uses:
- **AES-256-GCM** for authenticated encryption
- **Argon2id** for key derivation from the master key
- Unique salt and nonce per save operation

### Supported Credential Types

| Key Name | Description | Config Reference |
|----------|-------------|------------------|
| `anthropic-key` | Anthropic API key | Use env var instead |
| `gemini-key` | Google Gemini API key | Use env var instead |
| `telegram-token` | Telegram bot token | `secret:telegram-token` |
| `postgres-password` | PostgreSQL password | `secret:postgres-password` |
| `redis-password` | Redis password | `secret:redis-password` |
| `webhook-*` | Webhook secrets | `secret:webhook-github` |

> **Note:** LLM API keys (Anthropic, Gemini, ZAI) should be set via environment variables rather than the secrets store, as they're checked before the secrets system initializes.

## Security Hardening

Kaggen includes multiple security features for production deployments. This section covers authentication, command sandboxing, approval workflows, and security auditing.

### Token Authentication

Protect WebSocket and API access with token-based authentication. Tokens are hashed using Argon2-ID and validated with constant-time comparison to prevent timing attacks.

#### Enable Authentication

```json
{
  "security": {
    "auth": {
      "enabled": true,
      "token_file": "~/.kaggen/tokens.json"
    }
  }
}
```

#### Generate Tokens

**CLI:**
```bash
# Generate a token (displayed once, save it!)
kaggen token generate -n "My iPhone" -e 30d

# List configured tokens (metadata only, hashes never exposed)
kaggen token list

# Revoke a token
kaggen token revoke <token-id>
```

**Dashboard:** Navigate to Settings > Generate Access Token in the web UI.

#### Connect with a Token

```bash
# WebSocket query parameter
ws://localhost:18789/ws?token=kag_xxxxx

# HTTP Authorization header
curl -H "Authorization: Bearer kag_xxxxx" http://localhost:18789/api/overview
```

#### Token Security Details

- 32 bytes of cryptographic randomness, base64-encoded
- Stored as Argon2-ID hashes (Time=1, Memory=64MB, Threads=4)
- Unique 16-byte salt per token
- Token file permissions: 0600 (owner-only)
- Optional expiration with automatic rejection of expired tokens

---

### Command Sandbox

Block dangerous shell commands before execution. The sandbox uses regex pattern matching to reject destructive operations.

#### Enable the Sandbox

```json
{
  "security": {
    "command_sandbox": {
      "enabled": true,
      "blocked_patterns": []
    }
  }
}
```

#### Default Blocked Patterns

The sandbox blocks 25+ dangerous patterns by default:

| Category | Examples |
|----------|----------|
| **Destructive filesystem** | `rm -rf /`, `rm -rf ~`, `mkfs`, `dd if=/dev/zero of=/dev/sda` |
| **Fork bombs** | `:(){ :\|:& };:` |
| **Privilege escalation** | `sudo`, `su`, `chmod 777`, `chown root` |
| **Remote code execution** | `curl \| sh`, `wget \| sh`, `nc -e`, `bash -i` |
| **Credential access** | `cat ~/.ssh/`, `cat ~/.aws/credentials`, `cat /etc/shadow` |
| **System modification** | `> /etc/`, `systemctl stop`, `shutdown`, `reboot` |

#### Custom Blocked Patterns

Add additional regex patterns to block:

```json
{
  "security": {
    "command_sandbox": {
      "enabled": true,
      "blocked_patterns": [
        "docker\\s+run.*--privileged",
        "kubectl\\s+delete\\s+namespace"
      ]
    }
  }
}
```

---

### Guarded Skills & Approval System

Require human approval for dangerous operations before execution. The agent pauses and waits for approval via the dashboard or Telegram.

#### How It Works

1. Agent attempts to execute a guarded tool (e.g., Bash command)
2. Execution pauses, approval request sent to dashboard/Telegram
3. Human reviews the operation and approves or rejects
4. Agent resumes with approval or finds alternative approach

#### Approve via Dashboard

Navigate to the Approvals panel in the web dashboard to see pending requests with full context (tool name, arguments, description).

#### Auto-Approve Rules

Skip approval for safe operations:

```json
{
  "approval": {
    "auto_approve": [
      {"tool": "Read", "pattern": ""},
      {"tool": "Bash", "pattern": "^(ls|cat|grep|find)\\s"}
    ]
  }
}
```

| Field | Description |
|-------|-------------|
| `tool` | Tool name: `Bash`, `Read`, `Write`, `Edit` |
| `pattern` | Regex matched against operation description (empty = match all) |

#### Audit Database

All approval decisions are logged to `~/.kaggen/audit.db` (SQLite) with:
- Tool name, arguments, and human-readable description
- Session and user context
- Request and resolution timestamps
- Resolution status: `approved`, `rejected`, `timed_out`, `auto_approved`
- Who resolved the request

---

### Security Audit

Run automated security checks on your Kaggen installation:

```bash
# Check for issues
kaggen security-audit

# Auto-fix permission issues
kaggen security-audit --fix
```

#### Audit Checks

| Check | Severity | Description |
|-------|----------|-------------|
| **File permissions** | CRITICAL/HIGH | Detects world/group-readable sensitive files |
| **Gateway binding** | CRITICAL/MEDIUM | Warns if bound to 0.0.0.0 or non-localhost |
| **CORS origins** | CRITICAL/LOW | Detects wildcard or non-localhost origins |
| **Command sandbox** | MEDIUM | Warns if sandbox is disabled |
| **Plaintext credentials** | MEDIUM/HIGH | Detects passwords in config file |
| **PostgreSQL SSL** | HIGH | Warns if SSL disabled (credentials sent plaintext) |

#### Example Output

```
Kaggen Security Audit
=====================

[CRITICAL] file_permissions: World-readable file: config.json (mode 0644)
  → Fix: chmod 600 ~/.kaggen/config.json

[MEDIUM] command_sandbox: Command sandbox is disabled
  → Enable sandbox in config: security.command_sandbox.enabled = true

[HIGH] credentials: PostgreSQL SSL mode is 'disable' - credentials transmitted in plaintext
  → Set session.postgres.ssl_mode to 'require' or 'verify-full'

Summary: 1 critical, 1 high, 1 medium, 0 low
Fixed: 0 issues (run with --fix to auto-remediate)
```

---

### CORS Configuration

Control which origins can access the WebSocket and API endpoints.

#### Default (Localhost Only)

```json
{
  "gateway": {
    "allowed_origins": [
      "http://localhost",
      "https://localhost",
      "http://127.0.0.1",
      "https://127.0.0.1"
    ]
  }
}
```

#### Allow Additional Origins

```json
{
  "gateway": {
    "allowed_origins": [
      "http://localhost",
      "https://localhost",
      "https://my-app.example.com"
    ]
  }
}
```

> **Warning:** Never use `"*"` in production. The security audit flags wildcard origins as CRITICAL.

---

### Webhook HMAC Verification

Secure incoming webhooks with HMAC-SHA256 signature verification (compatible with GitHub webhooks).

```json
{
  "proactive": {
    "webhooks": [
      {
        "name": "github-deploy",
        "path": "/hooks/github",
        "secret": "your-webhook-secret",
        "prompt": "GitHub event received: {{.Payload}}",
        "channel": "telegram",
        "user_id": "default"
      }
    ]
  }
}
```

When `secret` is configured:
- Incoming requests must include `X-Hub-Signature-256` header
- Format: `sha256=<hex-encoded-hmac>`
- Requests with invalid or missing signatures return 403 Forbidden

---

### Security Configuration Reference

Complete security configuration block:

```json
{
  "security": {
    "auth": {
      "enabled": true,
      "token_file": "~/.kaggen/tokens.json"
    },
    "command_sandbox": {
      "enabled": true,
      "blocked_patterns": []
    }
  },
  "approval": {
    "audit_db_path": "~/.kaggen/audit.db",
    "auto_approve": [
      {"tool": "Read", "pattern": ""}
    ]
  },
  "gateway": {
    "bind": "127.0.0.1",
    "port": 18789,
    "allowed_origins": ["http://localhost", "https://localhost"]
  }
}
```

### Security Best Practices

1. **Always enable auth** when exposing the gateway beyond localhost
2. **Enable command sandbox** in production to block dangerous commands
3. **Run security-audit** periodically and fix issues promptly
4. **Use secrets store** for credentials instead of plaintext in config
5. **Bind to 127.0.0.1** unless remote access is explicitly required
6. **Configure CORS** with specific origins, never wildcards
7. **Set webhook secrets** for all inbound webhooks
8. **Review auto-approve rules** carefully - overly broad patterns reduce security

## External Task Orchestration

Kaggen can launch external work (GCP instances, CI pipelines, long-running jobs) and receive results back asynchronously. When external work completes, the result is routed back to the originating conversation and the agent picks up where it left off.

### How it works

1. The agent calls `external_task_register`, which returns a `task_id` and delivery details
2. The agent launches external work, passing the task ID along
3. The external system sends results back via **GCP Pub/Sub** or **direct HTTP callback**
4. Kaggen injects the result into the original session and the agent responds

You can use either delivery method alone, or both simultaneously.

---

### GCP Pub/Sub (recommended)

Messages are queued in Pub/Sub and survive Kaggen restarts (up to 7 days retention). This is the simplest option for most setups — no tunnels, no public URLs, works behind NAT.

#### Step 1: Enable the Pub/Sub API

```bash
gcloud services enable pubsub.googleapis.com --project=YOUR_PROJECT_ID
```

#### Step 2: Create a topic and subscription

```bash
gcloud pubsub topics create kaggen-callbacks \
  --project=YOUR_PROJECT_ID

gcloud pubsub subscriptions create kaggen-callbacks-sub \
  --topic=kaggen-callbacks \
  --project=YOUR_PROJECT_ID \
  --ack-deadline=60
```

#### Step 3: Ensure credentials

The service account or user running Kaggen needs the **Pub/Sub Admin** role (or at minimum `pubsub.subscriber` + `pubsub.viewer`). If using a service account:

```bash
gcloud projects add-iam-policy-binding YOUR_PROJECT_ID \
  --member="serviceAccount:YOUR_SA@YOUR_PROJECT_ID.iam.gserviceaccount.com" \
  --role="roles/pubsub.admin"
```

Or use Application Default Credentials:

```bash
gcloud auth application-default login
```

#### Step 4: Add to kaggen config

Add the `pubsub` block inside `gateway` in `~/.kaggen/config.json`:

```json
{
  "gateway": {
    "bind": "127.0.0.1",
    "port": 18789,
    "pubsub": {
      "enabled": true,
      "project_id": "YOUR_PROJECT_ID",
      "topic": "kaggen-callbacks",
      "subscription": "kaggen-callbacks-sub"
    }
  }
}
```

> `project_id` can also be set via the `GOOGLE_CLOUD_PROJECT` environment variable.

#### Step 5: Start the gateway

```bash
./kaggen gateway
```

The Pub/Sub bridge starts automatically. You should see `Pub/Sub: enabled` in the startup output.

#### Step 6: Verify

```bash
# Publish a test message
gcloud pubsub topics publish kaggen-callbacks \
  --project=YOUR_PROJECT_ID \
  --attribute=task_id=test-123 \
  --message='{"status": "success", "result": {"hello": "world"}}'

# Pull it back (to confirm the plumbing works before starting kaggen)
gcloud pubsub subscriptions pull kaggen-callbacks-sub \
  --project=YOUR_PROJECT_ID \
  --auto-ack --limit=1
```

#### How external systems send results

Messages must include a `task_id` — either as a Pub/Sub attribute (preferred) or in the JSON body:

```bash
# Via message attribute
gcloud pubsub topics publish kaggen-callbacks \
  --project=YOUR_PROJECT_ID \
  --attribute=task_id=abc-123 \
  --message='{"status": "success", "result": {"p50": 12, "p99": 45}}'

# Or in the JSON body
gcloud pubsub topics publish kaggen-callbacks \
  --project=YOUR_PROJECT_ID \
  --message='{"task_id": "abc-123", "status": "success", "result": {"p50": 12}}'
```

#### Standalone bridge (optional)

The bridge can also run as a separate process if you don't want it coupled to the gateway:

```bash
make build-bridge

./kaggen-pubsub-bridge \
  --project YOUR_PROJECT_ID \
  --subscription kaggen-callbacks-sub \
  --callback-url http://localhost:18789
```

---

### Cloudflare Tunnel (alternative)

Exposes Kaggen's callback endpoint to the internet via a reverse tunnel. External systems POST results directly to the tunnel URL. Useful when you need a public HTTP endpoint without Pub/Sub.

#### Quick tunnel (no account needed)

```json
{
  "gateway": {
    "tunnel": {
      "enabled": true,
      "provider": "cloudflare"
    }
  }
}
```

Cloudflare assigns a random URL on each restart (e.g. `https://abc-xyz.trycloudflare.com`). The agent gets this URL from `external_task_register` and passes it to external systems.

#### Named tunnel (stable URL)

Requires a domain on Cloudflare:

```bash
brew install cloudflared
cloudflared login          # authorize via browser
cloudflared tunnel create my-tunnel
```

```json
{
  "gateway": {
    "tunnel": {
      "enabled": true,
      "provider": "cloudflare",
      "named_tunnel": "my-tunnel"
    },
    "callback_base_url": "https://my-tunnel.example.com"
  }
}
```

#### How external systems send results

```bash
curl -X POST https://YOUR_TUNNEL_URL/callbacks/{task_id} \
  -H "Content-Type: application/json" \
  -d '{"status": "success", "result": {"key": "value"}}'
```

> **Note:** Callbacks are lost if the tunnel is down when they arrive. Pub/Sub doesn't have this problem.

---

### Callback protocol

Regardless of delivery method, result payloads follow the same format:

```json
{"status": "success", "result": {"any": "data"}}
```

```json
{"status": "error", "error": "description of what went wrong"}
```

Optional HMAC-SHA256 signature verification is supported via the `X-Callback-Signature` header. The secret is set per-task when calling `external_task_register`.

Task status can be polled at `GET /callbacks/{taskID}/status`.

## Workspace

The workspace at `~/.kaggen/workspace/` contains bootstrap Markdown files that shape the agent's personality and behavior:

| File | Purpose |
|------|---------|
| `SOUL.md` | Core values and boundaries |
| `IDENTITY.md` | Name, emoji, personality |
| `AGENTS.md` | Operating instructions and response style |
| `TOOLS.md` | Tool usage guidelines |
| `USER.md` | Your profile and preferences |
| `MEMORY.md` | Long-term memory across sessions |

Edit these files to customize the agent to your needs.

## Architecture

```
cmd/
  kaggen/                CLI entry point
  kaggen-pubsub-bridge/  Standalone Pub/Sub bridge sidecar
internal/
  agent/             Agent logic, async dispatch, in-flight task store, approval system
  auth/              Token authentication (Argon2-ID hashing)
  channel/           Channel interface + implementations
    channel.go         Router, Message, Response types
    websocket.go       WebSocket channel
    telegram.go        Telegram bot channel
  config/            Configuration loading
  gateway/           HTTP/WS gateway server, message handler, callback handler
  embedding/         Embedding interface + Ollama client
  memory/            File-based bootstrap memory, vector index, indexer
  model/anthropic/   Anthropic Claude adapter
  model/gemini/      Google Gemini adapter
  model/zai/         ZAI GLM adapter
  pubsub/            GCP Pub/Sub bridge
  secrets/           Secure credential storage (keychain, encrypted file)
  security/          Command sandbox and validation
  session/           File-backed session service
  tools/             Tool definitions (read, write, exec, memory, external tasks)
  tunnel/            Cloudflare Tunnel manager
```

## Development

```bash
# Build (include fts5 tag for full-text search support)
go build -tags "fts5" ./...

# Run tests
go test -tags "fts5" ./...

# Vet
go vet -tags "fts5" ./...
```

## License

Proprietary. Copyright (c) 2026 Stephan M. February. All rights reserved.
