#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]] || [[ "$1" == "--help" ]]; then
    echo "Usage: comments.sh <id>              — View comments"
    echo "       comments.sh add <id> <text>   — Add a comment"
    echo ""
    echo "View or manage comments on an issue."
    exit 0
fi

exec bd comments "$@"
