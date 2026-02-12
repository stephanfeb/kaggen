# Kaggen: A Personal AI Assistant Platform

> A presentation covering architecture, skills, security, and roadmap

---

## SLIDE 1: Title

# Kaggen

**A Multi-Model Personal AI Assistant Platform**

*Named after the mantis deity of the San people—associated with creativity and trickster wisdom*

Key Themes:
- Multi-model orchestration
- Extensible skills system
- Security-first design
- Production-ready for real-world deployment

---

## SLIDE 2: The Problem

### What We're Solving

**Challenge**: Building AI assistants that are:
- Not locked to a single provider
- Extensible without code changes
- Safe to expose to untrusted users
- Capable of long-running, complex tasks
- Accessible from anywhere (CLI, mobile, web)

**Current Pain Points**:
1. Vendor lock-in to single LLM providers
2. Hard-coded capabilities requiring redeploys
3. Security as an afterthought
4. Context window limitations blocking complex work
5. No async—every request blocks until complete

---

## SLIDE 3: Overview

### What is Kaggen?

A **Go-based AI assistant platform** with:

```
┌─────────────────────────────────────────────────────┐
│                   KAGGEN CORE                        │
├─────────────────────────────────────────────────────┤
│  Multi-Model Support    │  Claude, Gemini, ZAI GLM  │
│  Coordinator Pattern    │  Intelligent task routing  │
│  Async Dispatch         │  Background task execution │
│  Hot-Reload Skills      │  Zero-downtime updates     │
│  Trust Tiers            │  Owner/Auth/Third-Party    │
│  Multi-Channel          │  CLI, WebSocket, Telegram  │
│  Epistemic Memory       │  Typed, confidence-aware   │
└─────────────────────────────────────────────────────┘
```

**Deployment Options**:
- CLI agent for local development
- Gateway server for multi-client access
- Telegram/WhatsApp bots for mobile access

---

## SLIDE 4: Core Capabilities

### Feature Matrix

| Capability | Implementation | Benefit |
|------------|----------------|---------|
| **Provider Agnostic** | Adapter pattern | Switch models without code changes |
| **Coordinator Team** | LLM routing to specialists | Right model for each subtask |
| **Async Tasks** | Goroutines + task store | Non-blocking complex work |
| **Skills System** | YAML + Markdown | No-code extensibility |
| **Trust Tiers** | Sender classification | Safe third-party access |
| **Approval Workflows** | Guarded tools | Human-in-the-loop for danger |
| **Epistemic Memory** | Typed memories + entity graph | Knows what it knows |
| **Context Management** | Auto-pruning | No overflow errors |

---

## SLIDE 5: Architecture Overview

### High-Level System Design

```
                         ┌──────────────┐
                         │    Users     │
                         └──────┬───────┘
                                │
     ┌──────────────┬───────────┼───────────┬──────────────┐
     │              │           │           │              │
┌────▼────┐   ┌────▼────┐ ┌────▼────┐ ┌────▼────┐  ┌──────▼─────┐
│   CLI   │   │ Gateway │ │Telegram │ │WhatsApp │  │    P2P     │
│  Agent  │   │ Server  │ │   Bot   │ │   Bot   │  │  (Mobile)  │
└────┬────┘   └────┬────┘ └────┬────┘ └────┬────┘  └──────┬─────┘
     │             │           │           │              │
     └─────────────┴───────────┼───────────┴──────────────┘
                               │
                        ┌──────▼──────┐
                        │   Handler   │
                        │  (Router)   │
                        └──────┬──────┘
                               │
                        ┌──────▼──────┐
                        │    Agent    │
                        │ (Coordinator│
                        │   + Team)   │
                        └──────┬──────┘
                               │
        ┌──────────────────────┼──────────────────────┐
        │                      │                      │
   ┌────▼────┐           ┌────▼────┐           ┌────▼────┐
   │  Tools  │           │  Skills │           │ Memory  │
   │ (exec,  │           │(hot-load│           │(vectors,│
   │  read)  │           │ agents) │           │  FTS)   │
   └─────────┘           └─────────┘           └─────────┘
```

---

## SLIDE 6: Project Structure

### Codebase Organization

```
kaggen/
├── cmd/kaggen/
│   └── cmd/              # CLI commands
│       ├── agent.go      # Interactive CLI
│       ├── gateway.go    # HTTP/WS server
│       └── init.go       # Project setup
│
├── internal/
│   ├── agent/            # Core agent logic
│   │   ├── agent.go      # Coordinator team
│   │   ├── async.go      # Task dispatch
│   │   ├── skills.go     # Skill loader
│   │   └── approval.go   # Guarded tools
│   │
│   ├── channel/          # Multi-channel interface
│   │   ├── telegram.go   # Bot integration
│   │   └── websocket.go  # Real-time
│   │
│   ├── model/            # Provider adapters
│   │   ├── anthropic/    # Claude
│   │   ├── gemini/       # Google
│   │   └── zai/          # GLM
│   │
│   ├── tools/            # Tool implementations
│   ├── secrets/          # Encrypted storage
│   ├── trust/            # Tier classification
│   ├── oauth/            # Token management
│   └── memory/           # Vector + FTS
│
├── docs/                 # Documentation
└── testdata/eval/        # Test suite
```

---

## SLIDE 7: Coordinator Team Pattern

### Intelligent Task Routing

```
         User Request
              │
              ▼
    ┌─────────────────┐
    │   Coordinator   │   ◄── Fast/cheap model (Haiku)
    │   (Router LLM)  │       Makes routing decisions
    └────────┬────────┘
             │
    ┌────────┼────────┬──────────────┐
    │        │        │              │
    ▼        ▼        ▼              ▼
┌───────┐ ┌───────┐ ┌───────┐  ┌──────────┐
│Direct │ │ Skill │ │ Skill │  │  Async   │
│ Tool  │ │Agent 1│ │Agent 2│  │ Dispatch │
│ Call  │ │(sync) │ │(sync) │  │(bg task) │
└───────┘ └───────┘ └───────┘  └──────────┘
   │          │         │            │
   ▼          ▼         ▼            ▼
  Result   Result    Result    Task ID
                               (immediate)
                                   │
                                   ▼
                              Background
                              Goroutine
                                   │
                                   ▼
                              Callback
                              on Done
```

