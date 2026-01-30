#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]] || [[ "$1" == "--help" ]]; then
    echo "Usage: scaffold.sh <name> [output_dir]"
    echo ""
    echo "Create a new skill directory skeleton with a template SKILL.md."
    echo "Default output_dir is the current working directory."
    exit 0
fi

NAME="$1"
OUTPUT_DIR="${2:-.}"

SKILL_DIR="$OUTPUT_DIR/$NAME"

if [[ -d "$SKILL_DIR" ]]; then
    echo "Error: directory already exists: $SKILL_DIR" >&2
    exit 1
fi

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

# Replace placeholder with actual name
sed -i '' "s/SKILL_NAME_PLACEHOLDER/$NAME/g" "$SKILL_DIR/SKILL.md" 2>/dev/null || \
    sed -i "s/SKILL_NAME_PLACEHOLDER/$NAME/g" "$SKILL_DIR/SKILL.md"

echo "Scaffolded: $SKILL_DIR/"
echo "  SKILL.md      (template — fill in description, commands, examples)"
echo "  scripts/      (add your bash scripts here)"
echo ""
echo "Next steps:"
echo "  1. Edit SKILL.md with full documentation"
echo "  2. Add scripts to scripts/"
echo "  3. Run: bash validate.sh $SKILL_DIR"
echo "  4. Run: bash install.sh $SKILL_DIR"
