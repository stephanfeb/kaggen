# Kaggen Architecture

This document provides a detailed description of Kaggen's software architecture, including component responsibilities, data flows, and the integration of trpc-agent-go for advanced session management and streaming capabilities.

## Table of Contents

1. [Overview](#overview)
2. [High-Level Architecture](#high-level-architecture)
3. [Core Components](#core-components)
4. [Data Flow](#data-flow)
5. [Session Management](#session-management)
6. [trpc-agent-go Integration](#trpc-agent-go-integration)
7. [Gateway Architecture](#gateway-architecture)
8. [Configuration](#configuration)
9. [Directory Structure](#directory-structure)
10. [Extension Points](#extension-points)

---

## Overview

Kaggen is a personal AI assistant platform built in Go. It provides:

- **Multi-channel communication** via WebSocket gateway
- **ReAct agent loop** for reasoning and tool execution
- **Pluggable session backends** (file, Redis, PostgreSQL)
- **Streaming responses** for real-time output
- **Bootstrap-based identity** through markdown files

### Design Principles

1. **Modularity**: Components are loosely coupled with clear interfaces
2. **Extensibility**: New channels, tools, and backends can be added easily
3. **Simplicity**: Prefer stdlib where possible, avoid over-engineering
4. **Testability**: Interfaces enable mocking and unit testing

---

## High-Level Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              Entry Points                                    │
│  ┌────────────────────────┐              ┌────────────────────────────────┐ │
│  │      CLI (cobra)       │              │     Gateway Server             │ │
│  │  kaggen agent          │              │  kaggen gateway                │ │
│  │  kaggen init           │              │  WebSocket: ws://host/ws       │ │
│  │  kaggen status         │              │  Health: http://host/health    │ │
│  └───────────┬────────────┘              └───────────────┬────────────────┘ │
│              │                                           │                   │
├──────────────┼───────────────────────────────────────────┼───────────────────┤
│              │           Channel Layer                   │                   │
│              │     ┌─────────────────────────────────────┴──────┐           │
│              │     │                Router                       │           │
│              │     │  ┌───────────┐ ┌───────────┐ ┌───────────┐ │           │
│              │     │  │ WebSocket │ │ Telegram  │ │  Discord  │ │           │
│              │     │  │  Channel  │ │  Channel  │ │  Channel  │ │           │
│              │     │  └─────┬─────┘ └─────┬─────┘ └─────┬─────┘ │           │
│              │     └────────┼─────────────┼─────────────┼───────┘           │
│              │              └─────────────┼─────────────┘                   │
├──────────────┴────────────────────────────┼─────────────────────────────────┤
│                                           ▼                                  │
│  ┌───────────────────────────────────────────────────────────────────────┐  │
│  │                            Agent Core                                  │  │
│  │                                                                        │  │
│  │  ┌──────────────┐   ┌──────────────┐   ┌────────────────────────────┐ │  │
│  │  │   Context    │   │    ReAct     │   │         Tools              │ │  │
│  │  │   Builder    │   │    Loop      │   │        Registry            │ │  │
│  │  │              │   │              │   │                            │ │  │
│  │  │ - Bootstrap  │   │ - Generate   │   │  ┌──────┐ ┌─────┐ ┌─────┐ │ │  │
│  │  │ - System     │   │ - Execute    │   │  │ read │ │write│ │exec │ │ │  │
│  │  │   Prompt     │   │ - Iterate    │   │  └──────┘ └─────┘ └─────┘ │ │  │
│  │  │ - History    │   │ - Stream     │   │                            │ │  │
│  │  └──────────────┘   └──────────────┘   └────────────────────────────┘ │  │
│  │                                                                        │  │
│  └───────────────────────────────────────────────────────────────────────┘  │
│                                                                              │
├──────────────────────────────────────────────────────────────────────────────┤
│                              Service Layer                                   │
│                                                                              │
│  ┌──────────────────┐  ┌──────────────────┐  ┌──────────────────────────┐  │
│  │     Session      │  │      Memory      │  │       Model              │  │
│  │    (file-backed) │  │    (FileMemory)  │  │      Interface           │  │
│  │                  │  │                  │  │                          │  │
│  │  ┌────────────┐  │  │ - Bootstrap      │  │  ┌────────────────────┐  │  │
│  │  │  session   │  │  │ - Daily logs     │  │  │ Anthropic Client   │  │  │
│  │  │  .Service  │  │  │ - MEMORY.md      │  │  │                    │  │  │
│  │  └──────┬─────┘  │  │                  │  │  │ - Claude API       │  │  │
│  │         │        │  │                  │  │  │ - Tool calls       │  │  │
│  │    ┌────┴────┐   │  │                  │  │  │ - Streaming        │  │  │
│  │    ▼         ▼   │  │                  │  │  └────────────────────┘  │  │
│  │ ┌─────┐ ┌─────┐  │  │                  │  │                          │  │
│  │ │File │ │ Mem │  │  │                  │  │                          │  │
│  │ └─────┘ └─────┘  │  │                  │  │                          │  │
│  └──────────────────┘  └──────────────────┘  └──────────────────────────┘  │
│                                                                              │
└──────────────────────────────────────────────────────────────────────────────┘
```

---

## Core Components

### Agent (`internal/agent/`)

The agent implements the ReAct (Reasoning + Acting) pattern for AI-driven task execution.

| File | Responsibility |
|------|----------------|
| `agent.go` | Main ReAct loop, tool execution orchestration |
| `context.go` | System prompt construction, message history management |
| `stream.go` | Streaming execution with event channels |

**Key Types:**

```go
// Agent orchestrates the ReAct loop
type Agent struct {
    model    model.Model        // LLM interface
    tools    *tools.Registry    // Available tools
    context  *ContextBuilder    // Prompt construction
    logger   *slog.Logger
}

// Event types for streaming
type EventType string
const (
    EventTypeText       EventType = "text"
    EventTypeToolCall   EventType = "tool_call"
    EventTypeToolResult EventType = "tool_result"
    EventTypeDone       EventType = "done"
    EventTypeError      EventType = "error"
    EventTypeThinking   EventType = "thinking"
)
```

**ReAct Loop Flow:**

```
User Message
     │
     ▼
┌─────────────┐
│ Build       │ ◄── System prompt + session history
│ Context     │
└──────┬──────┘
       │
       ▼
┌─────────────┐
│  Generate   │ ◄── Call LLM with tools
│  Response   │
└──────┬──────┘
       │
       ▼
   ┌───────┐
   │ Tool  │──No──► Return final response
   │Calls? │
   └───┬───┘
       │Yes
       ▼
┌─────────────┐
│  Execute    │ ◄── Run each tool
│   Tools     │
└──────┬──────┘
       │
       ▼
┌─────────────┐
│ Append      │ ◄── Add results to context
│ Results     │
└──────┬──────┘
       │
       └────────► Loop (max 10 iterations)
```

### Session Management (`internal/session/`)

Session storage implements trpc-agent-go's `session.Service` interface directly, eliminating the need for an adapter layer.

| File | Responsibility |
|------|----------------|
| `file_service.go` | File-backed `session.Service` implementation |
| `file_service_test.go` | Tests |
| `jsonl.go` | JSONL read/write utilities for `event.Event` |

**Storage layout:**

```
<dataDir>/
  <appName>/<userID>/<sessionID>/
    events.jsonl   – append-only event log
    state.json     – session-level state
  <appName>/
    app_state.json – app-scoped state
  <appName>/<userID>/
    user_state.json – user-scoped state
```

Sessions persist across restarts in both CLI and gateway modes.

### Channel System (`internal/channel/`)

Channels provide the abstraction for multi-platform communication.

| File | Responsibility |
|------|----------------|
| `channel.go` | Channel interface, Router, Handler |
| `websocket.go` | WebSocket channel implementation |

**Channel Interface:**

```go
type Channel interface {
    Name() string
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
    Messages() <-chan *Message
    Send(ctx context.Context, resp *Response) error
}
```

### Gateway (`internal/gateway/`)

The gateway server routes messages between channels and the agent.

| File | Responsibility |
|------|----------------|
| `server.go` | HTTP/WebSocket server management |
| `handler.go` | Message processing and response streaming |

### Tools (`internal/tools/`)

Tools extend the agent's capabilities.

| Tool | Description |
|------|-------------|
| `read` | Read file contents |
| `write` | Write/append to files |
| `exec` | Execute shell commands |

**Tool Interface:**

```go
type Tool interface {
    Name() string
    Description() string
    Parameters() map[string]any
    Execute(ctx context.Context, input map[string]any) (string, error)
}
```

### Memory (`internal/memory/`)

Memory manages bootstrap files and daily logs.

**Bootstrap Files (loaded in order):**

1. `SOUL.md` - Core values and boundaries
2. `IDENTITY.md` - Name, personality, emoji
3. `AGENTS.md` - Operating instructions
4. `TOOLS.md` - Tool usage notes
5. `USER.md` - User profile
6. `MEMORY.md` - Long-term memory
7. Daily logs from `memory/YYYY-MM-DD.md`

### Model (`internal/model/`)

Model interface and LLM client implementations.

```go
type Model interface {
    Generate(ctx context.Context, messages []protocol.Message,
             tools []protocol.ToolDef) (*protocol.Response, error)
}
```

Currently implements:
- `anthropic.Client` - Claude API client (net/http based)

---

## Data Flow

### CLI Agent Flow

```
┌──────────┐    ┌──────────┐    ┌──────────┐    ┌──────────┐
│   User   │───►│   CLI    │───►│  Agent   │───►│ Anthropic│
│  Input   │    │ Command  │    │   Run    │    │   API    │
└──────────┘    └──────────┘    └──────────┘    └──────────┘
                     │               │               │
                     │               ▼               │
                     │         ┌──────────┐         │
                     │         │  Tool    │◄────────┘
                     │         │ Execute  │
                     │         └──────────┘
                     │               │
                     ▼               ▼
               ┌──────────┐    ┌──────────┐
               │ Session  │◄───│ Response │
               │  Save    │    │  Output  │
               └──────────┘    └──────────┘
```

### Gateway Flow

```
┌──────────┐    ┌──────────┐    ┌──────────┐    ┌──────────┐
│WebSocket │───►│  Router  │───►│ Handler  │───►│  Agent   │
│ Client   │    │          │    │          │    │RunStream │
└──────────┘    └──────────┘    └──────────┘    └──────────┘
     ▲                                               │
     │                                               │
     │          ┌──────────┐                        │
     └──────────│  Event   │◄───────────────────────┘
                │ Channel  │
                └──────────┘
```

---

## Session Management

### Backend Selection

Session backends are configured in `~/.kaggen/config.json`:

```json
{
  "session": {
    "backend": "file"
  }
}
```

| Backend | Storage | Use Case |
|---------|---------|----------|
| `file` (default) | JSONL files on disk | Persistent sessions, survives restarts |
| `memory` | In-memory (trpc inmemory) | Testing, ephemeral sessions |

Both CLI and gateway use `file` by default, storing sessions under `~/.kaggen/sessions/`.
The implementation speaks trpc-agent-go's `session.Service` interface natively — no adapter layer.

---

## trpc-agent-go Integration

### Why trpc-agent-go?

[trpc-agent-go](https://trpc.group/trpc-go/trpc-agent-go) is Tencent's open-source agent framework that provides:

1. **Production-ready session backends** - Redis, PostgreSQL support
2. **Event-driven streaming** - Efficient real-time communication
3. **Runner architecture** - Handles streaming execution naturally
4. **Session summarization** - Automatic conversation compression
5. **Multi-agent support** - For future Phase 6 implementation

### Integration Architecture

Kaggen implements trpc-agent-go's `session.Service` interface directly with a file-backed service.
There is no adapter layer — the file service speaks trpc's types natively (`event.Event`, `session.Session`, `session.StateMap`).

```
┌─────────────────────────────────────────────────────────────────┐
│                        Kaggen                                    │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │             session.Service implementations               │   │
│  │                                                           │   │
│  │     ┌────────────────┐           ┌────────────────┐      │   │
│  │     │  FileService   │           │   inmemory     │      │   │
│  │     │  (kaggen)      │           │  (trpc-agent)  │      │   │
│  │     │                │           │                │      │   │
│  │     │  JSONL + JSON  │           │   In-Memory    │      │   │
│  │     └────────────────┘           └────────────────┘      │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                                  │
└──────────────────────────────────────────────────────────────────┘
```

### Future trpc-agent-go Integration Points

| Phase | trpc-agent-go Feature | Purpose |
|-------|----------------------|---------|
| Phase 3 | `knowledge` package | RAG and semantic search |
| Phase 3 | `session/summary` | Automatic conversation summarization |
| Phase 5 | `tool` package | MCP tool bridge |
| Phase 6 | `runner` package | Multi-agent orchestration |
| Phase 6 | `graph` package | Agent workflow graphs |

---

## Gateway Architecture

### WebSocket Protocol

**Connection:**
```
ws://host:port/ws?session=SESSION_ID
```

**Incoming Message (Client → Server):**
```json
{
  "content": "User message text",
  "session_id": "optional-override",
  "metadata": {}
}
```

**Outgoing Response (Server → Client):**
```json
{
  "id": "uuid",
  "message_id": "original-message-uuid",
  "session_id": "session-id",
  "type": "text|thinking|tool_call|tool_result|done|error",
  "content": "Response content",
  "done": false,
  "metadata": {}
}
```

### Response Types

| Type | Description |
|------|-------------|
| `thinking` | Agent is processing |
| `text` | Partial text response |
| `tool_call` | Tool execution started |
| `tool_result` | Tool execution completed |
| `done` | Final response |
| `error` | Error occurred |

### Client Management

```
                    ┌─────────────────────────────┐
                    │     WebSocketChannel        │
                    │                             │
                    │  clients: map[id]*wsClient  │
                    │  messages: chan *Message    │
                    └─────────────────────────────┘
                                 │
            ┌────────────────────┼────────────────────┐
            │                    │                    │
            ▼                    ▼                    ▼
      ┌──────────┐        ┌──────────┐        ┌──────────┐
      │ wsClient │        │ wsClient │        │ wsClient │
      │          │        │          │        │          │
      │ id: uuid │        │ id: uuid │        │ id: uuid │
      │ session  │        │ session  │        │ session  │
      │ conn     │        │ conn     │        │ conn     │
      │ send chan│        │ send chan│        │ send chan│
      └──────────┘        └──────────┘        └──────────┘
```

---

## Configuration

### File Location

`~/.kaggen/config.json`

### Schema

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
    "backend": "file",
    "app_name": "kaggen",
    "user_id": "default"
  }
}
```

### Environment Variables

| Variable | Purpose |
|----------|---------|
| `ANTHROPIC_API_KEY` | Claude API authentication |

---

## Directory Structure

```
kaggen/
├── cmd/
│   └── kaggen/
│       ├── main.go              # Entry point
│       └── cmd/
│           ├── root.go          # Root command
│           ├── agent.go         # Interactive CLI
│           ├── gateway.go       # WebSocket server
│           ├── init.go          # Workspace setup
│           └── status.go        # Status display
│
├── internal/
│   ├── agent/
│   │   ├── agent.go             # ReAct loop
│   │   ├── context.go           # Context builder
│   │   └── stream.go            # Streaming execution
│   │
│   ├── channel/
│   │   ├── channel.go           # Channel interface
│   │   └── websocket.go         # WebSocket impl
│   │
│   ├── config/
│   │   └── config.go            # Configuration
│   │
│   ├── gateway/
│   │   ├── handler.go           # Message handler
│   │   └── server.go            # Gateway server
│   │
│   ├── memory/
│   │   └── file.go              # Bootstrap files
│   │
│   ├── model/
│   │   ├── model.go             # Model interface
│   │   └── anthropic/
│   │       └── client.go        # Claude client
│   │
│   ├── session/
│   │   ├── file_service.go      # File-backed session.Service
│   │   ├── file_service_test.go # Tests
│   │   └── jsonl.go             # JSONL utilities for event.Event
│   │
│   └── tools/
│       ├── tools.go             # Tool registry
│       ├── read.go              # Read tool
│       ├── write.go             # Write tool
│       └── exec.go              # Exec tool
│
├── pkg/
│   └── protocol/
│       └── types.go             # Shared types
│
└── docs/
    ├── ARCHITECTURE.md          # This document
    └── ROADMAP.md               # Development roadmap
```

### Workspace Structure

```
~/.kaggen/
├── config.json                  # Configuration
├── sessions/
│   ├── main.jsonl               # Default session
│   └── *.jsonl                  # Other sessions
└── workspace/
    ├── SOUL.md                  # Core values
    ├── IDENTITY.md              # Personality
    ├── AGENTS.md                # Instructions
    ├── TOOLS.md                 # Tool notes
    ├── USER.md                  # User profile
    ├── MEMORY.md                # Long-term memory
    └── memory/
        ├── 2025-01-26.md        # Yesterday's log
        └── 2025-01-27.md        # Today's log
```

---

## Extension Points

### Adding a New Channel

1. Implement the `Channel` interface in `internal/channel/`
2. Add channel to the router in `internal/gateway/server.go`
3. Handle channel-specific message formatting

### Adding a New Tool

1. Implement the `Tool` interface in `internal/tools/`
2. Register in `DefaultRegistry()` function
3. Add parameter schema for the LLM

### Adding a New Session Backend

1. Implement trpc-agent-go's `session.Service` interface
2. Add backend selection in `createSessionService()` in `cmd/kaggen/cmd/gateway.go`
3. Add configuration options in `internal/config/config.go`

### Adding a New LLM Provider

1. Implement the `Model` interface in `internal/model/`
2. Add provider selection logic
3. Handle provider-specific message formatting

---

## Version History

| Version | Date | Changes |
|---------|------|---------|
| 0.2.0 | 2025-01-27 | Phase 2 - Gateway, trpc-agent-go integration |
| 0.1.0 | 2025-01-27 | Phase 1 - Foundation |
