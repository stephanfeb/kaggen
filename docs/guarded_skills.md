# Guarded Skills (Maker-Checker)

Guarded skills add a human-in-the-loop approval gate to agent side-effects. The agent proposes an action (maker), and a human must approve it before execution proceeds (checker).

## Concept

Some skills perform actions where mistakes are costly to reverse — production deployments, destructive database operations, financial transactions. Guarded tools let you declare that specific tools within a skill require explicit human approval before they run.

When a guarded tool is invoked, execution **suspends for that task only**. The agent continues working on other tasks while the approval is pending. Once the user approves or rejects, the agent receives the decision and acts accordingly.

## Configuration

Add `guarded_tools` to the skill's SKILL.md frontmatter:

```yaml
---
name: deploy
description: Deploy services to production
tools: [Bash, Read]
guarded_tools: [Bash]
---
```

The `guarded_tools` list must be a subset of the tools available to the skill. Only tools listed here will require approval — all other tools execute normally.

## Lifecycle

```
Agent calls guarded tool
        │
        ▼
BeforeTool callback intercepts
        │
        ▼
Approval request registered in InFlightStore
(status: pending_approval, external: true)
        │
        ├──► Agent receives "approval required" error
        │    and continues with other tasks
        │
        ▼
User notified via active channels
        │
        ├── Mobile app: approval queue
        ├── Telegram: inline keyboard buttons
        └── WebSocket: approval_required event
        │
        ▼
User approves or rejects
        │
        ├── Approve ──► InjectCompletion fires
        │                Agent retries the tool call
        │
        └── Reject  ──► InjectCompletion fires
                         Agent adapts or informs user
        │
        ▼
Timeout (30 min) ──► Auto-reject if no action taken
```

## Approval Channels

### Mobile App (REST API)

Pending approvals appear in a queue accessible via the dashboard API.

```
GET  /api/approvals                  — list pending approvals
POST /api/approvals/approve          — approve  { "id": "<approval_id>" }
POST /api/approvals/reject           — reject   { "id": "<approval_id>", "reason": "..." }
```

The `GET` response returns an array of pending approval objects:

```json
[
  {
    "id": "abc-123",
    "status": "pending_approval",
    "session_id": "sess-456",
    "approval_request": {
      "tool_name": "Bash",
      "skill_name": "deploy",
      "description": "Bash: {\"command\": \"kubectl apply -f deployment.yaml\"}",
      "arguments": "{\"command\": \"kubectl apply -f deployment.yaml\"}",
      "requested_at": "2025-01-15T10:30:00Z"
    }
  }
]
```

### Telegram

When an approval is required, the bot sends an inline keyboard message to the chat:

```
Approval required: Bash
> kubectl apply -f deployment.yaml

[Approve]  [Reject]
```

Pressing a button immediately resolves the approval. The original message is edited to show the outcome.

### WebSocket

A JSON event is sent to connected clients:

```json
{
  "type": "approval_required",
  "metadata": {
    "approval_id": "abc-123",
    "tool_name": "Bash",
    "skill_name": "deploy",
    "description": "Bash: {\"command\": \"kubectl apply -f deployment.yaml\"}",
    "arguments": "{\"command\": \"kubectl apply -f deployment.yaml\"}"
  }
}
```

Custom UIs can listen for this event type and render their own approval interface, then call the REST API to resolve it.

## Agent Behavior

The agent is instructed to handle approvals gracefully:

- When a tool call returns "Approval required", the agent notes the approval ID and continues with other tasks.
- When it receives a completion message about an approval, it checks whether the action was approved or rejected.
- If approved, the agent retries the original tool call.
- If rejected, the agent informs the user and finds an alternative approach.

This means guarded tools do not block the agent — they block the specific task that triggered the tool call.

## Internals

Guarded tool interception reuses the existing external task infrastructure:

- **InFlightStore** tracks approval requests with `TaskPendingApproval` status and `External: true` flag.
- **RegisterApproval()** creates the task entry with tool name, arguments, skill name, session, and timeout.
- **Complete() / Fail()** resolve the approval, same as external task callbacks.
- **InjectCompletion** delivers the approval result back to the coordinator session.
- The existing **reaper** handles timeout — approval requests that go unanswered for 30 minutes are automatically failed.

### Guarded Tool Registry

During agent construction, `BuildSubAgents` collects `guarded_tools` from all skill frontmatter into a `map[string]string` (tool name to skill name). This map is passed to the agent via `WithGuardedTools()` and consulted in the `BeforeTool` callback.

### Files

| File | Role |
|------|------|
| `internal/agent/skills.go` | Parses `guarded_tools` from SKILL.md frontmatter |
| `internal/agent/async.go` | `TaskPendingApproval` status, `ApprovalRequest` struct, `RegisterApproval()` |
| `internal/agent/agent.go` | BeforeTool interception, `ApprovalNotifyFunc`, guarded tool registry |
| `internal/agent/factory.go` | Wires guarded tools map through agent construction |
| `internal/agent/provider.go` | Delegates `SetApprovalNotifyFunc` to current agent |
| `internal/gateway/handler.go` | Processes approval action messages, calls InjectCompletion |
| `internal/gateway/dashboard.go` | REST endpoints for listing/approving/rejecting |
| `internal/gateway/server.go` | Wires InFlightStore and approval notify function |
| `internal/channel/telegram.go` | Inline keyboard rendering, CallbackQuery handling |
| `cmd/kaggen/cmd/gateway.go` | Wires ApprovalNotifyFunc to channel responders |

## Examples

### Deploy skill with guarded Bash

```yaml
---
name: deploy
description: Deploy services to production environments
tools: [Bash, Read]
guarded_tools: [Bash]
---

# Deploy

Use this skill to deploy services. All bash commands require human approval before execution.

## Available Commands

### deploy.sh

```bash
bash scripts/deploy.sh <service> <environment>
```
```

### Database admin with guarded destructive operations

```yaml
---
name: db-admin
description: Manage database schemas and data
tools: [Bash, Read, Write]
guarded_tools: [Bash]
---

# Database Admin

Manage database schemas, run migrations, and query data. All bash commands (which include database operations) require approval.
```

### Read-only skill (no guards needed)

```yaml
---
name: log-viewer
description: Search and analyze application logs
tools: [Bash, Read]
---

# Log Viewer

Read-only log analysis. No guarded tools needed since all operations are non-destructive.
```
