# Protocol Tools Roadmap

This document outlines the strategy for adding protocol-level tooling to kaggen, enabling skills to interact with external services securely and declaratively.

## Philosophy

Skills should be **declarative**, focusing on *what* they want to accomplish rather than *how* to establish connections or manage credentials. The bot provides protocol tools that:

1. **Securely inject credentials** - Skills never handle raw secrets
2. **Manage connection lifecycle** - Connection pooling, reconnection, cleanup
3. **Provide consistent error handling** - Unified error patterns across protocols
4. **Abstract protocol complexity** - Skills work with high-level operations

This pattern was established with `http_tool` and extends to other protocols that skills commonly need.

## Foundation: OAuth Flow Handler

Before implementing protocol-specific tools, we need a robust OAuth flow handler. Many services (Google, Microsoft, etc.) require OAuth 2.0 for API access.

### Capabilities
- Authorization code flow with PKCE
- Token refresh and lifecycle management
- Secure token storage (integration with credential store)
- Multi-tenant support (user-specific tokens)
- Callback handling for authorization redirects

### Why First?
OAuth is a prerequisite for Email (Gmail, Outlook), CalDAV (Google Calendar, iCloud), and many other integrations. Building it first unblocks the highest-priority protocols.

---

## Priority 1: Email (SMTP/IMAP)

**Use Cases:**
- Send emails on behalf of user
- Read and summarize inbox
- Search for specific messages
- Manage folders and labels

**Operations:**
```yaml
# Example skill declaration
tool: email
action: send
params:
  to: ["recipient@example.com"]
  subject: "Meeting Follow-up"
  body: "Thanks for the discussion..."

tool: email
action: search
params:
  folder: INBOX
  query: "from:boss@company.com is:unread"
  limit: 10
```

**Credential Requirements:**
- OAuth tokens (Gmail, Outlook)
- App passwords (legacy providers)
- SMTP/IMAP server configuration

---

## Priority 2: CalDAV/CardDAV

**Use Cases:**
- Create, update, delete calendar events
- Query availability / free-busy
- Manage contacts
- Sync across providers (Google, iCloud, Fastmail)

**Operations:**
```yaml
tool: caldav
action: create_event
params:
  calendar: "Work"
  title: "Project Review"
  start: "2024-01-15T14:00:00Z"
  end: "2024-01-15T15:00:00Z"
  attendees: ["colleague@company.com"]

tool: carddav
action: search_contacts
params:
  query: "John"
  fields: ["name", "email", "phone"]
```

**Credential Requirements:**
- OAuth tokens (Google, iCloud)
- Basic auth (self-hosted, Fastmail)
- Server discovery (well-known URLs)

---

## Priority 3: WebSocket

**Use Cases:**
- Real-time chat integrations (Slack, Discord bots)
- Live data feeds (stocks, crypto, sensors)
- Bidirectional communication with services

**Operations:**
```yaml
tool: websocket
action: connect
params:
  url: "wss://api.example.com/stream"
  headers:
    Authorization: "Bearer ${token}"

tool: websocket
action: send
params:
  connection_id: "ws-123"
  message: {"type": "subscribe", "channel": "updates"}

tool: websocket
action: receive
params:
  connection_id: "ws-123"
  timeout: 30s
```

**Design Considerations:**
- Stateful connections (unlike HTTP)
- Connection lifecycle management
- Message buffering and backpressure
- Reconnection strategies

---

## Priority 4: GraphQL

**Use Cases:**
- GitHub API operations
- Shopify, Contentful, Hasura integrations
- Any service exposing GraphQL endpoint

**Operations:**
```yaml
tool: graphql
action: query
params:
  endpoint: "https://api.github.com/graphql"
  query: |
    query {
      viewer {
        repositories(first: 10) {
          nodes { name, stargazerCount }
        }
      }
    }
  variables: {}
```

**Features:**
- Schema introspection (optional, for validation)
- Automatic pagination helpers
- Mutation support
- Subscription support (via WebSocket)

---

## Priority 5: SQL

**Use Cases:**
- Query personal databases (expense trackers, inventory)
- Analytics and reporting
- Data migrations

**Operations:**
```yaml
tool: sql
action: query
params:
  connection: "personal-postgres"
  query: "SELECT * FROM expenses WHERE date >= $1"
  params: ["2024-01-01"]

tool: sql
action: execute
params:
  connection: "personal-postgres"
  query: "INSERT INTO expenses (amount, category) VALUES ($1, $2)"
  params: [42.50, "groceries"]
```

**Supported Databases:**
- PostgreSQL
- MySQL
- SQLite
- (Others via driver plugins)

**Security:**
- Query parameterization (prevent SQL injection)
- Connection pooling
- Read-only mode option
- Query timeouts

---

## Priority 6: MQTT

**Use Cases:**
- Home automation (Home Assistant, smart devices)
- IoT sensor data
- Pub/sub messaging patterns

**Operations:**
```yaml
tool: mqtt
action: publish
params:
  broker: "home-assistant"
  topic: "home/living-room/lights/set"
  payload: {"state": "off"}

tool: mqtt
action: subscribe
params:
  broker: "home-assistant"
  topic: "home/+/temperature"
  handler: "temperature_monitor"  # Skill callback
```

**Design Considerations:**
- QoS levels (0, 1, 2)
- Retained messages
- Last Will and Testament
- Topic wildcards

---

## Priority 7: SSH/SFTP

**Use Cases:**
- Remote server management
- Log retrieval
- File transfers
- Deployment automation

**Operations:**
```yaml
tool: ssh
action: exec
params:
  host: "server.example.com"
  command: "docker ps"

tool: sftp
action: download
params:
  host: "server.example.com"
  remote_path: "/var/log/app.log"
  local_path: "/tmp/app.log"
```

**Credential Requirements:**
- SSH keys (preferred)
- Password auth (fallback)
- Jump host / bastion support

---

## Implementation Notes

### Credential Store Integration

All protocol tools integrate with the existing credential store:

```go
type CredentialRef struct {
    Store   string `yaml:"store"`   // e.g., "keychain", "vault"
    Key     string `yaml:"key"`     // credential identifier
    Scope   string `yaml:"scope"`   // optional: user-specific scope
}
```

Skills reference credentials by name; the bot resolves and injects them at runtime.

### Error Handling

Consistent error types across all protocol tools:

| Error Type | Description |
|------------|-------------|
| `AuthError` | Credential invalid or expired |
| `ConnectionError` | Cannot reach service |
| `TimeoutError` | Operation timed out |
| `RateLimitError` | Service rate limit hit |
| `ProtocolError` | Protocol-specific error |

### Observability

All protocol tools emit:
- Structured logs with correlation IDs
- Metrics (latency, error rates, connection counts)
- Traces for distributed debugging

---

## Related Documents

- [Skills Guide](./skills-guide.md) - How to write skills
- [Architecture](./ARCHITECTURE.md) - System overview
- [Guarded Skills](./guarded_skills.md) - Security model
