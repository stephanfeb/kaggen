# Complex Task Orchestration

Architectural design for enabling Kaggen to handle complex, multi-step tasks through LLM-driven decomposition and specialist sub-agents.

## Problem

Kaggen currently uses a single `llmagent.LLMAgent` to handle all tasks. For simple requests this works well, but complex tasks (multi-step research, software projects, data pipelines, system administration workflows) hit several limitations:

- **Blocking execution**: Long-running tool calls block the agent. The bot can't respond to the user during execution.
- **Single-shot prompting**: No task decomposition — the LLM must handle everything in one pass.
- **No specialisation**: One agent with one instruction set handles all task types.
- **No failure recovery**: If a step fails, there's no structured retry or escalation mechanism.

## Target Architecture

```
User (Telegram / WebSocket / CLI)
  │
  ▼
Coordinator Agent (team.New — Coordinator pattern)
  │  - Receives user messages
  │  - Reasons about task complexity
  │  - Decomposes work into sub-tasks
  │  - Delegates to specialist sub-agents (as tools)
  │  - Decides sequential vs parallel invocation
  │  - Synthesizes results and responds to user
  │  - Escalates to user after N sub-agent failures
  │
  ├── Skill-derived sub-agents (auto-generated from skills/)
  │   ├── pandoc-agent (document conversion)
  │   ├── sqlite3-agent (database operations)
  │   ├── imagemagick-agent (image processing)
  │   └── ... (any new skill = new sub-agent automatically)
  │
  └── General-purpose agent (exec, read, write, claude-code)
```

### Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Task decomposition | LLM-driven (dynamic) | Handles novel/ad-hoc tasks without predefined workflows |
| Sub-agent definition | Skill-derived | Scales with the skill system — add a skill, get a sub-agent |
| Concurrency | LLM decides | Coordinator chooses sequential or parallel based on task dependencies |
| Result reporting | Sub-agent → Coordinator → User | Coordinator synthesizes and communicates; sub-agents don't talk to user directly |
| Failure handling | CycleAgent with escalation | Retry N times, then escalate to coordinator, who can ask the user |

## trpc-agent-go Framework Primitives

The framework (already a Kaggen dependency) provides all the building blocks:

### Coordinator Team (`team.New`)

A coordinator agent that receives specialist agents as callable tools. The coordinator decides which specialists to invoke, in what order, and synthesizes their outputs into a final response.

```go
coordinator := team.New(
    coordinatorAgent,  // The orchestrating LLM agent
    team.WithMembers(specialists...),  // Skill-derived sub-agents
)
```

- Members are automatically wrapped as tools the coordinator can call
- `WithStreamInner()` can forward sub-agent events for observability
- `WithHistoryScope(team.HistoryScopeIsolated)` keeps sub-agent contexts separate
- Teams implement `agent.Agent`, so the coordinator is a drop-in replacement for the current single agent

### AgentTool (`agent.AgentTool`)

Wraps any agent as a callable tool. Used internally by the Team pattern, but also useful standalone — e.g., wrapping the general-purpose agent as a tool the coordinator can invoke.

### CycleAgent (`multiagent.CycleAgent`)

Iterates sub-agents in a loop until a stopping condition is met. Used *within* a specialist sub-agent when iterative refinement is needed (e.g., build → evaluate → improve).

```go
cycle := multiagent.NewCycleAgent("refine", agents,
    multiagent.WithMaxIterations(5),
    multiagent.WithEscalationFunc(func(evt *event.Event) bool {
        // Return true to stop the cycle
        // After max iterations, escalate to coordinator
    }),
)
```

### Other Available Primitives

| Primitive | Use Case | When to Adopt |
|-----------|----------|---------------|
| ChainAgent | Fixed sequential pipeline | When task phases are known upfront |
| ParallelAgent | Multi-perspective analysis on same input | Research tasks needing diverse viewpoints |
| GraphAgent | DAG workflows with checkpoints & approval gates | Repeatable structured workflows (future) |
| Custom Agent | Full control over execution flow | Edge cases not covered by built-in patterns |

## Skill-Derived Sub-Agents

Each skill in `skills/` and `~/.kaggen/skills/` already has a `SKILL.md` with frontmatter:

```yaml
---
name: pandoc
description: Convert documents between formats using Pandoc
---
```

