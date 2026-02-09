# Skills Writing Guide

Skills are modular capability packages that extend Kaggen's sub-agents. Each skill defines a specialist agent with its own instructions, tools, and optionally helper scripts.

## Directory Structure

```
~/.kaggen/skills/<skill-name>/
├── SKILL.md           # Required: frontmatter + instructions
└── scripts/           # Optional: bash helper scripts
    ├── do_thing.sh
    └── ...
```

Skills are loaded from two directories (merged at startup):
- `<workspace>/skills/` — project-specific skills
- `~/.kaggen/skills/` — user-installed skills

Use `skill-builder` to scaffold new skills:
```bash
bash ~/.kaggen/skills/skill-builder/scripts/scaffold.sh my-skill
```

## SKILL.md Format

Every skill needs a `SKILL.md` with YAML frontmatter and a markdown body.

### Frontmatter Fields

```yaml
---
name: my-skill                    # Required. Lowercase, hyphens allowed.
description: One-line summary     # Required. Shown to coordinator for routing.
delegate: claude                  # Optional. "claude" = subprocess, omit = LLM agent.
claude_model: sonnet              # Optional. CLI alias: opus, sonnet, haiku.
claude_tools: Bash,Read,Edit,Write,Glob,Grep  # Optional. Comma-separated.
tools: [browser, memory_search]   # Optional. Tool filter for LLM agent mode.
guarded_tools: [Bash]             # Optional. Tools requiring human approval.
notify_tools: [Write]             # Optional. Tools that auto-execute with notification.
secrets: [api-key-name]           # Optional. Secrets for http_request tool. See "API Integration".
work_dir: ~/projects/foo          # Optional. --add-dir for claude subprocess.
---
```

