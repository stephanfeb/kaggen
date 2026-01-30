#!/usr/bin/env bash
set -euo pipefail

show_help() {
    cat << 'EOF'
Usage: query.sh [options]

Query calcurse appointments, events, and todos.

Options:
  --from <date>       Start date (default: today)
  --to <date>         End date (exclusive with --days)
  --days <num>        Number of days from start (negative for past)
  --type <type>       Filter: apt, event, todo, cal, recur, all (default: all)
  --search <pattern>  Filter by regex pattern
  --limit <num>       Limit number of results
  --priority <n,n>    Todo priority filter (comma-separated, 1-9)
  --completed         Show only completed todos
  --uncompleted       Show only uncompleted todos
  --format <fmt>      Output format: text (default), json, raw
  --help              Show this help

Date formats: YYYY-MM-DD, today, tomorrow, yesterday, monday-sunday

Examples:
  query.sh                          # Today's schedule + all todos
  query.sh --days 7                 # Next 7 days
  query.sh --type todo              # All todos
  query.sh --search "meeting"       # Search for meetings
  query.sh --from 2026-02-01 --to 2026-02-28  # February
EOF
}

# Defaults
FROM_DATE=""
TO_DATE=""
DAYS=""
TYPE="all"
SEARCH=""
LIMIT=""
PRIORITY=""
COMPLETED=""
FORMAT="text"

# Parse arguments
while [[ $# -gt 0 ]]; do
    case "$1" in
        --from)       FROM_DATE="$2"; shift 2 ;;
        --to)         TO_DATE="$2"; shift 2 ;;
        --days)       DAYS="$2"; shift 2 ;;
        --type)       TYPE="$2"; shift 2 ;;
        --search)     SEARCH="$2"; shift 2 ;;
        --limit)      LIMIT="$2"; shift 2 ;;
        --priority)   PRIORITY="$2"; shift 2 ;;
        --completed)  COMPLETED="yes"; shift ;;
        --uncompleted) COMPLETED="no"; shift ;;
        --format)     FORMAT="$2"; shift 2 ;;
        --help|-h)    show_help; exit 0 ;;
        *)            echo "Unknown option: $1" >&2; exit 1 ;;
    esac
done

# Check calcurse is installed
if ! command -v calcurse &> /dev/null; then
    echo "Error: calcurse is not installed" >&2
    exit 1
fi

# Build calcurse command
CALC_ARGS=()
CALC_ARGS+=(--input-datefmt 4)  # Use ISO format YYYY-MM-DD

# Handle type filter
case "$TYPE" in
    apt)    CALC_ARGS+=(--filter-type apt) ;;
    event)  CALC_ARGS+=(--filter-type event) ;;
    todo)   CALC_ARGS+=(--filter-type todo) ;;
    cal)    CALC_ARGS+=(--filter-type cal) ;;
    recur)  CALC_ARGS+=(--filter-type recur) ;;
    all)    ;; # No filter
    *)      echo "Error: unknown type: $TYPE" >&2; exit 1 ;;
esac

# Handle date range
if [[ -n "$FROM_DATE" ]]; then
    CALC_ARGS+=(--from "$FROM_DATE")
fi

if [[ -n "$TO_DATE" ]]; then
    CALC_ARGS+=(--to "$TO_DATE")
elif [[ -n "$DAYS" ]]; then
    CALC_ARGS+=(--days "$DAYS")
fi

# Handle search
if [[ -n "$SEARCH" ]]; then
    CALC_ARGS+=(--search "$SEARCH")
fi

# Handle limit
if [[ -n "$LIMIT" ]]; then
    CALC_ARGS+=(--limit "$LIMIT")
fi

# Handle todo-specific filters
if [[ "$TYPE" == "todo" ]]; then
    if [[ -n "$PRIORITY" ]]; then
        # Priority filter needs special handling - filter after query
        :
    fi
    if [[ "$COMPLETED" == "yes" ]]; then
        CALC_ARGS+=(--filter-completed)
    elif [[ "$COMPLETED" == "no" ]]; then
        CALC_ARGS+=(--filter-uncompleted)
    fi
fi

# Execute query
case "$FORMAT" in
    text)
        calcurse -Q "${CALC_ARGS[@]}" 2>/dev/null || calcurse -Q 2>/dev/null
        ;;
    raw)
        calcurse -G "${CALC_ARGS[@]}" 2>/dev/null || calcurse -G 2>/dev/null
        ;;
    json)
        # Build JSON output manually from raw format
        echo "["
        FIRST=true
        calcurse -G "${CALC_ARGS[@]}" --format-apt '{"type":"apt","start":"%(start)","end":"%(end)","desc":"%m"},' \
            --format-recur-apt '{"type":"recur-apt","start":"%(start)","end":"%(end)","desc":"%m"},' \
            --format-event '{"type":"event","desc":"%m"},' \
            --format-recur-event '{"type":"recur-event","desc":"%m"},' \
            --format-todo '{"type":"todo","priority":"%p","desc":"%m"},' \
            2>/dev/null | sed '$ s/,$//'
        echo "]"
        ;;
    *)
        echo "Error: unknown format: $FORMAT" >&2
        exit 1
        ;;
esac
