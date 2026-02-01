#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]] || [[ "$1" == "--help" ]]; then
    echo "Usage: scaffold.sh <name> [output_dir] [--delegate]"
    echo ""
    echo "Create a new skill directory skeleton with a template SKILL.md."
    echo "Default output_dir is the current working directory."
    echo ""
    echo "Options:"
    echo "  --delegate    Generate a delegate:claude template instead of LLM agent"
    exit 0
fi

NAME="$1"
shift

OUTPUT_DIR="."
DELEGATE=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        --delegate) DELEGATE=true; shift ;;
        *)          OUTPUT_DIR="$1"; shift ;;
    esac
done

SKILL_DIR="$OUTPUT_DIR/$NAME"

if [[ -d "$SKILL_DIR" ]]; then
    echo "Error: directory already exists: $SKILL_DIR" >&2
    exit 1
fi

if [[ "$DELEGATE" == "true" ]]; then
    # Delegate skill — no scripts directory needed
    mkdir -p "$SKILL_DIR"

    cat > "$SKILL_DIR/SKILL.md" << 'TEMPLATE'
---
name: SKILL_NAME_PLACEHOLDER
description: TODO — one-line description
delegate: claude
claude_model: sonnet
claude_tools: Bash,Read,Edit,Write,Glob,Grep
---

You are a TODO Role. Your job is to TODO describe responsibility.

## Workflow

1. Read the project's AGENTS.md, BACKLOG.md, and existing source code to understand context.

2. If the project uses beads issue tracking, check for relevant issues:
   ```bash
   bash ~/.kaggen/skills/beads/scripts/list.sh -s open --pretty
   ```

3. Claim issues before starting work:
   ```bash
   bash ~/.kaggen/skills/beads/scripts/update.sh <id> --claim
   ```

4. TODO — describe the main work steps.

5. After completing work, add a comment summarizing what was done:
   ```bash
   bash ~/.kaggen/skills/beads/scripts/comments.sh add <id> "TODO: <summary>"
   ```

## Rules

- TODO add constraints and boundaries
- Read existing code before making changes
- Do NOT close issues — leave for review
TEMPLATE
else
    # LLM agent skill — includes scripts directory
    mkdir -p "$SKILL_DIR/scripts"

    cat > "$SKILL_DIR/SKILL.md" << 'TEMPLATE'
---
name: SKILL_NAME_PLACEHOLDER
description: TODO — one-line description
---

# SKILL_NAME_PLACEHOLDER — TODO Title

Use this skill when the user asks to TODO.

## Available Commands

### example.sh — TODO describe

```bash
bash scripts/example.sh <input> [options...]
```

Examples:

```bash
# TODO add examples
bash scripts/example.sh input.txt
```

## Tips

- TODO add tips
TEMPLATE
fi

# Replace placeholder with actual name
sed -i '' "s/SKILL_NAME_PLACEHOLDER/$NAME/g" "$SKILL_DIR/SKILL.md" 2>/dev/null || \
    sed -i "s/SKILL_NAME_PLACEHOLDER/$NAME/g" "$SKILL_DIR/SKILL.md"

echo "Scaffolded: $SKILL_DIR/"
if [[ "$DELEGATE" == "true" ]]; then
    echo "  SKILL.md      (delegate:claude template — fill in role, workflow, rules)"
else
    echo "  SKILL.md      (LLM agent template — fill in description, commands, examples)"
    echo "  scripts/      (add your bash scripts here)"
fi
echo ""
echo "Next steps:"
echo "  1. Edit SKILL.md with full documentation"
if [[ "$DELEGATE" == "false" ]]; then
    echo "  2. Add scripts to scripts/"
fi
echo "  3. Run: bash validate.sh $SKILL_DIR"
echo "  4. Run: bash install.sh $SKILL_DIR"
