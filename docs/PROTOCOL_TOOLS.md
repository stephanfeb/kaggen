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

## Priority 3: WebSocket (Implemented)

**Status:** Implemented in `internal/agent/websocket_tool.go` and `websocket_manager.go`

**Use Cases:**
- Real-time chat integrations (Slack, Discord bots)
- Live data feeds (stocks, crypto, sensors)
- Bidirectional communication with services

**Actions:**
- `connect` - Establish WSS/WS connection, return connection_id
- `send` - Send text/JSON/binary message
- `receive` - Wait for messages (with timeout, count, or drain buffer)
- `close` - Gracefully close connection
- `list_connections` - Show all active connections

**Operations:**
```yaml
tool: websocket
action: connect
params:
  url: "wss://api.example.com/stream"
  oauth_provider: "slack"  # or auth_secret: "api-token"

tool: websocket
action: send
params:
  connection_id: "abc123"
  message_json: {"type": "subscribe", "channel": "updates"}

tool: websocket
action: receive
params:
  connection_id: "abc123"
  timeout_seconds: 30
  wait_count: 5
```

**Skill Declaration:**
```yaml
---
tools: [websocket, read]
oauth_providers: [slack]
secrets: [api-token]
---
```

**Implementation Details:**
- Stateful connection manager with ID-based tracking
- Per-connection goroutine for read pump with message buffering (100 msgs max)
- Ping/pong keepalive (30s interval)
- Auto-cleanup of idle connections (10 min)
- Max 10 concurrent connections per skill
- WSS/TLS support (insecure only for localhost)
- OAuth and secret-based auth injection

---

## Priority 4: GraphQL (Implemented)

**Status:** Implemented in `internal/agent/graphql_tool.go`

**Use Cases:**
- GitHub API operations
- Shopify, Contentful, Hasura integrations
- Any service exposing GraphQL endpoint

**Actions:**
- `query` - Execute GraphQL queries
- `mutation` - Execute GraphQL mutations
- `introspect` - Get schema information

**Operations:**
```yaml
tool: graphql
action: query
params:
  endpoint: "https://api.github.com/graphql"
  oauth_provider: "github"
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

**Skill Declaration:**
```yaml
---
tools: [graphql, read]
oauth_providers: [github]
secrets: [api-token]
---
```

**Implementation Details:**
- OAuth and secret-based auth injection (same as http_request)
- Variables and operation name support
- Schema introspection with built-in type filtering
- GraphQL error parsing with locations and paths
- Response size limit (500KB)
- Timeout handling (default 30s, max 5m)

---

## Priority 5: SQL (Implemented)

**Status:** Implemented in `internal/agent/sql_tool.go`

**Use Cases:**
- Query personal databases (expense trackers, inventory)
- Analytics and reporting
- Data migrations

**Actions:**
- `query` - Execute SELECT queries, return rows
- `execute` - Execute INSERT/UPDATE/DELETE, return affected count
- `tables` - List all tables in database
- `describe` - Get table schema (columns, types, keys)

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

**Skill Declaration:**
```yaml
---
tools: [sql, read]
databases: [personal-postgres, analytics]
---
```

**Config (in ~/.kaggen/config.json):**
```json
{
  "databases": {
    "connections": {
      "personal-postgres": {
        "driver": "postgres",
        "host": "localhost",
        "port": 5432,
        "user": "myuser",
        "password": "secret:postgres-password",
        "database": "mydb",
        "ssl_mode": "disable",
        "read_only": false
      },
      "analytics": {
        "driver": "sqlite",
        "database": "~/.kaggen/analytics.db",
        "read_only": true
      }
    }
  }
}
```

**Supported Databases:**
- PostgreSQL (`driver: "postgres"`)
- MySQL (`driver: "mysql"`)
- SQLite (`driver: "sqlite"`)

**Implementation Details:**
- Query parameterization enforced (prevents SQL injection)
- Connection pooling (default 5 connections)
- Read-only mode enforcement
- Query timeouts (default 30s, max 5m)
- Row limit (max 1000 rows per query)
- Password can use `secret:` prefix for secure storage

---

## Priority 6: MQTT (Implemented)

**Status:** Implemented in `internal/agent/mqtt_tool.go`

**Use Cases:**
- Home automation (Home Assistant, smart devices)
- IoT sensor data
- Pub/sub messaging patterns

**Actions:**
- `connect` - Connect to an MQTT broker, return connection_id
- `publish` - Publish message to a topic
- `subscribe` - Subscribe to topics (supports wildcards +/#)
- `receive` - Wait for messages (with timeout or count)
- `unsubscribe` - Unsubscribe from topics
- `disconnect` - Disconnect from broker
- `list_connections` - Show all active connections

**Operations:**
```yaml
tool: mqtt
action: connect
params:
  broker: "home-assistant"

