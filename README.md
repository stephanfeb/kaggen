# Kaggen

Kaggen is a personal AI assistant platform with multi-model support (Anthropic Claude, Google Gemini, ZAI GLM). It provides an interactive CLI agent, a WebSocket gateway, and messaging channel integrations (Telegram, WhatsApp) -- all with persistent conversation sessions and tool execution capabilities.

Named after the mantis deity of the San people, associated with creativity and trickster wisdom.

## Features

- **Interactive CLI agent** with session persistence
- **WebSocket gateway** for real-time multi-client communication
- **Telegram bot** with access control and rate limiting
- **WhatsApp bot** with QR code pairing and multi-device support
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
| `kaggen eval` | Run evaluation tests for agent performance |
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
    },
    "whatsapp": {
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

### Tiered Reasoning

Kaggen supports tiered reasoning, where complex tasks can be escalated from the primary coordinator model (Tier 1) to a deeper reasoning model (Tier 2) for thorough multi-approach analysis.

#### Enable Tiered Reasoning

```json
{
  "reasoning": {
    "enabled": true
  }
}
```

When enabled, the agent gains access to the `reasoning_escalate` tool, which invokes a more capable model for architectural decisions, trade-off analysis, and complex planning.

#### Family-Specific Model Selection

By default, the Tier 2 model is automatically selected based on the coordinator's model family:

| Coordinator Family | Tier 2 Model (auto-selected) |
|-------------------|------------------------------|
| Anthropic (`anthropic/claude-*`) | `anthropic/claude-opus-4-5-20251101` |
| Google Gemini (`gemini/*`) | `gemini/gemini-2.5-pro-preview-06-05` |
| ZAI (`zai/*`) | `zai/glm-4.7` |

This ensures the deep thinking model uses the same provider as your coordinator, avoiding cross-provider API key requirements.

#### Override Tier 2 Model

To use a specific model regardless of the coordinator's family:

```json
{
  "reasoning": {
    "enabled": true,
    "tier2_model": "anthropic/claude-opus-4-5-20251101"
  }
}
```

This is useful when you want to use Anthropic's Opus for deep reasoning even when running Gemini as the coordinator (requires both API keys).

#### Full Reasoning Configuration

```json
{
  "reasoning": {
    "enabled": true,
    "tier2_model": "",
    "escalation_threshold": 0.5,
    "max_subtasks_trigger": 5,
    "auto_escalate_keywords": [
      "design", "architect", "evaluate", "analyze",
      "compare", "trade-off", "strategic"
    ],
    "max_tokens": 8192
  }
}
```

| Field | Default | Description |
|-------|---------|-------------|
| `enabled` | `false` | Enable the reasoning escalation system |
| `tier2_model` | (auto) | Override Tier 2 model (format: `provider/model-name`) |
| `escalation_threshold` | `0.5` | Confidence below this triggers escalation (0-1) |
| `max_subtasks_trigger` | `5` | Subtask count that triggers escalation |
| `auto_escalate_keywords` | (see above) | Keywords in task that auto-trigger escalation |
| `max_tokens` | `8192` | Max tokens for Tier 2 reasoning calls |

#### When Escalation Triggers

The agent automatically escalates to the Tier 2 model when:

1. **Keywords detected**: Task contains words like "design", "architect", "evaluate", "compare"
2. **High subtask count**: Task decomposition suggests >5 subtasks
3. **Low confidence**: Agent has low confidence in the best approach
4. **Explicit call**: Agent invokes `reasoning_escalate` tool directly

### Context Management

Kaggen includes automatic context window management to prevent token overflow errors when agents accumulate large conversation histories. This is especially important for long-running async tasks that may exceed provider token limits.

#### The Problem

Each LLM provider has different input token limits:

| Provider | Max Input Tokens |
|----------|------------------|
| Anthropic Claude | 200,000 |
| Google Gemini | 1,048,576 |
| ZAI GLM | 131,072 |

Without context management, long-running tasks can fail with errors like:
```
"The input token count exceeds the maximum number of tokens allowed"
```

#### How It Works

Context management uses a tiered pruning strategy:

| Threshold | Action | Impact |
|-----------|--------|--------|
| **60%** | Truncate tool outputs | Large tool results (file reads, bash output) are shortened |
| **75%** | Consolidate messages | Keep first 2 + last 8 messages, drop middle |
| **90%** | Emergency pruning | Aggressive reduction with task re-injection |

**Task Preservation**: The original task is always preserved and re-injected after emergency pruning, ensuring the agent never loses track of what it was asked to do.

#### Enable Context Management

```json
{
  "agent": {
    "max_input_tokens": 900000,
    "token_safety_margin": 0.1,
    "tool_output_max_chars": 8000,
    "enable_context_prune": true
  }
}
```

#### Configuration Reference

| Field | Default | Description |
|-------|---------|-------------|
| `max_input_tokens` | (provider default) | Override max input tokens (0 = use provider default) |
| `token_safety_margin` | `0.1` | Safety margin as fraction of limit (0.1 = 10%) |
| `tool_output_max_chars` | `8000` | Max characters for tool outputs before truncation |
| `enable_context_prune` | `true` when `max_input_tokens` is set | Enable automatic context pruning |

#### Provider Defaults

When `max_input_tokens` is not set, Kaggen uses conservative defaults:

```
Anthropic: 180,000 effective (200K × 0.9)
Gemini:    943,718 effective (1M × 0.9)
ZAI:       117,964 effective (128K × 0.9)
```

#### Monitoring

Context pruning events are logged with details:

```
level=INFO msg="context manager: level 1 pruning (tool output truncation)"
  estimated_tokens=650000 limit=943718 threshold=60%

level=WARN msg="context manager: level 3 emergency pruning"
  estimated_tokens=900000 limit=943718 threshold=90%
```

#### Escalation Response

The Tier 2 model returns structured analysis:

```json
{
  "analysis": "Deep analysis of the problem and constraints",
  "approaches": [
    {
      "name": "Approach A",
      "strategy": "How this approach works",
      "pros": ["advantage 1", "advantage 2"],
      "cons": ["disadvantage 1"],
      "skills_required": ["coder", "researcher"],
      "effort": "medium"
    }
  ],
  "selected_plan": "Approach A",
  "confidence": 0.85,
  "next_steps": ["Step 1", "Step 2", "Step 3"],
  "model_used": "anthropic/claude-opus-4-5-20251101"
}
```

### Strategic Deliberation

When tiered reasoning is enabled, the agent also gains access to the `plan_deliberate` tool for strategic decision-making before task decomposition. This creates an audit trail linking strategic choices to execution plans.

#### When to Use Deliberation

The coordinator uses `plan_deliberate` when:

1. **Multiple valid approaches exist** with different trade-offs
2. **Strategic or architectural decisions** are involved
3. **Uncertainty about which approach is best** for the given constraints
4. **Significant downstream impact** of the choice

#### Deliberation Workflow

```
1. plan_deliberate    →  Evaluate approaches, select recommendation
2. backlog_decompose  →  Create execution plan linked to deliberation
3. dispatch_task      →  Execute each subtask
```

The `deliberation_id` returned by `plan_deliberate` can be passed to `backlog_decompose` to link the execution plan to the strategic deliberation, creating an audit trail.

#### Deliberation Response

```json
{
  "deliberation_id": "550e8400-e29b-41d4-a716-446655440000",
  "approaches": [
    {
      "name": "Approach A",
      "strategy": "How this approach works",
      "pros": ["advantage 1", "advantage 2"],
      "cons": ["disadvantage 1"],
      "skills_required": ["coder", "researcher"],
      "effort": "medium"
    },
    {
      "name": "Approach B",
      "strategy": "Alternative strategy",
      "pros": ["different advantage"],
      "cons": ["different trade-off"],
      "skills_required": ["coder"],
      "effort": "low"
    }
  ],
  "selected": "Approach A",
  "rationale": "Why this approach is recommended given constraints",
  "risks": ["risk 1", "risk 2"],
  "mitigations": ["how to handle risk 1", "how to handle risk 2"],
  "model_used": "anthropic/claude-opus-4-5-20251101"
}
```

#### Deliberation Tool Parameters

| Parameter | Required | Description |
|-----------|----------|-------------|
| `task` | Yes | The task or goal to deliberate on |
| `constraints` | No | Constraints to consider (e.g., `["time", "quality", "cost"]`) |
| `exploration_budget` | No | Number of approaches to evaluate (default: 3, max: 5) |
| `must_consider` | No | Specific approaches that must be included in the analysis |
| `context` | No | Additional context about the problem domain |

#### Linking to Backlog

When calling `backlog_decompose` after deliberation, include the `deliberation_id`:

```json
{
  "title": "Implement user authentication",
  "description": "Add auth system based on JWT approach",
  "deliberation_id": "550e8400-e29b-41d4-a716-446655440000",
  "subtasks": [
    {"title": "Create JWT utility module"},
    {"title": "Add login endpoint"},
    {"title": "Add middleware for protected routes"}
  ]
}
```

This creates a parent backlog item linked to the deliberation record, enabling full traceability from strategic decision to execution.

### Creativity Tools

When enabled, the agent gains access to tools for creative problem-solving.

#### Enable Creativity Tools

```json
{
  "creativity": {
    "enabled": true,
    "exploration_temp": 0.95,
    "max_analogies": 5
  }
}
```

#### Available Tools

| Tool | Purpose |
|------|---------|
| `explore_approaches` | Generate multiple creative approaches using elevated temperature |
| `find_analogies` | Search memory for similar past problems with adaptation suggestions |
| `synthesize_solution` | Combine partial solutions into coherent integrated plan |

**Note:** Requires `reasoning.enabled: true` (uses same Tier-2 model). The `find_analogies` tool also requires `memory.search.enabled: true`.

#### Tool Usage

**`explore_approaches`** - Use when brainstorming or stuck on a problem:
```json
{
  "task": "Design a caching strategy for API responses",
  "num_approaches": 4,
  "constraints": ["low latency", "memory efficient"],
  "avoid_patterns": ["in-memory only"]
}
```

**`find_analogies`** - Use to learn from past work:
```json
{
  "problem": "Rate limiting for API endpoints",
  "domain": "performance",
  "keywords": ["throttle", "quota"]
}
```

**`synthesize_solution`** - Use to combine partial solutions:
```json
{
  "goal": "Complete authentication system",
  "partial_solutions": [
    {"description": "JWT token generation", "approach": "RS256 signing", "status": "complete"},
    {"description": "Session management", "approach": "Redis store", "status": "partial", "gaps": ["expiry handling"]}
  ],
  "priority": "completeness"
}
```

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
WhatsApp: enabled

WebSocket endpoint: ws://localhost:18789/ws
Health check: http://localhost:18789/health
```

Open your bot in Telegram or WhatsApp and send a message. The bot will respond using the same agent and tools available in the CLI.

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

## WhatsApp Bot Setup

Kaggen supports WhatsApp as a messaging channel using the [whatsmeow](https://github.com/tulir/whatsmeow) library, which implements WhatsApp's multi-device protocol natively in Go.

### Important: Understanding WhatsApp Linking

> **Unlike Telegram**, WhatsApp does not have a separate "bot" identity. When you link Kaggen to WhatsApp, it becomes a **linked device on your existing WhatsApp account** — similar to WhatsApp Web or Desktop.

**This means:**

- The bot **shares your WhatsApp identity** (your phone number)
- The bot **receives all your incoming messages** (not just messages meant for it)
- Users cannot "message the bot directly" — they message *you*, and the bot responds
- You cannot message yourself to test it (you'd need a second phone number)

**Privacy implications:**

- All incoming messages pass through Kaggen's code
- Messages from allowed senders are sent to your configured LLM provider
- Use `allowed_phones` to restrict which conversations the bot processes

### Options for a Dedicated Bot Identity

If you want a WhatsApp bot with its own phone number that users can message directly:

| Option | Setup | Cost | Notes |
|--------|-------|------|-------|
| **Dedicated SIM** | Get a second phone number, link Kaggen to that account | ~$5-10/mo | Easiest solution. Use a prepaid SIM or eSIM. |
| **Google Voice** | Create a Google Voice number (US only), register WhatsApp on it | Free | Requires US Google account. May need a phone for initial verification. |
| **WhatsApp Business API** | Apply for Meta Business verification, use Cloud API | Varies | Official solution for businesses. More complex setup. |

**Recommended approach for a personal assistant bot:**

1. Get a cheap prepaid SIM or eSIM dedicated to the bot
2. Register WhatsApp on that number using a spare phone
3. Link Kaggen as a device on the bot's account (not your personal account)
4. Share the bot's phone number with users who should have access

This keeps your personal WhatsApp private while giving the bot its own identity.

---

### 1. Enable WhatsApp in config

Edit `~/.kaggen/config.json`:

```json
{
  "channels": {
    "whatsapp": {
      "enabled": true
    }
  }
}
```

### 2. Link a WhatsApp account

Start the gateway:

```bash
./kaggen gateway
```

On first startup, a QR code will be displayed in the terminal. Scan it with the WhatsApp account you want the bot to use:

1. Open WhatsApp on the phone with the bot's account (or your personal account if that's your preference)
2. Go to **Settings > Linked Devices > Link a Device**
3. Scan the QR code displayed in the terminal

After successful pairing, you'll see:

```
whatsapp connected (jid: 1234567890@s.whatsapp.net)
```

The session is stored in `~/.kaggen/whatsapp.db` (SQLite). On subsequent starts, the gateway reconnects automatically without requiring a new QR scan.

> **Tip:** If using a dedicated bot number, you only need the phone for initial setup and occasional re-pairing. The bot runs independently once linked.

### 3. Access control (recommended)

Restrict which phone numbers or groups the bot responds to. **This is especially important if linking your personal WhatsApp account**, as it prevents the bot from processing private conversations.

```json
{
  "channels": {
    "whatsapp": {
      "enabled": true,
      "allowed_phones": ["+1234567890", "+0987654321"],
      "allowed_groups": ["120363012345678901@g.us"],
      "reject_message": "Sorry, this bot is private."
    }
  }
}
```

| Scenario | Configuration |
|----------|---------------|
| Dedicated bot number | `allowed_phones: []` (allow all) or list specific users |
| Personal account | `allowed_phones: ["+friend1", "+friend2"]` (only listed numbers get bot responses) |
| Specific groups only | `allowed_phones: []`, `allowed_groups: ["group-jid@g.us"]` |

When both lists are empty (the default), all users are allowed. Phone numbers should include the country code.

Messages from non-allowed senders still arrive (the bot sees them) but receive only the `reject_message` — they are **not** sent to the LLM.

### 4. Rate limiting (optional)

Per-user rate limiting prevents abuse. Defaults to 10 messages per 60 seconds.

```json
{
  "channels": {
    "whatsapp": {
      "enabled": true,
      "user_rate_limit": 10,
      "user_rate_window": 60,
      "rate_limit_message": "Please slow down! Max 10 messages per minute."
    }
  }
}
```

### 5. Custom session database path (optional)

By default, the WhatsApp session is stored at `~/.kaggen/whatsapp.db`. You can customize this:

```json
{
  "channels": {
    "whatsapp": {
      "enabled": true,
      "session_db_path": "/path/to/custom/whatsapp.db"
    }
  }
}
```

### Session routing

- **DMs:** Session ID is `wa-dm-{phone}` -- each user gets their own persistent session
- **Groups:** Session ID is `wa-group-{group_jid}` -- all members share one session per group

### Bot commands

The WhatsApp bot supports the same commands as Telegram:

| Command | Description |
|---------|-------------|
| `/clear` | Delete the current session and start fresh |
| `/compact` | Summarize and truncate the session history |

### Re-pairing

If your WhatsApp session is invalidated (e.g., you unlinked the device from your phone), delete the session database and restart:

```bash
rm ~/.kaggen/whatsapp.db
./kaggen gateway
```

A new QR code will be displayed for pairing.

### Device name

By default, the bot appears as "Kaggen Bot" in WhatsApp's **Linked Devices** list. You can customize this:

```json
{
  "channels": {
    "whatsapp": {
      "enabled": true,
      "device_name": "Amelia Bot"
    }
  }
}
```

If `device_name` is not set, the name is read from your `IDENTITY.md` file (e.g., "Amelia" becomes "Amelia Bot"). If no identity is configured, it defaults to "Kaggen Bot".

> **Note:** WhatsApp's multi-device protocol does not support setting a custom profile picture programmatically. To change the bot's profile picture, use the WhatsApp mobile app on the linked phone.

### Configuration reference

```json
{
  "channels": {
    "whatsapp": {
      "enabled": true,
      "device_name": "Kaggen Bot",
      "session_db_path": "~/.kaggen/whatsapp.db",
      "allowed_phones": [],
      "allowed_groups": [],
      "reject_message": "Sorry, you are not authorized to use this bot.",
      "user_rate_limit": 10,
      "user_rate_window": 60,
      "rate_limit_message": "You're sending messages too quickly. Please wait a moment."
    }
  }
}
```

| Field | Default | Description |
|-------|---------|-------------|
| `enabled` | `false` | Enable the WhatsApp channel |
| `device_name` | (from IDENTITY.md or "Kaggen Bot") | Name shown in WhatsApp's Linked Devices list |
| `session_db_path` | `~/.kaggen/whatsapp.db` | SQLite database for session persistence |
| `allowed_phones` | `[]` | Phone numbers allowed to use the bot (empty = all allowed) |
| `allowed_groups` | `[]` | Group JIDs allowed to use the bot (empty = all allowed) |
| `reject_message` | (default) | Message sent to unauthorized users |
| `user_rate_limit` | `10` | Max messages per user per window |
| `user_rate_window` | `60` | Rate limit window in seconds |
| `rate_limit_message` | (default) | Message sent when rate limited |

## Trust Tiers

Kaggen implements a trust-tier security system that classifies message senders and routes them through appropriate handlers. This prevents API cost attacks from unknown senders and enables proactive outbound messaging for trusted users.

### Trust Levels

| Tier | Description | Capabilities |
|------|-------------|--------------|
| **Owner** | Bot owner (configured phone/Telegram IDs) | Full access: all tools, shell, file system, proactive messaging |
| **Authorized** | Allowlisted users (existing allowlist) | Standard access: conversation, tools per configuration |
| **Third-party** | Unknown senders (not in any allowlist) | Sandboxed: conversation only, can request message relay to owner |

### Configuration

Add trust configuration to `~/.kaggen/config.json`:

```json
{
  "trust": {
    "owner_phones": ["+1234567890"],
    "owner_telegram": [123456789],
    "third_party": {
      "enabled": true,
      "use_local_llm": true,
      "local_llm_model": "llama3.2:3b",
      "max_session_length": 20,
      "allow_relay": true,
      "system_prompt": ""
    }
  }
}
```

#### Trust Configuration Reference

| Field | Default | Description |
|-------|---------|-------------|
| `owner_phones` | `[]` | Phone numbers with full owner access |
| `owner_telegram` | `[]` | Telegram user IDs with full owner access |
| `third_party.enabled` | `false` | Allow messages from unknown senders |
| `third_party.use_local_llm` | `false` | Route third-party to local Ollama instead of frontier model |
| `third_party.local_llm_model` | `"llama3.2:3b"` | Ollama model for third-party conversations |
| `third_party.max_session_length` | `0` | Max messages per third-party session (0 = unlimited) |
| `third_party.allow_relay` | `false` | Allow third-party to request message relay to owner |
| `third_party.system_prompt` | (default) | Custom system prompt for sandboxed conversations |

### How Trust Classification Works

When a message arrives:

1. **Owner check**: If the sender's phone or Telegram ID is in `owner_phones` or `owner_telegram`, they get full access
2. **Allowlist check**: If the sender is in the channel's allowlist (`allowed_users`, `allowed_phones`), they're Authorized
3. **Third-party**: All other senders are treated as Third-party

```
Incoming Message
      │
      ▼
┌─────────────────┐
│ Owner phones/   │──Yes──▶ TrustTierOwner (full access)
│ Telegram IDs?   │
└────────┬────────┘
         │ No
         ▼
┌─────────────────┐
│ In channel      │──Yes──▶ TrustTierAuthorized (standard access)
│ allowlist?      │
└────────┬────────┘
         │ No
         ▼
   TrustTierThirdParty (sandboxed)
```

### Send Tools (Owner Only)

Owners have access to proactive messaging tools:

#### send_telegram

Send messages to any Telegram chat:

```json
{
  "tool": "send_telegram",
  "input": {
    "chat_id": 123456789,
    "message": "Hello from Kaggen!"
  }
}
```

#### send_whatsapp

Send messages to any WhatsApp number:

```json
{
  "tool": "send_whatsapp",
  "input": {
    "phone": "+1234567890",
    "message": "Hello from Kaggen!"
  }
}
```

> **Note:** These tools are only available to Owner-tier users. Attempts by Authorized or Third-party users will be rejected.

### Third-Party Sandbox

Third-party users interact with a sandboxed agent that has limited capabilities:

#### What Third-Party Users Can Do

- Have friendly conversations
- Ask general knowledge questions
- Request message relay to the owner

#### What Third-Party Users Cannot Do

- Access files, tools, or system commands
- Send messages to other people
- Access private or personal information
- Perform any actions that modify systems

#### Default Sandbox System Prompt

```
You are a helpful assistant in limited mode. You can:
- Have friendly conversations
- Answer general knowledge questions
- Take messages for the owner (say "Please tell the owner..." or "Message for owner: ...")

You cannot:
- Access files, tools, or system commands
- Send messages to other people
- Access private or personal information
- Perform any actions that modify systems

If someone asks you to do something you cannot do, politely explain your limitations and suggest they contact the owner directly.

Keep responses concise and helpful.
```

### Relay Requests

When `allow_relay` is enabled, third-party users can request that messages be relayed to the owner:

**Example phrases that trigger relay:**

- "Please tell the owner I need help with my account"
- "Message for owner: I have a question about pricing"
- "Can you ask the owner about the project deadline?"
- "Notify the owner that the payment was received"

When a relay request is detected:

1. The message is extracted and stored in a relay queue
2. The owner is notified via their preferred channel (if configured)
3. The third-party receives confirmation: "I've noted your message for the owner. They will be notified and may reach out to you."

### Local LLM for Third-Party (Cost Protection)

To prevent API cost attacks where unknown senders run up expensive frontier model bills, you can route third-party conversations through a local LLM (Ollama):

```json
{
  "trust": {
    "third_party": {
      "enabled": true,
      "use_local_llm": true,
      "local_llm_model": "llama3.2:3b"
    }
  }
}
```

#### Requirements

1. Install [Ollama](https://ollama.com/):
   ```bash
   brew install ollama  # macOS
   ollama serve
   ```

2. Pull a model:
   ```bash
   ollama pull llama3.2:3b
   ```

#### Fallback Behavior

If Ollama is unavailable when a third-party message arrives:

1. The system checks `http://localhost:11434/api/tags`
2. If unavailable, falls back to frontier model with strict rate limiting
3. Third-party usage is logged separately for cost tracking

### Session Limits

Limit how many messages third-party users can send per session:

```json
{
  "trust": {
    "third_party": {
      "max_session_length": 20
    }
  }
}
```

When the limit is exceeded, the user receives: "I'm sorry, but we've reached the message limit for this conversation. Please contact the owner directly for further assistance."

### Example Configuration

Full trust-tier configuration with all options:

```json
{
  "trust": {
    "owner_phones": ["+1234567890", "+0987654321"],
    "owner_telegram": [123456789],
    "third_party": {
      "enabled": true,
      "use_local_llm": true,
      "local_llm_model": "llama3.2:3b",
      "max_session_length": 20,
      "allow_relay": true,
      "system_prompt": "You are a helpful assistant. Keep responses brief."
    }
  },
  "channels": {
    "telegram": {
      "enabled": true,
      "bot_token": "...",
      "allowed_users": [111111111, 222222222]
    },
    "whatsapp": {
      "enabled": true,
      "allowed_phones": ["+1111111111"]
    }
  }
}
```

In this example:

- `+1234567890` and `+0987654321` (phone) and Telegram user `123456789` are **Owners**
- Telegram users `111111111`, `222222222` and WhatsApp phone `+1111111111` are **Authorized**
- All other senders are **Third-party** and will be routed to the local LLM

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

## Web Search

Kaggen supports web search for the `researcher` skill, enabling autonomous research and documentation lookup. When configured, the agent can search the web to discover documentation, find solutions, and gather information needed for skill acquisition.

### Supported Providers

| Provider | Type | Notes |
|----------|------|-------|
| [SearXNG](https://docs.searxng.org/) | Self-hosted | Privacy-focused, aggregates multiple search engines |
| [Brave Search](https://brave.com/search/api/) | Commercial | Free tier available (rate-limited) |
| [Google Custom Search](https://developers.google.com/custom-search/) | Commercial | Requires API key + Custom Search Engine ID |

### Configuration

Add to `~/.kaggen/config.json`:

```json
{
  "web_search": {
    "provider": "searxng",
    "base_url": "http://localhost:8888",
    "num_results": 5
  }
}
```

#### SearXNG (recommended)

```json
{
  "web_search": {
    "provider": "searxng",
    "base_url": "http://localhost:8888"
  }
}
```

Run a local SearXNG instance:

```bash
docker run -d -p 8888:8080 searxng/searxng
```

#### Brave Search

```json
{
  "web_search": {
    "provider": "brave",
    "api_key": "your-brave-api-key"
  }
}
```

Get an API key at [brave.com/search/api](https://brave.com/search/api/).

#### Google Custom Search

```json
{
  "web_search": {
    "provider": "google",
    "api_key": "your-api-key:your-cx-id"
  }
}
```

The `api_key` must be in the format `API_KEY:CX_ID` where CX_ID is your Custom Search Engine ID.

### Autonomous Skill Acquisition

When web search is enabled, the coordinator can automatically:

1. Detect when no existing skill can handle a task
2. Dispatch the `researcher` skill to gather documentation
3. Use `skill-builder` to create a new skill based on research
4. Hot-reload the new skill with `reload_skills`
5. Retry the original task

This enables Kaggen to extend its own capabilities without manual intervention.

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

### TLS/WSS Configuration

Enable TLS to secure WebSocket (wss://) and HTTP (https://) connections. Required for production deployments and mobile app connectivity.

#### Enable TLS

```json
{
  "gateway": {
    "bind": "0.0.0.0",
    "port": 18789,
    "tls": {
      "enabled": true,
      "cert_file": "/path/to/cert.pem",
      "key_file": "/path/to/key.pem"
    }
  }
}
```

When TLS is enabled:
- WebSocket endpoint: `wss://your-ip:18789/ws`
- Dashboard: `https://your-ip:18789/`
- QR codes in the dashboard use `wss://` URLs

#### Obtaining Certificates

**Let's Encrypt (free, requires domain):**

```bash
# Install certbot
brew install certbot  # macOS
sudo apt install certbot  # Ubuntu

# Get certificate (requires port 80 accessible)
sudo certbot certonly --standalone -d yourdomain.com

# Certificates are saved to:
# /etc/letsencrypt/live/yourdomain.com/fullchain.pem
# /etc/letsencrypt/live/yourdomain.com/privkey.pem
```

**Self-signed (for development/local network):**

```bash
# Generate a self-signed certificate valid for 365 days
openssl req -x509 -newkey rsa:4096 \
  -keyout key.pem -out cert.pem \
  -days 365 -nodes \
  -subj "/CN=kaggen-local"

# For local network access, include IP SANs:
openssl req -x509 -newkey rsa:4096 \
  -keyout key.pem -out cert.pem \
  -days 365 -nodes \
  -subj "/CN=kaggen-local" \
  -addext "subjectAltName=IP:192.168.1.100,IP:127.0.0.1"
```

> **Note:** Mobile apps connecting to self-signed certificates will need to trust the certificate. On iOS, install the cert via AirDrop or email and enable full trust in Settings > General > About > Certificate Trust Settings.

#### TLS with Reverse Proxy

Alternatively, terminate TLS at a reverse proxy (nginx, Caddy, Traefik) and keep Kaggen on plain HTTP internally:

```nginx
# nginx example
server {
    listen 443 ssl;
    server_name kaggen.example.com;

    ssl_certificate /etc/letsencrypt/live/kaggen.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/kaggen.example.com/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:18789;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
    }
}
```

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

1. **Enable TLS** for any network-accessible deployment (required for mobile apps)
2. **Always enable auth** when exposing the gateway beyond localhost
3. **Enable command sandbox** in production to block dangerous commands
4. **Run security-audit** periodically and fix issues promptly
5. **Use secrets store** for credentials instead of plaintext in config
6. **Bind to 127.0.0.1** unless remote access is explicitly required
7. **Configure CORS** with specific origins, never wildcards
8. **Set webhook secrets** for all inbound webhooks
9. **Review auto-approve rules** carefully - overly broad patterns reduce security

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

## P2P Networking (libp2p)

Kaggen supports peer-to-peer connectivity via [libp2p](https://libp2p.io/) for mobile client connections. When enabled, the gateway runs a libp2p node with Kademlia DHT for peer discovery and GossipSub for pub/sub messaging.

### Identity

On first startup, Kaggen generates an Ed25519 keypair and stores it at `~/.kaggen/p2p/identity.key`. This ensures the PeerID remains stable across restarts. The key file has `0600` permissions (owner-only).

### Configuration

Add to `~/.kaggen/config.json`:

```json
{
  "p2p": {
    "enabled": true,
    "port": 4001,
    "identity_path": "~/.kaggen/p2p/identity.key",
    "transports": ["udx", "tcp"],
    "dht_mode": "server",
    "topics": ["kaggen/notifications", "kaggen/presence"],
    "relay_enabled": true
  }
}
```

### Configuration Reference

| Field | Default | Description |
|-------|---------|-------------|
| `enabled` | `false` | Enable libp2p networking |
| `port` | `4001` | Listen port for P2P connections |
| `identity_path` | `~/.kaggen/p2p/identity.key` | Path to Ed25519 private key |
| `transports` | `["udx"]` | Transport protocols: `"udx"`, `"tcp"` |
| `dht_mode` | `"server"` | DHT mode: `"server"` or `"client"` |
| `bootstrap_peers` | `[]` | Multiaddrs of bootstrap peers to connect on startup |
| `topics` | `[]` | GossipSub topics to join automatically |
| `relay_enabled` | `false` | Enable circuit relay v2 for NAT traversal |

### Transports

| Transport | Protocol | Use Case |
|-----------|----------|----------|
| `udx` | UDP-based | Primary transport for mobile clients, better NAT traversal |
| `tcp` | TCP | Fallback for server-to-server, debugging |

### Startup Output

When P2P is enabled, the gateway prints the PeerID and listen addresses:

```
Kaggen Gateway
==============
Bind: 127.0.0.1:18789
P2P: enabled
PeerID: 12D3KooWxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
P2P Listen: /ip4/0.0.0.0/tcp/4001/p2p/12D3KooWxxxxxx
P2P Listen: /ip4/0.0.0.0/udp/4001/udx/p2p/12D3KooWxxxxxx
```

### Dashboard

The PeerID and multiaddrs are available via the `/api/settings` endpoint and displayed in the Settings panel of the web dashboard.

### Connecting Mobile Clients

Mobile clients using [dart-libp2p](https://github.com/example/dart-libp2p) can connect using the gateway's multiaddr:

```
/ip4/<gateway-ip>/tcp/4001/p2p/<peer-id>
```

Or via UDX transport:

```
/ip4/<gateway-ip>/udp/4001/udx/p2p/<peer-id>
```

## Agent Evaluation

Kaggen includes a built-in evaluation framework for measuring agent performance. The system supports two evaluation modes:

| Mode | Flag | Purpose |
|------|------|---------|
| **Default (V1)** | (none) | Basic tool calling tests for development |
| **Coordinator (V2)** | `--coordinator` | Full production system tests — coordinator + skills |

The **Coordinator mode** tests what actually matters: whether the coordinator selects the right skill, asks for clarification when instructions are ambiguous, and delegates appropriately.

### Quick Start

```bash
# Basic tool calling tests (V1)
kaggen eval -s testdata/eval

# Coordinator behavior tests (V2) — tests the full production system
kaggen eval -s testdata/eval/coordinator --coordinator --skills testdata/eval/skills

# Run with specific model
kaggen eval -s testdata/eval/coordinator --coordinator --model anthropic/claude-sonnet-4-20250514

# Run specific category
kaggen eval -s testdata/eval/coordinator --coordinator --category skill_selection

# Run specific test cases
kaggen eval -s testdata/eval/coordinator --coordinator --case skill-001 --case clarify-001

# Output results to JSON
kaggen eval -s testdata/eval/coordinator --coordinator -o results.json
```

### Test Case Format

#### Coordinator Tests (V2)

Coordinator tests verify skill selection, clarification behavior, and delegation patterns. Tests support both **single-turn** and **multi-turn** formats.

**Single-turn tests** (simple):

```yaml
# testdata/eval/coordinator/skill_selection.yaml
- id: "skill-001"
  name: "Select calculator for math"
  category: skill_selection
  user_message: "What is 15 multiplied by 23?"
  assert:
    - type: skill-selected
      skill: calculator
      required: true
    - type: contains
      value: "345"
```

**Multi-turn tests** (conversational flows):

```yaml
# testdata/eval/coordinator/context.yaml
- id: "context-001"
  name: "Clarification then answer"
  category: context
  context:
    files:
      "config.yaml": |
        debug: true
        port: 8080
  turns:
    - user: "Is debug mode enabled in the config?"
      assert:
        - type: asked-clarification
          required: true
    - user: "config.yaml"
      assert:
        - type: llm-rubric
          rubric: "Response identifies debug mode is enabled"
          min_score: 0.7
```

Multi-turn tests are useful for:
- Testing clarification flows (coordinator asks, user answers)
- Testing conversational context (follow-up questions)
- Testing error recovery (file not found, retry with different file)

**Clarification tests:**

```yaml
# testdata/eval/coordinator/clarification.yaml
- id: "clarify-001"
  name: "Ask clarification for ambiguous file"
  category: clarification
  user_message: "Update the config file"
  context:
    files:
      "config.yaml": "port: 8080"
      "config.json": '{"port": 8080}'
      "config.toml": "port = 8080"
  assert:
    - type: asked-clarification
      required: true
      about: "which"

- id: "clarify-004"
  name: "Don't ask for clear request"
  category: clarification
  user_message: "Read README.md and tell me what it says"
  context:
    files:
      "README.md": "# Hello World"
  assert:
    - type: asked-clarification
      forbidden: true
    - type: skill-selected
      skill: file_reader
      required: true
```

#### Basic Tool Tests (V1)

Basic tests verify direct tool calling behavior:

```yaml
# testdata/eval/instruction_following/basic.yaml
- id: "if-001"
  name: "Read and extract information"
  user_message: "Read config.yaml and tell me the port number"
  context:
    files:
      "config.yaml": |
        server:
          port: 8080
          host: localhost
  assert:
    - type: tool-called
      tool: read
      params:
        path: {contains: "config.yaml"}
    - type: contains
      value: "8080"
```

### Assertion Types

#### Coordinator Assertions (V2)

| Type | Description | Fields |
|------|-------------|--------|
| `skill-selected` | Coordinator delegated to a skill | `skill`, `required`, `forbidden` |
| `asked-clarification` | Coordinator asked for clarification | `required`, `forbidden`, `about` |

**skill-selected examples:**
```yaml
# Skill MUST be selected
- type: skill-selected
  skill: calculator
  required: true

# Skill must NOT be selected
- type: skill-selected
  skill: file_writer
  forbidden: true
```

**asked-clarification examples:**
```yaml
# MUST ask for clarification
- type: asked-clarification
  required: true

# Must NOT ask for clarification
- type: asked-clarification
  forbidden: true

# MUST ask clarification about a specific topic
- type: asked-clarification
  required: true
  about: "which file"
```

#### Common Assertions

| Type | Description | Example |
|------|-------------|---------|
| `contains` | Response contains string | `value: "8080"` |
| `not-contains` | Response doesn't contain string | `value: "error"` |
| `regex` | Response matches pattern | `value: "port.*\\d+"` |
| `tool-called` | Tool was invoked | `tool: read`, `params: {path: {contains: "config"}}` |
| `tool-sequence` | Tools called in order | `sequence: ["read", "write"]` |
| `llm-rubric` | LLM-as-judge evaluation | `rubric: "Correctly explains...", min_score: 0.7` |

### Test Skills

For coordinator testing, create minimal test skills in `testdata/eval/skills/`:

```markdown
<!-- testdata/eval/skills/calculator/SKILL.md -->
---
name: calculator
description: Performs mathematical calculations
tools: [exec]
---

Perform calculations using Python or bc.
```

```markdown
<!-- testdata/eval/skills/file_reader/SKILL.md -->
---
name: file_reader
description: Reads and summarizes file contents
tools: [read]
---

Read files and extract information as requested.
```

### Reproducible Testing

Record model interactions for deterministic replay:

```bash
# Record a baseline
kaggen eval -s testdata/eval/coordinator --coordinator --record golden/baseline.jsonl

# Replay without API calls (deterministic)
kaggen eval -s testdata/eval/coordinator --coordinator --replay golden/baseline.jsonl

# Compare new model against baseline
kaggen eval -s testdata/eval/coordinator --coordinator --model gemini/gemini-2.5-pro --compare golden/baseline.jsonl
```

### Eval Flags

```
-s, --suite string      Path to test suite directory (default "testdata/eval")
    --model string      Model to evaluate (e.g., anthropic/claude-sonnet-4)
    --judge string      Model for LLM-as-judge (defaults to same as --model)
    --category string   Filter to specific category
    --case strings      Run specific test case(s) by ID
    --coordinator       Use V2 runner for coordinator testing (full production system)
    --skills string     Skills directory for coordinator tests
    --trace string      Directory to write execution traces (for debugging)
    --timeout duration  Timeout per test case (default 5m, e.g., 30s, 1m)
    --record string     Record interactions to file for later replay
    --replay string     Replay from recorded file (deterministic)
    --compare string    Compare results against baseline file
-o, --output string     Output results to JSON file
-v, --verbose           Verbose output
```

### Example Output

```
═══════════════════════════════════════════════════════════════
                     EVALUATION RESULTS
═══════════════════════════════════════════════════════════════

  ✓ Pass Rate: 83.3% (10/12)
    Avg Score: 0.87

  By Category:
    ✓ skill_selection: 100.0% (5/5), avg=0.94
    ✗ clarification: 71.4% (5/7), avg=0.81

  Results:
    ✓ [skill-001] Select calculator for math (score=1.00, turns=3)
    ✓ [skill-002] Select file_reader for reading (score=0.95, turns=4)
    ✓ [clarify-001] Ask clarification for ambiguous file (score=1.00, turns=2)
    ✗ [clarify-003] Ask clarification for vague task (score=0.00, turns=5)
        └─ asked-clarification: coordinator should have asked for clarification but didn't
```

### Directory Structure

```
testdata/eval/
  coordinator/              # Coordinator behavior tests (V2)
    skill_selection.yaml    # Skill selection tests
    clarification.yaml      # Clarification behavior tests
  skills/                   # Test-specific skills
    calculator/SKILL.md
    file_reader/SKILL.md
    file_writer/SKILL.md
    summarizer/SKILL.md
  instruction_following/    # Basic instruction tests (V1)
  tool_calling/             # Basic tool calling tests (V1)
```

### Writing Test Cases

**For coordinator tests (V2):**
1. Create YAML files under `testdata/eval/coordinator/`
2. Use `user_message` + `assert` for single-turn tests
3. Use `turns` array for multi-turn conversational tests
4. Test skill selection with `skill-selected` assertions
5. Test clarification behavior with `asked-clarification` assertions
6. Use `context.files` to create workspace files for context-dependent tests
7. Combine with `contains` or `llm-rubric` to verify output quality

**For multi-turn tests:**
- Each turn has a `user` message and optional `assert` list
- Session context is preserved across turns
- Test stops if any turn's assertions fail
- Great for clarification flows and follow-up interactions

**For basic tests (V1):**
1. Create YAML files under `testdata/eval/<category>/`
2. Test tool calling with `tool-called` assertions
3. Category is inferred from directory name

### CI Integration

```yaml
# .github/workflows/eval.yml
name: Agent Evaluation

on:
  pull_request:
    paths:
      - 'internal/agent/**'
      - 'defaults/skills/**'

jobs:
  eval:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.24'
      - name: Run Coordinator Eval Suite
        env:
          ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}
        run: |
          go build -tags fts5 -o kaggen ./cmd/kaggen
          ./kaggen eval -s testdata/eval/coordinator --coordinator --skills testdata/eval/skills
```

---

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
testdata/
  eval/                  Evaluation test cases and golden baselines
internal/
  agent/             Agent logic, async dispatch, in-flight task store, approval system
  auth/              Token authentication (Argon2-ID hashing)
  channel/           Channel interface + implementations
    channel.go         Router, Message, Response types
    websocket.go       WebSocket channel
    telegram.go        Telegram bot channel
    whatsapp.go        WhatsApp bot channel (whatsmeow)
  eval/              Agent evaluation framework (assertions, replay, runner)
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
