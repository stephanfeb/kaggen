# Kaggen Roadmap

A personal AI assistant platform built in Go.

## Overview

Kaggen is designed as a multi-channel AI assistant with file-based memory, tool execution, and eventually proactive capabilities. The implementation is divided into phases, each building on the previous.

---

## Phase 1: Foundation ✅

**Status:** Complete
**Completed:** 2025-01-27

### Deliverables

- [x] Project scaffolding and Go module setup
- [x] Configuration system (`~/.kaggen/config.json`)
- [x] Protocol types for messages and tool calls
- [x] Anthropic Claude API client (stdlib net/http)
- [x] Basic tools: `read`, `write`, `exec`
- [x] JSONL session persistence
- [x] Bootstrap file loading (SOUL.md, IDENTITY.md, etc.)
- [x] Daily memory logs support
- [x] ReAct agent loop with tool execution
- [x] CLI commands: `init`, `agent`, `status`, `gateway` (stub)
- [x] Unit tests for core components

### Architecture

```
┌─────────────────────────────────────────────────────────┐
│                      CLI (cobra)                        │
├─────────────────────────────────────────────────────────┤
│                        Agent                            │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────┐ │
│  │   Context   │  │   ReAct     │  │      Tools      │ │
│  │   Builder   │  │   Loop      │  │    Registry     │ │
│  └─────────────┘  └─────────────┘  └─────────────────┘ │
├─────────────────────────────────────────────────────────┤
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────┐ │
│  │   Session   │  │   Memory    │  │    Anthropic    │ │
│  │   Manager   │  │   (Files)   │  │     Client      │ │
│  └─────────────┘  └─────────────┘  └─────────────────┘ │
└─────────────────────────────────────────────────────────┘
```

---

## Phase 2: Gateway & Channels ✅

**Status:** Complete
**Completed:** 2025-01-27

### Goals

Transform Kaggen from a CLI tool into a multi-channel assistant with a WebSocket gateway.

### Deliverables

- [x] WebSocket gateway server (`kaggen gateway`)
- [x] Channel abstraction interface
- [x] WebSocket channel implementation
- [x] Message routing and session management
- [x] Streaming agent execution (real-time responses)
- [x] Session backend abstraction (file, Redis, PostgreSQL ready)
- [x] trpc-agent-go integration for session storage
- [ ] Telegram channel adapter (future)
- [ ] Discord channel adapter (future)
- [ ] Channel-specific formatting (future)

### Architecture Addition

```
┌─────────────────────────────────────────────────────────┐
│                      Gateway                            │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐             │
│  │   CLI    │  │ Telegram │  │ Discord  │  ...        │
│  │ Channel  │  │ Channel  │  │ Channel  │             │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘             │
│       └─────────────┼─────────────┘                    │
│                     ▼                                  │
│              ┌─────────────┐                           │
│              │   Router    │                           │
│              └─────────────┘                           │
└─────────────────────────────────────────────────────────┘
```

### Key Decisions

- WebSocket for real-time bidirectional communication
- Each channel maintains its own connection state
- Unified message format across channels
- trpc-agent-go for session backend abstraction
- Streaming events for real-time response delivery
- Pluggable backend architecture (file → Redis/PostgreSQL)

---

## Phase 3: Enhanced Memory

**Status:** Not Started
**Target:** TBD

### Goals

Add semantic search and structured memory capabilities.

### Planned Deliverables

- [ ] SQLite database for structured storage
- [ ] sqlite-vec for vector embeddings
- [ ] Memory search tool for the agent
- [ ] Automatic memory extraction from conversations
- [ ] Memory consolidation (daily → weekly → monthly summaries)
- [ ] Entity tracking (people, projects, preferences)

### Memory Types

| Type | Storage | Purpose |
|------|---------|---------|
| Bootstrap | Markdown files | Identity, instructions, user profile |
| Episodic | JSONL + SQLite | Conversation history |
| Semantic | sqlite-vec | Searchable knowledge |
| Entity | SQLite | People, projects, facts |

---

## Phase 4: Proactive Engine

**Status:** Not Started
**Target:** TBD

### Goals

Enable Kaggen to take initiative based on schedules, events, and learned patterns.

### Planned Deliverables

- [ ] Cron-based scheduler
- [ ] Webhook receiver for external triggers
- [ ] Proactive task queue
- [ ] Notification preferences per channel
- [ ] Time-aware context injection
- [ ] Habit and reminder tracking

### Trigger Types

- **Scheduled**: Daily briefings, reminders, check-ins
- **Event-driven**: Calendar events, email digests, RSS updates
- **Pattern-based**: Learned user routines

---

## Phase 5: Tool Expansion

**Status:** Not Started
**Target:** TBD

### Goals

Expand tool capabilities for broader task automation.

### Planned Tools

| Tool | Purpose |
|------|---------|
| `web_search` | Search the web via API |
| `web_fetch` | Fetch and parse web pages |
| `calendar` | Read/write calendar events |
| `email` | Read/send emails |
| `notes` | Structured note-taking |
| `code_exec` | Sandboxed code execution |

### Integration Considerations

- OAuth for third-party services
- Rate limiting and quotas
- Credential management (`~/.kaggen/credentials/`)
- Tool permissions per channel

---

## Phase 6: Multi-Agent

**Status:** Not Started
**Target:** TBD

### Goals

Support specialized sub-agents for complex tasks.

### Concepts

- **Orchestrator**: Main agent that delegates to specialists
- **Specialists**: Focused agents (research, coding, writing)
- **Handoff protocol**: Context transfer between agents
- **Parallel execution**: Multiple agents working simultaneously

---

## Future Considerations

### Performance
- Response streaming
- Token usage tracking and budgeting
- Context window management
- Caching for repeated queries

### Security
- Tool sandboxing improvements
- Audit logging
- Rate limiting
- Input validation

### Observability
- Structured logging (slog)
- Metrics collection
- Conversation analytics
- Cost tracking

### User Experience
- Web UI for configuration
- Mobile app integration
- Voice input/output
- Rich media handling

---

## Version History

| Version | Date | Description |
|---------|------|-------------|
| 0.2.0 | 2025-01-27 | Phase 2 complete - Gateway & trpc-agent-go |
| 0.1.0 | 2025-01-27 | Phase 1 complete - Foundation |

---

## Contributing

This is a personal project, but the roadmap serves as documentation for future development direction and architectural decisions.
