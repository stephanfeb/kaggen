#!/usr/bin/env bash
set -euo pipefail

show_help() {
    cat << 'EOF'
Usage: import.sh <file.ics> [--dry-run]

Import iCal (.ics) data into calcurse.

Options:
  --dry-run    Show what would be imported without saving
  --help       Show this help

Examples:
  import.sh calendar.ics
  import.sh holidays.ics --dry-run
EOF
}

if [[ $# -lt 1 ]] || [[ "$1" == "--help" ]] || [[ "$1" == "-h" ]]; then
    show_help
    exit 0
fi

FILE="$1"
shift

DRY_RUN=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        --dry-run) DRY_RUN=true; shift ;;
        *)         shift ;;
    esac
done

if [[ ! -f "$FILE" ]]; then
    echo "Error: file not found: $FILE" >&2
    exit 1
fi

# Check calcurse is installed
if ! command -v calcurse &> /dev/null; then
    echo "Error: calcurse is not installed" >&2
    exit 1
fi

if [[ "$DRY_RUN" == "true" ]]; then
    echo "=== Dry run: would import the following ===" 
    calcurse -i "$FILE" --dump-imported 2>/dev/null || cat "$FILE"
    echo ""
    echo "=== End dry run ==="
else
    calcurse -i "$FILE"
    echo "Imported: $FILE"
fi
