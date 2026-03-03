#!/usr/bin/env bash
# Extract all visible text from the current page
# Usage: content.sh

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOOL="$SCRIPT_DIR/../playwright_tool.py"

if [[ "${1:-}" == "--help" || "${1:-}" == "-h" ]]; then
    echo "Usage: content.sh"
    exit 0
fi

exec python3 "$TOOL" '{"action": "content"}'
