#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]] || [[ "$1" == "--help" ]]; then
    echo "Usage: install.sh <skill_dir> [--force]"
    echo ""
    echo "Install a skill directory into ~/.kaggen/skills/."
    echo "Use --force to overwrite an existing skill."
    exit 0
fi

SKILL_DIR="$1"
FORCE=false

shift
while [[ $# -gt 0 ]]; do
    case "$1" in
        --force) FORCE=true; shift ;;
        *)       shift ;;
    esac
done

if [[ ! -d "$SKILL_DIR" ]]; then
    echo "Error: not a directory: $SKILL_DIR" >&2
    exit 1
fi

if [[ ! -f "$SKILL_DIR/SKILL.md" ]]; then
    echo "Error: no SKILL.md found in $SKILL_DIR" >&2
    exit 1
fi

# Extract skill name from frontmatter
NAME=$(sed -n '2,/^---$/p' "$SKILL_DIR/SKILL.md" | grep '^name:' | head -1 | sed 's/^name:[[:space:]]*//')
if [[ -z "$NAME" ]]; then
    echo "Error: could not extract skill name from SKILL.md frontmatter" >&2
    exit 1
fi

INSTALL_DIR="$HOME/.kaggen/skills/$NAME"

if [[ -d "$INSTALL_DIR" ]] && [[ "$FORCE" == "false" ]]; then
    echo "Error: skill '$NAME' already exists at $INSTALL_DIR" >&2
    echo "Use --force to overwrite." >&2
    exit 1
fi

# Install
mkdir -p "$HOME/.kaggen/skills"
rm -rf "$INSTALL_DIR"
cp -r "$SKILL_DIR" "$INSTALL_DIR"

# Set executable permissions on scripts
if [[ -d "$INSTALL_DIR/scripts" ]]; then
    chmod +x "$INSTALL_DIR/scripts"/*.sh 2>/dev/null || true
fi

echo "Installed: $INSTALL_DIR/"
echo ""
echo "To activate, reload skills:"
echo "  kill -HUP \$(pgrep -f 'kaggen (agent|gateway)')"