At startup, Kaggen auto-generates a sub-agent for each skill:

1. Read `SKILL.md` frontmatter for name and description
2. Read the full `SKILL.md` body as the sub-agent's instruction
3. Create an `llmagent.New()` with the skill's tools/scripts
4. Register as a member of the Coordinator Team

The **general-purpose agent** is always present and handles tasks that don't match any skill — including Claude Code invocations via `exec`.

### Adding a New Specialist

To add a new specialist capability:

1. Create `skills/<name>/SKILL.md` with frontmatter and instructions
2. Add any scripts to `skills/<name>/scripts/`
3. Restart Kaggen — the new sub-agent is automatically available

No code changes required.

## Async Execution Model

All sub-agent invocations are async by default. There is no attempt to predict task duration — fast tasks complete immediately, slow tasks run in the background. The coordinator never blocks on a sub-agent.

### Execution Flow

```
User message ──────────────┐
                           ▼
                    Coordinator Agent
                     │           ▲
          dispatch   │           │  completion events
                     ▼           │
              Async Sub-agents (goroutines)
                │    │    │
                ▼    ▼    ▼
             task1 task2 task3
```

1. **Dispatch**: Coordinator calls a sub-agent tool. The tool spawns a goroutine running the sub-agent and returns immediately with `{ status: "accepted", taskId: "..." }`.
2. **Continue**: Coordinator remains responsive — it can process new user messages, dispatch more sub-agents, or answer questions while background work runs.
3. **Completion**: When a sub-agent finishes, it produces a completion event containing the result (or error). This event flows back to the coordinator, not directly to the user.
4. **Synthesis**: The coordinator receives the completion event, reasons about it (combine with other results, check if more work is needed), and communicates the synthesized outcome to the user.

### Coordinator Dual Input

The coordinator handles two types of input:

- **User messages** — from Telegram/WebSocket/CLI, as today
- **Sub-agent completion events** — internal messages injected into the coordinator's session when background tasks finish

Both trigger a new coordinator reasoning turn. The coordinator sees a completion event the same way it sees a user message — it reasons about what to do next.

### Completion Trigger Policy

The coordinator specifies a trigger policy per dispatched task:

| Policy | Behavior | Use Case |
|--------|----------|----------|
| `auto` | Completion event immediately triggers a coordinator turn and user notification | Default — most tasks |
| `queue` | Results are queued; coordinator processes them on the next user message | Low-priority batch work, background maintenance |

The coordinator decides the policy at dispatch time based on context. Example: a user-requested build is `auto`; a background memory cleanup is `queue`.

### Implementation: Async Tool Wrapper

A custom `AsyncAgentTool` wraps any sub-agent as an async callable tool:

```go
type AsyncAgentTool struct {
    agent      agent.Agent
    taskStore  *TaskStore       // tracks in-flight tasks
    completeFn CompletionFunc   // callback to inject completion events
}

// Execute spawns the sub-agent in a goroutine and returns immediately.
func (t *AsyncAgentTool) Execute(ctx context.Context, args AsyncTaskArgs) (*AsyncTaskResult, error) {
    taskID := uuid.New().String()
    t.taskStore.Register(taskID, args)

    go func() {
        result, err := t.runAgent(ctx, args)
        t.completeFn(taskID, result, err)  // inject completion event
    }()

    return &AsyncTaskResult{
        Status: "accepted",
        TaskID: taskID,
    }, nil
}
```

### Task Store

An in-memory store tracks in-flight tasks:

```go
type TaskStore struct {
    mu    sync.RWMutex
    tasks map[string]*TaskState
}

type TaskState struct {
    ID        string
    AgentName string
    Args      AsyncTaskArgs
    Status    string        // "running", "completed", "failed"
    Result    any
    StartedAt time.Time
    Policy    TriggerPolicy // "auto" or "queue"
}
```

The coordinator can query task status (e.g., "what's still running?") via a `task_status` tool that reads from the store.

### Completion Event Injection

When a sub-agent completes, the `CompletionFunc` must:

1. Update the task store with the result
2. If policy is `auto`: inject a completion message into the coordinator's session and trigger a new agent run
3. If policy is `queue`: store the result; it will be included as context on the next user-triggered run