**Key Insight**: Coordinator uses a fast model for routing decisions, delegates heavy work to capable specialists.

---

## SLIDE 8: Provider Abstraction

### Multi-Model Support

```
                  ┌─────────────────────┐
                  │  Unified Interface  │
                  │   (trpc-agent-go)   │
                  └──────────┬──────────┘
                             │
         ┌───────────────────┼───────────────────┐
         │                   │                   │
    ┌────▼────┐        ┌────▼────┐        ┌────▼────┐
    │Anthropic│        │ Gemini  │        │   ZAI   │
    │ Adapter │        │ Adapter │        │ Adapter │
    └────┬────┘        └────┬────┘        └────┬────┘
         │                  │                  │
    ┌────▼────┐        ┌────▼────┐        ┌────▼────┐
    │ Claude  │        │ Gemini  │        │   GLM   │
    │ Opus/   │        │ 2.0/2.5 │        │  4-Plus │
    │ Sonnet  │        │  Flash  │        │         │
    └─────────┘        └─────────┘        └─────────┘
```

**Selection Priority**: ZAI → Gemini → Anthropic

**Why**: Cost optimization + redundancy. Fall back gracefully if a provider is unavailable.

---

## SLIDE 9: P2P Interface

### Direct Mobile Connectivity via libp2p

```
┌─────────────────────────────────────────────────────────────┐
│                  P2P ARCHITECTURE                           │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  ┌─────────────────┐              ┌─────────────────┐      │
│  │  Mobile Client  │◄──libp2p───►│  Kaggen Server  │      │
│  │  (Flutter/Swift)│   stream     │                 │      │
│  └─────────────────┘              └─────────────────┘      │
│           │                                │                │
│           │                                │                │
│  Transport: UDP (QUIC) or TCP              │                │
│  Security: Noise protocol                  │                │
│  Muxer: yamux                              │                │
│           │                                │                │
│           └────────────┬───────────────────┘                │
│                        │                                    │
│  Available Protocols:                                       │
│  ┌─────────────────────────────────────────────────────┐   │
│  │  /kaggen/chat/1.0.0       Streaming AI chat         │   │
│  │  /kaggen/sessions/1.0.0   Session management        │   │
│  │  /kaggen/tasks/1.0.0      Task monitoring           │   │
│  │  /kaggen/approvals/1.0.0  Human-in-the-loop         │   │
│  │  /kaggen/system/1.0.0     System status & config    │   │
│  │  /kaggen/secrets/1.0.0    Secret management         │   │
│  │  /kaggen/files/1.0.0      File downloads            │   │
│  └─────────────────────────────────────────────────────┘   │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

**Authentication**: PeerID verified via Noise handshake—no additional tokens required.

---

## SLIDE 10: P2P Mobile Connection Flow

### How Mobile Clients Connect

```
┌─────────────────────────────────────────────────────────────┐
│                 MOBILE CONNECTION FLOW                      │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  1. SERVER STARTUP                                          │
│     ┌─────────────────────────────────────────────────┐    │
│     │ kaggen gateway                                   │    │
│     │                                                  │    │
│     │ PeerID: 12D3KooWExample...                       │    │
│     │ Listen: /ip4/192.168.1.100/udp/4001/quic-v1     │    │
│     │ Listen: /ip4/192.168.1.100/tcp/4001             │    │
│     └─────────────────────────────────────────────────┘    │
│                         │                                   │
│                         ▼                                   │
│  2. QR CODE PAIRING                                         │
│     ┌─────────────────────────────────────────────────┐    │
│     │  Dashboard displays multiaddr as QR code         │    │
│     │  Mobile app scans → extracts PeerID + address    │    │
│     └─────────────────────────────────────────────────┘    │
│                         │                                   │
│                         ▼                                   │
│  3. LIBP2P CONNECTION                                       │
│     ┌─────────────────────────────────────────────────┐    │
│     │  • Noise handshake authenticates both peers      │    │
│     │  • yamux multiplexes protocol streams            │    │
│     │  • Session assigned from PeerID (first 16 chars) │    │
│     └─────────────────────────────────────────────────┘    │
│                         │                                   │
│                         ▼                                   │
│  4. CHAT STREAMING                                          │
│     ┌─────────────────────────────────────────────────┐    │
│     │  Client sends: ChatMessage (protobuf)            │    │
│     │  Server streams: ChatResponse[] (done=true ends) │    │
│     └─────────────────────────────────────────────────┘    │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

**Platform Libraries**: Go, Rust, JavaScript, Dart/Flutter, Kotlin, Swift

---

## SLIDE 11: The Skills System

### Declarative Agent Definition

**What is a Skill?**

A skill is a **sub-agent** defined entirely in YAML + Markdown:

```
~/.kaggen/skills/
├── pandoc/
│   └── SKILL.md        # Convert documents
├── plane/
│   └── SKILL.md        # Issue tracking
├── gmail/
│   └── SKILL.md        # Email operations
└── coder/
    ├── SKILL.md        # Code implementation
    └── scripts/
        └── list.sh     # Helper scripts
```

**No code changes required** to add new capabilities.

---

## SLIDE 12: Skill Anatomy

### SKILL.md Format

```yaml
---
name: my-skill
description: One-line summary for coordinator
tools: [exec, read]           # Restrict available tools
secrets: [api-key]            # Declare required secrets
guarded_tools: [Bash]         # Require approval
oauth_providers: [google]     # OAuth integrations
delegate: claude              # Optional: subprocess mode
---

# Instructions (becomes system prompt)

You are a [role]. Your job is to [task].

## How to Use Tools

1. First, check the current state...
2. Then, perform the operation...
3. Finally, verify the result...

## Examples

User: "Convert report.docx to PDF"
You: [use exec tool with pandoc command]
```

