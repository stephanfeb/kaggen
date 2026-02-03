#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]] || [[ "$1" == "--help" ]]; then
    echo "Usage: validate.sh <skill_dir>"
    echo ""
    echo "Validate a skill directory for correct structure and conventions."
    echo "Supports both LLM agent and delegate:claude skills."
    echo "Exits 0 on pass, 1 on failure."
    exit 0
fi

SKILL_DIR="$1"
ERRORS=0
WARNINGS=0

fail() {
    echo "FAIL: $1" >&2
    ERRORS=$((ERRORS + 1))
}

warn() {
    echo "WARN: $1" >&2
    WARNINGS=$((WARNINGS + 1))
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
    echo ""
    echo "Result: FAILED ($ERRORS error(s))"
    exit 1
fi

# Extract frontmatter
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

# Known valid tool names (must match Go tool declarations in internal/tools/)
VALID_TOOLS="exec read write browser memory_search memory_store cron_schedules cron_add cron_update cron_delete backlog_list backlog_add backlog_update backlog_complete backlog_decompose backlog_plan_status external_task_register external_task_list"

# Validate tools: field if present
TOOLS_LINE=$(echo "$FRONTMATTER" | grep '^tools:' | sed 's/^tools://' | tr -d '[],' | xargs || true)
if [[ -n "$TOOLS_LINE" ]]; then
    for TOOL in $TOOLS_LINE; do
        if ! echo "$VALID_TOOLS" | grep -qw "$TOOL"; then
            fail "Unknown tool '$TOOL' in tools: field. Valid tools: $VALID_TOOLS"
        fi
    done
fi

# Validate guarded_tools: field if present
GUARDED_LINE=$(echo "$FRONTMATTER" | grep '^guarded_tools:' | sed 's/^guarded_tools://' | tr -d '[],' | xargs || true)
if [[ -n "$GUARDED_LINE" ]]; then
    for GTOOL in $GUARDED_LINE; do
        if ! echo "$VALID_TOOLS" | grep -qw "$GTOOL"; then
            fail "Unknown tool '$GTOOL' in guarded_tools: field. Valid tools: $VALID_TOOLS"
        fi
        # Also check it's in the tools list
        if [[ -n "$TOOLS_LINE" ]] && ! echo "$TOOLS_LINE" | grep -qw "$GTOOL"; then
            fail "guarded_tool '$GTOOL' must also be listed in tools:"
        fi
    done
fi

# Validate notify_tools: field if present
NOTIFY_LINE=$(echo "$FRONTMATTER" | grep '^notify_tools:' | sed 's/^notify_tools://' | tr -d '[],' | xargs || true)
if [[ -n "$NOTIFY_LINE" ]]; then
    for NTOOL in $NOTIFY_LINE; do
        if ! echo "$VALID_TOOLS" | grep -qw "$NTOOL"; then
            fail "Unknown tool '$NTOOL' in notify_tools: field. Valid tools: $VALID_TOOLS"
        fi
        if [[ -n "$TOOLS_LINE" ]] && ! echo "$TOOLS_LINE" | grep -qw "$NTOOL"; then
            fail "notify_tool '$NTOOL' must also be listed in tools:"
        fi
    done
fi

# Determine agent type
IS_DELEGATE=false
if echo "$FRONTMATTER" | grep -q '^delegate:.*claude'; then
    IS_DELEGATE=true
fi

# Extract body (everything after the second ---)
BODY=$(awk 'BEGIN{n=0} /^---$/{n++; next} n>=2{print}' "$SKILL_MD")
BODY_LINES=$(echo "$BODY" | sed '/^$/d' | wc -l | tr -d ' ')

if [[ "$BODY_LINES" -lt 10 ]]; then
    warn "SKILL.md body is very short ($BODY_LINES lines) — consider adding more documentation"
fi

if [[ "$IS_DELEGATE" == "true" ]]; then
    # --- Delegate skill checks ---
    echo "Type: delegate:claude"

    # claude_model should be set
    if ! echo "$FRONTMATTER" | grep -q '^claude_model:'; then
        warn "delegate skill missing 'claude_model:' — will default to sonnet"
    fi

    # Body should start with a role statement, not meta-instructions
    FIRST_BODY_LINE=$(echo "$BODY" | sed '/^$/d' | head -1)
    if echo "$FIRST_BODY_LINE" | grep -qi 'exec:\|claude -p\|--output-format'; then
        fail "delegate skill body contains meta-delegation instructions (exec:, claude -p, --output-format)"
    fi
    if ! echo "$FIRST_BODY_LINE" | grep -qi 'you are'; then
        warn "delegate skill body should start with 'You are a [Role]...' statement"
    fi

    # Should have a workflow section
    if ! echo "$BODY" | grep -qi '## Workflow\|## Steps'; then
        warn "delegate skill body should include a '## Workflow' section"
    fi

    # Scripts are optional for delegate skills
    if [[ -d "$SKILL_DIR/scripts" ]]; then
        SCRIPT_COUNT=$(find "$SKILL_DIR/scripts" -name '*.sh' 2>/dev/null | wc -l | tr -d ' ')
        if [[ "$SCRIPT_COUNT" -gt 0 ]]; then
            echo "Note: delegate skill has $SCRIPT_COUNT script(s) (optional for delegate skills)"
        fi
    fi
else
    # --- LLM agent skill checks ---
    echo "Type: LLM agent"

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
fi

echo ""
if [[ "$ERRORS" -gt 0 ]]; then
    echo "Result: FAILED ($ERRORS error(s), $WARNINGS warning(s))"
    exit 1
else
    echo "Result: PASSED ($WARNINGS warning(s))"
    exit 0
fi
