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
- [ ] Tested manually: dispatch a task and verify the agent behaves correctly
