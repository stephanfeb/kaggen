#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]] || [[ "$1" == "--help" ]]; then
    echo "Usage: graph.sh <id>"
    echo ""
    echo "Display ASCII dependency graph for an issue."
    echo "Left-to-right = execution order. Same column = parallel."
    exit 0
fi

exec bd graph "$@"
