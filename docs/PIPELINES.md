# Pipelines

Pipelines are declarative YAML definitions that tell Kaggen's coordinator how to orchestrate multi-agent workflows. Each pipeline defines a sequence of agent stages that are dispatched in order, with optional failure-retry policies.

## How Pipelines Work

### Loading

At startup, `internal/pipeline/pipeline.go` loads all `.yaml` and `.yml` files from `~/.kaggen/pipelines/`. The `BuildInstruction()` function converts them into coordinator system prompt text, so the coordinator LLM knows which pipeline to activate for a given request.

Pipelines reload on SIGHUP alongside skills — no restart required.

### Execution Flow

```
User Request
     │
     ▼
Coordinator (Haiku) matches request against pipeline triggers
     │
     ▼
Stage 1: dispatch_task(agent=stage_1_agent, async, policy=auto)
     │
     ▼ [Task Completed]
Stage 2: dispatch_task(agent=stage_2_agent, async, policy=auto)
     │
     ▼ [Task Completed]
  ...continues through all stages...
     │
     ▼
Final stage completes → coordinator summarizes result to user
```

Each stage runs as an async dispatch (`dispatch_task`). The coordinator waits for the `[Task Completed]` callback before advancing to the next stage. Pipelines are sequential — stages do not run in parallel.

### Failure Handling

If a stage has `on_fail` set, the coordinator loops back to the named agent on failure, then re-runs the failed stage. This repeats up to `max_retries` times (default 3).

```
coder → qa (FAIL) → coder (with QA feedback) → qa (retry) → ...
```

### Relationship to Skills

Each `agent` name in a pipeline stage must correspond to a skill in `~/.kaggen/skills/<name>/SKILL.md`. The skill defines what the agent actually does. The pipeline defines the order in which agents are called.

Pipeline stages reference skills by name. If a skill doesn't exist for a stage's agent name, the dispatch will fail at runtime.

## Pipeline YAML Schema

```yaml
# Required fields
name: string          # Unique identifier (snake_case)
description: string   # Human-readable summary
trigger: string       # When to activate — the coordinator matches user
                      # requests against this text to decide which pipeline applies

# At least one stage is required
stages:
  - agent: string          # Skill name to dispatch (must exist in ~/.kaggen/skills/)
    description: string    # What this stage does (included in coordinator prompt)
    on_fail: string        # Optional: agent name to loop back to on failure
    max_retries: int       # Optional: max failure loops (default 3 if on_fail is set)
```

## Example: Software Development Pipeline

```yaml
# ~/.kaggen/pipelines/software_dev.yaml
name: software_dev
description: Full software development lifecycle
trigger: "building or modifying software, bug reports, bug fixes, new features, code changes, deployments"
stages:
  - agent: product_owner
    description: Creates beads epic with user stories and acceptance criteria
  - agent: architect
    description: Reviews backlog and produces technical specifications
  - agent: coder
    description: Builds code via Claude Code CLI
  - agent: qa
    description: Validates code against acceptance criteria
    on_fail: coder
    max_retries: 3
```

## Adding a New Pipeline

### Step 1: Create the Skills

Each stage agent needs a corresponding skill. Create a directory under `~/.kaggen/skills/<agent_name>/` with a `SKILL.md` file.

All skills follow the same delegation pattern — they use `exec` to call Claude Code CLI (`claude -p`) which does the actual work. The skill SKILL.md defines:

1. **Frontmatter** — name, description, and `tools: exec` (restricts the agent to only the exec tool)
2. **Delegation prompt** — instructions for how to construct the `claude -p` command
3. **Rules** — constraints on behavior

Template for a new skill:

