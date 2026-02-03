# Mobile Client Integration: Approval System

This guide describes how to integrate the maker-checker approval system into a mobile client. The backend handles all approval logic — the mobile client needs to display pending approvals, allow users to approve/reject, and handle real-time notifications.

## Overview

When an agent invokes a guarded tool, execution suspends and an approval request is created. The mobile client can:

1. **Poll for pending approvals** via REST API
2. **Receive real-time notifications** via WebSocket
3. **Approve or reject** via REST API
4. **Display audit history** (optional)

## Data Structures

### Approval Request Object

When fetching pending approvals or receiving WebSocket notifications, the approval object has this shape:

```json
{
  "id": "abc-123-uuid",
  "status": "pending_approval",
  "session_id": "sess-456",
  "user_id": "user-789",
  "agent_name": "deploy",
  "task": "Run command: kubectl apply -f deployment.yaml",
  "started_at": "2025-01-15T10:30:00Z",
  "timeout_at": "2025-01-15T11:00:00Z",
  "approval_request": {
    "tool_name": "Bash",
    "skill_name": "deploy",
    "description": "Run command: kubectl apply -f deployment.yaml",
    "arguments": "{\"command\": \"kubectl apply -f deployment.yaml\"}",
    "requested_at": "2025-01-15T10:30:00Z"
  }
}
```

| Field | Description |
|-------|-------------|
| `id` | Unique approval ID. Use this when approving/rejecting. |
| `status` | Always `"pending_approval"` for active requests. |
| `session_id` | The conversation session that triggered this approval. |
| `user_id` | The user who initiated the action. |
| `agent_name` | The skill/agent that requested the action. |
| `task` | Human-readable summary (same as `approval_request.description`). |
| `started_at` | When the approval request was created. |
| `timeout_at` | When the approval will auto-expire (typically 30 minutes from creation). |
| `approval_request.tool_name` | The tool being invoked (e.g., `Bash`, `Write`, `Edit`). |
| `approval_request.skill_name` | The skill that contains this guarded tool. |
| `approval_request.description` | Human-readable description of the action. |
| `approval_request.arguments` | Raw JSON arguments passed to the tool (for advanced display). |
| `approval_request.requested_at` | Timestamp of the request. |

### Description Format

The `description` field contains a human-readable summary based on the tool type:

| Tool | Description Format | Example |
|------|-------------------|---------|
| Bash | `Run command: <command>` | `Run command: kubectl apply -f deployment.yaml` |
| Write | `Write file: <path>` | `Write file: /app/config.json` |
| Edit | `Edit file: <path>` | `Edit file: /app/src/main.go` |
| Read | `Read file: <path>` | `Read file: /etc/hosts` |
| Other | `<ToolName>: <truncated args>` | `CustomTool: {"key": "value"...}` |

Descriptions are truncated to 200 characters maximum.

## REST API

Base URL: Your Kaggen gateway (e.g., `https://kaggen.example.com` or `http://localhost:18789`).

### List Pending Approvals

```
GET /api/approvals
```

**Response** (200 OK):
```json
[
  {
    "id": "abc-123",
    "status": "pending_approval",
    "session_id": "sess-456",
    "user_id": "user-789",
    "agent_name": "deploy",
    "task": "Run command: kubectl apply -f deployment.yaml",
    "started_at": "2025-01-15T10:30:00Z",
    "timeout_at": "2025-01-15T11:00:00Z",
    "approval_request": {
      "tool_name": "Bash",
      "skill_name": "deploy",
      "description": "Run command: kubectl apply -f deployment.yaml",
      "arguments": "{\"command\": \"kubectl apply -f deployment.yaml\"}",
      "requested_at": "2025-01-15T10:30:00Z"
    }
  }
]
```

Returns an empty array `[]` if no approvals are pending.

### Approve

```
POST /api/approvals/approve
Content-Type: application/json

{
  "id": "abc-123"
}
```

**Response** (200 OK):
```json
{
  "status": "approved",
  "id": "abc-123"
}
```

**Error** (400/404):
```json
{
  "error": "approval not found or already resolved"
}
```

After approval, the agent receives a completion message and retries the tool call.

### Reject

```
POST /api/approvals/reject
Content-Type: application/json

{
  "id": "abc-123",
  "reason": "Not authorized for production deployment"
}
```

The `reason` field is optional but recommended for audit purposes.

**Response** (200 OK):
```json
{
  "status": "rejected",
  "id": "abc-123"
}
```

After rejection, the agent receives a completion message indicating the action was rejected and adapts accordingly.

## WebSocket Integration

The mobile client can receive real-time approval notifications via WebSocket.

### Connection

Connect to the WebSocket endpoint:
```
ws://localhost:18789/ws?session_id=<session_id>&user_id=<user_id>
```

Or with TLS:
```
wss://kaggen.example.com/ws?session_id=<session_id>&user_id=<user_id>
```

### Approval Required Event

When a guarded tool is invoked, the server sends:

