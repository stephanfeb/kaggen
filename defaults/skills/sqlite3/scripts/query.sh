#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 2 ]] || [[ "$1" == "--help" ]]; then
    echo "Usage: query.sh <database> <sql> [--format csv|json|table|markdown] [--headers] [--write]"
    echo ""
    echo "Run a SQL query against a SQLite database."
    echo "Read-only by default. Use --write for INSERT/UPDATE/DELETE."
    exit 0
fi

DB="$1"
SQL="$2"
shift 2

FORMAT="table"
HEADERS=true
WRITE=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        --format)  FORMAT="$2"; shift 2 ;;
        --headers) HEADERS=true; shift ;;
        --write)   WRITE=true; shift ;;
        *)         shift ;;
    esac
done

if [[ ! -f "$DB" ]]; then
    echo "Error: database not found: $DB" >&2
    exit 1
fi

# Block writes unless --write flag is set
if [[ "$WRITE" == "false" ]]; then
    SQL_UPPER=$(echo "$SQL" | tr '[:lower:]' '[:upper:]')
    if [[ "$SQL_UPPER" =~ ^[[:space:]]*(INSERT|UPDATE|DELETE|DROP|ALTER|CREATE|REPLACE) ]]; then
        echo "Error: write operation detected. Use --write flag to enable." >&2
        exit 1
    fi
fi

SQLITE_ARGS=()
case "$FORMAT" in
    csv)
        SQLITE_ARGS+=(-csv)
        [[ "$HEADERS" == true ]] && SQLITE_ARGS+=(-header)
        ;;
    json)
        SQLITE_ARGS+=(-json)
        ;;
    table)
        SQLITE_ARGS+=(-column)
        [[ "$HEADERS" == true ]] && SQLITE_ARGS+=(-header)
        ;;
    markdown)
        SQLITE_ARGS+=(-markdown)
        [[ "$HEADERS" == true ]] && SQLITE_ARGS+=(-header)
        ;;
    *)
        echo "Error: unknown format: $FORMAT (use csv, json, table, markdown)" >&2
        exit 1
        ;;
esac

sqlite3 "${SQLITE_ARGS[@]}" "$DB" "$SQL"