---

## SLIDE 13: Two Agent Types

### LLM Agent vs Claude Agent

```
┌─────────────────────────────────────────────────────────────┐
│                    LLM AGENT (default)                      │
├─────────────────────────────────────────────────────────────┤
│  • Runs in-process within trpc-agent-go                     │
│  • Body becomes agent's system prompt                       │
│  • Calls tools directly through framework                   │
│  • Fast startup, shared context                             │
│  • Best for: CLI wrappers, simple automation                │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│                 CLAUDE AGENT (delegate: claude)             │
├─────────────────────────────────────────────────────────────┤
│  • Runs as `claude -p` subprocess                           │
│  • Body prepended to task, passed as -p argument            │
│  • Independent model, tools, and session                    │
│  • Best for: Complex multi-step workflows, code gen         │
│                                                             │
│  Config:                                                    │
│    delegate: claude                                         │
│    claude_model: opus                                       │
│    claude_tools: Bash,Read,Edit,Write                       │
│    work_dir: ~/projects/foo                                 │
└─────────────────────────────────────────────────────────────┘
```

---

## SLIDE 14: Skill Example - Plane Integration

### Real-World Skill Definition

```yaml
---
name: plane
description: Manage issues and projects in Plane
tools: [http_request]
secrets: [plane-api-key]
---

# Plane — Issue Tracking Integration

You manage issues in Plane via their REST API.

## Authentication

Use http_request with:
- auth_secret: plane-api-key
- auth_scheme: bearer

## Available Operations

### List Issues
GET /api/v1/workspaces/{workspace}/projects/{project}/issues/

### Create Issue
POST /api/v1/workspaces/{workspace}/projects/{project}/issues/
Body: {"name": "...", "description": "...", "priority": "high"}

### Update Issue Status
PATCH /api/v1/workspaces/{workspace}/projects/{project}/issues/{id}/
Body: {"state": "in_progress"}
```

**Key Point**: The LLM references `plane-api-key` by name—never sees the actual secret value.

---

## SLIDE 15: Skills vs Anthropic's Original

### How Kaggen Differs

| Feature | Kaggen | Anthropic Skills |
|---------|--------|------------------|
| **Definition** | Declarative YAML + MD | JSON config files |
| **Tool Filtering** | Per-skill via frontmatter | Per-skill config |
| **Subprocess Mode** | `delegate: claude` for heavy work | Single process only |
| **Approval Flows** | Built-in guarded tools | Not included |
| **Hot-Reload** | SIGHUP → atomic swap | Requires restart |
| **OAuth Integration** | Built-in token refresh | Manual integration |
| **Secrets Management** | Keychain + AES-256 | External solutions |
| **API Auth** | http_request + secret refs | Custom per-skill |
| **Eval Framework** | Full coordinator tests | Not included |

**Bottom Line**: Kaggen skills are production-ready with security, auth, and ops concerns addressed.

---

## SLIDE 16: Tool Filtering

### Principle of Least Privilege

```yaml
---
name: pandoc
tools: [exec]          # ONLY exec tool available
---
```

```
                    ┌─────────────────┐
                    │  All Available  │
                    │      Tools      │
                    └────────┬────────┘
                             │
                    ┌────────▼────────┐
                    │  filterTools()  │
                    └────────┬────────┘
                             │
              ┌──────────────┴──────────────┐
              │                             │
     ┌────────▼────────┐          ┌────────▼────────┐
     │ pandoc skill    │          │ plane skill     │
     │ tools: [exec]   │          │ tools:          │
     │                 │          │  [http_request] │
     │ Can ONLY use:   │          │                 │
     │   - exec        │          │ Can ONLY use:   │
     │                 │          │   - http_request│
     │ BLOCKED:        │          │                 │
     │   - read        │          │ BLOCKED:        │
     │   - write       │          │   - exec        │
     │   - http_request│          │   - read        │
     └─────────────────┘          └─────────────────┘
```

**Why**: Prevents skill agents from accessing tools they shouldn't. A document converter can't make HTTP requests; an API client can't execute shell commands.

---

## SLIDE 17: Security Overview

### Defense in Depth

```
┌─────────────────────────────────────────────────────────────┐
│                    SECURITY LAYERS                          │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  Layer 1: TRUST TIERS                                       │
│  ┌─────────────────────────────────────────────────────┐   │
│  │ Owner → Authorized → Third-Party (sandboxed)        │   │
│  └─────────────────────────────────────────────────────┘   │
│                                                             │
│  Layer 2: SECRETS MANAGEMENT                                │
│  ┌─────────────────────────────────────────────────────┐   │
│  │ OS Keychain (preferred) │ AES-256-GCM (fallback)    │   │
│  └─────────────────────────────────────────────────────┘   │
│                                                             │
│  Layer 3: TOOL CONTROLS                                     │
│  ┌─────────────────────────────────────────────────────┐   │
│  │ Per-skill filtering │ Guarded tools │ Approval flows│   │
│  └─────────────────────────────────────────────────────┘   │
│                                                             │
│  Layer 4: COMMAND SANDBOX                                   │
│  ┌─────────────────────────────────────────────────────┐   │
│  │ 25+ dangerous patterns blocked │ Custom rules        │   │
│  └─────────────────────────────────────────────────────┘   │
│                                                             │
│  Layer 5: CONTEXT MANAGEMENT                                │
│  ┌─────────────────────────────────────────────────────┐   │
│  │ Auto-pruning │ Task re-injection │ Binary stripping  │   │
│  └─────────────────────────────────────────────────────┘   │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

---

## SLIDE 18: Secrets Management

### Two-Tier Storage Strategy

```
┌─────────────────────────────────────────────────────────────┐
│                    SECRET STORAGE                           │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  TIER 1: OS Keychain (Preferred)                            │
│  ┌─────────────────────────────────────────────────────┐   │
│  │  • macOS Keychain                                    │   │
│  │  • Linux Secret Service                              │   │
│  │  • Windows Credential Manager                        │   │
│  │                                                      │   │
│  │  Benefits:                                           │   │
│  │    - OS-level encryption                             │   │
│  │    - Protected by user login                         │   │
│  │    - Automatic session unlocking                     │   │
│  └─────────────────────────────────────────────────────┘   │
│                                                             │
│  TIER 2: Encrypted File (Fallback for Servers)              │
│  ┌─────────────────────────────────────────────────────┐   │
│  │  • AES-256-GCM authenticated encryption              │   │
│  │  • Argon2id key derivation                           │   │
│  │  • Stored at ~/.kaggen/secrets.enc                   │   │
│  │  • Requires KAGGEN_MASTER_KEY env var                │   │
│  └─────────────────────────────────────────────────────┘   │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

