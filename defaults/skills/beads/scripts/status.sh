#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "--help" ]]; then
    echo "Usage: status.sh [options...]"
    echo ""
    echo "Show issue database overview and statistics."
    echo ""
    echo "Options:"
    echo "  --assigned       Show issues assigned to current user"
    echo "  --no-activity    Skip git activity (faster)"
    echo "  --json           JSON output"
    exit 0
fi

exec bd status "$@"