```markdown
---
name: <agent_name>
description: <what this agent does>
tools: exec
---

You are a <Role> delegation agent. Your ONLY job is to pass <domain> tasks to Claude Code CLI via `exec` and report the results.

**WORKFLOW:**

1. Delegate the ENTIRE task in ONE call:
   ```
   exec (timeout_seconds: 1800): claude -p '<prompt>' --add-dir /Users/stephanfeb/claude-projects/<project-name> --allowed-tools 'Bash,Read,Edit,Write,Glob,Grep' --output-format json --dangerously-skip-permissions
   ```

   The prompt must instruct Claude Code to:
   - <list of specific tasks>
   - <artifacts to produce>
   - <beads commands to run for tracking>

2. Parse the JSON output. Report the result.

**RULES:**
- NEVER call `write`, `read`, or `edit` tools — you only have `exec`
- <domain-specific constraints>
- Always set timeout_seconds to 1800
- Use single quotes around the prompt; escape inner single quotes as `'\''`
- All beads scripts are at `/Users/stephanfeb/.kaggen/skills/beads/scripts/`

**COMMAND FORMATTING:**
- Use `--allowed-tools` (with a dash), NOT `--allowedTools`
- Use `--output-format`, NOT `--outputFormat`
- Use `--add-dir` to set the working directory (there is NO `-C` flag)
- Always include `--dangerously-skip-permissions`
- Always include `--output-format json`
```

### Step 2: Create the Pipeline YAML

Create a file in `~/.kaggen/pipelines/<pipeline_name>.yaml`:

```yaml
name: <pipeline_name>
description: <what this pipeline does>
trigger: "<when to activate — be specific and comprehensive>"
stages:
  - agent: <skill_name_1>
    description: <what this stage does>
  - agent: <skill_name_2>
    description: <what this stage does>
  - agent: <skill_name_3>
    description: <what this stage does>
    on_fail: <skill_name_2>    # optional: retry loop
    max_retries: 3             # optional
```

### Step 3: Reload

Send SIGHUP to the kaggen process to reload skills and pipelines without restarting:

```bash
kill -HUP $(pgrep kaggen)
```

Or restart the service.

### Verification

After reload, the coordinator's system prompt will include the new pipeline. You can verify by checking the logs for pipeline loading messages, or by sending a request that matches the new pipeline's trigger and confirming the coordinator dispatches the correct sequence of agents.

## Beads Integration

Pipelines coordinate shared work through beads, a git-backed issue tracker. The standard pattern:

1. **First stage** (e.g., product_owner) creates a beads epic with child issues
2. **Middle stages** (e.g., architect, coder) read issues, add specs/comments, update status
3. **Final stage** (e.g., qa) validates against acceptance criteria and closes issues

Beads scripts are at `/Users/stephanfeb/.kaggen/skills/beads/scripts/`. Key commands:

| Script | Purpose |
|--------|---------|
| `create.sh` | Create issues (epic, feature, bug, task, chore) |
| `list.sh` | List issues with filters |
| `show.sh` | Show issue details |
| `update.sh` | Update status, priority, assignee |
| `close.sh` | Close issues with reason |
| `comments.sh` | View/add comments (used for specs, QA findings) |
| `dep.sh` | Manage dependencies between issues |
| `status.sh` | Overview of issue database |

All beads data lives in `.beads/` inside the project directory and is versioned through git.

## File Locations

| Path | Purpose |
|------|---------|
| `~/.kaggen/pipelines/*.yaml` | Pipeline definitions |
| `~/.kaggen/skills/<name>/SKILL.md` | Skill definitions (agents) |
| `internal/pipeline/pipeline.go` | Pipeline loader and instruction generator |
| `internal/agent/agent.go` | Coordinator construction (calls `pipeline.BuildInstruction`) |
| `internal/agent/skills.go` | Skill loader with per-skill tool filtering |

## Design Constraints

- **Coordinator is routing-only.** It has no action tools (no exec, read, write). It can only dispatch tasks and check status. This prevents the coordinator from "helping" by running commands directly.
- **Skills declare their tools.** The `tools` field in SKILL.md frontmatter restricts which tools the agent gets. Most skills use `tools: exec` to force delegation to Claude Code CLI.
- **Pipelines are sequential.** The coordinator dispatches one stage at a time and waits for completion. Parallel stage execution is not currently supported.
- **All agent work goes through Claude Code CLI.** Skills call `claude -p` via `exec`. Claude Code handles planning, tool use, and task management internally. Skills should never micromanage.