tool: mqtt
action: publish
params:
  connection_id: "abc123"
  topic: "home/living-room/lights/set"
  payload: '{"state": "off"}'
  qos: 1
  retain: false

tool: mqtt
action: subscribe
params:
  connection_id: "abc123"
  topics: ["home/+/temperature", "home/+/humidity"]
  qos: 1

tool: mqtt
action: receive
params:
  connection_id: "abc123"
  timeout_seconds: 30
  wait_count: 5
```

**Skill Declaration:**
```yaml
---
tools: [mqtt, read]
brokers: [home-assistant, sensors]
---
```

**Config (in ~/.kaggen/config.json):**
```json
{
  "mqtt": {
    "brokers": {
      "home-assistant": {
        "host": "homeassistant.local",
        "port": 1883,
        "username": "mqtt_user",
        "password": "secret:mqtt-password",
        "client_id": "kaggen-agent"
      },
      "sensors": {
        "host": "mqtt.example.com",
        "port": 8883,
        "tls": true,
        "username": "sensor_reader",
        "password": "secret:sensor-password"
      }
    }
  }
}
```

**Implementation Details:**
- Stateful connection manager with ID-based tracking
- QoS levels 0, 1, 2 supported
- Topic wildcards: `+` (single level), `#` (multi-level)
- Retained message support
- Per-connection message buffering (100 messages max)
- TLS support with optional client certificates
- Auto-cleanup of idle connections (10 minutes)
- Max 10 concurrent connections per skill
- Password can use `secret:` prefix for secure storage

---

## Priority 7: SSH/SFTP (Implemented)

**Status:** Implemented in `internal/agent/ssh_manager.go`, `ssh_tool.go`, `sftp_tool.go`

**Use Cases:**
- Remote server management
- Log retrieval
- File transfers
- Deployment automation

**SSH Actions:**
- `connect` - Establish SSH connection, return connection_id
- `exec` - Execute command on established connection
- `disconnect` - Close SSH connection
- `list_connections` - Show all active connections

**SFTP Actions:**
- `upload` - Upload file (from local path or content string)
- `download` - Download file (to local path or return content)
- `list` - List directory contents (supports recursive)
- `mkdir` - Create directory
- `rm` - Remove file or directory (supports recursive)
- `stat` - Get file/directory information

**Operations:**
```yaml
tool: ssh
action: connect
params:
  host: "production"

tool: ssh
action: exec
params:
  connection_id: "abc123"
  command: "docker ps"
  timeout_seconds: 60

tool: sftp
action: download
params:
  connection_id: "abc123"
  remote_path: "/var/log/app.log"
  local_path: "/tmp/app.log"
```

**Skill Declaration:**
```yaml
---
tools: [ssh, sftp, read]
ssh_hosts: [production, staging]
guarded_tools: [ssh]
---
```

**Config (in ~/.kaggen/config.json):**
```json
{
  "ssh": {
    "hosts": {
      "production": {
        "host": "prod.example.com",
        "user": "deploy",
        "use_agent": true,
        "host_key_check": "strict"
      },
      "bastion": {
        "host": "bastion.example.com",
        "user": "jump-user",
        "private_key": "~/.ssh/bastion_key",
        "passphrase": "secret:bastion-passphrase"
      },
      "internal": {
        "host": "10.0.1.50",
        "user": "admin",
        "private_key": "secret:internal-key",
        "proxy_jump": "bastion"
      }
    }
  }
}
```

**Key Management (Priority Order):**
1. **ssh-agent** (`use_agent: true`) - Most secure, no secrets in config
2. **Key file + passphrase** - Key on disk, passphrase from secrets store
3. **Embedded key** (`private_key: "secret:key-name"`) - Full key in secrets store
4. **Password** (`password: "secret:pass"`) - Fallback only

**Host Key Verification:**
- `strict` (default) - Reject unknown/changed keys (production)
- `accept-new` - Accept unknown, reject changed (first-time setup)
- `none` - Skip verification (localhost only, insecure)

**Implementation Details:**
- Stateful connection manager with ID-based tracking
- ssh-agent integration for key management
- ProxyJump support for bastion/jump hosts
- Keepalive loop (30s interval)
- Auto-cleanup of idle connections (10 min)
- Max 10 connections per skill
- Dangerous command detection (rm -rf /, shutdown, etc.)
- Output sanitization (redacts passwords/tokens)

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