Injection requires access to the session service and the handler's respond function — this is wired up at initialization time.

## Failure Handling & Escalation

When a sub-agent fails:

1. **Retry within the sub-agent** — If using CycleAgent, retry up to N iterations
2. **Report failure to coordinator** — Sub-agent returns error context
3. **Coordinator decides next action**:
   - Try a different approach or sub-agent
   - Ask the user for guidance (escalate)
   - Abort and report what happened

The coordinator's instruction should include guidance on escalation:

```
When a sub-agent fails after multiple attempts, do not keep retrying.
Instead, summarize what was attempted, what failed, and ask the user
how they'd like to proceed.
```

## Autonomous Execution (Self-Triggered Work)

The coordinator can be triggered not only by user messages but by cron schedules, webhooks, and heartbeats — all via Kaggen's existing proactive engine. This enables the bot to autonomously identify and execute useful work.

### Wakeup Flow

```
Cron trigger (e.g. "@every 30m" or "0 9 * * 1-5")
  │
  ▼
Proactive engine injects wakeup prompt into coordinator
  │
  ▼
Coordinator reasons about what to work on:
  │
  ├── 1. Check work backlog → explicit tasks (user or self-created)
  │
  ├── 2. Scan memory + recent sessions → inferred opportunities
  │      (open follow-ups, stale items, patterns worth acting on)
  │
  ├── 3. Prioritize: explicit tasks first, then inferred work
  │
  ├── 4. Dispatch sub-agents async for selected tasks
  │
  └── 5. Completion → synthesize → notify user of what was accomplished
```

The proactive engine already handles the trigger mechanics (cron scheduling, retry with backoff, timeout, history tracking). The new parts are the **work backlog** and the **wakeup prompt pattern**.

### Work Backlog

A persistent, queryable task list that serves as the bot's work queue. Distinct from the in-memory `TaskStore` that tracks in-flight async sub-agent executions — this is a **durable backlog** that persists across sessions and restarts.

**Sources of tasks:**
- **User-created**: User tells the bot "add to your backlog: research X" or "next time you wake up, build phase 2 of the dashboard"
- **Coordinator-created**: Bot discovers work during conversations — "I should follow up on Y" — and adds it to its own backlog
- **Sub-agent-created**: A sub-agent identifies follow-up work while executing a task

**Schema:**

```go
type BacklogItem struct {
    ID          string         `json:"id"`
    Title       string         `json:"title"`
    Description string         `json:"description"`
    Priority    string         `json:"priority"`     // "high", "normal", "low"
    Status      string         `json:"status"`       // "pending", "in_progress", "completed", "failed", "blocked"
    Source      string         `json:"source"`       // "user", "coordinator", "sub-agent"
    Context     map[string]any `json:"context"`      // links to sessions, files, conversations
    CreatedAt   time.Time      `json:"created_at"`
    UpdatedAt   time.Time      `json:"updated_at"`
}
```

**Storage:** SQLite (alongside existing `proactive.db` and `memory.db`), queryable via a `backlog` tool available to the coordinator.

**Coordinator tools for backlog:**

| Tool | Action |
|------|--------|
| `backlog_list` | List pending tasks, optionally filtered by priority/status |
| `backlog_add` | Add a new task (coordinator or sub-agent self-assigns work) |
| `backlog_update` | Update status, priority, or add context to a task |
| `backlog_complete` | Mark a task as completed with a summary of what was done |

### Memory Scanning for Inferred Work

Beyond explicit tasks, the coordinator scans memory for opportunities:

- **Open threads**: Conversations where the user asked something but the bot couldn't complete it at the time
- **Follow-ups**: "I'll look into that" or "let me get back to you on this"
- **Stale items**: Tasks or information that may need refreshing
- **Patterns**: Recurring user needs that could be proactively addressed

The coordinator uses existing `memory_search` to find relevant context during wakeup, then decides whether to create backlog items or act immediately.

### Wakeup Prompt

The proactive cron job uses an open-ended prompt that gives the coordinator autonomy:

```json
{
  "name": "autonomous-wakeup",
  "schedule": "@every 30m",
  "prompt": "Check your work backlog for pending tasks and review recent memory for useful follow-ups. Prioritize explicit tasks over inferred work. Execute what you can, and update the backlog with your progress. If nothing needs doing, do nothing.",
  "user_id": "system",
  "channel": "telegram",
  "timeout": "15m",
  "max_retries": 1
}
```

