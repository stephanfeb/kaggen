#!/usr/bin/env bash
# Get an attribute value from an element
# Usage: get_attr.sh <selector> <attribute> [--timeout <ms>]

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOOL="$SCRIPT_DIR/../playwright_tool.py"

selector=""
attribute=""
timeout_ms=5000

while [[ $# -gt 0 ]]; do
    case "$1" in
        --timeout) timeout_ms="$2"; shift 2 ;;
        --help|-h)
            echo "Usage: get_attr.sh <selector> <attribute> [--timeout <ms>]"
            exit 0
            ;;
        -*) echo "Unknown option: $1" >&2; exit 1 ;;
        *)
            if [[ -z "$selector" ]]; then
                selector="$1"
            else
                attribute="$1"
            fi
            shift
            ;;
    esac
done

if [[ -z "$selector" || -z "$attribute" ]]; then
    echo '{"success": false, "message": "Selector and attribute required. Usage: get_attr.sh <selector> <attribute>"}' >&2
    exit 1
fi

selector_escaped=$(echo "$selector" | sed 's/\\/\\\\/g; s/"/\\"/g')

exec python3 "$TOOL" "{\"action\": \"getAttribute\", \"selector\": \"$selector_escaped\", \"attribute\": \"$attribute\", \"timeout_ms\": $timeout_ms}"