| Field | Used By | Description |
|-------|---------|-------------|
| `name` | Both | Skill identifier. Must match directory name. |
| `description` | Both | Coordinator reads this to decide which agent to dispatch. Write it clearly. |
| `delegate` | Claude agent | Set to `"claude"` to run as a `claude -p` subprocess. |
| `claude_model` | Claude agent | Model for the subprocess. Defaults to config `claude_model` or `"sonnet"`. |
| `claude_tools` | Claude agent | Tools available to the subprocess. Defaults to config `claude_tools`. |
| `tools` | LLM agent | Restricts which coordinator tools the agent can use. Omit for all tools. |
| `guarded_tools` | Both | Tools that require human approval before execution. See [Guarded Tools](#guarded-tools). |
| `notify_tools` | Both | Tools that auto-execute but send a notification. See [Guarded Tools](#guarded-tools). |
| `secrets` | LLM agent | Secret names for API authentication. See [API Integration](#api-integration-with-secrets). |
| `work_dir` | Claude agent | Working directory added via `--add-dir`. Supports `~` expansion. |

### Body (Instructions)

Everything after the closing `---` is the skill instruction. How it's used depends on the agent type.

## Two Agent Types

### 1. LLM Agent (default)

When `delegate` is omitted, the skill runs as an in-process LLM agent within the trpc-agent-go framework. The body becomes the agent's system prompt.

**Best for:** Wrapping CLI tools, simple automation, tasks that need specific tool access (e.g., browser control).

**How tools work:** The agent calls tools directly through the framework. Use the `tools` frontmatter field to restrict which tools are available.

**Example — CLI wrapper skill:**

```yaml
---
name: pandoc
description: Convert documents between formats using pandoc
tools: [exec]
---
```

```markdown
# Pandoc — Document Conversion

Use this skill when the user asks to convert documents between formats.

## Available Commands

### convert.sh — General conversion

​```bash
bash scripts/convert.sh <input> <output> [extra pandoc flags...]
​```

Auto-detects formats from file extensions.

**Examples:**
​```bash
bash scripts/convert.sh report.md report.pdf
bash scripts/convert.sh data.csv table.html
​```
```

### 2. Claude Agent (`delegate: claude`)

When `delegate: claude` is set, the skill runs as a `claude -p` subprocess. The body is prepended to the task text and passed as the `-p` argument. The subprocess runs independently with its own model, tools, and session.

**Best for:** Complex multi-step workflows, code generation, architecture, QA — tasks requiring deep reasoning and autonomous tool use.

**How it works:**
1. Coordinator dispatches a task to this agent
2. Kaggen spawns: `claude -p "<instruction>\n\n---\n\n<task>" --model <model> --allowed-tools <tools> --output-format stream-json --verbose`
3. Events stream back in real-time for monitoring
4. Supervisor can intervene (kill + resume) if the agent goes off-track

**The instruction body should be written as a direct prompt for Claude Code.** Do NOT write meta-instructions about calling `exec` or `claude -p` — the subprocess dispatch handles that.

**Example — Claude delegate skill:**

```yaml
---
name: coder
description: Implements code according to backlog and technical specs
delegate: claude
claude_model: opus
claude_tools: Bash,Read,Edit,Write,Glob,Grep
---
```

```markdown
You are a Software Engineer. Your job is to implement code according to the backlog and technical specs.

## Workflow

1. Read BACKLOG.md, SPEC.md, and existing source code.

2. List open issues:
   ​```bash
   bash /path/to/beads/scripts/list.sh -s open --pretty
   ​```

3. Claim each issue before starting:
   ​```bash
   bash /path/to/beads/scripts/update.sh <id> --claim
   ​```

4. Implement the code.

5. Add completion comments:
   ​```bash
   bash /path/to/beads/scripts/comments.sh add <id> "Implemented: <summary>"
   ​```

## Rules

- Work from beads issues, not ad-hoc tasks
- Read existing code before implementing
- Do NOT close issues — leave for QA
```

## Guarded Tools

Some skills perform side-effects that should require explicit human approval before execution (e.g., deploying to production, deleting resources, running destructive commands). Use `guarded_tools` to declare which tools need approval.

```yaml
---
name: deploy
description: Deploy services to production
tools: [Bash, Read]
guarded_tools: [Bash]
---
```

When the agent invokes a guarded tool, execution for that task is **suspended** — not the entire agent. The agent continues working on other tasks while the approval is pending.

### How It Works

1. Agent calls a guarded tool (e.g., `Bash`)
2. The `BeforeTool` callback intercepts the call and registers an approval request
3. The agent receives an "approval required" message and moves on to other work
4. The user is notified:
   - **Mobile app**: Approval appears in the pending approvals queue (`GET /api/approvals`)
   - **Telegram**: Inline keyboard with Approve / Reject buttons
   - **WebSocket**: `approval_required` event for custom UIs
5. User approves or rejects
6. The agent receives the decision and retries the tool (if approved) or adapts (if rejected)

Approvals time out after 30 minutes if no action is taken.

### When to Use Guarded Tools

- Production deployments
- Destructive operations (delete, drop, truncate)
- Financial transactions or external API calls with real consequences
- Any action where a mistake is costly to reverse

### Notify Tools (Lower-Risk Tier)

For tools where visibility is sufficient but blocking is unnecessary, use `notify_tools`. These auto-execute but send a notification to the user:

```yaml
---
name: deploy
description: Deploy services to production
tools: [Bash, Read, Write]
guarded_tools: [Bash]
notify_tools: [Write]
---
```

Notify-tier tool calls are logged to the audit trail with resolution `"notified"`.

### Auto-Approval Rules

To reduce approval fatigue for safe patterns, configure auto-approval rules in `~/.kaggen/config.json`:

```json
{
  "approval": {
    "auto_approve": [
      {"tool": "Bash", "pattern": "^Run command: git (status|log|diff)"},
      {"tool": "Read"}
    ]
  }
}
```

- `tool` — the tool name to match
- `pattern` — regex matched against the human-readable description (e.g., `Run command: git status`). Omit for match-all.

Auto-approved calls are logged to the audit trail with resolution `"auto_approved"`.

### Audit Trail

All approval events (requested, approved, rejected, timed out, auto-approved, notified) are persisted in `~/.kaggen/audit.db`. Configure the path:

```json
{
  "approval": {
    "audit_db_path": "~/.kaggen/audit.db"
  }
}
```

### REST API

```
GET  /api/approvals                  — list pending approvals
POST /api/approvals/approve          — approve (body: {"id": "<approval_id>"})
POST /api/approvals/reject           — reject  (body: {"id": "<approval_id>", "reason": "..."})
```

## API Integration with Secrets

Skills can call external APIs with authentication using the `secrets` frontmatter field and the `http_request` tool. Secrets are injected securely — the LLM never sees the actual secret values.

### How It Works

1. Store your API key in the secrets store:
   ```bash
   kaggen secrets set plane-api-key
   # Enter your API key when prompted
   ```

2. Declare the secret in your skill's frontmatter:
   ```yaml
   ---
   name: plane
   description: Manage issues in Plane
   secrets: [plane-api-key]
   ---
   ```

3. At load time, Kaggen fetches the secrets and injects them into the `http_request` tool's closure

4. In your skill instructions, document how to use `http_request` with the secret reference:
   ```markdown
   Use http_request to call the Plane API:
   - url: https://api.plane.so/api/v1/workspaces/{workspace}/issues/
   - method: POST
   - auth_secret: plane-api-key
   - body: {"name": "...", "description": "..."}
   ```

5. The LLM calls `http_request` with `auth_secret: "plane-api-key"` (the name, not the value)

6. The tool resolves the secret internally and adds the `Authorization: Bearer <secret>` header

### The `http_request` Tool

When a skill declares `secrets`, the `http_request` tool is automatically added to its available tools.

**Arguments:**

| Field | Required | Description |
|-------|----------|-------------|
| `url` | Yes | The URL to request |
| `method` | Yes | HTTP method: GET, POST, PUT, PATCH, DELETE |
| `headers` | No | Additional HTTP headers (map) |
| `body` | No | Request body (typically JSON) |
| `auth_secret` | No | Secret name for authentication |
| `auth_header` | No | Custom header name (default: `Authorization`) |
| `auth_scheme` | No | Auth scheme: `bearer` (default), `api-key`, `basic` |
| `timeout_seconds` | No | Request timeout (default: 30, max: 300) |
| `content_type` | No | Content-Type header (default: `application/json` for requests with body) |

**Returns:**

```json
{
  "status_code": 200,
  "status": "200 OK",
  "headers": {"Content-Type": "application/json", ...},
  "body": "{...}",
  "message": "HTTP POST https://api.example.com/... -> 200 OK"
}
```

### Example: Plane Integration Skill

```yaml
---
name: plane
description: Manage issues and projects in Plane
secrets: [plane-api-key]
---
```

```markdown
# Plane Skill

You manage issues in Plane via the API.

## Configuration

- Base URL: https://api.plane.so/api/v1
- Workspace slug: `my-workspace` (adjust as needed)
- Project ID: Ask the user or list projects first

## Create Issue

Use http_request:
- url: https://api.plane.so/api/v1/workspaces/{workspace}/projects/{project}/issues/
- method: POST
- auth_secret: plane-api-key
- body: {"name": "Issue title", "description": "Details...", "priority": "medium"}

## List Issues

- url: https://api.plane.so/api/v1/workspaces/{workspace}/projects/{project}/issues/
- method: GET
- auth_secret: plane-api-key

## Update Issue

- url: https://api.plane.so/api/v1/workspaces/{workspace}/projects/{project}/issues/{issue_id}/
- method: PATCH
- auth_secret: plane-api-key
- body: {"state": "done"}
```

### Security Model

- **Scoped access:** Each skill can only access secrets it declares in frontmatter
- **No context exposure:** Secret values never appear in the LLM context, tool arguments, or responses
- **Reference-based:** The LLM uses secret names (`auth_secret: "plane-api-key"`), not values
- **Logged safely:** HTTP requests are logged without exposing auth headers

### Different Auth Schemes

**Bearer token (default):**
```
auth_secret: my-api-key
# Adds: Authorization: Bearer <secret>
```

**API key in custom header:**
```
auth_secret: my-api-key
auth_header: X-API-Key
auth_scheme: api-key
# Adds: X-API-Key: <secret>
```

**Basic auth:**
```
auth_secret: my-basic-creds  # Store as base64-encoded "user:pass"
auth_scheme: basic
# Adds: Authorization: Basic <secret>
```

## Writing Good Instructions

### For LLM Agent Skills

- **Document available commands** with usage syntax, options, and examples
- **Use "Available Commands" sections** — one per script
- **Include practical examples** showing common use cases
- **Add a Tips section** for gotchas and edge cases
- **Reference scripts as** `bash scripts/<name>.sh` (relative to skill directory)

### For Claude Delegate Skills

- **Start with a role statement:** "You are a [Role]. Your job is to..."
- **Number the workflow steps** — Claude follows ordered lists well
- **Include exact bash commands** — Claude will run them as-is
- **State constraints clearly** in a Rules section at the end
- **Keep it focused** — the task text from the coordinator provides specifics; the instruction provides the framework
- **Do NOT include:**
  - `exec:` prefixes (no intermediate delegation)
  - `claude -p` invocations (you're already inside one)
  - `--output-format`, `--timeout-seconds`, or other CLI flags
  - State management (`bd agent state`) — the dispatcher handles this
  - "Parse the JSON output" — there's no JSON to parse

### Model Selection Guidelines

| Model | Cost | Use When |
|-------|------|----------|
| `opus` | High | Complex reasoning: architecture, code generation, product analysis |
| `sonnet` | Medium | Balanced tasks, general-purpose (default) |
| `haiku` | Low | Validation, linting, simple checks, high-volume tasks |

## Writing Helper Scripts

Scripts live in `scripts/` within the skill directory. Every script must:

1. Start with `#!/usr/bin/env bash` and `set -euo pipefail`
2. Support `--help` (print usage, exit 0)
3. Validate inputs and print errors to stderr
4. Report results to stdout
5. Exit 0 on success, 1 on failure

**Template:**

```bash
#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]] || [[ "$1" == "--help" ]]; then
    echo "Usage: $(basename "$0") <required_arg> [options...]"
    echo ""
    echo "Description of what this script does."
    exit 0
fi

INPUT="$1"
if [[ ! -f "$INPUT" ]]; then
    echo "Error: file not found: $INPUT" >&2
    exit 1
fi

# Do work...
echo "Done."
```

**Naming:** lowercase with underscores (e.g., `convert.sh`, `batch_process.sh`). Keep to 3–6 scripts per skill.

## Installing Skills

Copy to the runtime directory:

```bash
cp -r my-skill ~/.kaggen/skills/my-skill
```

Or use the skill-builder:

```bash
bash ~/.kaggen/skills/skill-builder/scripts/install.sh ./my-skill
```

Validate before installing:

```bash
bash ~/.kaggen/skills/skill-builder/scripts/validate.sh ./my-skill
```

Skills are loaded at startup and on SIGHUP (hot-reload without restart).

## Checklist

- [ ] `SKILL.md` has `name` and `description` in frontmatter
- [ ] `name` matches directory name
- [ ] `description` is clear enough for the coordinator to route tasks correctly
- [ ] Body instructions match the agent type (direct prompt for claude, tool docs for LLM)
- [ ] Scripts have shebang, `set -euo pipefail`, and `--help` support
- [ ] `guarded_tools` (if used) lists only tools declared in `tools` or available by default
- [ ] `secrets` (if used) lists secrets that exist in the store (`kaggen secrets list`)
- [ ] API instructions document `http_request` usage with `auth_secret` references
- [ ] Tested manually: dispatch a task and verify the agent behaves correctly