Key prompt characteristics:
- **Permissive but bounded**: "execute what you can" but also "if nothing needs doing, do nothing"
- **Priority-aware**: explicit tasks first
- **Self-documenting**: "update the backlog with your progress"
- **No-op safe**: heartbeat-style suppression means silent wakeups don't spam the user

### Safeguards

- **Cost control**: Wakeup frequency and timeout limit how much autonomous work runs. A 30-minute interval with 15-minute timeout means at most ~50% duty cycle.
- **User notification**: Coordinator always notifies the user of completed autonomous work (via `auto` trigger policy). The user sees what was done.
- **Backlog visibility**: User can ask "what's on your backlog?" at any time to see and adjust priorities.
- **Kill switch**: Removing the cron job from config disables autonomous execution entirely.

## Implementation Plan

### What Changes

| File | Change |
|------|--------|
| `internal/agent/agent.go` | Replace single `llmagent.New()` with `team.New()` coordinator pattern. Auto-generate sub-agents from skills. Add general-purpose sub-agent. Wire up completion event injection. |
| `internal/agent/skills.go` (new) | Skill-to-agent generation logic: read SKILL.md, create llmagent, register tools |
| `internal/agent/async.go` (new) | `AsyncAgentTool` wrapper, in-flight `TaskStore`, `CompletionFunc` — the async execution infrastructure |
| `internal/backlog/backlog.go` (new) | Persistent work backlog: SQLite store, `BacklogItem` schema, CRUD operations |
| `internal/tools/backlog.go` (new) | Backlog tools for coordinator: `backlog_list`, `backlog_add`, `backlog_update`, `backlog_complete` |
| `internal/gateway/handler.go` | Support completion event injection: accept internal messages that trigger coordinator runs without a user message |
| `cmd/kaggen/cmd/agent.go` | Pass skill-derived agents and backlog tools to `NewAgent()` |
| `cmd/kaggen/cmd/gateway.go` | Same as above for gateway mode |

### What Doesn't Change

- Channel infrastructure (Telegram, WebSocket, CLI) — coordinator still produces events that flow through channels
- Existing tools (read, write, exec) — available to the general-purpose sub-agent
- Existing skills — they become sub-agents instead of being invoked through the single agent
- Bootstrap files (SOUL.md, IDENTITY.md, etc.) — feed into the coordinator's instruction
- Session service — the coordinator's session persists across user messages and completion events

## Prior Art: Moltbot

Moltbot (formerly Clawdbot, 60k+ GitHub stars) solves similar problems with:

- **Subagents** (`sessions_spawn`): Background agent instances in isolated sessions, announce results back to chat
- **Lobster**: Deterministic pipeline engine with approval gates and resume tokens
- **llm-task**: Isolated LLM calls with JSON Schema validation for use in pipelines

Key difference: Moltbot's orchestration is partly infrastructure-driven (Lobster workflows), partly LLM-driven (subagent spawning). Kaggen's approach is fully LLM-driven, using the trpc-agent-go Team pattern for coordination.

## References

### trpc-agent-go Framework
- [Agent Docs](https://trpc-group.github.io/trpc-agent-go/agent/)
- [Multi-Agent](https://trpc-group.github.io/trpc-agent-go/multiagent/)
- [Team](https://trpc-group.github.io/trpc-agent-go/team/)
- [Graph](https://trpc-group.github.io/trpc-agent-go/graph/)
- [Custom Agent](https://trpc-group.github.io/trpc-agent-go/custom-agent/)

### Moltbot
- [Subagents](https://docs.molt.bot/tools/subagents.md)
- [Lobster Workflow Runtime](https://docs.molt.bot/tools/lobster.md)
- [llm-task Plugin](https://docs.molt.bot/tools/llm-task.md)
- [Agent Loop](https://docs.molt.bot/concepts/agent-loop.md)
- [Multi-Agent Routing](https://docs.molt.bot/concepts/multi-agent)
- [GitHub](https://github.com/clawdbot/clawdbot)
- [Awesome Moltbot Skills](https://github.com/VoltAgent/awesome-moltbot-skills)
