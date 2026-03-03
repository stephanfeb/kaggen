#!/usr/bin/env bash
# Navigate to a URL
# Usage: navigate.sh <url> [--timeout <ms>] [--headless false]

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOOL="$SCRIPT_DIR/../playwright_tool.py"

url=""
timeout_ms=30000
headless=true

while [[ $# -gt 0 ]]; do
    case "$1" in
        --timeout) timeout_ms="$2"; shift 2 ;;
        --headless) headless="$2"; shift 2 ;;
        --help|-h)
            echo "Usage: navigate.sh <url> [--timeout <ms>] [--headless false]"
            exit 0
            ;;
        -*) echo "Unknown option: $1" >&2; exit 1 ;;
        *) url="$1"; shift ;;
    esac
done

if [[ -z "$url" ]]; then
    echo '{"success": false, "message": "URL required. Usage: navigate.sh <url>"}' >&2
    exit 1
fi

exec python3 "$TOOL" "{\"action\": \"navigate\", \"url\": \"$url\", \"timeout_ms\": $timeout_ms, \"headless\": $headless}"
