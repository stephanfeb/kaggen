#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "--help" ]]; then
    echo "Usage: ready.sh [options...]"
    echo ""
    echo "Show issues with no open blockers that are ready for work."
    echo "All options are passed directly to 'bd ready'."
    echo ""
    echo "Options:"
    echo "  --assignee <name>   Filter by assignee"
    echo "  --priority <n>      Filter by priority (0=highest)"
    echo "  --limit <n>         Max issues (default 10)"
    echo "  --unassigned        Only unassigned issues"
    echo "  --pretty            Tree format with status symbols"
    echo "  --json              JSON output"
    exit 0
fi

exec bd ready "$@"
