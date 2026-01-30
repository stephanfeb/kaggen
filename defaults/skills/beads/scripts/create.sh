#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]] || [[ "$1" == "--help" ]]; then
    echo "Usage: create.sh <title> [options...]"
    echo ""
    echo "Create a new issue. All options are passed directly to 'bd create'."
    echo ""
    echo "Options:"
    echo "  -d <desc>          Description"
    echo "  -p <0-4>           Priority (default 2)"
    echo "  -t <type>          Type: task, bug, feature, epic, chore"
    echo "  -a <assignee>      Assignee"
    echo "  -l <labels>        Comma-separated labels"
    echo "  --parent <id>      Parent issue for sub-tasks"
    echo "  --deps <ids>       Dependencies (comma-separated)"
    echo "  --due <date>       Due date (+6h, +1d, tomorrow, 2025-01-15)"
    echo "  --body-file <f>    Read description from file"
    exit 0
fi

exec bd create "$@"
