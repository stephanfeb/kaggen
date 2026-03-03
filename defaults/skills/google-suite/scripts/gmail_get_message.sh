#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]] || [[ "$1" == "--help" ]]; then
    echo "Usage: gmail_get_message.sh <message_id>"
    echo ""
    echo "Get the content of a specific email."
    exit 0
fi

python3 "$(dirname "$0")/../main.py" gmail_get_message "$1"