**CLI Usage**:
```bash
kaggen secrets set api-key           # Prompt for value
kaggen secrets import-env API_KEY    # Import from env
kaggen secrets list                  # Keys only (no values)
```

---

## SLIDE 19: Secrets in Skills

### Reference-Based Access

```
┌─────────────────────────────────────────────────────────────┐
│                    SECRET FLOW                              │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│   SKILL.md                      LLM Tool Call               │
│   ┌───────────────┐            ┌─────────────────────┐     │
│   │ secrets:      │            │ http_request:       │     │
│   │   - api-key   │ ──────────►│   url: "..."        │     │
│   └───────────────┘            │   auth_secret:      │     │
│                                │     "api-key"  ◄────┼──┐  │
│                                └─────────────────────┘  │  │
│                                         │               │  │
│                                         ▼               │  │
│                                ┌─────────────────────┐  │  │
│                                │   Tool Resolver     │  │  │
│                                │                     │  │  │
│                                │ 1. Look up "api-key"│──┘  │
│                                │    in secret store  │     │
│                                │ 2. Inject into      │     │
│                                │    Authorization    │     │
│                                │    header           │     │
│                                │ 3. Make HTTP request│     │
│                                └─────────────────────┘     │
│                                         │                  │
│                                         ▼                  │
│                                ┌─────────────────────┐     │
│                                │  ACTUAL SECRET      │     │
│                                │  NEVER IN CONTEXT   │     │
│                                └─────────────────────┘     │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

**Key Principle**: LLM only knows the secret **name**, never the **value**.

---

## SLIDE 20: Trust Tiers

### Sender Classification

```
                    Incoming Message
                          │
                          ▼
               ┌──────────────────┐
               │ Classify Sender  │
               └────────┬─────────┘
                        │
        ┌───────────────┼───────────────┐
        │               │               │
        ▼               ▼               ▼
   ┌─────────┐    ┌───────────┐   ┌────────────┐
   │  OWNER  │    │AUTHORIZED │   │THIRD-PARTY │
   └────┬────┘    └─────┬─────┘   └─────┬──────┘
        │               │               │
        ▼               ▼               ▼
┌───────────────┐ ┌───────────┐ ┌─────────────────┐
│ Full Access   │ │ Standard  │ │ SANDBOXED       │
│               │ │ Access    │ │                 │
│ • All tools   │ │           │ │ • Conversation  │
│ • Shell       │ │ • Tools   │ │   only          │
│ • File system │ │   per     │ │ • No tools      │
│ • Send msgs   │ │   config  │ │ • Optional      │
│ • Dangerous   │ │ • Limited │ │   local LLM     │
│   operations  │ │   shell   │ │ • Relay to      │
│               │ │           │ │   owner         │
└───────────────┘ └───────────┘ └─────────────────┘
```

**Config**:
```json
{
  "trust": {
    "owner_phones": ["+1234567890"],
    "owner_telegram": [123456789],
    "third_party": {
      "enabled": true,
      "use_local_llm": true,
      "allow_relay": true
    }
  }
}
```

---

## SLIDE 21: Guarded Tools

### Human-in-the-Loop Approval

```
┌─────────────────────────────────────────────────────────────┐
│                 APPROVAL WORKFLOW                           │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│   1. Agent calls guarded tool (e.g., Bash)                  │
│                                                             │
│   2. BeforeTool callback intercepts                         │
│      ┌─────────────────────────────────────┐               │
│      │ Tool: Bash                          │               │
│      │ Command: rm -rf ~/old-project       │               │
│      │ Skill: cleanup                      │               │
│      │ Risk: HIGH                          │               │
│      └─────────────────────────────────────┘               │
│                                                             │
│   3. Request queued, agent continues other work             │
│                                                             │
│   4. User reviews in dashboard/Telegram                     │
│      ┌─────────────────────────────────────┐               │
│      │  [APPROVE]  [REJECT]  [MODIFY]      │               │
│      └─────────────────────────────────────┘               │
│                                                             │
│   5. Agent retries (approved) or adapts (rejected)          │
│                                                             │
│   6. Decision logged to audit trail                         │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

**Skill Config**:
```yaml
---
name: deploy
guarded_tools: [Bash]     # Require approval
notify_tools: [Write]     # Auto-exec + notify
---
```

---

## SLIDE 22: Command Sandbox

### Dangerous Pattern Blocking

