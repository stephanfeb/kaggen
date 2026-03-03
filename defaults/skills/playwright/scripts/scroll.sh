#!/usr/bin/env bash
# Scroll the page up or down
# Usage: scroll.sh <direction> [amount]
# direction: up | down
# amount: pixels to scroll (default: 300)

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOOL="$SCRIPT_DIR/../playwright_tool.py"

direction=""
amount=300

while [[ $# -gt 0 ]]; do
    case "$1" in
        --help|-h)
            echo "Usage: scroll.sh <direction> [amount]"
            echo "direction: up | down"
            echo "amount: pixels to scroll (default: 300)"
            exit 0
            ;;
        up|down) direction="$1"; shift ;;
        [0-9]*) amount="$1"; shift ;;
        -*) echo "Unknown option: $1" >&2; exit 1 ;;
        *) direction="$1"; shift ;;
    esac
done

if [[ -z "$direction" ]]; then
    echo '{"success": false, "message": "Direction required. Usage: scroll.sh <up|down> [amount]"}' >&2
    exit 1
fi

exec python3 "$TOOL" "{\"action\": \"scroll\", \"direction\": \"$direction\", \"amount\": $amount}"
