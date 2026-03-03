#!/usr/bin/env bash
# Set browser viewport dimensions
# Usage: viewport.sh <width> <height>

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOOL="$SCRIPT_DIR/../playwright_tool.py"

width=""
height=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --help|-h)
            echo "Usage: viewport.sh <width> <height>"
            echo "Example: viewport.sh 375 812  # iPhone X dimensions"
            exit 0
            ;;
        -*) echo "Unknown option: $1" >&2; exit 1 ;;
        *)
            if [[ -z "$width" ]]; then
                width="$1"
            else
                height="$1"
            fi
            shift
            ;;
    esac
done

if [[ -z "$width" || -z "$height" ]]; then
    echo '{"success": false, "message": "Width and height required. Usage: viewport.sh <width> <height>"}' >&2
    exit 1
fi

exec python3 "$TOOL" "{\"action\": \"setViewport\", \"width\": $width, \"height\": $height}"
