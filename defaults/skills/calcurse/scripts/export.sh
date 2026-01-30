#!/usr/bin/env bash
set -euo pipefail

show_help() {
    cat << 'EOF'
Usage: export.sh [options]

Export calcurse data to iCal or pcal format.

Options:
  --from <date>      Start date for export range
  --to <date>        End date for export range
  --days <num>       Number of days from start
  --format <fmt>     Output format: ical (default), pcal
  --output <file>    Output file (default: stdout)
  --help             Show this help

Examples:
  export.sh --output backup.ics
  export.sh --from today --days 30 --output upcoming.ics
  export.sh --format pcal > calendar.pcal
EOF
}

FROM_DATE=""
TO_DATE=""
DAYS=""
FORMAT="ical"
OUTPUT=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --from)    FROM_DATE="$2"; shift 2 ;;
        --to)      TO_DATE="$2"; shift 2 ;;
        --days)    DAYS="$2"; shift 2 ;;
        --format)  FORMAT="$2"; shift 2 ;;
        --output)  OUTPUT="$2"; shift 2 ;;
        --help|-h) show_help; exit 0 ;;
        *)         shift ;;
    esac
done

# Check calcurse is installed
if ! command -v calcurse &> /dev/null; then
    echo "Error: calcurse is not installed" >&2
    exit 1
fi

CALC_ARGS=(-x "$FORMAT")

if [[ -n "$FROM_DATE" ]]; then
    CALC_ARGS+=(--from "$FROM_DATE")
fi

if [[ -n "$TO_DATE" ]]; then
    CALC_ARGS+=(--to "$TO_DATE")
elif [[ -n "$DAYS" ]]; then
    CALC_ARGS+=(--days "$DAYS")
fi

if [[ -n "$OUTPUT" ]]; then
    calcurse "${CALC_ARGS[@]}" > "$OUTPUT"
    echo "Exported to: $OUTPUT"
else
    calcurse "${CALC_ARGS[@]}"
fi
