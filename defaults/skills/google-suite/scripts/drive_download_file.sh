#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 2 ]] || [[ "$1" == "--help" ]]; then
    echo "Usage: drive_download_file.sh <file_id> <dest_path>"
    echo ""
    echo "Download a file from Google Drive."
    exit 0
fi

python3 "$(dirname "$0")/../main.py" drive_download_file "$1" "$2"
