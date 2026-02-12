# Kaggen: Technical Overview

> A focused look at architecture, tools, security, and memory

---

## SLIDE 1: Kaggen vs OpenClaw

### Similar Foundations, Different Focus

Both use **SKILL.md files with YAML frontmatter** for extensibility. Key differences:

| Aspect | OpenClaw | Kaggen |
|--------|----------|--------|
| **Provider** | Primarily Claude | Multi-model (Claude, Gemini, ZAI) |
| **Skills** | SKILL.md + ClawdHub (2900+ community skills) | SKILL.md + hot-reload + delegate modes |
| **Security** | Basic trust model | Trust tiers, approval workflows, command sandbox |
| **Task Model** | Synchronous | Async dispatch with background workers |
| **Memory** | Conversation history | Epistemic memory (typed, confidence-aware) |
| **Secrets** | Config-based | OS Keychain + AES-256 encrypted storage |
| **Tool Control** | Per-skill tools | Per-skill + guarded tools + approval flows |
| **Mobile** | Messaging apps (Telegram, WhatsApp) | Native P2P via libp2p (direct connection) |

**OpenClaw Strengths**: Large community, 2900+ skills, easy setup, messaging-first.

**Kaggen Focus**: Security-first for untrusted users, multi-model orchestration, epistemic memory, native mobile P2P, production hardening.

---

## SLIDE 2: How LLM Tool Calling Works

### The Agentic Loop

The LLM doesn't execute tools—it returns structured JSON. The **host application** drives the loop.

```
┌─────────────────────────────────────────────────────────────────────┐
│                        AGENTIC LOOP                                 │
└─────────────────────────────────────────────────────────────────────┘

  ┌─────────────────────────────────────────────────────────────────┐
  │  1. APP SENDS REQUEST                                           │
  │     ┌─────────────────────────────────────────────────────────┐ │
  │     │  { "messages": [...], "tools": [read, write, exec] }    │ │
  │     └─────────────────────────────────────────────────────────┘ │
  └───────────────────────────────┬───────────────────────────────────┘
                                  │
                                  ▼
  ┌─────────────────────────────────────────────────────────────────┐
  │  2. LLM RETURNS RESPONSE                                        │
  │     ┌─────────────────────────────────────────────────────────┐ │
  │     │  {                                                      │ │
  │     │    "stop_reason": "tool_use",                           │ │
  │     │    "content": [{                                        │ │
  │     │      "type": "tool_use",                                │ │
  │     │      "id": "toolu_123",                                 │ │
  │     │      "name": "read",                                    │ │
  │     │      "input": {"path": "/foo/bar.go"}                   │ │
  │     │    }]                                                   │ │
  │     │  }                                                      │ │
  │     └─────────────────────────────────────────────────────────┘ │
  └───────────────────────────────┬───────────────────────────────────┘
                                  │
                                  ▼
  ┌─────────────────────────────────────────────────────────────────┐
  │  3. APP EXECUTES TOOL LOCALLY                                   │
  │                                                                 │
  │     stop_reason == "tool_use"?                                  │
  │           │                                                     │
  │     ┌─────┴─────┐                                               │
  │     │           │                                               │
  │    YES          NO → Done (return to user)                      │
  │     │                                                           │
  │     ▼                                                           │
  │   Parse tool_use block                                          │
  │     │                                                           │
  │     ▼                                                           │
  │   Look up tool by name → Execute Go function                    │
  │     │                                                           │
  │     ▼                                                           │
  │   Append tool_result to messages                                │
  │     │                                                           │
  │     ▼                                                           │
  │   Loop back to step 1                                           │
  └─────────────────────────────────────────────────────────────────┘
```

**Key Insight**: The LLM is stateless. Each call reconstructs the full conversation including prior tool calls and results.

---

## SLIDE 3: Kaggen's Coordinator Pattern

### Intelligent Task Routing

```
                    User Request
                         │
                         ▼
               ┌─────────────────┐
               │   Coordinator   │   ◄── Fast/cheap model (Haiku)
               │   (Router LLM)  │       Decides where to route
               └────────┬────────┘
                        │
           ┌────────────┼────────────┬──────────────┐
           │            │            │              │
           ▼            ▼            ▼              ▼
      ┌─────────┐  ┌─────────┐  ┌─────────┐  ┌──────────┐
      │  Direct │  │  Skill  │  │  Skill  │  │  Async   │
      │  Tool   │  │ Agent 1 │  │ Agent 2 │  │ Dispatch │
      │  Call   │  │ (sync)  │  │ (sync)  │  │ (bg task)│
      └────┬────┘  └────┬────┘  └────┬────┘  └────┬─────┘
           │            │            │             │
           ▼            ▼            ▼             ▼
        Result       Result       Result      Task ID
                                             (immediate)
                                                  │
                                            ┌─────┴─────┐
                                            │ Background│
                                            │ Goroutine │
                                            └─────┬─────┘
                                                  │
                                            Callback on
                                             completion
```

