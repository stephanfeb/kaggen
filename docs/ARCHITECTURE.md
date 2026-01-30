# Kaggen Architecture

## Overview

Kaggen is a multi-channel agentic bot built on the [trpc-agent-go](https://trpc.group/trpc-go/trpc-agent-go) framework. It uses a **Coordinator Team** pattern where a central LLM coordinator delegates work to specialist sub-agents loaded from skill definitions. It supports both interactive CLI and gateway (Telegram/WebSocket) modes, with async task dispatch, file-based sessions, semantic memory, and hot-reloadable skills.

```
                        +-----------------------+
                        |     User / Client     |
                        +----------+------------+
                                   |
                    +--------------+--------------+
                    |                             |
             +------+------+            +--------+--------+
             |  CLI (agent) |            | Gateway (gateway)|
             +------+------+            +--------+--------+
                    |                             |
                    |                    +--------+--------+
                    |                    |  Channel Router  |
                    |                    |  (Telegram / WS) |
                    |                    +--------+--------+
                    |                             |
                    +-------------+---------------+
                                  |
                          +-------+-------+
                          |    Runner     |
                          | (trpc-agent)  |
                          +-------+-------+
                                  |
                    +-------------+-------------+
                    |                           |
             +------+------+          +--------+--------+
             |   Session   |          |   Memory        |
             | (file-based)|          | (SQLite + vec)  |
             +-------------+          +-----------------+
                                  |
                          +-------+-------+
                          |  Agent (Team) |
                          |  Coordinator  |
                          +-------+-------+
                                  |
              +-------------------+-------------------+
              |                   |                   |
      +-------+------+   +-------+------+   +-------+------+
      | Sub-Agent:   |   | Sub-Agent:   |   | Sub-Agent:   |
      |   coder      |   |   general    |   |  <skill N>   |
      | (exec only)  |   | (all tools)  |   | (per-skill)  |
      +--------------+   +--------------+   +--------------+
```

---

## Entry Points

### `kaggen agent` -- Interactive CLI

Reads user input from stdin, runs the agent in a ReAct loop, prints responses to stdout. Supports `/clear` to reset the session and `SIGHUP` for skill hot-reload.

**File:** `cmd/kaggen/cmd/agent.go`

### `kaggen gateway` -- Multi-Channel Server

Starts an HTTP/WebSocket server and optional Telegram bot. Routes messages from all channels through a shared handler. Enables memory service, proactive engine, and dashboard API.

**File:** `cmd/kaggen/cmd/gateway.go`

### `kaggen init` -- Workspace Bootstrap

Creates the `~/.kaggen` directory tree with default bootstrap files (SOUL.md, IDENTITY.md, AGENTS.md, TOOLS.md, USER.md, MEMORY.md) and `config.json`.

**File:** `cmd/kaggen/cmd/init.go`

### `kaggen status` -- System Status

Displays current system status information.

**File:** `cmd/kaggen/cmd/status.go`

---

## Core Components

### 1. Agent (Coordinator Team)

**Files:** `internal/agent/agent.go`, `internal/agent/skills.go`

The `Agent` wraps a trpc-agent-go `team.Team` configured with the **Coordinator** pattern:

- **Coordinator**: An LLM agent with access to all tools (read, write, exec, dispatch_task, task_status, memory tools). It receives the combined system instruction from bootstrap files and decides whether to handle work directly or delegate to a specialist.
- **Sub-Agents (Members)**: One per loaded skill, plus a `general` fallback. Each has its own instruction (from SKILL.md body) and tool set (optionally filtered via the `tools` frontmatter field).

The coordinator can delegate synchronously (blocking sub-agent call) or asynchronously (via `dispatch_task`).

```
Coordinator (e.g. Haiku -- fast/cheap routing model)
  |
  |-- dispatch_task("coder", "build X")      --> async goroutine --> Coder sub-agent
  |-- dispatch_task("researcher", "find Y")  --> async goroutine --> Researcher sub-agent
  |-- [direct tool call]                     --> read / write / exec / memory_search
  |
  v
Synthesize results, respond to user
```

**Key struct:**

```go
type Agent struct {
    team          *team.Team          // trpc-agent-go Team (coordinator + members)
    memory        *memory.FileMemory  // Bootstrap file loader
    tools         []tool.Tool         // Available tools
    model         model.Model         // LLM adapter
    inFlightStore *InFlightStore      // Async task tracking
    dispatcher    *asyncDispatcher    // Background task runner
}
```

### 2. Async Dispatch

**File:** `internal/agent/async.go`, `internal/agent/async_status.go`

Long-running tasks are dispatched asynchronously so the coordinator can return immediately with a task ID while work continues in the background.

**Key types:**

| Type | Purpose |
|------|---------|
| `asyncDispatcher` | Spawns sub-agent goroutines with 15-minute timeout |
| `InFlightStore` | Thread-safe registry of running/completed tasks |
| `TaskState` | Per-task metadata: ID, agent, status, result, events, token usage |
| `TriggerPolicy` | `auto` (inject result immediately) or `queue` (wait for next user turn) |

**Async dispatch flow:**

```
1. Coordinator calls dispatch_task(agent_name, task, policy)
      |
      v
2. Tool returns immediately: {task_id: "uuid", status: "accepted"}
      |
      v
3. Goroutine runs sub-agent:
   +-- 15-minute context timeout
   +-- Circuit breaker: abort after 3 consecutive errors
   +-- Event collection + token accumulation
      |
      v
4. On completion:
   +-- TaskState updated in InFlightStore
   +-- Completion callback fires
   +-- policy=auto  --> result injected into session immediately
   +-- policy=queue --> result queued for next user message
      |
      v
5. Dashboard receives real-time updates via TaskEventCallback
```

### 3. Model Adapters

**Files:** `internal/model/anthropic/`, `internal/model/gemini/`, `internal/model/zai/`

Each adapter implements the trpc-agent-go `model.Model` interface, converting between the framework's unified request/response format and the provider's native API.

| Provider | Package | API Format |
|----------|---------|------------|
| Anthropic Claude | `model/anthropic` | Messages API with `tool_use`/`tool_result` content blocks |
| Google Gemini | `model/gemini` | GenerateContent with `functionCall`/`functionResponse` parts |
| Z.AI GLM | `model/zai` | OpenAI-compatible chat completions with `tool_calls` |

**Provider selection** (priority order): `ZAI_API_KEY` > `GEMINI_API_KEY` > `ANTHROPIC_API_KEY`

**Rate limiting:** All adapters are wrapped with `RateLimitedModel` (`internal/model/ratelimit.go`) -- a semaphore-based concurrency limiter (default: 4 concurrent API calls).

```go
type RateLimitedModel struct {
    inner model.Model
    sem   chan struct{} // buffered channel as semaphore
}
```

### 4. Tools

**Files:** `internal/tools/`

| Tool | File | Description |
|------|------|-------------|
| `read` | `read.go` | Read file contents with optional line limit (default 1000 lines) |
| `write` | `write.go` | Write/append to file (atomic: write to .tmp then rename). Rejects empty content. |
| `exec` | `exec.go` | Execute shell command via `bash -c`. Default timeout 30s, max 30min. |
| `dispatch_task` | `async.go` | Dispatch async sub-agent task. Returns task ID immediately. |
| `task_status` | `async_status.go` | Query task status by ID or filter |
| `memory_add` | `memory/service.go` | Add fact to semantic memory |
| `memory_search` | `memory/service.go` | Semantic search via embeddings |
| `memory_update` | `memory/service.go` | Update existing fact |
| `memory_delete` | `memory/service.go` | Remove fact |
| `backlog_*` | `backlog.go` | Persistent task backlog (SQLite, gateway only) |

All tools are built using `trpc-agent-go/tool/function.NewFunctionTool()` with automatic JSON schema generation from Go struct tags.

### 5. Skill System

**Files:** `internal/agent/skills.go`, `~/.kaggen/skills/*/SKILL.md`

Skills are defined as directories containing a `SKILL.md` file with YAML frontmatter and a markdown body:

```markdown
---
name: coder
description: Delegates software engineering tasks to Claude Code CLI
tools: exec
---

<instruction body becomes the sub-agent's system prompt>
```

**Frontmatter fields:**

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Unique skill identifier |
| `description` | Yes | Short description (shown to coordinator for delegation decisions) |
| `tools` | No | Comma-separated tool names. If set, only these tools are available to the skill agent. If absent, all tools are provided. |

**Tool filtering:** `BuildSubAgents()` in `skills.go` parses the `tools` frontmatter field via `parseSkillTools()` and filters the general tool list using `filterTools()`. This prevents skill agents from falling back to tools they shouldn't use (e.g., the coder agent only gets `exec` so it must delegate to Claude Code CLI rather than writing files directly).

**Hot-reload:** Sending `SIGHUP` to the process triggers `AgentFactory.Rebuild()`, which atomically swaps in a new Agent with freshly loaded skills via `AgentProvider` (atomic pointer swap -- in-flight requests drain on the old agent).

```
SIGHUP
  |
  v
AgentFactory.Rebuild()
  |-- Reload skills from disk
  |-- BuildSubAgents()
  |-- Create new Agent (Team)
  |-- AgentProvider.Swap(newAgent)  <-- atomic pointer swap
  |
  v
Old agent drains in-flight requests
New agent handles new requests
```

### 6. Channel System

**Files:** `internal/channel/`

| Channel | File | Transport |
|---------|------|-----------|
| WebSocket | `websocket.go` | HTTP upgrade at `/ws`, ping/pong keepalive (60s timeout), 1MB max message |
| Telegram | `telegram.go` | Long-polling via telegram-bot-api, user whitelist, per-user rate limiting |

The `Router` multiplexes messages from all channels and dispatches each to the handler in a separate goroutine.

```
Telegram -----+
              |
              +--> Router --> Handler.HandleMessage()
              |
WebSocket ----+
```

**Key interfaces:**

```go
type Channel interface {
    Name() string
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
    Messages() <-chan *Message
    Send(ctx context.Context, resp *Response) error
}
```

- `Message`: Incoming (ID, SessionID, UserID, Content, Channel, Attachments, Metadata)
- `Response`: Outgoing (ID, MessageID, SessionID, Content, Type, Done, Metadata)

### 7. Session Management

**Files:** `internal/session/`

File-based implementation of trpc-agent-go's `session.Service`:

```
~/.kaggen/sessions/kaggen/
  <userID>/
    <sessionID>/
      events.jsonl       # Append-only event log (one JSON object per line)
      state.json         # Session state snapshot
    user_state.json      # User-level persistent state
  app_state.json         # App-level persistent state
```

| File | Purpose |
|------|---------|
| `file_service.go` | File-backed `session.Service` implementation |
| `jsonl.go` | JSONL serialization for `event.Event` objects |
| `sanitize_wrapper.go` | Strips binary data (images, file attachments) from events before persistence |

### 8. Memory System

**Files:** `internal/memory/`

Two layers:

**Bootstrap files** (`memory/file.go`): Static markdown files loaded into the coordinator instruction at startup. Loaded in order:

1. `SOUL.md` -- Core values and boundaries
2. `IDENTITY.md` -- Name, personality
3. `AGENTS.md` -- Operating instructions
4. `TOOLS.md` -- Tool usage notes
5. `USER.md` -- User profile
6. `MEMORY.md` -- Long-term memory
7. Daily logs from `memory/YYYY-MM-DD.md`

**Semantic memory service** (`memory/service.go`): Dynamic fact store backed by SQLite + sqlite-vec for vector search.

```
User message
  |
  v
Auto-extractor (background LLM call)
  |-- extracts facts --> memory_add
  |
Agent can call:
  |-- memory_search (semantic via embeddings)
  |-- memory_add / memory_update / memory_delete
  |
Synthesis job (periodic)
  |-- compresses and summarizes stored facts
```

| Component | File | Purpose |
|-----------|------|---------|
| `FileMemoryService` | `service.go` | Main service with tools, auto-extraction, synthesis |
| `VectorIndex` | `vectorindex.go` | sqlite-vec integration with FTS5 keyword search |
| `OllamaEmbedder` | `embedding/ollama.go` | Local embedding generation (default: `nomic-embed-text`) |

### 9. Gateway Server

**Files:** `internal/gateway/`

Orchestrates all gateway-mode components:

```
Server
  |-- Handler          (message processing, event streaming)
  |-- Router           (channel multiplexing)
  |-- WebSocketChannel + TelegramChannel
  |-- ProactiveEngine  (cron jobs, webhooks, heartbeats)
  |-- DashboardAPI     (task monitoring UI backend)
  |-- MemoryService    (semantic memory)
```

The `Handler` converts channel messages to agent invocations, streams events back as responses, and extracts `[send_file: /path]` directives for file delivery.

---

## Request Flow

### CLI Mode

```
stdin
  --> read line
  --> model.Message{Role: user}
  --> runner.Run(sessionID, message)
       |-- Session.Load or Create
       |-- Agent.Run(invocation)
       |    |-- Coordinator LLM call
       |    |-- Tool detection:
       |    |    +-- dispatch_task --> async goroutine (return task ID)
       |    |    +-- sub-agent    --> sync delegation (block)
       |    |    +-- regular tool --> direct execution
       |    |-- Event stream
       |-- Session.Update(events)
  --> print response to stdout
  --> loop
```

### Gateway Mode

```
Channel.Messages()
  --> Router
  --> Handler.HandleMessage()
       |-- Register respond callback (for async routing)
       |-- Convert attachments to file paths
       |-- runner.Run(sessionID, message)
       |    |-- (same as CLI flow above)
       |-- Stream events as Response objects
       |-- Channel.Send()
```

### Async Completion Path

```
dispatch_task called by coordinator
  --> asyncDispatcher.dispatch()
       |-- Register TaskState in InFlightStore
       |-- Spawn goroutine:
       |    |-- Create sub-agent Invocation
       |    |-- sub-agent.Run() with 15min timeout
       |    |-- Collect events, accumulate tokens
       |    |-- Circuit breaker (3 consecutive errors = abort)
       |    |-- Mark complete/failed in InFlightStore
       |    |-- Call completion callback
       |
       |--> Completion callback:
            |-- policy=auto: InjectCompletion into session
            |    --> coordinator wakes, synthesizes result
            |-- policy=queue: queued for next user turn
            |
            +--> Dashboard sees real-time updates via TaskEventCallback
```

---

## Configuration

**File:** `~/.kaggen/config.json`

```json
{
  "agent": {
    "model": "anthropic/claude-sonnet-4",
    "workspace": "~/.kaggen/workspace",
    "max_history_runs": 10,
    "max_concurrent_llm": 4
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
      "token": "",
      "allowed_users": []
    }
  },
  "memory": {
    "embedding": {
      "model": "nomic-embed-text",
      "url": "http://localhost:11434"
    }
  },
  "proactive": {
    "jobs": [],
    "webhooks": [],
    "heartbeats": []
  }
}
```

**Environment variable overrides:** `ANTHROPIC_API_KEY`, `GEMINI_API_KEY`, `ZAI_API_KEY`, `TELEGRAM_BOT_TOKEN`

---

## Directory Layout

### Source Code

```
kaggen/
  cmd/kaggen/
    main.go                        # Entry point
    cmd/
      root.go                      # Cobra root command
      agent.go                     # CLI agent command
      gateway.go                   # Gateway server command
      init.go                      # Workspace init command
      status.go                    # Status display command
  internal/
    agent/
      agent.go                     # Agent (Team wrapper)
      async.go                     # Async dispatch + InFlightStore
      async_status.go              # Task status tool
      factory.go                   # AgentFactory for hot-reload
      provider.go                  # AgentProvider (atomic swap)
      skills.go                    # Sub-agent builder + tool filtering
    channel/
      channel.go                   # Channel interface + Router
      websocket.go                 # WebSocket channel
      telegram.go                  # Telegram bot channel
    config/
      config.go                    # JSON config loading
    gateway/
      server.go                    # Gateway orchestration
      handler.go                   # Message handler + event streaming
    memory/
      file.go                      # Bootstrap file loading
      service.go                   # Semantic memory service
      vectorindex.go               # sqlite-vec integration
      embedding/
        ollama.go                  # Ollama embedder client
    model/
      ratelimit.go                 # Concurrency-limited model wrapper
      anthropic/                   # Anthropic Claude adapter
        client.go                  #   HTTP client + legacy Generate
        trpc_adapter.go            #   model.Model implementation
      gemini/                      # Google Gemini adapter
        client.go
        trpc_adapter.go
      zai/                         # Z.AI GLM adapter
        client.go
        trpc_adapter.go
    session/
      file_service.go              # File-backed session persistence
      jsonl.go                     # JSONL serialization
      sanitize_wrapper.go          # Binary data stripping
    tools/
      tools.go                     # Tool registry (DefaultTools)
      read.go                      # File read tool
      write.go                     # File write tool
      exec.go                      # Shell exec tool
      backlog.go                   # Persistent task backlog
      proactive.go                 # Cron job tools
  docs/                            # Documentation
```

### Workspace (Runtime)

```
~/.kaggen/
  config.json                      # Configuration
  sessions/kaggen/                 # Session storage
    <userID>/<sessionID>/
      events.jsonl
      state.json
  workspace/                       # Bootstrap files
    SOUL.md
    IDENTITY.md
    AGENTS.md
    TOOLS.md
    USER.md
    MEMORY.md
    memory/                        # Daily logs
      YYYY-MM-DD.md
  skills/                          # Skill definitions
    coder/SKILL.md
    <skill-name>/SKILL.md
  memory.db                        # Semantic memory (SQLite)
  backlog.db                       # Task backlog (SQLite)
```

---

## Key Design Decisions

1. **Coordinator delegates, doesn't do**: The coordinator LLM (typically a fast/cheap model like Haiku) makes routing decisions. Heavy work is delegated to specialist sub-agents or external tools like Claude Code CLI (which runs a more capable model like Opus).

2. **Async-first for long tasks**: `dispatch_task` returns immediately so the coordinator can dispatch multiple tasks in parallel or respond to the user while work runs in the background.

3. **Skill-driven architecture**: New capabilities are added by dropping a `SKILL.md` file into the skills directory. No code changes needed. Hot-reload via SIGHUP.

4. **Per-skill tool restriction**: Skills declare which tools they need in their frontmatter (`tools: exec`). This prevents agents from taking shortcuts (e.g., the coder agent only gets `exec` so it must use Claude Code CLI rather than writing files directly).

5. **Framework leverage**: Built on trpc-agent-go for session management, event streaming, tool execution, and the ReAct loop -- avoiding reimplementation of core agentic infrastructure.

6. **Multi-model support**: Provider-agnostic via adapter pattern. The coordinator can run on a cheap/fast model while sub-agents (via external tools) use more capable models.

7. **Atomic hot-reload**: `AgentProvider` uses `atomic.Pointer` for zero-downtime skill updates. In-flight requests drain on the old agent while new requests go to the reloaded one.
