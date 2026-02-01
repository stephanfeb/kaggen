---
name: skill-builder
description: Scaffold, validate, and install new Kaggen skills from descriptions or templates
---

# Skill Builder — Create New Kaggen Skills

Use this skill when the user asks to create, build, or add a new skill to Kaggen. This skill helps you scaffold a complete skill directory with proper conventions, validate it, and install it.

## Workflow

1. **Understand** what the skill needs to do and which agent type fits
2. **Scaffold** the directory structure with `scaffold.sh`
3. **Write** the SKILL.md and (for LLM skills) bash scripts using your file tools
4. **Validate** the result with `validate.sh`
5. **Install** into `~/.kaggen/skills/` with `install.sh`
6. **Remind** the user to reload skills with `kill -HUP $(pgrep kaggen)`

## Available Commands

### scaffold.sh — Create skill skeleton

```bash
bash scripts/scaffold.sh <name> [output_dir]
bash scripts/scaffold.sh <name> [output_dir] --delegate
```

Creates a directory with a template SKILL.md and empty scripts/ folder. Default output_dir is the current working directory.

Use `--delegate` to generate a `delegate: claude` template instead of the default LLM agent template.

### validate.sh — Lint a skill directory

```bash
bash scripts/validate.sh <skill_dir>
```

Checks:
- SKILL.md exists with valid `name:` and `description:` frontmatter
- For LLM skills: scripts/*.sh have a shebang, pass `bash -n` syntax check, and respond to `--help`
- For delegate skills: `claude_model` is set, body starts with a role statement

Exits 0 on pass, 1 on failure with diagnostics.

### install.sh — Install to ~/.kaggen/skills/

```bash
bash scripts/install.sh <skill_dir> [--force]
```

Copies the skill directory into `~/.kaggen/skills/`, sets executable permissions, and warns on name collisions (use `--force` to overwrite).

## Two Agent Types

### 1. LLM Agent (default)

When `delegate` is omitted, the skill runs as an in-process LLM agent. The body documents available CLI commands and scripts for the agent to call.

**Best for:** Wrapping CLI tools, simple automation, tasks needing specific tool access.

**Template structure:**
- "Use this skill when..." paragraph
- `## Available Commands` with one section per script
- `## Tips` with gotchas

### 2. Claude Agent (`delegate: claude`)

When `delegate: claude` is set, the skill runs as a `claude -p` subprocess. The body is a direct prompt for Claude Code — NOT meta-instructions about calling `claude -p`.

**Best for:** Complex multi-step workflows, code generation, architecture, QA — tasks requiring deep reasoning and autonomous tool use.

**Template structure:**
- "You are a [Role]. Your job is to..." opening
- Numbered workflow steps with exact bash commands
- `## Rules` section with constraints

**Important:** Do NOT include `exec:` prefixes, `claude -p` invocations, CLI flags, or state management in delegate skill bodies. The subprocess dispatch handles all of that.

## Skill Conventions

### SKILL.md Frontmatter

```yaml
---
name: <lowercase-hyphenated-name>       # Required. Must match directory name.
description: <one-line description>      # Required. Coordinator reads this to route tasks.
delegate: claude                         # Optional. Set to "claude" for subprocess agent.
claude_model: sonnet                     # Optional. opus, sonnet, or haiku. Default: sonnet.
claude_tools: Bash,Read,Edit,Write,Glob,Grep  # Optional. Tools for subprocess.
tools: [browser, memory_search]          # Optional. Tool filter for LLM agent mode.
work_dir: ~/projects/foo                 # Optional. Extra working directory for subprocess.
---
```

### Script Conventions (LLM skills only)

Every bash script MUST:

1. Start with `#!/usr/bin/env bash` and `set -euo pipefail`
2. Support `--help` flag (print usage and exit 0)
3. Validate inputs and print errors to stderr (`>&2`)
4. Report results to stdout (filename, size, counts)
5. Exit 0 on success, 1 on failure

### Naming

- Skill name: lowercase, hyphens for word separation (e.g. `ffmpeg-video`, `aws-s3`)
- Script names: lowercase, underscores, descriptive (e.g. `convert.sh`, `batch_process.sh`)
- Keep script count to 3-6 per skill — each script should do one thing well

### Model Selection (delegate skills)

| Model | Cost | Use When |
|-------|------|----------|
| `opus` | High | Complex reasoning: architecture, code generation, product analysis |
| `sonnet` | Medium | Balanced tasks, general-purpose (default) |
| `haiku` | Low | Validation, linting, simple checks, high-volume tasks |
