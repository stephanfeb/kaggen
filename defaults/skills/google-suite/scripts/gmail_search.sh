#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]] || [[ "$1" == "--help" ]]; then
    echo "Usage: gmail_search.sh <label_name>"
    echo ""
    echo "Search for emails with a specific label."
    exit 0
fi

python3 "$(dirname "$0")/../main.py" gmail_search "$1"
