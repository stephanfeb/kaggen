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
cmd/kaggen/          CLI entry point
internal/
  agent/             Agent logic and context assembly
  channel/           Channel interface + implementations
    channel.go         Router, Message, Response types
    websocket.go       WebSocket channel
    telegram.go        Telegram bot channel
  config/            Configuration loading
  gateway/           HTTP/WS gateway server + message handler
  embedding/         Embedding interface + Ollama client
  memory/            File-based bootstrap memory, vector index, indexer
  model/anthropic/   Anthropic Claude adapter
  model/gemini/      Google Gemini adapter
  model/zai/         ZAI GLM adapter
  session/           File-backed session service
  tools/             Tool definitions (read, write, exec, memory_search, memory_write)
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
