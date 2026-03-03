#!/usr/bin/env bash
# Run JavaScript in the page context
# Usage: evaluate.sh <script>

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOOL="$SCRIPT_DIR/../playwright_tool.py"

script=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --help|-h)
            echo "Usage: evaluate.sh <script>"
            echo "Example: evaluate.sh 'document.querySelectorAll(\"a\").length'"
            exit 0
            ;;
        -*) echo "Unknown option: $1" >&2; exit 1 ;;
        *) script="$1"; shift ;;
    esac
done

if [[ -z "$script" ]]; then
    echo '{"success": false, "message": "Script required. Usage: evaluate.sh <script>"}' >&2
    exit 1
fi

# Escape script for JSON (handle quotes and backslashes)
script_escaped=$(echo "$script" | sed 's/\\/\\\\/g; s/"/\\"/g')

exec python3 "$TOOL" "{\"action\": \"evaluate\", \"script\": \"$script_escaped\"}"
