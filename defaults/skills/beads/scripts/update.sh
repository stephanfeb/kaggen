#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]] || [[ "$1" == "--help" ]]; then
    echo "Usage: update.sh <id> [options...]"
    echo ""
    echo "Update an issue. Options passed directly to 'bd update'."
    echo ""
    echo "Options:"
    echo "  --title <text>         New title"
    echo "  -d <desc>              New description"
    echo "  -s <status>            open, in_progress, blocked, closed"
    echo "  -p <0-4>               New priority"
    echo "  -a <assignee>          New assignee"
    echo "  --add-label <label>    Add a label"
    echo "  --remove-label <l>     Remove a label"
    echo "  --claim                Assign to you + set in_progress"
    echo "  --due <date>           Set due date"
    echo "  --notes <text>         Additional notes"
    exit 0
fi

exec bd update "$@"
