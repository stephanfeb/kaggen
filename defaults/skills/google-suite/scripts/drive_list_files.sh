#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]] || [[ "$1" == "--help" ]]; then
    echo "Usage: drive_list_files.sh <folder_id>"
    echo ""
    echo "List files in a shared folder."
    exit 0
fi

python3 "$(dirname "$0")/../main.py" drive_list_files "$1"
