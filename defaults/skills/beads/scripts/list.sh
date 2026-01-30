#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "--help" ]]; then
    echo "Usage: list.sh [options...]"
    echo ""
    echo "List issues with filters. Options passed directly to 'bd list'."
    echo ""
    echo "Options:"
    echo "  -s <status>      open, in_progress, blocked, closed"
    echo "  -p <priority>    Filter by priority"
    echo "  -t <type>        Filter by type"
    echo "  -a <assignee>    Filter by assignee"
    echo "  -l <labels>      Filter by labels"
    echo "  --all            Include closed issues"
    echo "  --limit <n>      Max results (default 50)"
    echo "  --sort <field>   Sort: priority, created, updated, status, title"
    echo "  --pretty         Tree format"
    echo "  --long           Detailed output"
    echo "  --json           JSON output"
    exit 0
fi

exec bd list "$@"
