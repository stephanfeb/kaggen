# Kaggen

Kaggen is a personal AI assistant platform powered by Claude. It provides an interactive CLI agent, a WebSocket gateway, and a Telegram bot interface -- all with persistent conversation sessions and tool execution capabilities.

Named after the mantis deity of the San people, associated with creativity and trickster wisdom.

## Features

- **Interactive CLI agent** with session persistence
- **WebSocket gateway** for real-time multi-client communication
- **Telegram bot** with access control and rate limiting
- **Tool execution** -- read/write files, run shell commands
- **File-backed sessions** for conversation history across restarts
- **Bootstrap memory** -- customizable personality, identity, and instructions via Markdown files

## Prerequisites

- Go 1.23+
- An [Anthropic API key](https://console.anthropic.com/)

## Installation

```bash
git clone https://github.com/yourusername/kaggen.git
cd kaggen
go build -o kaggen ./cmd/kaggen
```

## Quick Start

```bash
# Set your API key
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
  }
}
```

### Environment variables

| Variable | Description |
|----------|-------------|
| `ANTHROPIC_API_KEY` | **Required.** Your Anthropic API key. |
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
  memory/            File-based bootstrap memory
  model/anthropic/   Anthropic API client adapter
  session/           File-backed session service
  tools/             Tool definitions (read, write, exec)
```

## Development

```bash
# Build
go build ./...

# Run tests
go test ./...

# Vet
go vet ./...
```

## License

Proprietary. Copyright (c) 2026 Stephan M. February. All rights reserved.
