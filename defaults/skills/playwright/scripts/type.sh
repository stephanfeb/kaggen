#!/usr/bin/env bash
# Type text into an input element
# Usage: type.sh <selector> <text> [--timeout <ms>]

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOOL="$SCRIPT_DIR/../playwright_tool.py"

selector=""
text=""
timeout_ms=5000

while [[ $# -gt 0 ]]; do
    case "$1" in
        --timeout) timeout_ms="$2"; shift 2 ;;
        --help|-h)
            echo "Usage: type.sh <selector> <text> [--timeout <ms>]"
            exit 0
            ;;
        -*) echo "Unknown option: $1" >&2; exit 1 ;;
        *)
            if [[ -z "$selector" ]]; then
                selector="$1"
            else
                text="$1"
            fi
            shift
            ;;
    esac
done

if [[ -z "$selector" || -z "$text" ]]; then
    echo '{"success": false, "message": "Selector and text required. Usage: type.sh <selector> <text>"}' >&2
    exit 1
fi

# Escape for JSON
selector_escaped=$(echo "$selector" | sed 's/\\/\\\\/g; s/"/\\"/g')
text_escaped=$(echo "$text" | sed 's/\\/\\\\/g; s/"/\\"/g')

exec python3 "$TOOL" "{\"action\": \"type\", \"selector\": \"$selector_escaped\", \"text\": \"$text_escaped\", \"timeout_ms\": $timeout_ms}"
