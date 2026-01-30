#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]] || [[ "$1" == "--help" ]]; then
    echo "Usage: search.sh <query> [options...]"
    echo ""
    echo "Search issues by text across title, description, and ID."
    echo "Options passed directly to 'bd search'."
    echo ""
    echo "Options:"
    echo "  -s <status>      Filter by status"
    echo "  -t <type>        Filter by type"
    echo "  -a <assignee>    Filter by assignee"
    echo "  -l <labels>      Filter by labels"
    echo "  --limit <n>      Max results"
    echo "  --sort <field>   Sort: priority, created, updated"
    echo "  --long           Detailed output"
    echo "  --json           JSON output"
    exit 0
fi

exec bd search "$@"
