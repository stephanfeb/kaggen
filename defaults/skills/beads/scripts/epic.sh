#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]] || [[ "$1" == "--help" ]]; then
    echo "Usage: epic.sh status <id>       — Show epic completion"
    echo "       epic.sh close-eligible    — Close completed epics"
    exit 0
fi

exec bd epic "$@"
