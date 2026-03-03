#!/usr/bin/env bash
# Browser history navigation
# Usage: history.sh <back|forward|reload>

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOOL="$SCRIPT_DIR/../playwright_tool.py"

action=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --help|-h)
            echo "Usage: history.sh <back|forward|reload>"
            exit 0
            ;;
        back) action="goBack"; shift ;;
        forward) action="goForward"; shift ;;
        reload) action="reload"; shift ;;
        -*) echo "Unknown option: $1" >&2; exit 1 ;;
        *) echo "Unknown action: $1. Use back, forward, or reload." >&2; exit 1 ;;
    esac
done

if [[ -z "$action" ]]; then
    echo '{"success": false, "message": "Action required. Usage: history.sh <back|forward|reload>"}' >&2
    exit 1
fi

exec python3 "$TOOL" "{\"action\": \"$action\"}"
