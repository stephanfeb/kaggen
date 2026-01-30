#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "--help" ]]; then
    echo "Usage: close.sh [id] [options...]"
    echo ""
    echo "Close one or more issues. If no ID given, closes last touched issue."
    echo "Options passed directly to 'bd close'."
    echo ""
    echo "Options:"
    echo "  -r <reason>       Reason for closing"
    echo "  --suggest-next    Show newly unblocked issues"
    exit 0
fi

exec bd close "$@"
