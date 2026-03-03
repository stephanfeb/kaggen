#!/usr/bin/env bash
# Take a screenshot of the current page
# Usage: screenshot.sh [path] [--full-page]

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOOL="$SCRIPT_DIR/../playwright_tool.py"

path=""
full_page=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        --full-page) full_page=true; shift ;;
        --help|-h)
            echo "Usage: screenshot.sh [path] [--full-page]"
            exit 0
            ;;
        -*) echo "Unknown option: $1" >&2; exit 1 ;;
        *) path="$1"; shift ;;
    esac
done

if [[ -n "$path" ]]; then
    exec python3 "$TOOL" "{\"action\": \"screenshot\", \"path\": \"$path\", \"full_page\": $full_page}"
else
    exec python3 "$TOOL" "{\"action\": \"screenshot\", \"full_page\": $full_page}"
fi