**Why This Works**:
- Cheap router makes fast decisions
- Expensive models only used when needed
- Background tasks don't block the user
- Skills are specialized sub-agents with filtered tools

---

## SLIDE 4: Security - Defense in Depth

### Five Layers of Protection

```
┌─────────────────────────────────────────────────────────────────────┐
│                      SECURITY LAYERS                                │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  LAYER 1: TRUST TIERS                                               │
│  ┌───────────────────────────────────────────────────────────────┐ │
│  │  Owner          │  Full access to all tools and operations    │ │
│  │  Authorized     │  Standard access per config                 │ │
│  │  Third-Party    │  Sandboxed: conversation only, no tools     │ │
│  └───────────────────────────────────────────────────────────────┘ │
│                                                                     │
│  LAYER 2: SECRETS MANAGEMENT                                        │
│  ┌───────────────────────────────────────────────────────────────┐ │
│  │  OS Keychain (macOS/Linux/Windows) or AES-256-GCM fallback   │ │
│  │  LLM sees secret NAME only, never the VALUE                   │ │
│  └───────────────────────────────────────────────────────────────┘ │
│                                                                     │
│  LAYER 3: TOOL FILTERING                                            │
│  ┌───────────────────────────────────────────────────────────────┐ │
│  │  Each skill declares allowed tools: tools: [exec, read]       │ │
│  │  Skill agents CANNOT access undeclared tools                  │ │
│  └───────────────────────────────────────────────────────────────┘ │
│                                                                     │
│  LAYER 4: GUARDED TOOLS + APPROVAL                                  │
│  ┌───────────────────────────────────────────────────────────────┐ │
│  │  guarded_tools: [Bash] → requires human approval              │ │
│  │  notify_tools: [Write] → auto-execute + notification          │ │
│  └───────────────────────────────────────────────────────────────┘ │
│                                                                     │
│  LAYER 5: COMMAND SANDBOX                                           │
│  ┌───────────────────────────────────────────────────────────────┐ │
│  │  25+ dangerous patterns blocked: rm -rf /, fork bombs,        │ │
│  │  curl | sh, sudo su, reverse shells, disk wipes               │ │
│  └───────────────────────────────────────────────────────────────┘ │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

**Principle**: Even if prompt injection succeeds, multiple layers limit damage.

---

## SLIDE 5: The Skills System

### Declarative Agent Definition

A skill is a **sub-agent** defined entirely in YAML frontmatter + Markdown instructions:

```yaml
# ~/.kaggen/skills/plane/SKILL.md
---
name: plane
description: Manage issues and projects in Plane
tools: [http_request]              # Only these tools available
secrets: [plane-api-key]           # Declare required secrets
guarded_tools: [Bash]              # Require approval (if used)
oauth_providers: [google]          # OAuth integrations
delegate: claude                   # Optional: run as subprocess
---

# Plane Integration

You manage issues in Plane via their REST API.

## Authentication

Use http_request with:
- auth_secret: plane-api-key
- auth_scheme: bearer

## Operations

### Create Issue
POST /api/v1/workspaces/{workspace}/projects/{project}/issues/
Body: {"name": "...", "description": "...", "priority": "high"}
```

**Key Benefits**:
- **No code changes** to add new capabilities
- **Hot-reload** via SIGHUP (zero downtime)
- **Tool filtering** enforces least privilege
- **Secret references** keep values out of LLM context

---

## SLIDE 6: Skill Types

### LLM Agent vs Claude Agent

```
┌─────────────────────────────────────────────────────────────────────┐
│                    LLM AGENT (default)                              │
├─────────────────────────────────────────────────────────────────────┤
│  • Runs in-process within trpc-agent-go framework                   │
│  • Markdown body becomes agent's system prompt                      │
│  • Calls tools directly through framework                           │
│  • Fast startup, shared context                                     │
│  • Best for: API wrappers, simple automation, quick tasks           │
└─────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────┐
│                 CLAUDE AGENT (delegate: claude)                     │
├─────────────────────────────────────────────────────────────────────┤
│  • Runs as `claude -p` subprocess (Claude Code CLI)                 │
│  • Body prepended to task as context                                │
│  • Independent model, tools, and session                            │
│  • Best for: Complex multi-step workflows, code generation          │
│                                                                     │
│  Config:                                                            │
│    delegate: claude                                                 │
│    claude_model: opus                                               │
│    claude_tools: Bash,Read,Edit,Write                               │
│    work_dir: ~/projects/myapp                                       │
└─────────────────────────────────────────────────────────────────────┘
```

---

## SLIDE 7: Epistemic Memory

### Not All Memories Are Equal

Inspired by the [Hindsight paper](https://arxiv.org/abs/2512.12818) on epistemic memory for AI agents.

```
┌─────────────────────────────────────────────────────────────────────┐
│                    FOUR MEMORY TYPES                                │
├──────────────┬──────────────────────────────────────────────────────┤
│    TYPE      │                    EXAMPLE                           │
├──────────────┼──────────────────────────────────────────────────────┤
│   FACT       │  "User works as a software engineer"                 │
│              │  Objectively true, stable                            │
├──────────────┼──────────────────────────────────────────────────────┤
│  EXPERIENCE  │  "User relocated to Berlin in January 2025"          │
│              │  Something that happened, timestamped                │
├──────────────┼──────────────────────────────────────────────────────┤
│   OPINION    │  "User prefers Go over Rust for CLI tools"           │
│              │  Preference or belief, can change over time          │
├──────────────┼──────────────────────────────────────────────────────┤
│ OBSERVATION  │  "User discusses work topics on weekday mornings"    │
│              │  Pattern Kaggen noticed, self-generated              │
└──────────────┴──────────────────────────────────────────────────────┘
```

**Confidence Tracking**: Opinions have confidence scores (0-1) that evolve:
- Same preference repeated → confidence increases
- Contradictory statement → confidence decreases
- Smoothing prevents flip-flopping from one offhand remark

---

## SLIDE 8: Epistemic Memory - Recall & Synthesis

### Four-Way Search + Background Synthesis

```
              "What did I work on in Berlin?"
                          │
          ┌───────────────┼───────────────┐
          │       │       │       │       │
          ▼       ▼       ▼       ▼       ▼
     ┌────────┐ ┌────────┐ ┌────────┐ ┌────────┐
     │SEMANTIC│ │KEYWORD │ │ ENTITY │ │TEMPORAL│
     │        │ │        │ │ GRAPH  │ │        │
     │Similar │ │ Exact  │ │Related │ │ "in    │
     │meaning │ │ match  │ │entities│ │ Berlin"│
     └───┬────┘ └───┬────┘ └───┬────┘ └───┬────┘
         │          │          │          │
         └──────────┴────┬─────┴──────────┘
                         │
                         ▼
               ┌─────────────────────┐
               │  Reciprocal Rank    │  Memories appearing in
               │     Fusion          │  multiple channels rank
               └──────────┬──────────┘  higher
                          │
                          ▼
                 Best memories, ranked
