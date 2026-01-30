#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]] || [[ "$1" == "--help" ]]; then
    echo "Usage: show.sh <id> [options...]"
    echo ""
    echo "Show issue details. Options passed directly to 'bd show'."
    echo ""
    echo "Options:"
    echo "  --json    JSON output"
    echo "  --refs    Show referencing issues"
    echo "  --short   Compact one-line output"
    exit 0
fi

exec bd show "$@"
