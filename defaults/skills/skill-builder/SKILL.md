---
name: skill-builder
description: Create, validate, and install new Kaggen skills by writing SKILL.md files to the workspace VFS
tools: [read, write, reload_skills]
---

# Skill Builder

Use this skill when the coordinator asks you to create a new skill for Kaggen. You will scaffold a complete skill directory on the VFS, write a valid SKILL.md, and reload the skill registry.

## How Skills Work

A skill is a directory under `skills/` containing a `SKILL.md` file. The SKILL.md has two parts:

1. **YAML frontmatter** — declares metadata and tool/resource access
2. **Markdown body** — LLM instructions that become the sub-agent's system prompt

When loaded, each skill becomes a specialist sub-agent on the coordinator's team. The coordinator routes tasks to skills based on the `description` field.

## Workflow

1. **Understand** — Read the specification from the coordinator. Clarify what the skill should do, what external systems it needs, and what tools it requires.
2. **Check for conflicts** — Use `read` on `skills/` to see existing skills. Avoid name collisions.
3. **Write SKILL.md** — Use `write` to create `skills/<name>/SKILL.md` with valid frontmatter and a clear instruction body.
4. **Verify** — Use `read` to confirm the file was written correctly.
5. **Reload** — Call `reload_skills` to hot-reload the skill registry. The new skill becomes available immediately.

## SKILL.md Format

```markdown
---
name: <lowercase-hyphenated-name>
description: <one-line summary for the coordinator to route tasks>
tools: [<tool-list>]
# Optional fields below — only include what the skill needs:
guarded_tools: [<tools-requiring-human-approval>]
notify_tools: [<tools-that-notify-on-execution>]
secrets: [<secret-names>]
oauth_providers: [<provider-names>]
databases: [<database-connection-names>]
brokers: [<mqtt-broker-names>]
ssh_hosts: [<ssh-host-names>]
---

# Instruction body here (markdown)

Describe the agent's role, capabilities, and workflow.
```

## Frontmatter Reference

### Required Fields

| Field | Description |
|-------|-------------|
| `name` | Lowercase, hyphen-separated. Must match the directory name. |
| `description` | One-line summary. The coordinator reads this to decide when to dispatch to this skill. Make it specific and actionable. |

### Tool Access

| Field | Description |
|-------|-------------|
| `tools` | List of tools the skill can use. If omitted, the skill gets all default tools (read, write). Protocol tools must be explicitly listed to be injected. |
| `guarded_tools` | Subset of `tools` that require human approval before execution. Execution pauses until approved. Use for destructive or costly operations. |
| `notify_tools` | Subset of `tools` that auto-execute but send a notification to the user. Use for mutations where visibility is enough. |

### Available Tools

**Default tools** (always available unless filtered):
- `read` — Read files and list directories on the VFS
- `write` — Write/append files on the VFS

**Protocol tools** (must be listed in `tools` and have matching resource config):
- `http_request` — HTTP calls (REST, webhooks). Requires `secrets` or `oauth_providers`.
- `email` — Send/read email via IMAP/SMTP. Requires `oauth_providers`.
- `caldav` — Calendar operations. Requires `oauth_providers` or `secrets`.
- `carddav` — Contact operations. Requires `oauth_providers` or `secrets`.
- `websocket` — WebSocket connections for real-time data.
- `graphql` — GraphQL queries/mutations. Requires `oauth_providers` or `secrets`.
- `sql` — Database queries. Requires `databases`.
- `mqtt` — Publish/subscribe to MQTT topics. Requires `brokers`.
- `ssh` — Remote command execution. Requires `ssh_hosts`.
- `sftp` — Remote file transfer. Requires `ssh_hosts`.

**System tools** (available in gateway mode):
- `reload_skills` — Trigger hot-reload of the skill registry.

### Resource Declarations

These fields connect the skill to operator-configured external systems in `kaggen.yaml`:

| Field | Description | Example |
|-------|-------------|---------|
| `secrets` | Named secrets from the secret store. Injected into HTTP/CalDAV/CardDAV tools as auth headers. | `[github-token, stripe-key]` |
| `oauth_providers` | OAuth provider names. Enables token-based auth for HTTP, email, CalDAV, CardDAV. | `[google, github]` |
| `databases` | Database connection names. Enables the `sql` tool with scoped access. | `[analytics-postgres, app-mysql]` |
| `brokers` | MQTT broker names. Enables the `mqtt` tool with scoped access. | `[home-assistant, sensors]` |
| `ssh_hosts` | SSH host names. Enables `ssh` and `sftp` tools with scoped access. | `[production, staging]` |

## Writing Good Instructions

The markdown body becomes the sub-agent's system prompt. Write it as direct instructions to an LLM:

1. **Open with role and purpose** — "You manage calendar events using CalDAV. Use the caldav tool to..."
2. **Document the workflow** — Step-by-step what the agent should do for typical requests.
3. **Explain tool usage** — Which tool to use for what, expected inputs/outputs, error handling.
4. **Add constraints** — What the agent should NOT do, edge cases to watch for.
5. **Keep it focused** — One skill, one domain. If a skill tries to do too much, split it.

### Good Example

```markdown
You manage the user's Google Calendar. When asked about schedule, events, or availability:

1. Use `caldav` tool with action "list" to fetch events for the relevant date range
2. Summarize results clearly with times, titles, and locations
3. For creating events, confirm details with the user before calling "create"

Always use ISO 8601 dates. Default to the user's local timezone.
Do not delete events without explicit confirmation.
```

### Bad Example

```markdown
You are a helpful assistant that can do many things including calendar management,
email, and general tasks. Try your best to help the user.
```

(Too vague. The coordinator won't know when to route to this skill, and the agent won't know what tools to use.)

## Guarded vs Notify Tools

**Guarded tools** pause execution and wait for human approval:
- Use for: deployments, financial transactions, destructive operations, sending external messages
- The task suspends until the user approves or rejects via the dashboard or Telegram

**Notify tools** execute immediately but send a notification:
- Use for: file writes, non-destructive API calls, data fetches where visibility matters

```yaml
# Example: a deploy skill with approval gates
guarded_tools: [ssh]
notify_tools: [http_request]
```

## Naming Conventions

- **Skill name**: lowercase, hyphens for words (e.g. `github-issues`, `home-automation`)
- **Match directory**: `skills/github-issues/SKILL.md` must have `name: github-issues`
- **Be specific**: `calendar-manager` not `google-stuff`; `postgres-analytics` not `database`

## Validation Checklist

Before calling `reload_skills`, verify:

- [ ] `name` is lowercase-hyphenated and matches the directory name
- [ ] `description` is a single clear sentence
- [ ] `tools` only lists tools that exist (see Available Tools above)
- [ ] `guarded_tools` and `notify_tools` are subsets of `tools`
- [ ] Resource fields (`secrets`, `databases`, etc.) reference names that the operator has configured
- [ ] The instruction body is clear, specific, and actionable
- [ ] No references to bash, shell commands, exec, or subprocess delegation
