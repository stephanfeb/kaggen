---
marp: true
theme: default
paginate: true
size: 4:3
style: |
  section {
    font-family: 'Segoe UI', Arial, sans-serif;
    font-size: 22px;
    padding: 40px;
  }
  section.purple {
    background-color: #5B2D5B;
    color: white;
  }
  section.blue {
    background-color: #1A1A6B;
    color: white;
  }
  section.coral {
    background-color: #C96B4B;
    color: white;
  }
  section.divider {
    display: flex;
    flex-direction: column;
    justify-content: center;
    align-items: flex-start;
    padding-left: 80px;
  }
  section.divider h1 {
    font-size: 48px;
    margin-bottom: 10px;
  }
  section.divider p {
    font-size: 24px;
    opacity: 0.8;
  }
  h1 {
    color: #5B2D5B;
    font-size: 32px;
    margin-bottom: 20px;
  }
  h2 {
    font-size: 24px;
    margin-bottom: 15px;
  }
  h3 {
    font-size: 20px;
  }
  section.purple h1, section.blue h1, section.coral h1 {
    color: white;
  }
  table {
    font-size: 16px;
    width: 100%;
  }
  th, td {
    padding: 6px 10px;
  }
  code {
    background-color: #f0f0f0;
    border-radius: 3px;
    padding: 1px 4px;
    font-size: 14px;
  }
  pre {
    background-color: #f5f5f5;
    color: #333;
    border-radius: 6px;
    font-size: 12px;
    padding: 15px;
    overflow-x: auto;
    border: 1px solid #ddd;
  }
  pre code {
    background: none;
    padding: 0;
    font-size: 12px;
    color: #333;
  }
  ul, ol {
    font-size: 18px;
  }
  li {
    margin-bottom: 6px;
  }
---

<!-- _class: purple divider -->

# Kaggen

**A Multi-Model AI Assistant Platform**

*Technical Overview*

---

# Kaggen vs OpenClaw

| Aspect | OpenClaw | Kaggen |
|--------|----------|--------|
| **Provider** | Primarily Claude | Multi-model (Claude, Gemini, ZAI) |
| **Skills** | SKILL.md + ClawdHub (2900+) | SKILL.md + hot-reload + delegate modes |
| **Security** | Basic trust model | Trust tiers + approval workflows + sandbox |
| **Task Model** | Synchronous | Async dispatch + background workers |
| **Memory** | Conversation history | Epistemic (typed + confidence tracking) |
| **Secrets** | Config-based | OS Keychain + AES-256 encrypted |
| **Mobile** | Messaging apps | Native P2P via libp2p |

**OpenClaw**: Large community, 2900+ skills, messaging-first
**Kaggen**: Security-first, multi-model, epistemic memory, production-ready

---

<!-- _class: blue divider -->

# How LLM Tool Calling Works

*The Agentic Loop*

---

# The Agentic Loop

The LLM is **stateless** — it returns structured JSON, the host app executes tools.

```
1. APP sends request    ──►  { "messages": [...], "tools": [read, write, exec] }

2. LLM returns response ──►  { "stop_reason": "tool_use",
                               "content": [{
                                 "type": "tool_use",
                                 "id": "toolu_123",
                                 "name": "read",
                                 "input": {"path": "/foo/bar.go"}
                               }] }

3. APP checks: stop_reason == "tool_use"?
   ├── YES → Parse tool_use block → Execute Go function → Append tool_result
   │         → Loop back to step 1
   └── NO  → Done (return to user)
```

**Key insight**: Each call reconstructs the full conversation including prior tool calls and results.

---

# Coordinator Pattern

```
                        User Request
                             │
                             ▼
                   ┌─────────────────┐
                   │   Coordinator   │  ◄── Fast/cheap model (Haiku)
                   │   (Router LLM)  │      Makes routing decisions
                   └────────┬────────┘
                            │
           ┌────────────────┼────────────────┐
           │                │                │
           ▼                ▼                ▼
      ┌─────────┐      ┌─────────┐     ┌──────────┐
      │  Direct │      │  Skill  │     │  Async   │
      │  Tool   │      │  Agent  │     │ Dispatch │
      │  Call   │      │  (sync) │     │ (bg task)│
      └─────────┘      └─────────┘     └──────────┘
           │                │                │
           ▼                ▼                ▼
        Result           Result          Task ID → Background goroutine → Callback
```

**Why**: Cheap router, capable specialists. Background tasks don't block the user.

---

<!-- _class: coral divider -->

# Security

*Defense in Depth*

---

# Five Security Layers