```
┌─────────────────────────────────────────────────────────────┐
│                 COMMAND SANDBOX                             │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  BLOCKED PATTERNS (25+):                                    │
│                                                             │
│  ┌──────────────────────────────────────────────────────┐  │
│  │ • rm -rf /                    Recursive delete root  │  │
│  │ • :(){ :|:& };:               Fork bomb              │  │
│  │ • chmod -R 777 /              World-writable root    │  │
│  │ • dd if=/dev/zero of=/dev/sda Disk wipe             │  │
│  │ • mkfs.*                      Filesystem format      │  │
│  │ • > /etc/passwd               Credential wipe        │  │
│  │ • curl | sh                   Remote code exec       │  │
│  │ • sudo su                     Privilege escalation   │  │
│  │ • nc -l -p                    Reverse shell          │  │
│  │ • ssh-keygen overwrite        Key replacement        │  │
│  └──────────────────────────────────────────────────────┘  │
│                                                             │
│  CUSTOM PATTERNS:                                           │
│  ┌──────────────────────────────────────────────────────┐  │
│  │ Configurable per-deployment regex patterns           │  │
│  │ E.g., block access to production databases           │  │
│  └──────────────────────────────────────────────────────┘  │
│                                                             │
│  VALIDATION: Before tool execution, always                  │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

---

## SLIDE 23: Prompt Injection Mitigations

### Multi-Layer Defense

```
┌─────────────────────────────────────────────────────────────┐
│            PROMPT INJECTION DEFENSES                        │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  1. TRUST TIER SANDBOXING                                   │
│     Third-party users = conversation only                   │
│     No tools = no injection attack surface                  │
│                                                             │
│  2. CONTEXT MANAGEMENT                                      │
│     ┌──────────────────────────────────────────────────┐   │
│     │ 60% tokens: Truncate tool outputs (8000 chars)   │   │
│     │ 75% tokens: Consolidate messages (first 2+last 8)│   │
│     │ 90% tokens: Emergency prune + task re-injection  │   │
│     └──────────────────────────────────────────────────┘   │
│                                                             │
│  3. TASK RE-INJECTION                                       │
│     Original goal always restored after pruning             │
│     Agent cannot "forget" what it was asked to do           │
│                                                             │
│  4. BINARY STRIPPING                                        │
│     Images/attachments removed before persistence           │
│     Reduces hidden payload attack surface                   │
│                                                             │
│  5. TOOL FILTERING                                          │
│     Skills can only use declared tools                      │
│     Injected prompts can't access unauthorized tools        │
│                                                             │
│  6. APPROVAL WORKFLOWS                                      │
│     Dangerous operations require human confirmation         │
│     Even successful injection hits human checkpoint         │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

---

## SLIDE 24: Security Audit

### Built-in Security Checking

```bash
$ kaggen security-audit

┌─────────────────────────────────────────────────────────────┐
│                   SECURITY AUDIT                            │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  [✓] File permissions                                       │
│      ~/.kaggen/secrets.enc: 0600 (owner-only)               │
│      ~/.kaggen/tokens.json: 0600 (owner-only)               │
│                                                             │
│  [✓] Gateway binding                                        │
│      Bound to 127.0.0.1:18789 (localhost only)              │
│                                                             │
│  [!] CORS origins                                           │
│      Warning: Wildcard origin detected                      │
│                                                             │
│  [✓] Command sandbox                                        │
│      25 patterns active                                     │
│                                                             │
│  [✓] Credentials check                                      │
│      No plaintext secrets in config                         │
│                                                             │
│  [✓] Database SSL                                           │
│      PostgreSQL SSL mode: require                           │
│                                                             │
└─────────────────────────────────────────────────────────────┘

$ kaggen security-audit --fix
# Auto-remediate where possible
```

---

## SLIDE 25: OAuth Integration

### Secure Token Management

```
┌─────────────────────────────────────────────────────────────┐
│                    OAUTH FLOW                               │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│   SKILL.md                                                  │
│   ┌───────────────────┐                                    │
│   │ oauth_providers:  │                                    │
│   │   - google        │                                    │
│   └─────────┬─────────┘                                    │
│             │                                               │
│             ▼                                               │
│   ┌───────────────────────────────────────────────────┐    │
│   │            Authorization Code Flow                 │    │
│   │                                                    │    │
│   │  1. User visits /oauth/google/authorize            │    │
│   │  2. Redirect to Google consent screen              │    │
│   │  3. User approves, callback with code              │    │
│   │  4. Exchange code for tokens (PKCE)                │    │
│   │  5. Store encrypted in SQLite                      │    │
│   └───────────────────────────────────────────────────┘    │
│             │                                               │
│             ▼                                               │
│   ┌───────────────────────────────────────────────────┐    │
│   │            Automatic Token Refresh                 │    │
│   │                                                    │    │
│   │  • Tokens checked before each API call             │    │
│   │  • Automatic refresh if expired                    │    │
│   │  • Encrypted storage with AES-256-GCM              │    │
│   └───────────────────────────────────────────────────┘    │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

---

## SLIDE 26: Async Task Dispatch

### Background Task Execution

```
┌─────────────────────────────────────────────────────────────┐
│                 ASYNC DISPATCH                              │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│   User: "Generate a comprehensive market analysis report"   │
│                                                             │
│   ┌─────────────────────────────────────────────────────┐  │
│   │              Coordinator                             │  │
│   │                                                      │  │
│   │  "This is complex work. I'll dispatch it async."     │  │
│   │                                                      │  │
│   │  dispatch_task("researcher", "market analysis...")   │  │
│   └───────────────────────────┬─────────────────────────┘  │
│                               │                             │
│   ┌───────────────────────────┼─────────────────────────┐  │
│   │           IMMEDIATE       │        BACKGROUND       │  │
│   │                           │                         │  │
│   │   Response to user:       │   Goroutine running:    │  │
│   │   "Started task abc123.   │   • Web searches        │  │
│   │    I'll notify you when   │   • Data gathering      │  │
│   │    it's complete."        │   • Analysis            │  │
│   │                           │   • Report writing      │  │
│   │                           │                         │  │
│   │   User can:               │   15-minute timeout     │  │
│   │   • Ask other questions   │                         │  │
│   │   • Check status          │   On completion:        │  │
│   │   • Continue working      │   → Callback injected   │  │
│   │                           │   → User notified       │  │
│   └───────────────────────────┴─────────────────────────┘  │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

---

## SLIDE 27: Future Work - Protocol Support

### Expanding Integration Capabilities