```json
{
  "type": "approval_required",
  "id": "abc-123",
  "session_id": "sess-456",
  "metadata": {
    "approval_id": "abc-123",
    "tool_name": "Bash",
    "skill_name": "deploy",
    "description": "Run command: kubectl apply -f deployment.yaml",
    "arguments": "{\"command\": \"kubectl apply -f deployment.yaml\"}"
  }
}
```

| Field | Description |
|-------|-------------|
| `type` | Always `"approval_required"` for approval notifications. |
| `id` | Same as `metadata.approval_id`. |
| `session_id` | The session that triggered this approval. |
| `metadata.approval_id` | Use this ID when calling approve/reject endpoints. |
| `metadata.tool_name` | The tool requiring approval. |
| `metadata.skill_name` | The skill containing this tool. |
| `metadata.description` | Human-readable action description. |
| `metadata.arguments` | Raw tool arguments (JSON string). |

### Tool Notification Event

For `notify_tools` (lower-risk tier that auto-executes), the server sends:

```json
{
  "type": "tool_notification",
  "id": "notify-456",
  "session_id": "sess-456",
  "metadata": {
    "tool_name": "Write",
    "skill_name": "deploy",
    "description": "Write file: /app/config.json"
  }
}
```

These are informational only — no user action required. Display as a notification or activity log entry.

### Filtering by Session

WebSocket events include `session_id`. If your app supports multiple sessions, filter events to show only approvals relevant to the current session, or aggregate all pending approvals in a dedicated queue view.

## Suggested Integration Patterns

### Pattern 1: Dedicated Approval Queue

1. On app launch, call `GET /api/approvals` to fetch all pending approvals
2. Display as a list/queue (badge count on icon if approvals exist)
3. Subscribe to WebSocket for real-time updates
4. When `approval_required` arrives, add to the queue and show a notification
5. User taps an approval → show details → approve/reject buttons
6. After approve/reject, remove from queue and refresh

### Pattern 2: In-Chat Approval

1. Subscribe to WebSocket for the active chat session
2. When `approval_required` arrives for that session, insert an approval card into the chat
3. User interacts with the card inline (approve/reject buttons)
4. On action, call the REST API and update the card to show the outcome

### Pattern 3: Push Notification + Deep Link

1. Backend sends push notification when approval is required (requires additional push infrastructure)
2. Notification deep-links to the approval detail screen
3. User approves/rejects directly from the detail screen

## Timeout Handling

Approvals expire after 30 minutes (`timeout_at` field). Consider:

- Display remaining time (countdown or relative time like "expires in 25 min")
- Disable approve/reject buttons after expiry
- Remove expired approvals from the queue on next refresh
- The backend auto-fails expired approvals; the agent receives a timeout notification

## Error Handling

| Scenario | Handling |
|----------|----------|
| Approval not found (404) | Approval was already resolved or expired. Remove from local queue, refresh list. |
| Network error | Retry with exponential backoff. Show offline indicator. |
| Approval already resolved | Backend returns error. Refresh the approval list to sync state. |
| WebSocket disconnected | Reconnect with backoff. On reconnect, call `GET /api/approvals` to sync. |

## Security Considerations

- **Authentication**: Ensure all REST and WebSocket requests include proper authentication tokens.
- **Authorization**: The backend does not currently enforce per-user approval permissions. All authenticated users can approve/reject any pending approval. Implement app-level restrictions if needed.
- **Sensitive Data**: The `arguments` field may contain sensitive data (file paths, commands, API keys). Consider whether to display raw arguments or only the sanitized `description`.

## Testing

### Manual Testing

1. Create a skill with `guarded_tools: [Bash]`
2. Send a message that triggers a Bash command
3. Verify `GET /api/approvals` returns the pending approval
4. Verify WebSocket receives `approval_required` event
5. Call `POST /api/approvals/approve` with the approval ID
6. Verify the agent resumes and completes the action
7. Repeat with reject and verify the agent adapts

### Test Approval Object

For UI development without a live backend, use this mock object:

```json
{
  "id": "test-approval-001",
  "status": "pending_approval",
  "session_id": "test-session",
  "user_id": "test-user",
  "agent_name": "deploy",
  "task": "Run command: kubectl apply -f deployment.yaml",
  "started_at": "2025-01-15T10:30:00Z",
  "timeout_at": "2025-01-15T11:00:00Z",
  "approval_request": {
    "tool_name": "Bash",
    "skill_name": "deploy",
    "description": "Run command: kubectl apply -f deployment.yaml",
    "arguments": "{\"command\": \"kubectl apply -f deployment.yaml\"}",
    "requested_at": "2025-01-15T10:30:00Z"
  }
}
```

## Audit History (Optional)

The backend persists all approval events in `~/.kaggen/audit.db`. If you want to display approval history in the mobile app, a REST endpoint can be added to query the audit table. Contact the backend team if this feature is needed.

Audit records include:
- Approval ID, tool name, skill name, arguments, description
- Session ID, user ID
- Requested timestamp, resolved timestamp
- Resolution: `approved`, `rejected`, `timed_out`, `auto_approved`, `notified`
- Who resolved it (user ID or `"system"` for timeouts/auto-approvals)