| Layer | Implementation | Purpose |
|-------|----------------|---------|
| **Trust Tiers** | Owner → Authorized → Third-Party | Third-party = sandboxed, no tools |
| **Secrets** | OS Keychain or AES-256-GCM | LLM sees secret NAME, never VALUE |
| **Tool Filtering** | `tools: [exec, read]` in SKILL.md | Skill agents can't access undeclared tools |
| **Guarded Tools** | `guarded_tools: [Bash]` | Requires human approval before execution |
| **Command Sandbox** | 25+ regex patterns | Blocks `rm -rf /`, fork bombs, `curl \| sh`, etc. |

**Principle**: Even if prompt injection succeeds, multiple layers limit the blast radius.

---

<!-- _class: purple divider -->

# Skills System

*Declarative Agent Definition*

---

# SKILL.md Format

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

## Create Issue
POST /api/v1/workspaces/{workspace}/projects/{project}/issues/
Body: {"name": "...", "description": "...", "priority": "high"}
```

**No code changes** to add capabilities. **Hot-reload** via SIGHUP. **Zero downtime**.

---

<!-- _class: blue divider -->

# Epistemic Memory

*Not All Memories Are Equal*

---

# Four Memory Types

| Type | What It Means | Example |
|------|---------------|---------|
| **Fact** | Objectively true, stable | "User works as a software engineer" |
| **Experience** | Something that happened | "User relocated to Berlin in Jan 2025" |
| **Opinion** | Preference or belief, can change | "User prefers Go over Rust for CLIs" |
| **Observation** | Pattern Kaggen noticed | "User discusses work on weekday mornings" |

### Confidence Tracking
- Same preference repeated → confidence **increases**
- Contradictory statement → confidence **decreases**
- Smoothing prevents flip-flopping from one offhand remark

*Inspired by the [Hindsight paper](https://arxiv.org/abs/2512.12818) on epistemic memory.*

---

# Four-Way Recall + Entity Graph

```
              "What did I work on in Berlin?"
                          │
          ┌───────────────┼───────────────┐
          │       │       │       │       │
          ▼       ▼       ▼       ▼       ▼
     ┌────────┐┌────────┐┌────────┐┌────────┐
     │SEMANTIC││KEYWORD ││ ENTITY ││TEMPORAL│
     │ (vec)  ││ (FTS)  ││ GRAPH  ││ (date) │
     └───┬────┘└───┬────┘└───┬────┘└───┬────┘
         └─────────┴────┬────┴─────────┘
                        ▼
              Reciprocal Rank Fusion
              (memories in multiple channels rank higher)
```

**Entity Graph**: Two entities mentioned together strengthen their connection.
→ Enables recall of related knowledge even if not directly asked.

**Background Synthesis**: Kaggen reviews entities with 3+ memories and generates new observations.

---

<!-- _class: coral divider -->

# RAG in Kaggen

*Hybrid Retrieval*

---

# RAG Pipeline 

| Stage | Implementation |
|-------|----------------|
| **Ingestion** | Markdown files → chunked on paragraph/heading boundaries |
| **Chunking** | ~400 words per chunk, 80-word overlap, line tracking |
| **Embedding** | Ollama (`nomic-embed-text`) with batch support |
| **Storage** | SQLite + `sqlite-vec` (vectors) + FTS5 (keywords) |
| **Retrieval** | **4-way hybrid**: Vector KNN, FTS5, Entity Graph, Temporal |
| **Fusion** | Reciprocal Rank Fusion (k=60), parallel channels |
| **Indexing** | Polls files every 30s, re-indexes on change |

### Epistemic Memory Integration
- **Typed memories**: fact / experience / opinion / observation
- **Confidence scores**: 0.0-1.0, evolve with repetition/contradiction
- **Entity graph**: Co-occurrence edges, 2-hop spreading activation
- **Background synthesis**: LLM summarizes entities with 3+ memories

---

<!-- _class: purple divider -->

# Summary

---

# Kaggen at a Glance

| Capability | Implementation |
|------------|----------------|
| **Multi-Model** | Claude, Gemini, ZAI with automatic fallback |
| **Skills** | YAML + Markdown, hot-reload, no code changes |
| **Security** | Trust tiers, approval workflows, command sandbox |
| **Async** | Background tasks with callbacks, non-blocking |
| **Memory** | Epistemic: typed, confidence-aware, entity graph |
| **RAG** | Hybrid vector + FTS with Reciprocal Rank Fusion |
| **Channels** | CLI, WebSocket, Telegram, WhatsApp, P2P |

> *"A personal AI assistant that is truly yours—extensible, secure, and available everywhere."*

---

<!-- _class: blue divider -->

# Thank You

**Questions?**