```
┌─────────────────────────────────────────────────────────────┐
│              PROTOCOL ROADMAP                               │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  ✅ COMPLETED                                               │
│  ┌────────────────────────────────────────────────────┐    │
│  │ • OAuth 2.0 with PKCE                              │    │
│  │ • Automatic token refresh                          │    │
│  │ • Multi-provider support (Google, GitHub)          │    │
│  └────────────────────────────────────────────────────┘    │
│                                                             │
│  🔜 PRIORITY 2: EMAIL                                       │
│  ┌────────────────────────────────────────────────────┐    │
│  │ • SMTP/IMAP integration                            │    │
│  │ • OAuth + app password support                     │    │
│  │ • Send/read/search/manage inbox                    │    │
│  └────────────────────────────────────────────────────┘    │
│                                                             │
│  📋 PRIORITY 3: CALDAV/CARDDAV                              │
│  ┌────────────────────────────────────────────────────┐    │
│  │ • Calendar management (Google, iCloud, Fastmail)   │    │
│  │ • Contact sync                                     │    │
│  │ • Free-busy queries                                │    │
│  └────────────────────────────────────────────────────┘    │
│                                                             │
│  📋 PRIORITY 4: WEBSOCKET                                   │
│  ┌────────────────────────────────────────────────────┐    │
│  │ • Real-time chat (Slack, Discord)                  │    │
│  │ • Live data feeds                                  │    │
│  │ • Bidirectional communication                      │    │
│  └────────────────────────────────────────────────────┘    │
│                                                             │
│  📋 PRIORITY 5: GRAPHQL                                     │
│  ┌────────────────────────────────────────────────────┐    │
│  │ • GitHub API operations                            │    │
│  │ • Shopify, Contentful, Hasura                      │    │
│  └────────────────────────────────────────────────────┘    │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

---

## SLIDE 28: Future Work - Business Builder

### Business Builder Vision

```
┌─────────────────────────────────────────────────────────────┐
│           AUTONOMOUS BUSINESS CREATION                      │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  12 Specialized Pipelines:                                  │
│                                                             │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐         │
│  │   Market    │  │ Competitive │  │   Legal &   │         │
│  │ Validation  │  │Intelligence │  │ Compliance  │         │
│  └─────────────┘  └─────────────┘  └─────────────┘         │
│                                                             │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐         │
│  │   Design    │  │  Software   │  │  Payment &  │         │
│  │   System    │  │Development  │  │Monetization │         │
│  └─────────────┘  └─────────────┘  └─────────────┘         │
│                                                             │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐         │
│  │   DevOps    │  │  Content &  │  │  Marketing  │         │
│  │   Infra     │  │    SEO      │  │  Campaign   │         │
│  └─────────────┘  └─────────────┘  └─────────────┘         │
│                                                             │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐         │
│  │  Growth &   │  │  Customer   │  │ Operations  │         │
│  │ Analytics   │  │  Success    │  │ & Scaling   │         │
│  └─────────────┘  └─────────────┘  └─────────────┘         │
│                                                             │
│  Each pipeline: Agents → Claude Code → Issue tracking       │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

---

## SLIDE 29: Epistemic Memory - Overview

### Not All Memories Are Equal

