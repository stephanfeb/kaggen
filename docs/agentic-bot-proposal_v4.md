# Software Engineering Proposal: Agentic Personal Assistant Platform

## Project Codename: **Hermes**

**Version:** 2.0  
**Date:** January 2026  
**Classification:** Internal Engineering Document

---

## Executive Summary

This proposal outlines the development of **Hermes**, a Go-based personal assistant platform inspired by Clawdbot's architecture. Hermes provides intelligent, context-aware assistance through a local-first Gateway that connects to messaging platforms, executes skills, and maintains persistent memory—all with zero external database dependencies.

**Key Differentiators:**

- **Go-native implementation** — Single binary deployment, low memory footprint, ideal for oDroid/Raspberry Pi
- **File-based persistence** — JSONL sessions, Markdown memory, git-friendly workspace
- **Built-in channel integrations** — WhatsApp, Telegram, Discord, Slack, Signal, iMessage
- **Proactive engine** — Cron jobs, webhooks, heartbeats (agent initiates contact)
- **Device nodes** — Camera, location, voice, browser control via connected devices
- **Open-ended extensibility** — Agent Skills pattern (no recompilation required)

---

## Table of Contents

1. [Background & Motivation](#1-background--motivation)
2. [Goals & Non-Goals](#2-goals--non-goals)
3. [System Architecture](#3-system-architecture)
4. [Gateway Design](#4-gateway-design)
5. [Agent Runtime](#5-agent-runtime)
6. [Workspace & Bootstrap Files](#6-workspace--bootstrap-files)
7. [Memory System](#7-memory-system)
8. [Session Management](#8-session-management)
9. [Skills System](#9-skills-system)
10. [Channel Integrations](#10-channel-integrations)
11. [Proactive Engine](#11-proactive-engine)
12. [Device Nodes](#12-device-nodes)
13. [WebSocket Protocol](#13-websocket-protocol)
14. [Security Model](#14-security-model)
15. [Observability](#15-observability)
16. [Development Phases](#16-development-phases)
17. [Risk Assessment](#17-risk-assessment)
18. [Success Metrics](#18-success-metrics)

---

## 1. Background & Motivation

### 1.1 Problem Statement

Current personal assistant solutions suffer from fundamental limitations:

| Limitation | Impact |
|------------|--------|
| **Closed capability sets** | Users cannot extend functionality without vendor involvement |
| **Ephemeral context** | Assistants forget preferences, history, and ongoing tasks |
| **Cloud dependency** | Requires internet, raises privacy concerns |
| **Reactive only** | Assistants wait for user input, never initiate contact |
| **No device integration** | Cannot access camera, location, or control browser |

### 1.2 Clawdbot as Reference Architecture

Clawdbot (by Peter Steinberger) has proven that a local-first, skill-extensible personal assistant is viable:

- 20k+ GitHub stars, active community
- Multi-channel support (WhatsApp, Telegram, Discord, etc.)
- File-based memory and sessions
- Proactive features (cron, webhooks, heartbeats)
- Device nodes for camera, voice, browser control

Hermes aims to provide **functional parity with Clawdbot** while leveraging Go's strengths:

| Clawdbot | Hermes |
|----------|--------|
| TypeScript/Node.js | Go |
| npm/pnpm | Single binary |
| ~50MB+ runtime | ~15MB binary |
| Node.js 22+ required | No runtime dependencies |

### 1.3 Why Go?

- **Single binary deployment** — Copy one file to Raspberry Pi, done
- **Low memory footprint** — Critical for oDroid/RPi deployments
- **Excellent concurrency** — Goroutines for multi-channel handling
- **Cross-compilation** — Build for ARM from x86 easily
- **tRPC-Agent-Go** — Production-validated agent framework (Tencent)

### 1.4 Target Deployment Scenarios

| Scenario | Hardware | Use Case |
|----------|----------|----------|
| **Headless Server** | oDroid, Raspberry Pi, VPS | Always-on assistant, minimal resources |
| **Desktop Companion** | macOS, Linux, Windows | Browser control, local skills |
| **Hybrid** | Server + Device Nodes | Server runs agent, phone provides camera/location |

---

## 2. Goals & Non-Goals

### 2.1 Goals

| ID | Goal | Priority |
|----|------|----------|
| G1 | Functional parity with Clawdbot core features | P0 |
| G2 | Single binary deployment with zero external dependencies | P0 |
| G3 | Built-in channel integrations (WhatsApp, Telegram, Discord, Slack) | P0 |
| G4 | File-based persistence (JSONL sessions, Markdown memory) | P0 |
| G5 | Agent Skills system compatible with Anthropic spec | P0 |
| G6 | Proactive engine (cron, webhooks, heartbeats) | P1 |
| G7 | Device nodes (camera, location, voice, browser) | P1 |
| G8 | WebSocket control plane for clients | P1 |
| G9 | Bootstrap files system (SOUL.md, AGENTS.md, etc.) | P1 |
| G10 | Cross-platform: Linux ARM (oDroid/RPi), macOS, Windows | P1 |

### 2.2 Non-Goals

| ID | Non-Goal | Rationale |
|----|----------|-----------|
| NG1 | Database backends (Redis, PostgreSQL) | Conflicts with zero-dependency goal |
| NG2 | Multi-tenant / shared server deployment | Focus on single-user, local-first |
| NG3 | Hosting LLM inference locally | Integrate with external providers |
| NG4 | Building native mobile apps | Device nodes connect via WebSocket |
| NG5 | Real-time voice conversation | Deferred to future phase |

---

## 3. System Architecture

### 3.1 High-Level Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           MESSAGING CHANNELS                                │
│   WhatsApp │ Telegram │ Discord │ Slack │ Signal │ iMessage │ WebChat      │
└─────────────────────────────────┬───────────────────────────────────────────┘
                                  │
┌─────────────────────────────────▼───────────────────────────────────────────┐
│                         HERMES GATEWAY                                      │
│                       ws://127.0.0.1:18789                                  │
│                                                                             │
│  ┌───────────────────────────────────────────────────────────────────────┐  │
│  │                        CHANNEL ROUTER                                 │  │
│  │   • Inbound message routing (channel → session)                       │  │
│  │   • Outbound message delivery (session → channel)                     │  │
│  │   • Allowlists, pairing, group policies                               │  │
│  └───────────────────────────────────────────────────────────────────────┘  │
│                                    │                                        │
│  ┌─────────────────────────────────▼─────────────────────────────────────┐  │
│  │                        SESSION MANAGER                                │  │
│  │   • Session lifecycle (create, load, save)                            │  │
│  │   • JSONL persistence                                                 │  │
│  │   • Context injection (bootstrap files, memory, skills)               │  │
│  │   • Compaction with memory flush                                      │  │
│  └───────────────────────────────────────────────────────────────────────┘  │
│                                    │                                        │
│  ┌─────────────────────────────────▼─────────────────────────────────────┐  │
│  │                     AGENT RUNTIME (LLMAgent)                          │  │
│  │                                                                       │  │
│  │   ┌───────────────────────────────────────────────────────────────┐   │  │
│  │   │                    ReAct Loop                                 │   │  │
│  │   │                                                               │   │  │
│  │   │   User Message + Context                                      │   │  │
│  │   │         │                                                     │   │  │
│  │   │         ▼                                                     │   │  │
│  │   │   ┌───────────┐    ┌───────────┐    ┌───────────┐            │   │  │
│  │   │   │   LLM     │───▶│   Tool    │───▶│  Result   │────┐       │   │  │
│  │   │   │  Reason   │    │  Execute  │    │  Observe  │    │       │   │  │
│  │   │   └───────────┘    └───────────┘    └───────────┘    │       │   │  │
│  │   │         ▲                                            │       │   │  │
│  │   │         └────────────────────────────────────────────┘       │   │  │
│  │   │                     (iterate until done)                      │   │  │
│  │   └───────────────────────────────────────────────────────────────┘   │  │
│  │                                                                       │  │
│  │   Tools: read │ write │ exec │ edit │ browser │ node.invoke │ ...    │  │
│  └───────────────────────────────────────────────────────────────────────┘  │
│                                                                             │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐        │
│  │  PROACTIVE  │  │   DEVICE    │  │  WEBSOCKET  │  │   SKILLS    │        │
│  │   ENGINE    │  │   NODES     │  │   SERVER    │  │   LOADER    │        │
│  │             │  │             │  │             │  │             │        │
│  │ • Cron      │  │ • Camera    │  │ • Clients   │  │ • Bundled   │        │
│  │ • Webhooks  │  │ • Location  │  │ • Control   │  │ • Workspace │        │
│  │ • Heartbeat │  │ • Voice     │  │ • Events    │  │ • Managed   │        │
│  │             │  │ • Browser   │  │             │  │             │        │
│  └─────────────┘  └─────────────┘  └─────────────┘  └─────────────┘        │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                         FILE SYSTEM (Workspace)                             │
│                                                                             │
│  ~/.hermes/                                                                 │
│  ├── config.json                 # Gateway configuration                    │
│  ├── workspace/                  # Agent workspace                          │
│  │   ├── AGENTS.md               # Operating instructions                   │
│  │   ├── SOUL.md                 # Persona, boundaries, tone                │
│  │   ├── TOOLS.md                # Tool usage notes                         │
│  │   ├── IDENTITY.md             # Agent name/vibe/emoji                    │
│  │   ├── USER.md                 # User profile                             │
│  │   ├── BOOTSTRAP.md            # First-run ritual (deleted after)         │
│  │   ├── MEMORY.md               # Long-term curated memory                 │
│  │   ├── memory/                 # Daily logs                               │
│  │   │   └── 2026-01-26.md                                                  │
│  │   └── skills/                 # Workspace skills (highest priority)      │
│  ├── sessions/                   # JSONL session transcripts                │
│  │   └── <session-id>.jsonl                                                 │
│  ├── skills/                     # Managed skills                           │
│  └── credentials/                # Channel auth tokens                      │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                           EXTERNAL SERVICES                                 │
│                                                                             │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐        │
│  │  LLM APIs   │  │  WhatsApp   │  │  Telegram   │  │   Device    │        │
│  │             │  │   (Baileys) │  │  (Bot API)  │  │   Nodes     │        │
│  │ • Anthropic │  │             │  │             │  │             │        │
│  │ • OpenAI    │  │             │  │             │  │ • macOS     │        │
│  │ • DeepSeek  │  │             │  │             │  │ • iOS       │        │
│  │ • Ollama    │  │             │  │             │  │ • Android   │        │
│  └─────────────┘  └─────────────┘  └─────────────┘  └─────────────┘        │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 3.2 Data Flow: User Message → Response

```
1. User sends message via WhatsApp
                │
                ▼
2. WhatsApp adapter receives message
                │
                ▼
3. Channel Router determines session
   • DM → main session
   • Group → isolated group session
                │
                ▼
4. Session Manager loads/creates session
   • Load JSONL transcript
   • Inject bootstrap files (first turn only)
   • Inject memory context
   • Inject skill overviews
                │
                ▼
5. Agent Runtime processes message
   • LLM reasons about response
   • Optionally calls tools (read, exec, browser, etc.)
   • ReAct loop until done
                │
                ▼
6. Session Manager saves session
   • Append to JSONL
   • Check compaction threshold
   • Trigger memory flush if needed
                │
                ▼
7. Channel Router sends response
   • Chunking for long messages
   • Typing indicators
                │
                ▼
8. User receives response on WhatsApp
```

---

## 4. Gateway Design

### 4.1 Gateway Responsibilities

The Gateway is the central daemon that:

1. **Owns all channel connections** — Single WhatsApp session, single Telegram bot, etc.
2. **Routes messages** — Inbound to sessions, outbound to channels
3. **Manages sessions** — Create, load, save, compact
4. **Runs agent** — LLM + tools execution
5. **Exposes WebSocket API** — For clients (CLI, web UI, apps)
6. **Executes proactive tasks** — Cron, webhooks, heartbeats
7. **Coordinates device nodes** — Route commands to connected devices

### 4.2 Gateway Configuration

```json
{
  "gateway": {
    "bind": "127.0.0.1",
    "port": 18789,
    "auth": {
      "token": "your-secret-token"
    }
  },
  "agent": {
    "model": "anthropic/claude-sonnet-4-20250514",
    "workspace": "~/.hermes/workspace",
    "thinkingLevel": "medium"
  },
  "channels": {
    "whatsapp": {
      "enabled": true,
      "allowFrom": ["+1234567890"],
      "groups": ["family-chat"]
    },
    "telegram": {
      "enabled": true,
      "botToken": "123456:ABCDEF"
    },
    "discord": {
      "enabled": false
    }
  },
  "cron": [
    {
      "name": "morning-briefing",
      "schedule": "0 8 * * *",
      "prompt": "Give me a morning briefing: weather, calendar, news."
    }
  ]
}
```

### 4.3 Gateway Lifecycle

```go
func main() {
    cfg := config.Load("~/.hermes/config.json")
    
    // Initialize components
    sessionMgr := session.NewFileManager("~/.hermes/sessions")
    memoryMgr := memory.NewFileManager(cfg.Agent.Workspace)
    skillLoader := skill.NewLoader(cfg.Agent.Workspace)
    
    // Initialize agent runtime
    agent := agent.New(
        agent.WithModel(cfg.Agent.Model),
        agent.WithWorkspace(cfg.Agent.Workspace),
        agent.WithSkills(skillLoader),
        agent.WithMemory(memoryMgr),
    )
    
    // Initialize channels
    channels := []channel.Channel{
        whatsapp.New(cfg.Channels.WhatsApp),
        telegram.New(cfg.Channels.Telegram),
        discord.New(cfg.Channels.Discord),
    }
    
    // Initialize proactive engine
    proactive := proactive.New(
        proactive.WithCron(cfg.Cron),
        proactive.WithWebhooks(cfg.Webhooks),
    )
    
    // Initialize WebSocket server
    wsServer := ws.NewServer(cfg.Gateway)
    
    // Create gateway
    gw := gateway.New(
        gateway.WithSessionManager(sessionMgr),
        gateway.WithAgent(agent),
        gateway.WithChannels(channels),
        gateway.WithProactive(proactive),
        gateway.WithWebSocket(wsServer),
    )
    
    // Run
    gw.Run(ctx)
}
```

---

## 5. Agent Runtime

### 5.1 Agent Architecture

The agent runtime is responsible for:

1. **Context assembly** — Combine system prompt, bootstrap files, memory, skills, history
2. **LLM interaction** — Send prompts, receive responses
3. **Tool execution** — Parse tool calls, execute, return results
4. **ReAct loop** — Iterate until final response

```go
type Agent struct {
    model       model.Model
    workspace   string
    skills      *skill.Loader
    memory      *memory.Manager
    tools       []tool.Tool
}

func (a *Agent) Run(ctx context.Context, session *Session, message string) (*Response, error) {
    // 1. Build context
    context := a.buildContext(session, message)
    
    // 2. ReAct loop
    for {
        // Call LLM
        response, err := a.model.Generate(ctx, context)
        if err != nil {
            return nil, err
        }
        
        // Check for tool calls
        if len(response.ToolCalls) == 0 {
            // Final response
            return &Response{Content: response.Content}, nil
        }
        
        // Execute tools
        for _, call := range response.ToolCalls {
            result := a.executeTool(ctx, call)
            context.AddToolResult(call.ID, result)
        }
        
        // Continue loop
    }
}
```

### 5.2 Built-in Tools

| Tool | Description |
|------|-------------|
| `read` | Read file contents |
| `write` | Write/create file |
| `edit` | Edit file with diff |
| `exec` | Execute shell command |
| `browser.navigate` | Navigate to URL |
| `browser.snapshot` | Get page accessibility tree |
| `browser.click` | Click element |
| `browser.type` | Type text |
| `node.invoke` | Execute command on device node |
| `memory_search` | Semantic search over memory files (hybrid vector + BM25) |
| `memory_write` | Write to MEMORY.md or daily logs |
| `sessions.list` | List active sessions |
| `sessions.send` | Send message to another session |

### 5.3 Model Configuration

```go
// Supported model providers
model := openai.New("gpt-4o", openai.WithAPIKey(key))
model := anthropic.New("claude-sonnet-4-20250514", anthropic.WithAPIKey(key))
model := deepseek.New("deepseek-chat", deepseek.WithAPIKey(key))
model := ollama.New("llama3", ollama.WithBaseURL("http://localhost:11434"))
```

---

## 6. Workspace & Bootstrap Files

### 6.1 Bootstrap File System

Hermes adopts Clawdbot's bootstrap file pattern. These files customize the agent's behavior without code changes.

| File | Purpose | Injection Timing |
|------|---------|------------------|
| `AGENTS.md` | Operating instructions, "how to behave" | Every session start |
| `SOUL.md` | Persona, boundaries, tone, ethics | Every session start |
| `TOOLS.md` | User notes on tool usage | Every session start |
| `IDENTITY.md` | Agent name, vibe, emoji | Every session start |
| `USER.md` | User profile, preferences | Every session start |
| `BOOTSTRAP.md` | First-run ritual | Once, then deleted |

### 6.2 Example: SOUL.md

```markdown
# Soul

You are Hermes, a personal AI assistant.

## Core Traits

- **Helpful**: You exist to make the user's life easier
- **Honest**: Never deceive or mislead
- **Humble**: Acknowledge limitations and uncertainties
- **Proactive**: Suggest relevant actions, don't just wait

## Tone

- Friendly but professional
- Concise, not verbose
- Use emoji sparingly 🦞

## Boundaries

- Never share user's private data externally
- Ask before taking irreversible actions
- Decline requests that could harm the user or others

## Memory

- Write important facts to MEMORY.md
- Use daily logs for running context
- If someone says "remember this," write it down
```

### 6.3 Example: IDENTITY.md

```markdown
# Identity

- **Name**: Hermes
- **Emoji**: 🪽
- **Vibe**: Helpful messenger, swift and reliable
- **Greeting**: "Hey! What can I help you with?"
```

### 6.4 Example: USER.md

```markdown
# User Profile

- **Name**: Alex
- **Timezone**: America/Los_Angeles
- **Language**: English
- **Preferred Address**: "Alex" (not "sir" or formal)

## Preferences

- Prefers concise responses
- Likes bullet points for lists
- Morning person, most active 6am-12pm

## Context

- Software engineer
- Working on personal AI projects
- Has oDroid and Raspberry Pi for home automation
```

### 6.5 Context Injection Logic

```go
func (a *Agent) buildContext(session *Session, message string) *Context {
    ctx := &Context{}
    
    // 1. System prompt (static)
    ctx.AddSystem(systemPrompt)
    
    // 2. Bootstrap files (if first turn or session start)
    if session.IsFirstTurn() {
        ctx.AddSystem(a.readFile("SOUL.md"))
        ctx.AddSystem(a.readFile("IDENTITY.md"))
        ctx.AddSystem(a.readFile("AGENTS.md"))
        ctx.AddSystem(a.readFile("TOOLS.md"))
        ctx.AddSystem(a.readFile("USER.md"))
        
        // One-time bootstrap
        if bootstrap := a.readFile("BOOTSTRAP.md"); bootstrap != "" {
            ctx.AddSystem(bootstrap)
            a.deleteFile("BOOTSTRAP.md") // Delete after use
        }
    }
    
    // 3. Memory context
    ctx.AddSystem(a.readFile("MEMORY.md"))
    ctx.AddSystem(a.readFile("memory/" + today() + ".md"))
    ctx.AddSystem(a.readFile("memory/" + yesterday() + ".md"))
    
    // 4. Skill overviews
    for _, skill := range a.skills.List() {
        ctx.AddSystem(fmt.Sprintf("<skill name=%q>%s</skill>", 
            skill.Name, skill.Description))
    }
    
    // 5. Conversation history
    ctx.AddHistory(session.History)
    
    // 6. Current message
    ctx.AddUser(message)
    
    return ctx
}
```

---

## 7. Memory System

### 7.1 Architecture Overview

Hermes implements a **multi-layer memory architecture** that balances simplicity with powerful retrieval:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         MEMORY ARCHITECTURE                                 │
│                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                    CONTEXT WINDOW (Injected)                        │    │
│  │                                                                     │    │
│  │  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐     │    │
│  │  │   MEMORY.md     │  │  today.md       │  │  yesterday.md   │     │    │
│  │  │   (curated)     │  │  (daily log)    │  │  (daily log)    │     │    │
│  │  └─────────────────┘  └─────────────────┘  └─────────────────┘     │    │
│  │                                                                     │    │
│  │  Automatically injected at session start (bounded size)             │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                    │                                        │
│                                    │ memory_search tool                     │
│                                    ▼                                        │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                    VECTOR INDEX (On-Demand Retrieval)               │    │
│  │                                                                     │    │
│  │  ┌─────────────────────────────────────────────────────────────┐   │    │
│  │  │                    sqlite-vec Database                       │   │    │
│  │  │                                                              │   │    │
│  │  │  memory/2025-11-01.md ──┐                                    │   │    │
│  │  │  memory/2025-12-15.md ──┼──► Chunked + Embedded + Indexed    │   │    │
│  │  │  memory/2026-01-20.md ──┤                                    │   │    │
│  │  │  MEMORY.md ─────────────┘                                    │   │    │
│  │  │                                                              │   │    │
│  │  │  Hybrid Search: Vector Similarity + BM25 Full-Text           │   │    │
│  │  └─────────────────────────────────────────────────────────────┘   │    │
│  │                                                                     │    │
│  │  Agent calls memory_search → retrieves relevant snippets            │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

**Key Insight**: Not all memory is injected. Only recent context (MEMORY.md + last 2 days) is automatically loaded. Older memories are **searchable on-demand** via the `memory_search` tool, preventing unbounded context growth.

### 7.2 File-Based Memory Storage

Memory is plain Markdown in the workspace. The LLM only "remembers" what gets written to disk.

```
workspace/
├── MEMORY.md              # Curated long-term memory (always injected in main session)
└── memory/
    ├── 2025-11-01.md      # Historical (searchable, not auto-injected)
    ├── 2025-12-15.md      # Historical (searchable, not auto-injected)
    ├── 2026-01-25.md      # Yesterday (auto-injected)
    └── 2026-01-26.md      # Today (auto-injected)
```

**Injection Rules**:

| File | When Injected | Session Type |
|------|---------------|--------------|
| `MEMORY.md` | Always at session start | Main session only (never in groups) |
| `memory/today.md` | Always at session start | All sessions |
| `memory/yesterday.md` | Always at session start | All sessions |
| Older daily logs | **Never auto-injected** | Searchable via `memory_search` |

### 7.3 MEMORY.md Structure

```markdown
# Long-Term Memory

## User Facts

- Alex is a software engineer in San Francisco
- Prefers TypeScript but learning Go
- Has two cats: Luna and Mochi

## Preferences

- Likes dark mode
- Prefers concise responses
- Uses 24-hour time format

## Projects

- Building personal AI assistant (this project!)
- Home automation with oDroid
- Learning Rust on weekends

## Important Dates

- Birthday: March 15
- Work anniversary: June 1
```

### 7.4 Daily Log Structure

```markdown
# 2026-01-26

## Morning

- Alex asked about weather → sunny, 65°F
- Reminded about dentist appointment at 2pm

## Afternoon

- Helped debug Go WebSocket code
- Alex mentioned feeling stressed about deadline

## Evening

- Created grocery list for tomorrow
- Set reminder for morning standup
```

### 7.5 Vector Search with sqlite-vec

For historical memory retrieval, Hermes uses **sqlite-vec** — a lightweight SQLite extension for vector search that runs anywhere (including Raspberry Pi).

#### Why sqlite-vec?

| Feature | sqlite-vec | PostgreSQL + pgvector |
|---------|------------|----------------------|
| Dependencies | Zero (single file) | PostgreSQL server |
| Memory footprint | ~30MB | 100MB+ |
| Raspberry Pi | ✅ Native ARM support | ⚠️ Heavy |
| Setup complexity | None | Database admin |
| Performance (100k vectors) | <100ms queries | <50ms queries |
| Suitable for | Local-first, single-user | Multi-tenant, enterprise |

#### Go Integration

```go
import (
    "database/sql"
    sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
    _ "github.com/mattn/go-sqlite3"
)

// Initialize sqlite-vec extension
func init() {
    sqlite_vec.Auto()
}

type VectorIndex struct {
    db        *sql.DB
    embedder  Embedder
    dimension int
}

func NewVectorIndex(dbPath string, embedder Embedder) (*VectorIndex, error) {
    db, err := sql.Open("sqlite3", dbPath)
    if err != nil {
        return nil, err
    }
    
    // Create vector table
    _, err = db.Exec(`
        CREATE VIRTUAL TABLE IF NOT EXISTS memory_chunks USING vec0(
            embedding float[?],
            +file_path TEXT,
            +line_start INTEGER,
            +line_end INTEGER,
            +content TEXT,
            +updated_at TEXT
        )
    `, embedder.Dimension())
    
    // Create FTS5 table for BM25 keyword search
    _, err = db.Exec(`
        CREATE VIRTUAL TABLE IF NOT EXISTS memory_fts USING fts5(
            content,
            file_path,
            chunk_id UNINDEXED
        )
    `)
    
    return &VectorIndex{db: db, embedder: embedder, dimension: embedder.Dimension()}, nil
}
```

### 7.6 Hybrid Search (Vector + BM25)

Hermes implements **hybrid search** combining:
- **Vector similarity**: Semantic matching ("Go project" finds "Golang initiative")
- **BM25 keyword search**: Exact token matching (IDs, code symbols, error messages)

```go
type SearchResult struct {
    FilePath   string
    LineStart  int
    LineEnd    int
    Content    string
    Score      float64
    MatchType  string // "vector", "keyword", or "hybrid"
}

type HybridSearchConfig struct {
    VectorWeight       float64 // Default: 0.7
    TextWeight         float64 // Default: 0.3
    CandidateMultiplier int    // Default: 4 (fetch 4x candidates, then merge)
}

func (vi *VectorIndex) HybridSearch(ctx context.Context, query string, limit int, cfg HybridSearchConfig) ([]SearchResult, error) {
    // 1. Get query embedding
    queryVec, err := vi.embedder.Embed(ctx, query)
    if err != nil {
        return nil, err
    }
    
    // 2. Vector search (semantic)
    vectorResults, err := vi.vectorSearch(ctx, queryVec, limit*cfg.CandidateMultiplier)
    if err != nil {
        return nil, err
    }
    
    // 3. BM25 keyword search
    keywordResults, err := vi.keywordSearch(ctx, query, limit*cfg.CandidateMultiplier)
    if err != nil {
        // Fall back to vector-only if FTS fails
        return vi.mergeResults(vectorResults, nil, limit, cfg), nil
    }
    
    // 4. Merge with Reciprocal Rank Fusion (RRF)
    return vi.mergeResultsRRF(vectorResults, keywordResults, limit, cfg), nil
}

func (vi *VectorIndex) vectorSearch(ctx context.Context, queryVec []float32, limit int) ([]SearchResult, error) {
    rows, err := vi.db.QueryContext(ctx, `
        SELECT file_path, line_start, line_end, content, distance
        FROM memory_chunks
        WHERE embedding MATCH ?
        ORDER BY distance
        LIMIT ?
    `, sqlite_vec.SerializeFloat32(queryVec), limit)
    // ... process rows
}

func (vi *VectorIndex) keywordSearch(ctx context.Context, query string, limit int) ([]SearchResult, error) {
    rows, err := vi.db.QueryContext(ctx, `
        SELECT file_path, content, bm25(memory_fts) as score
        FROM memory_fts
        WHERE memory_fts MATCH ?
        ORDER BY score
        LIMIT ?
    `, query, limit)
    // ... process rows
}

// Reciprocal Rank Fusion for combining ranked lists
func (vi *VectorIndex) mergeResultsRRF(vector, keyword []SearchResult, limit int, cfg HybridSearchConfig) []SearchResult {
    const k = 60 // RRF constant
    scores := make(map[string]float64)
    results := make(map[string]SearchResult)
    
    // Score vector results
    for rank, r := range vector {
        key := fmt.Sprintf("%s:%d", r.FilePath, r.LineStart)
        scores[key] += cfg.VectorWeight * (1.0 / float64(k+rank+1))
        results[key] = r
    }
    
    // Score keyword results
    for rank, r := range keyword {
        key := fmt.Sprintf("%s:%d", r.FilePath, r.LineStart)
        scores[key] += cfg.TextWeight * (1.0 / float64(k+rank+1))
        if _, exists := results[key]; !exists {
            results[key] = r
        }
    }
    
    // Sort by combined score and return top-k
    // ...
}
```

### 7.7 Embedding Providers

Hermes supports multiple embedding providers, with automatic fallback:

```go
// Embedder interface (compatible with tRPC-Agent-Go's knowledge module)
type Embedder interface {
    Embed(ctx context.Context, text string) ([]float32, error)
    EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
    Dimension() int
}

// Provider priority (configurable)
// 1. Local (Ollama) — zero network, privacy-preserving
// 2. Remote (OpenAI, Gemini) — higher quality, requires API key

// Local embedding via Ollama
type OllamaEmbedder struct {
    baseURL string
    model   string // e.g., "nomic-embed-text", "mxbai-embed-large"
}

func (e *OllamaEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
    resp, err := http.Post(e.baseURL+"/api/embeddings", "application/json", 
        bytes.NewReader([]byte(fmt.Sprintf(`{"model":"%s","prompt":"%s"}`, e.model, text))))
    // ... parse response
}

// Remote embedding via OpenAI
type OpenAIEmbedder struct {
    apiKey string
    model  string // e.g., "text-embedding-3-small"
}

// Leverage tRPC-Agent-Go's embedder implementations
import "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"

func NewEmbedder(cfg EmbedderConfig) (Embedder, error) {
    switch cfg.Provider {
    case "ollama":
        return embedder.NewOllama(cfg.Model, embedder.WithBaseURL(cfg.BaseURL))
    case "openai":
        return embedder.NewOpenAI(cfg.Model, embedder.WithAPIKey(cfg.APIKey))
    case "gemini":
        return embedder.NewGemini(cfg.Model, embedder.WithAPIKey(cfg.APIKey))
    default:
        return nil, fmt.Errorf("unknown embedder provider: %s", cfg.Provider)
    }
}
```

### 7.8 Memory Indexing Pipeline

```go
type MemoryIndexer struct {
    index     *VectorIndex
    embedder  Embedder
    workspace string
    watcher   *fsnotify.Watcher
}

// Index all memory files
func (mi *MemoryIndexer) IndexAll(ctx context.Context) error {
    files, _ := filepath.Glob(filepath.Join(mi.workspace, "memory", "*.md"))
    files = append(files, filepath.Join(mi.workspace, "MEMORY.md"))
    
    for _, file := range files {
        if err := mi.indexFile(ctx, file); err != nil {
            log.Warn("failed to index file", "file", file, "error", err)
        }
    }
    return nil
}

// Index a single file with chunking
func (mi *MemoryIndexer) indexFile(ctx context.Context, filePath string) error {
    content, err := os.ReadFile(filePath)
    if err != nil {
        return err
    }
    
    // Chunk with ~400 tokens, 80 token overlap
    chunks := mi.chunkMarkdown(string(content), 400, 80)
    
    // Embed chunks in batch
    texts := make([]string, len(chunks))
    for i, c := range chunks {
        texts[i] = c.Content
    }
    embeddings, err := mi.embedder.EmbedBatch(ctx, texts)
    if err != nil {
        return err
    }
    
    // Store in vector index
    for i, chunk := range chunks {
        mi.index.Insert(ctx, MemoryChunk{
            FilePath:  filePath,
            LineStart: chunk.LineStart,
            LineEnd:   chunk.LineEnd,
            Content:   chunk.Content,
            Embedding: embeddings[i],
        })
    }
    
    return nil
}

// Watch for file changes (debounced)
func (mi *MemoryIndexer) Watch(ctx context.Context) error {
    mi.watcher.Add(filepath.Join(mi.workspace, "memory"))
    mi.watcher.Add(filepath.Join(mi.workspace, "MEMORY.md"))
    
    debouncer := time.NewTimer(0)
    <-debouncer.C // drain initial
    
    for {
        select {
        case event := <-mi.watcher.Events:
            if event.Op&(fsnotify.Write|fsnotify.Create) != 0 {
                debouncer.Reset(1500 * time.Millisecond)
            }
        case <-debouncer.C:
            mi.IndexAll(ctx) // Re-index changed files
        case <-ctx.Done():
            return nil
        }
    }
}
```

### 7.9 Memory Tools

The agent uses these tools to interact with memory:

```go
// memory_search — semantic search over all memory files
type MemorySearchTool struct {
    index *VectorIndex
}

func (t *MemorySearchTool) Definition() tool.Definition {
    return tool.Definition{
        Name:        "memory_search",
        Description: "Search through memory files for relevant information. Use when you need to recall past conversations, user preferences, or historical context.",
        Parameters: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "query": map[string]any{
                    "type":        "string",
                    "description": "Natural language search query",
                },
                "limit": map[string]any{
                    "type":        "integer",
                    "description": "Maximum number of results (default: 5)",
                    "default":     5,
                },
            },
            "required": []string{"query"},
        },
    }
}

func (t *MemorySearchTool) Execute(ctx context.Context, params map[string]any) (string, error) {
    query := params["query"].(string)
    limit := 5
    if l, ok := params["limit"].(float64); ok {
        limit = int(l)
    }
    
    results, err := t.index.HybridSearch(ctx, query, limit, DefaultHybridConfig)
    if err != nil {
        return "", err
    }
    
    // Format results for LLM
    var sb strings.Builder
    for _, r := range results {
        sb.WriteString(fmt.Sprintf("## %s (lines %d-%d)\n", r.FilePath, r.LineStart, r.LineEnd))
        sb.WriteString(r.Content)
        sb.WriteString("\n\n")
    }
    return sb.String(), nil
}

// memory_write — write to memory files
type MemoryWriteTool struct {
    workspace string
}

func (t *MemoryWriteTool) Execute(ctx context.Context, params map[string]any) (string, error) {
    file := params["file"].(string) // "MEMORY.md" or "memory/2026-01-26.md"
    content := params["content"].(string)
    append := params["append"].(bool)
    
    path := filepath.Join(t.workspace, file)
    
    // Validate path is within workspace/memory
    if !strings.HasPrefix(path, t.workspace) {
        return "", fmt.Errorf("invalid memory path: %s", file)
    }
    
    if append {
        f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
        if err != nil {
            return "", err
        }
        defer f.Close()
        f.WriteString("\n" + content)
    } else {
        os.WriteFile(path, []byte(content), 0644)
    }
    
    return fmt.Sprintf("Written to %s", file), nil
}
```

### 7.10 Context Injection with Memory

```go
func (a *Agent) buildContext(session *Session, message string) *Context {
    ctx := &Context{}
    
    // 1. System prompt (static)
    ctx.AddSystem(systemPrompt)
    
    // 2. Bootstrap files (if first turn)
    if session.IsFirstTurn() {
        ctx.AddSystem(a.readFile("SOUL.md"))
        ctx.AddSystem(a.readFile("IDENTITY.md"))
        ctx.AddSystem(a.readFile("AGENTS.md"))
        ctx.AddSystem(a.readFile("TOOLS.md"))
        ctx.AddSystem(a.readFile("USER.md"))
    }
    
    // 3. Memory context (bounded, always injected)
    // Only MEMORY.md + today + yesterday — NOT all historical logs
    if session.Type == SessionTypeMain {
        ctx.AddSystem(wrapSection("Long-Term Memory", a.readFile("MEMORY.md")))
    }
    today := time.Now().Format("2006-01-02")
    yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
    ctx.AddSystem(wrapSection("Today's Log", a.readFile("memory/"+today+".md")))
    ctx.AddSystem(wrapSection("Yesterday's Log", a.readFile("memory/"+yesterday+".md")))
    
    // 4. Skill overviews (compact XML)
    ctx.AddSystem(a.formatSkillOverviews())
    
    // 5. Available tools (including memory_search for historical retrieval)
    ctx.AddSystem(a.formatToolDefinitions())
    
    // 6. Conversation history
    ctx.AddHistory(session.History)
    
    // 7. Current message
    ctx.AddUser(message)
    
    return ctx
}

func wrapSection(title, content string) string {
    if content == "" {
        return ""
    }
    return fmt.Sprintf("<%s>\n%s\n</%s>", toSnakeCase(title), content, toSnakeCase(title))
}
```

### 7.11 Memory Flush Before Compaction

When session context approaches compaction threshold, trigger memory flush:

```go
func (s *SessionManager) checkCompaction(session *Session) {
    tokens := s.estimateTokens(session)
    threshold := s.config.ContextWindow - s.config.ReserveTokens
    flushThreshold := s.config.FlushThreshold // Default: 4000 tokens before compaction
    
    // Soft threshold: flush memory before compaction
    if tokens > threshold-flushThreshold && !session.MemoryFlushed {
        // Trigger silent memory flush
        s.agent.Run(session, memoryFlushPrompt)
        session.MemoryFlushed = true
    }
    
    // Hard threshold: compact session
    if tokens > threshold {
        s.compact(session)
        session.MemoryFlushed = false // Reset for next cycle
    }
}

const memoryFlushPrompt = `<system_notice>
Session nearing compaction. Before context is summarized, store any important 
information to MEMORY.md (for durable facts) or memory/%s.md (for today's notes).

Rules:
- Write decisions, preferences, and important facts to MEMORY.md
- Write day-to-day context to memory/%s.md  
- If nothing important to store, reply with NO_REPLY
- Do not mention this process to the user
</system_notice>`
```

### 7.12 Configuration

```json
{
  "memory": {
    "workspace": "~/.hermes/workspace",
    "search": {
      "enabled": true,
      "provider": "ollama",
      "model": "nomic-embed-text",
      "fallback": "openai",
      "hybrid": {
        "enabled": true,
        "vectorWeight": 0.7,
        "textWeight": 0.3
      }
    },
    "index": {
      "path": "~/.hermes/memory.sqlite",
      "chunkSize": 400,
      "chunkOverlap": 80,
      "watchDebounceMs": 1500
    },
    "injection": {
      "memoryMd": true,
      "dailyLogDays": 2
    }
  },
  "compaction": {
    "reserveTokens": 20000,
    "flushThreshold": 4000,
    "memoryFlush": {
      "enabled": true,
      "silent": true
    }
  }
}
```

### 7.13 Leveraging tRPC-Agent-Go

Hermes leverages tRPC-Agent-Go's **Knowledge module** for RAG capabilities:

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
    "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
    "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

// For simple deployments: use tRPC-Agent-Go's in-memory vector store
func NewSimpleMemoryIndex(cfg Config) (*knowledge.Knowledge, error) {
    // Create embedder
    emb, err := embedder.NewOpenAI("text-embedding-3-small",
        embedder.WithAPIKey(cfg.OpenAIKey))
    if err != nil {
        return nil, err
    }
    
    // Create in-memory vector store
    store := inmemory.New(emb)
    
    // Create knowledge instance
    k := knowledge.New(
        knowledge.WithEmbedder(emb),
        knowledge.WithVectorStore(store),
        knowledge.WithChunkSize(400),
        knowledge.WithChunkOverlap(80),
    )
    
    // Load memory files
    k.Load(ctx, source.NewDirectory(cfg.Workspace+"/memory"))
    k.Load(ctx, source.NewFile(cfg.Workspace+"/MEMORY.md"))
    
    return k, nil
}

// For persistent deployments: use sqlite-vec (our custom implementation)
// See VectorIndex implementation above
```

**When to use which:**

| Scenario | Recommendation |
|----------|----------------|
| Development/testing | tRPC-Agent-Go in-memory store |
| Raspberry Pi / oDroid | sqlite-vec (persistent, low memory) |
| Desktop with Ollama | sqlite-vec + Ollama embeddings |
| Cloud deployment | tRPC-Agent-Go with Qdrant or Milvus |

---

## 8. Session Management

### 8.1 JSONL Session Format

Sessions are stored as JSONL (JSON Lines) files:

```
~/.hermes/sessions/
├── main.jsonl                    # Main DM session
├── wa-group-family.jsonl         # WhatsApp group
├── tg-123456789.jsonl            # Telegram user
└── discord-guild-chan.jsonl      # Discord channel
```

### 8.2 JSONL Entry Structure

```json
{"type":"user","content":"What's the weather?","timestamp":"2026-01-26T10:00:00Z","channel":"whatsapp","from":"+1234567890"}
{"type":"assistant","content":"Let me check...","timestamp":"2026-01-26T10:00:01Z"}
{"type":"tool_call","id":"tc_1","name":"exec","input":{"command":"curl wttr.in/SF?format=3"}}
{"type":"tool_result","id":"tc_1","output":"SF: ☀️ +65°F"}
{"type":"assistant","content":"It's sunny and 65°F in San Francisco!","timestamp":"2026-01-26T10:00:02Z"}
```

### 8.3 Session Manager Interface

```go
type SessionManager interface {
    // Load or create session
    Load(ctx context.Context, sessionID string) (*Session, error)
    
    // Save session (append to JSONL)
    Save(ctx context.Context, session *Session) error
    
    // List all sessions
    List(ctx context.Context) ([]SessionMeta, error)
    
    // Delete session
    Delete(ctx context.Context, sessionID string) error
    
    // Compact session (summarize and trim)
    Compact(ctx context.Context, session *Session) error
    
    // Reset session (clear history)
    Reset(ctx context.Context, sessionID string) error
}
```

### 8.4 Session Routing

| Source | Session ID Pattern | Isolation |
|--------|-------------------|-----------|
| WhatsApp DM | `main` | Shared main session |
| WhatsApp Group | `wa-group-{groupId}` | Isolated per group |
| Telegram DM | `main` | Shared main session |
| Telegram Group | `tg-group-{chatId}` | Isolated per group |
| Discord DM | `main` | Shared main session |
| Discord Channel | `discord-{guildId}-{channelId}` | Isolated per channel |
| WebChat | `webchat-{clientId}` | Per client |
| CLI | `cli` | Dedicated CLI session |

---

## 9. Skills System

### 9.1 Skills Overview

Skills provide open-ended extensibility without recompilation. Compatible with Anthropic's Agent Skills specification.

### 9.2 Skill Loading Priority

```
1. Workspace skills:  ~/.hermes/workspace/skills/  (highest)
2. Managed skills:    ~/.hermes/skills/
3. Bundled skills:    embedded in binary             (lowest)
```

### 9.3 SKILL.md Format

```yaml
---
name: web-search
description: Search the web using DuckDuckGo and summarize results
metadata:
  requires:
    bins: ["curl", "jq"]
    env: []
  os: ["darwin", "linux"]
---

# Web Search Skill

Use this skill when the user asks to search the web or find information online.

## Usage

Run the search script with a query:

```bash
./scripts/search.sh "your search query"
```

## Output

Returns JSON with search results:

```json
{
  "results": [
    {"title": "...", "url": "...", "snippet": "..."}
  ]
}
```

## Guidelines

- Summarize results, don't just dump JSON
- Cite sources with URLs
- If no good results, say so
```

### 9.4 Skill Injection

Skills are injected as compact XML in the system prompt:

```xml
<skills>
  <skill name="web-search">Search the web using DuckDuckGo</skill>
  <skill name="calendar">Manage Google Calendar events</skill>
  <skill name="email">Send and read emails via Gmail</skill>
</skills>
```

When the agent needs a skill, it reads the full SKILL.md and follows instructions.

### 9.5 Skill Execution

Skills execute via the standard `exec` tool:

```go
// Agent decides to use web-search skill
// 1. Read skill instructions
toolCall{name: "read", input: {path: "skills/web-search/SKILL.md"}}

// 2. Execute skill script
toolCall{name: "exec", input: {command: "./skills/web-search/scripts/search.sh 'golang websocket'"}}
```

---

## 10. Channel Integrations

### 10.1 Supported Channels

| Channel | Library/Protocol | Status |
|---------|------------------|--------|
| WhatsApp | go-whatsapp (Baileys port) | P0 |
| Telegram | telegram-bot-api | P0 |
| Discord | discordgo | P1 |
| Slack | slack-go | P1 |
| Signal | signal-cli (exec) | P2 |
| iMessage | imessage-exporter (macOS only) | P2 |
| WebChat | Built-in WebSocket | P0 |

### 10.2 Channel Adapter Interface

```go
type Channel interface {
    // Name returns the channel identifier
    Name() string
    
    // Connect establishes connection to the service
    Connect(ctx context.Context) error
    
    // Disconnect cleanly closes the connection
    Disconnect(ctx context.Context) error
    
    // Receive returns a channel of incoming messages
    Receive() <-chan IncomingMessage
    
    // Send delivers a message to a recipient
    Send(ctx context.Context, msg OutgoingMessage) error
    
    // SendTyping shows typing indicator
    SendTyping(ctx context.Context, recipient string) error
}

type IncomingMessage struct {
    ID        string
    From      string
    Content   string
    Channel   string
    Group     *string  // nil for DMs
    Timestamp time.Time
    Media     []Media  // images, audio, etc.
}

type OutgoingMessage struct {
    To      string
    Content string
    ReplyTo *string  // optional reply reference
    Media   []Media
}
```

### 10.3 Channel Configuration

```json
{
  "channels": {
    "whatsapp": {
      "enabled": true,
      "allowFrom": ["+1234567890", "+0987654321"],
      "dmPolicy": "pairing",
      "groups": {
        "family-chat": {"requireMention": false},
        "*": {"requireMention": true}
      }
    },
    "telegram": {
      "enabled": true,
      "botToken": "123456:ABCDEF",
      "allowFrom": ["@username"],
      "webhookUrl": "https://example.com/webhook/telegram"
    }
  }
}
```

### 10.4 DM Pairing Flow

For unknown senders (security):

```
1. Unknown user sends message
2. Gateway generates pairing code
3. Reply: "Send this code to verify: ABC123"
4. User runs: hermes pairing approve whatsapp ABC123
5. User added to allowlist
6. Future messages processed normally
```

---

## 11. Proactive Engine

### 11.1 Proactive Capabilities

The agent can initiate contact, not just respond:

| Feature | Trigger | Use Case |
|---------|---------|----------|
| **Cron Jobs** | Schedule (crontab format) | Morning briefings, daily summaries |
| **Webhooks** | External HTTP POST | GitHub notifications, IoT events |
| **Heartbeats** | Periodic interval | Inbox monitoring, health checks |

### 11.2 Cron Configuration

```json
{
  "cron": [
    {
      "name": "morning-briefing",
      "schedule": "0 8 * * *",
      "prompt": "Good morning! Give me a briefing: weather, calendar for today, any important emails.",
      "channel": "whatsapp",
      "to": "+1234567890"
    },
    {
      "name": "weekly-review",
      "schedule": "0 18 * * FRI",
      "prompt": "It's Friday! Summarize what we accomplished this week based on our conversations.",
      "channel": "telegram"
    }
  ]
}
```

### 11.3 Webhook Configuration

```json
{
  "webhooks": {
    "enabled": true,
    "port": 18790,
    "endpoints": [
      {
        "path": "/github",
        "secret": "webhook-secret",
        "prompt": "GitHub event received: {{.payload}}. Summarize what happened.",
        "channel": "discord",
        "to": "dev-channel"
      }
    ]
  }
}
```

### 11.4 Heartbeat Configuration

```json
{
  "heartbeats": [
    {
      "name": "email-monitor",
      "interval": "5m",
      "prompt": "Check for new important emails. Only notify me if something urgent.",
      "condition": "result contains 'URGENT'",
      "channel": "whatsapp"
    }
  ]
}
```

### 11.5 Proactive Engine Implementation

```go
type ProactiveEngine struct {
    cron       *cron.Cron
    webhookSrv *http.Server
    heartbeats []*Heartbeat
    agent      *Agent
    channels   map[string]Channel
}

func (p *ProactiveEngine) Start(ctx context.Context) error {
    // Start cron scheduler
    p.cron.Start()
    
    // Start webhook server
    go p.webhookSrv.ListenAndServe()
    
    // Start heartbeat goroutines
    for _, hb := range p.heartbeats {
        go p.runHeartbeat(ctx, hb)
    }
    
    return nil
}

func (p *ProactiveEngine) runCronJob(job CronJob) {
    // Create dedicated session for cron job
    session := p.sessionMgr.Create("cron-" + job.Name)
    
    // Run agent with cron prompt
    response, err := p.agent.Run(ctx, session, job.Prompt)
    if err != nil {
        log.Error("cron job failed", "name", job.Name, "error", err)
        return
    }
    
    // Send response to configured channel
    channel := p.channels[job.Channel]
    channel.Send(ctx, OutgoingMessage{
        To:      job.To,
        Content: response.Content,
    })
}
```

---

## 12. Device Nodes

### 12.1 Node Architecture

Device nodes connect to the Gateway via WebSocket and expose device-specific capabilities:

```
┌─────────────┐     WebSocket      ┌─────────────────┐
│   Gateway   │◄──────────────────►│   macOS Node    │
│             │                    │                 │
│             │                    │ • camera.snap   │
│             │                    │ • screen.record │
│             │                    │ • browser.*     │
│             │                    │ • system.notify │
└─────────────┘                    └─────────────────┘
       ▲
       │ WebSocket
       ▼
┌─────────────────┐
│   iOS Node      │
│                 │
│ • camera.snap   │
│ • location.get  │
│ • voice.listen  │
└─────────────────┘
```

### 12.2 Node Registration

Nodes connect with `role: node` and declare capabilities:

```json
{
  "type": "req",
  "method": "connect",
  "params": {
    "role": "node",
    "nodeId": "macbook-pro",
    "platform": "darwin",
    "capabilities": ["camera", "screen", "browser", "system"],
    "commands": [
      {"name": "camera.snap", "description": "Take a photo"},
      {"name": "camera.record", "description": "Record video clip"},
      {"name": "screen.record", "description": "Record screen"},
      {"name": "browser.navigate", "description": "Navigate to URL"},
      {"name": "browser.snapshot", "description": "Get page content"},
      {"name": "system.notify", "description": "Show notification"}
    ]
  }
}
```

### 12.3 Node Invocation

The agent invokes node commands via `node.invoke`:

```json
{
  "tool": "node.invoke",
  "input": {
    "nodeId": "macbook-pro",
    "command": "camera.snap",
    "params": {}
  }
}
```

Gateway routes to the appropriate node:

```go
func (g *Gateway) handleNodeInvoke(ctx context.Context, call ToolCall) (string, error) {
    nodeID := call.Input["nodeId"].(string)
    command := call.Input["command"].(string)
    params := call.Input["params"]
    
    node, ok := g.nodes[nodeID]
    if !ok {
        return "", fmt.Errorf("node not found: %s", nodeID)
    }
    
    // Send command to node
    result, err := node.Invoke(ctx, command, params)
    if err != nil {
        return "", err
    }
    
    return result, nil
}
```

### 12.4 Node Capabilities

| Capability | Commands | Platforms |
|------------|----------|-----------|
| **camera** | `camera.snap`, `camera.record` | macOS, iOS, Android |
| **screen** | `screen.record`, `screen.snapshot` | macOS, Windows, Linux |
| **location** | `location.get` | iOS, Android, macOS |
| **browser** | `browser.navigate`, `browser.snapshot`, `browser.click`, `browser.type` | macOS, Windows, Linux |
| **system** | `system.notify`, `system.run` | All |
| **voice** | `voice.listen`, `voice.speak` | macOS, iOS, Android |

### 12.5 Browser Control

For browser automation on macOS/desktop:

```go
// Browser controller using Chrome DevTools Protocol
type BrowserController struct {
    conn *chromedp.Context
}

func (b *BrowserController) Navigate(url string) error {
    return chromedp.Run(b.conn, chromedp.Navigate(url))
}

func (b *BrowserController) Snapshot() (string, error) {
    var html string
    err := chromedp.Run(b.conn, chromedp.OuterHTML("html", &html))
    return html, err
}

func (b *BrowserController) Click(selector string) error {
    return chromedp.Run(b.conn, chromedp.Click(selector))
}
```

---

## 13. WebSocket Protocol

### 13.1 Protocol Overview

- **Transport**: WebSocket, text frames with JSON payloads
- **Endpoint**: `ws://127.0.0.1:18789`
- **Authentication**: Token-based via `connect` handshake

### 13.2 Frame Types

| Type | Direction | Purpose |
|------|-----------|---------|
| `req` | Client → Server | Request with method and params |
| `res` | Server → Client | Response to request |
| `event` | Server → Client | Async events (agent output, presence) |

### 13.3 Request/Response Format

```json
// Request
{
  "type": "req",
  "id": "req-123",
  "method": "agent",
  "params": {
    "sessionId": "main",
    "message": "What's the weather?"
  }
}

// Response (success)
{
  "type": "res",
  "id": "req-123",
  "ok": true,
  "payload": {
    "runId": "run-456",
    "status": "accepted"
  }
}

// Response (error)
{
  "type": "res",
  "id": "req-123",
  "ok": false,
  "error": {
    "code": "INVALID_SESSION",
    "message": "Session not found"
  }
}
```

### 13.4 Event Format

```json
{
  "type": "event",
  "event": "agent",
  "payload": {
    "runId": "run-456",
    "type": "text_delta",
    "content": "The weather in "
  }
}
```

### 13.5 Methods

| Method | Description |
|--------|-------------|
| `connect` | Initial handshake with auth |
| `agent` | Send message to agent |
| `status` | Get gateway status |
| `health` | Health check |
| `sessions.list` | List all sessions |
| `sessions.get` | Get session details |
| `sessions.reset` | Reset a session |
| `send` | Send message to channel |
| `nodes.list` | List connected nodes |
| `nodes.invoke` | Invoke node command |

### 13.6 Event Types

| Event | Description |
|-------|-------------|
| `agent` | Agent output (streaming) |
| `presence` | Online/offline status |
| `message` | New message received |
| `typing` | Typing indicator |
| `health` | Health status update |
| `node.connected` | Node came online |
| `node.disconnected` | Node went offline |

### 13.7 Connection Lifecycle

```
Client                           Gateway
   │                                │
   │──── req:connect ──────────────►│
   │     {auth: {token: "..."}}     │
   │                                │
   │◄─── res:connect ───────────────│
   │     {ok: true, payload: {...}} │
   │                                │
   │◄─── event:presence ────────────│
   │◄─── event:health ──────────────│
   │                                │
   │──── req:agent ────────────────►│
   │     {sessionId, message}       │
   │                                │
   │◄─── res:agent ─────────────────│
   │     {runId, status: "accepted"}│
   │                                │
   │◄─── event:agent (streaming) ───│
   │◄─── event:agent (streaming) ───│
   │◄─── event:agent (complete) ────│
   │                                │
```

---

## 14. Security Model

### 14.1 Security Layers

| Layer | Mechanism |
|-------|-----------|
| **Gateway Auth** | Token-based authentication |
| **Channel Allowlists** | Per-channel sender restrictions |
| **DM Pairing** | Verification for unknown senders |
| **Sandbox** | Docker isolation for non-main sessions |
| **Tool Policy** | Allow/deny lists for tools |

### 14.2 Gateway Authentication

```json
{
  "gateway": {
    "auth": {
      "mode": "token",
      "token": "your-secret-token"
    }
  }
}
```

All WebSocket clients must provide token in `connect`:

```json
{
  "type": "req",
  "method": "connect",
  "params": {
    "auth": {"token": "your-secret-token"}
  }
}
```

### 14.3 Channel Allowlists

```json
{
  "channels": {
    "whatsapp": {
      "allowFrom": ["+1234567890"],
      "dmPolicy": "pairing"
    }
  }
}
```

- `allowFrom`: Only listed senders can interact
- `dmPolicy`: `"pairing"` requires verification, `"open"` allows all

### 14.4 Sandbox Mode

For group/channel sessions (untrusted input):

```json
{
  "agent": {
    "sandbox": {
      "mode": "non-main",
      "docker": {
        "image": "hermes-sandbox:latest",
        "networkDisabled": true,
        "readOnlyRoot": true
      }
    }
  }
}
```

### 14.5 Tool Policy

```json
{
  "agent": {
    "tools": {
      "allow": ["read", "write", "exec", "browser.*"],
      "deny": ["node.invoke"],
      "elevated": {
        "enabled": true,
        "commands": ["rm -rf", "sudo"]
      }
    }
  }
}
```

---

## 15. Observability

### 15.1 Logging

Structured JSON logging to stdout/file:

```json
{
  "level": "info",
  "ts": "2026-01-26T10:00:00Z",
  "msg": "message received",
  "channel": "whatsapp",
  "from": "+1234567890",
  "sessionId": "main"
}
```

### 15.2 Metrics

Prometheus-compatible metrics:

| Metric | Type | Description |
|--------|------|-------------|
| `hermes_messages_total` | Counter | Total messages processed |
| `hermes_agent_runs_total` | Counter | Agent invocations |
| `hermes_tool_calls_total` | Counter | Tool executions |
| `hermes_tokens_total` | Counter | LLM tokens used |
| `hermes_session_count` | Gauge | Active sessions |
| `hermes_node_count` | Gauge | Connected nodes |

### 15.3 Health Endpoint

```
GET http://127.0.0.1:18789/health

{
  "status": "healthy",
  "version": "0.1.0",
  "uptime": "24h15m",
  "channels": {
    "whatsapp": "connected",
    "telegram": "connected"
  },
  "nodes": ["macbook-pro", "iphone"],
  "sessions": 5
}
```

---

## 16. Development Phases

### Phase 1: Foundation (Weeks 1-8)

| Task | Description |
|------|-------------|
| Project setup | Go module, directory structure, CI/CD |
| WebSocket server | JSON protocol, connection handling |
| Agent runtime | LLM integration, ReAct loop, basic tools |
| Session manager | JSONL persistence, load/save |
| File-based memory | MEMORY.md, daily logs |
| Bootstrap files | SOUL.md, AGENTS.md, etc. |
| CLI | `hermes gateway`, `hermes agent`, `hermes status` |

**Deliverable**: Working agent via CLI and WebSocket

### Phase 2: Channels (Weeks 9-14)

| Task | Description |
|------|-------------|
| WhatsApp integration | go-whatsapp, pairing, groups |
| Telegram integration | Bot API, webhooks |
| Channel router | Message routing, session mapping |
| Allowlists/pairing | Security policies |
| WebChat | Browser-based client |

**Deliverable**: Multi-channel messaging

### Phase 3: Skills & Proactive (Weeks 15-20)

| Task | Description |
|------|-------------|
| Skill loader | Bundled, managed, workspace skills |
| Skill execution | Integration with exec tool |
| Cron engine | Scheduled tasks |
| Webhook server | External triggers |
| Heartbeats | Periodic background tasks |

**Deliverable**: Extensible, proactive assistant

### Phase 4: Device Nodes (Weeks 21-26)

| Task | Description |
|------|-------------|
| Node protocol | Registration, capabilities, invocation |
| macOS node | Camera, screen, browser, notifications |
| Browser controller | Chrome DevTools Protocol |
| iOS/Android nodes | Basic implementations |

**Deliverable**: Device integration

### Phase 5: Polish & Production (Weeks 27-30)

| Task | Description |
|------|-------------|
| Discord/Slack | Additional channels |
| Documentation | User guide, API reference |
| Testing | Integration tests, load testing |
| Packaging | Binary releases, Homebrew, Docker |

**Deliverable**: Production-ready release

---

## 17. Risk Assessment

| Risk | Impact | Likelihood | Mitigation |
|------|--------|------------|------------|
| WhatsApp ban | High | Medium | Use official Business API, rate limiting |
| LLM API costs | Medium | High | Token budgets, model fallback, caching |
| File corruption | High | Low | Atomic writes, backups, fsync |
| Memory leak | Medium | Medium | Profiling, bounded caches |
| Security breach | High | Low | Sandboxing, allowlists, auditing |

---

## 18. Success Metrics

### Technical Metrics

| Metric | Target |
|--------|--------|
| Message latency (p95) | < 500ms (excluding LLM) |
| Memory usage (idle) | < 50MB |
| Binary size | < 30MB |
| Startup time | < 2s |
| Crash rate | < 0.1% |

### Adoption Metrics (6 months)

| Metric | Target |
|--------|--------|
| GitHub stars | 1,000+ |
| Active users | 500+ |
| Community skills | 20+ |
| Platform coverage | Linux ARM, macOS, Windows |

---

## Appendix A: Comparison with Clawdbot

| Feature | Clawdbot | Hermes |
|---------|----------|--------|
| Language | TypeScript | Go |
| Protocol | WebSocket + JSON | WebSocket + JSON ✓ |
| Memory | File-based Markdown | File-based Markdown ✓ |
| Sessions | JSONL | JSONL ✓ |
| Bootstrap files | Yes | Yes ✓ |
| Channels | Built-in | Built-in ✓ |
| Proactive | Cron/Webhooks/Heartbeats | Cron/Webhooks/Heartbeats ✓ |
| Device nodes | Yes | Yes ✓ |
| Skills | AgentSkills spec | AgentSkills spec ✓ |
| Database | None | None ✓ |

---

## Appendix B: Directory Structure

```
hermes/
├── cmd/
│   └── hermes/
│       └── main.go
├── internal/
│   ├── gateway/
│   │   ├── gateway.go
│   │   ├── router.go
│   │   └── websocket.go
│   ├── agent/
│   │   ├── agent.go
│   │   ├── context.go
│   │   └── tools.go
│   ├── session/
│   │   ├── manager.go
│   │   └── jsonl.go
│   ├── memory/
│   │   └── file.go
│   ├── skill/
│   │   └── loader.go
│   ├── channel/
│   │   ├── channel.go
│   │   ├── whatsapp/
│   │   ├── telegram/
│   │   └── discord/
│   ├── proactive/
│   │   ├── cron.go
│   │   ├── webhook.go
│   │   └── heartbeat.go
│   ├── node/
│   │   ├── registry.go
│   │   └── browser.go
│   └── model/
│       ├── model.go
│       ├── anthropic/
│       └── openai/
├── pkg/
│   └── protocol/
│       └── types.go
├── skills/
│   ├── web-search/
│   └── calendar/
├── docs/
├── go.mod
└── go.sum
```

---

## Appendix C: Sample Interaction

```
User (WhatsApp): What's my schedule for today?

Hermes:
1. Reads workspace/MEMORY.md → Finds calendar integration info
2. Executes: skills/calendar/scripts/today.sh
3. Returns: "Here's your schedule for today:
   - 9:00 AM: Team standup
   - 11:00 AM: 1:1 with Sarah
   - 2:00 PM: Dentist appointment
   - 4:00 PM: Code review
   
   Reminder: Your dentist appointment is in 4 hours!"

User: Can you take a photo of my office setup?

Hermes:
1. Checks connected nodes → macbook-pro available
2. Invokes: node.invoke(macbook-pro, camera.snap)
3. Receives: Photo data
4. Returns: [Photo] "Here's your office setup!"

User: Remember that I prefer morning meetings

Hermes:
1. Writes to workspace/MEMORY.md:
   "- Prefers morning meetings (before noon)"
2. Returns: "Got it! I'll remember you prefer morning meetings."
```

---

*End of Proposal*