```

**Background Synthesis**: Kaggen periodically reviews entities with 3+ memories and generates new **observations** by spotting patterns across them.

**Entity Graph**: Two entities mentioned together strengthen their connection, enabling recall of related knowledge even if not directly asked.

---

## SLIDE 9: RAG in Kaggen

### Retrieval-Augmented Generation

```
┌─────────────────────────────────────────────────────────────────────┐
│                        RAG PIPELINE                                 │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│   1. DOCUMENT INGESTION                                             │
│      ┌─────────────────────────────────────────────────────────┐   │
│      │  • PDF, Markdown, text files                            │   │
│      │  • Chunked with overlap for context preservation        │   │
│      │  • Embedded via model (e.g., text-embedding-3-small)    │   │
│      └─────────────────────────────────────────────────────────┘   │
│                           │                                         │
│                           ▼                                         │
│   2. VECTOR STORAGE                                                 │
│      ┌─────────────────────────────────────────────────────────┐   │
│      │  • SQLite with vector extension (sqlite-vec)            │   │
│      │  • Full-text search index (FTS5) for keyword matching   │   │
│      │  • Metadata: source, chunk index, timestamps            │   │
│      └─────────────────────────────────────────────────────────┘   │
│                           │                                         │
│                           ▼                                         │
│   3. HYBRID RETRIEVAL (on query)                                    │
│      ┌─────────────────────────────────────────────────────────┐   │
│      │  • Vector similarity search (semantic)                  │   │
│      │  • FTS keyword search (exact terms)                     │   │
│      │  • Reciprocal Rank Fusion combines results              │   │
│      └─────────────────────────────────────────────────────────┘   │
│                           │                                         │
│                           ▼                                         │
│   4. CONTEXT INJECTION                                              │
│      ┌─────────────────────────────────────────────────────────┐   │
│      │  Top-k chunks injected into LLM context                 │   │
│      │  Source attribution for citations                       │   │
│      └─────────────────────────────────────────────────────────┘   │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

**Epistemic Memory + RAG**: Memory system uses the same hybrid retrieval, but with typed memories, confidence scores, and entity relationships layered on top.

---

## SLIDE 10: Summary

### Kaggen at a Glance

```
┌─────────────────────────────────────────────────────────────────────┐
│                                                                     │
│   MULTI-MODEL          Claude, Gemini, ZAI with automatic fallback │
│                                                                     │
│   SKILLS SYSTEM        YAML + Markdown, hot-reload, no code        │
│                                                                     │
│   SECURITY-FIRST       Trust tiers, secrets, approval workflows    │
│                                                                     │
│   ASYNC DISPATCH       Background tasks, non-blocking              │
│                                                                     │
│   EPISTEMIC MEMORY     Typed memories, confidence, entity graph    │
│                                                                     │
│   HYBRID RAG           Vector + FTS + Reciprocal Rank Fusion       │
│                                                                     │
│   MULTI-CHANNEL        CLI, WebSocket, Telegram, WhatsApp, P2P     │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘

         "A personal AI assistant that is TRULY yours—
          extensible, secure, and available everywhere."
```

---

*End of presentation*