Inspired by the [Hindsight paper](https://arxiv.org/abs/2512.12818) on epistemic memory for AI agents.

```
┌─────────────────────────────────────────────────────────────┐
│              EPISTEMIC MEMORY                               │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  Core Insight: Different types of information need         │
│  different treatment                                        │
│                                                             │
│  ┌─────────────────────────────────────────────────────┐   │
│  │                                                     │   │
│  │   A NAME is different from a PREFERENCE             │   │
│  │                                                     │   │
│  │   A PREFERENCE is different from an EVENT           │   │
│  │                                                     │   │
│  │   An EVENT is different from a PATTERN              │   │
│  │                                                     │   │
│  └─────────────────────────────────────────────────────┘   │
│                                                             │
│  Kaggen understands these differences and uses them to     │
│  recall the right thing at the right time.                 │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

---

## SLIDE 30: Epistemic Memory - Four Memory Types

### Classification System

```
┌─────────────────────────────────────────────────────────────┐
│              FOUR KINDS OF MEMORY                           │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  ┌──────────────┬────────────────────────────────────────┐ │
│  │    TYPE      │           WHAT IT MEANS                 │ │
│  ├──────────────┼────────────────────────────────────────┤ │
│  │              │                                        │ │
│  │   FACT       │  Something objectively true            │ │
│  │              │  "User works as a software engineer"   │ │
│  │              │                                        │ │
│  ├──────────────┼────────────────────────────────────────┤ │
│  │              │                                        │ │
│  │  EXPERIENCE  │  Something that happened               │ │
│  │              │  "User relocated to Berlin in Jan 2025"│ │
│  │              │                                        │ │
│  ├──────────────┼────────────────────────────────────────┤ │
│  │              │                                        │ │
│  │   OPINION    │  A preference or belief                │ │
│  │              │  "User prefers Go over Rust for CLIs"  │ │
│  │              │                                        │ │
│  ├──────────────┼────────────────────────────────────────┤ │
│  │              │                                        │ │
│  │ OBSERVATION  │  A pattern Kaggen noticed              │ │
│  │              │  "User discusses work on weekday AM"   │ │
│  │              │                                        │ │
│  └──────────────┴────────────────────────────────────────┘ │
│                                                             │
│  Facts, Experiences, Opinions → from conversations          │
│  Observations → Kaggen generates by spotting patterns       │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

---

## SLIDE 31: Epistemic Memory - Extraction Pipeline

### How Memories Are Created

```
┌─────────────────────────────────────────────────────────────┐
│              MEMORY EXTRACTION                              │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│                    Conversation                             │
│                         │                                   │
│                         ▼                                   │
│              ┌─────────────────────┐                       │
│              │  Memory Extractor   │  Reviews what was said│
│              │  (Background)       │  Decides what to keep │
│              └──────────┬──────────┘                       │
│                         │                                   │
│           For each new memory:                              │
│                         │                                   │
│         ┌───────────────┼───────────────┐                  │
│         │               │               │                  │
│         ▼               ▼               ▼                  │
│    ┌─────────┐    ┌─────────┐    ┌─────────┐              │
│    │CLASSIFY │    │   TAG   │    │  RATE   │              │
│    │         │    │         │    │         │              │
│    │ Fact?   │    │ Who?    │    │ How     │              │
│    │ Opinion?│    │ What?   │    │ certain?│              │
│    │ Event?  │    │ When?   │    │ (0-1)   │              │
│    └─────────┘    └─────────┘    └─────────┘              │
│         │               │               │                  │
│         └───────────────┼───────────────┘                  │
│                         ▼                                   │
│              ┌─────────────────────┐                       │
│              │   Store with all    │                       │
│              │     metadata        │                       │
│              └─────────────────────┘                       │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

**Example stored memory**:
- Type: `opinion`
- Content: "User prefers Go over Rust"
- Confidence: `0.8`
- Entities: `[Go, Rust]`
- Topics: `[preferences, programming]`

---

## SLIDE 32: Epistemic Memory - Confidence Evolution

### Opinions Change Over Time

```
┌─────────────────────────────────────────────────────────────┐
│              CONFIDENCE TRACKING                            │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  Opinions aren't static. Kaggen tracks how they evolve.     │
│                                                             │
│  ┌─────────────────────────────────────────────────────┐   │
│  │                                                     │   │
│  │    +1.0 ├─────────────────────...............       │   │
│  │        │                   ...                      │   │
│  │        │                 ..                         │   │
│  │        │               ..                           │   │
│  │        │             .    ◄── Confidence rises      │   │
│  │        │           .         as user repeats        │   │
│  │    +0.5├─────────.           the same preference    │   │
│  │        │       .                                    │   │
│  │        │     .                                      │   │
│  │        │   .                                        │   │
│  │        │ .                                          │   │
│  │    0.0 ├───┬───┬───┬───┬───┬───                     │   │
│  │            1   2   3   4   5   (mentions over time) │   │
│  │                                                     │   │
│  └─────────────────────────────────────────────────────┘   │
│                                                             │
│  • Same preference repeated → confidence increases          │
│  • Contradictory statement → confidence decreases           │
│  • Smoothing formula prevents flip-flopping                 │
│  • One offhand remark won't erase an established opinion    │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

---

## SLIDE 33: Epistemic Memory - Entity Graph

### Building a Web of Connections

```
┌─────────────────────────────────────────────────────────────┐
│              ENTITY GRAPH                                   │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  Kaggen doesn't just remember text—it builds relationships. │
│                                                             │
│  ┌─────────────────────────────────────────────────────┐   │
│  │                                                     │   │
│  │              Berlin  ────────  Germany              │   │
│  │               /                     \               │   │
│  │              /                       \              │   │
│  │         User lives              User visited        │   │
│  │              \                       /              │   │
│  │               \                     /               │   │
│  │            Software Eng. ── Go ── Rust              │   │
│  │                              \                      │   │
│  │                               \                     │   │
│  │                             CLI tools               │   │
│  │                                                     │   │
│  └─────────────────────────────────────────────────────┘   │
│                                                             │
│  How it works:                                              │
│  • Two entities in same memory → connection strengthens     │
│  • Enables answering questions never directly asked         │
│                                                             │
│  Example:                                                   │
│    Q: "What do you know about Germany?"                     │
│    → Surfaces Berlin memory even if "Germany" never said    │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

---

## SLIDE 34: Epistemic Memory - Four-Way Recall

### Multi-Channel Search

```
┌─────────────────────────────────────────────────────────────┐
│              FOUR-WAY RECALL                                │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│               "What did I do in Berlin?"                    │
│                         │                                   │
│         ┌───────────────┼───────────────┐                  │
│         │       │       │       │       │                  │
│         ▼       ▼       ▼       ▼       ▼                  │
│    ┌────────┐ ┌────────┐ ┌────────┐ ┌────────┐            │
│    │MEANING │ │KEYWORD │ │ GRAPH  │ │  TIME  │            │
│    │        │ │        │ │        │ │        │            │
│    │Similar │ │ Exact  │ │Entity  │ │Temporal│            │
│    │semantic│ │ word   │ │relation│ │ range  │            │
│    │meaning │ │ match  │ │  walk  │ │ parse  │            │
│    └───┬────┘ └───┬────┘ └───┬────┘ └───┬────┘            │
│        │          │          │          │                  │
│        └──────────┴────┬─────┴──────────┘                  │
│                        │                                   │
│                        ▼                                   │
│              ┌─────────────────────┐                       │
│              │  Reciprocal Rank    │                       │
│              │     Fusion          │                       │
│              │                     │                       │
│              │ Higher weight to    │                       │
│              │ memories appearing  │                       │
│              │ in multiple channels│                       │
│              └──────────┬──────────┘                       │
│                         │                                   │
│                         ▼                                   │
│              Best memories, ranked                          │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

---

## SLIDE 35: Epistemic Memory - Observation Synthesis

### Self-Generated Insights

```
┌─────────────────────────────────────────────────────────────┐
│           BACKGROUND OBSERVATION SYNTHESIS                  │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  Periodically, Kaggen reviews entities with 3+ memories     │
│  and asks: "What do I know about this?"                     │
│                                                             │
│  ┌─────────────────────────────────────────────────────┐   │
│  │                                                     │   │
│  │  Memories about "Go":                               │   │
│  │                                                     │   │
│  │    • User prefers Go over Rust for CLI tools        │   │
│  │    • User works as a software engineer              │   │
│  │    • User built a project in Go last month          │   │
│  │                                                     │   │
│  └─────────────────────────────────────────────────────┘   │
│                         │                                   │
│                         ▼  (Synthesis)                      │
│  ┌─────────────────────────────────────────────────────┐   │
│  │                                                     │   │
│  │  NEW OBSERVATION:                                   │   │
│  │                                                     │   │
│  │  "The user is a software engineer who actively      │   │
│  │   uses Go for projects and prefers it over Rust     │   │
│  │   for command-line tooling."                        │   │
│  │                                                     │   │
│  └─────────────────────────────────────────────────────┘   │
│                                                             │
│  Observations participate in search like any other memory,  │
│  enabling recall of synthesized knowledge.                  │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

---

## SLIDE 36: Epistemic Memory - Complete System

### The Virtuous Cycle

```
┌─────────────────────────────────────────────────────────────┐
│           EPISTEMIC MEMORY ARCHITECTURE                     │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│            Conversation                                     │
│                 │                                           │
│                 ▼                                           │
│       ┌──────────────────┐                                 │
│       │   Extraction     │  Classify, tag, rate            │
│       └────────┬─────────┘                                 │
│                │                                           │
│       ┌────────┴────────┐                                  │
│       │                 │                                  │
│       ▼                 ▼                                  │
│  ┌─────────┐      ┌──────────┐                            │
│  │Memories │◄────►│ Entity   │                            │
│  │   DB    │      │  Graph   │                            │
│  └────┬────┘      └────┬─────┘                            │
│       │                │                                   │
│       │     ┌──────────┘                                   │
│       │     │                                              │
│       ▼     ▼                                              │
│  ┌────────────────┐         ┌─────────────────┐           │
│  │   Four-Way     │ ◄─────► │   Background    │           │
│  │    Recall      │         │   Synthesis     │           │
│  └───────┬────────┘         └────────┬────────┘           │
│          │                           │                     │
│          ▼                           ▼                     │
│   Relevant memories           New observations             │
│   for conversation            added to memory DB           │
│                                                             │
│  ═══════════════════════════════════════════════════════   │
│  Conversations → Memories → Entity Graph → Richer Recall   │
│  → Background Synthesis → New Observations → Even Better   │
│  ═══════════════════════════════════════════════════════   │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

---

## SLIDE 37: Future Work - Advanced P2P

### Beyond Direct Connections

```
┌─────────────────────────────────────────────────────────────┐
│              ADVANCED P2P FEATURES                          │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  ✅ IMPLEMENTED: Direct P2P via libp2p                      │
│     • Protobuf streaming over QUIC/TCP                      │
│     • QR code pairing from dashboard                        │
│     • Noise authentication                                  │
│                                                             │
│  🔜 PLANNED: NAT Traversal & Discovery                      │
│  ┌────────────────────────────────────────────────────┐    │
│  │                                                    │    │
│  │  Problem: Both peers behind NAT                    │    │
│  │                                                    │    │
│  │  [Mobile]  ◄────X────►  [Kaggen]                   │    │
│  │     NAT                    NAT                     │    │
│  │                                                    │    │
│  │  Solution: DHT + Relay                             │    │
│  │                                                    │    │
│  │  [Mobile]                                          │    │
│  │      │                                             │    │
│  │      ▼                                             │    │
│  │  [Kademlia DHT] ─── Peer Discovery ───┐            │    │
│  │      │                                │            │    │
│  │      ▼                                ▼            │    │
│  │  [Circuit Relay v2] ◄────────► [Kaggen]            │    │
│  │                                                    │    │
│  │  • Automatic peer discovery via rendezvous         │    │
│  │  • Relay fallback when direct fails                │    │
│  │  • UDP hole punching via UDX                       │    │
│  └────────────────────────────────────────────────────┘    │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

---

## SLIDE 38: Closing - Key Takeaways

### What Makes Kaggen Different

```
┌─────────────────────────────────────────────────────────────┐
│              KEY DIFFERENTIATORS                            │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  1. PROVIDER AGNOSTIC                                       │
│     Not locked to a single LLM vendor                       │
│     Switch models based on cost, capability, availability   │
│                                                             │
│  2. EXTENSIBLE WITHOUT CODE                                 │
│     Skills are YAML + Markdown                              │
│     Hot-reload via SIGHUP, zero downtime                    │
│                                                             │
│  3. SECURITY-FIRST                                          │
│     Trust tiers, secrets management, approval workflows     │
│     Defense in depth, not afterthought                      │
│                                                             │
│  4. PRODUCTION-READY                                        │
│     Multi-channel (CLI, WebSocket, Telegram, P2P)           │
│     Native mobile via libp2p + async tasks + OAuth          │
│                                                             │
│  5. INTELLIGENT ROUTING                                     │
│     Coordinator pattern: right model for each task          │
│     Cheap router, capable specialists                       │
│                                                             │
│  6. EPISTEMIC MEMORY                                        │
│     Four memory types with confidence tracking              │
│     Entity graph + four-way recall + synthesis              │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

---

## SLIDE 39: Closing - Vision

### The Goal

```
┌─────────────────────────────────────────────────────────────┐
│                    KAGGEN VISION                            │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│                                                             │
│           "A personal AI assistant that is                  │
│            TRULY yours—extensible, secure,                  │
│            and available everywhere."                       │
│                                                             │
│                                                             │
│  ┌─────────────────────────────────────────────────────┐   │
│  │                                                     │   │
│  │   YOUR SKILLS         Define what it can do         │   │
│  │                                                     │   │
│  │   YOUR MODELS         Choose your providers         │   │
│  │                                                     │   │
│  │   YOUR DATA           Stays on your infrastructure  │   │
│  │                                                     │   │
│  │   YOUR CONTROL        Approval workflows + trust    │   │
│  │                                                     │   │
│  └─────────────────────────────────────────────────────┘   │
│                                                             │
│                                                             │
│  Named after the mantis deity—a trickster who creates       │
│  through cleverness and wisdom, not brute force.            │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

---

## SLIDE 40: Thank You

### Questions?

```
┌─────────────────────────────────────────────────────────────┐
│                                                             │
│                                                             │
│                     THANK YOU                               │
│                                                             │
│                                                             │
│                  Questions & Discussion                     │
│                                                             │
│                                                             │
│                                                             │
│  Resources:                                                 │
│  ───────────────────────────────────────────────────────   │
│                                                             │
│  docs/ARCHITECTURE.md          - Technical deep dive        │
│  docs/skills-guide.md          - Skill authoring guide      │
│  docs/EPISTEMIC_MEMORY.md      - Memory system design       │
│  docs/p2p-integration-guide.md - Mobile client development  │
│  docs/OAUTH_GUIDE.md           - OAuth integration          │
│                                                             │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

---

*End of presentation*
