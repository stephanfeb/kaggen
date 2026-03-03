#!/usr/bin/env bash
# Get page info (title or URL)
# Usage: info.sh <title|url>

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOOL="$SCRIPT_DIR/../playwright_tool.py"

what=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --help|-h)
            echo "Usage: info.sh <title|url>"
            exit 0
            ;;
        title) what="getTitle"; shift ;;
        url) what="getCurrentUrl"; shift ;;
        -*) echo "Unknown option: $1" >&2; exit 1 ;;
        *) echo "Unknown info type: $1. Use title or url." >&2; exit 1 ;;
    esac
done

if [[ -z "$what" ]]; then
    echo '{"success": false, "message": "Info type required. Usage: info.sh <title|url>"}' >&2
    exit 1
fi

exec python3 "$TOOL" "{\"action\": \"$what\"}"
