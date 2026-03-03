#!/usr/bin/env bash
# Wait for an element to appear on the page
# Usage: wait.sh <selector> [--timeout <ms>]

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOOL="$SCRIPT_DIR/../playwright_tool.py"

selector=""
timeout_ms=30000

while [[ $# -gt 0 ]]; do
    case "$1" in
        --timeout) timeout_ms="$2"; shift 2 ;;
        --help|-h)
            echo "Usage: wait.sh <selector> [--timeout <ms>]"
            exit 0
            ;;
        -*) echo "Unknown option: $1" >&2; exit 1 ;;
        *) selector="$1"; shift ;;
    esac
done

if [[ -z "$selector" ]]; then
    echo '{"success": false, "message": "Selector required. Usage: wait.sh <selector>"}' >&2
    exit 1
fi

selector_escaped=$(echo "$selector" | sed 's/\\/\\\\/g; s/"/\\"/g')

exec python3 "$TOOL" "{\"action\": \"wait\", \"selector\": \"$selector_escaped\", \"timeout_ms\": $timeout_ms}"
