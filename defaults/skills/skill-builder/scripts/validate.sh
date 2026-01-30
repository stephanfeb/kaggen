#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]] || [[ "$1" == "--help" ]]; then
    echo "Usage: validate.sh <skill_dir>"
    echo ""
    echo "Validate a skill directory for correct structure and conventions."
    echo "Exits 0 on pass, 1 on failure."
    exit 0
fi

SKILL_DIR="$1"
ERRORS=0

fail() {
    echo "FAIL: $1" >&2
    ERRORS=$((ERRORS + 1))
}

warn() {
    echo "WARN: $1" >&2
}

# Check directory exists
if [[ ! -d "$SKILL_DIR" ]]; then
    echo "Error: not a directory: $SKILL_DIR" >&2
    exit 1
fi

SKILL_MD="$SKILL_DIR/SKILL.md"

# Check SKILL.md exists
if [[ ! -f "$SKILL_MD" ]]; then
    fail "SKILL.md not found in $SKILL_DIR"
    echo ""
    echo "Result: $ERRORS error(s)"
    exit 1
fi

# Check frontmatter
if ! head -1 "$SKILL_MD" | grep -q '^---$'; then
    fail "SKILL.md missing frontmatter (must start with ---)"
else
    # Extract frontmatter
    # Extract lines between first and second --- (the frontmatter body)
    FRONTMATTER=$(awk 'NR==1{next} /^---$/{exit} {print}' "$SKILL_MD")

    if ! echo "$FRONTMATTER" | grep -q '^name:'; then
        fail "SKILL.md frontmatter missing 'name:' field"
    fi
    if ! echo "$FRONTMATTER" | grep -q '^description:'; then
        fail "SKILL.md frontmatter missing 'description:' field"
    fi

    # Check for TODO placeholders
    if echo "$FRONTMATTER" | grep -qi 'TODO'; then
        fail "SKILL.md frontmatter contains TODO placeholder"
    fi
fi

# Check body is non-trivial
BODY_LINES=$(sed '1,/^---$/d' "$SKILL_MD" | sed '1,/^---$/d' | wc -l | tr -d ' ')
if [[ "$BODY_LINES" -lt 10 ]]; then
    warn "SKILL.md body is very short ($BODY_LINES lines) — consider adding more documentation"
fi

# Check scripts
SCRIPTS_DIR="$SKILL_DIR/scripts"
if [[ ! -d "$SCRIPTS_DIR" ]]; then
    warn "No scripts/ directory found"
else
    SCRIPT_COUNT=0
    for SCRIPT in "$SCRIPTS_DIR"/*.sh; do
        [[ -f "$SCRIPT" ]] || continue
        SCRIPT_COUNT=$((SCRIPT_COUNT + 1))
        BASENAME=$(basename "$SCRIPT")

        # Check shebang
        FIRST_LINE=$(head -1 "$SCRIPT")
        if [[ "$FIRST_LINE" != "#!/usr/bin/env bash" ]] && [[ "$FIRST_LINE" != "#!/bin/bash" ]]; then
            fail "$BASENAME: missing or incorrect shebang (got: $FIRST_LINE)"
        fi

        # Check syntax
        if ! bash -n "$SCRIPT" 2>/dev/null; then
            fail "$BASENAME: bash syntax error"
        fi

        # Check --help support
        if ! grep -q '\-\-help' "$SCRIPT"; then
            warn "$BASENAME: does not appear to handle --help"
        fi

        # Check set -euo pipefail
        if ! grep -q 'set -euo pipefail' "$SCRIPT"; then
            warn "$BASENAME: missing 'set -euo pipefail'"
        fi
    done

    if [[ "$SCRIPT_COUNT" -eq 0 ]]; then
        warn "No .sh scripts found in scripts/"
    else
        echo "Checked $SCRIPT_COUNT script(s)"
    fi
fi

echo ""
if [[ "$ERRORS" -gt 0 ]]; then
    echo "Result: FAILED ($ERRORS error(s))"
    exit 1
else
    echo "Result: PASSED"
    exit 0
fi
