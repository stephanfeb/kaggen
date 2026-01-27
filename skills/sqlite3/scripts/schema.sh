#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]] || [[ "$1" == "--help" ]]; then
    echo "Usage: schema.sh <database> [table_name]"
    echo ""
    echo "Without table_name: list all tables with row counts."
    echo "With table_name: show columns, indexes, and foreign keys."
    exit 0
fi

DB="$1"
TABLE="${2:-}"

if [[ ! -f "$DB" ]]; then
    echo "Error: database not found: $DB" >&2
    exit 1
fi

if [[ -z "$TABLE" ]]; then
    echo "=== Tables ==="
    TABLES=$(sqlite3 "$DB" "SELECT name FROM sqlite_master WHERE type='table' ORDER BY name;")
    for T in $TABLES; do
        COUNT=$(sqlite3 "$DB" "SELECT COUNT(*) FROM \"$T\";")
        printf "%-30s %s rows\n" "$T" "$COUNT"
    done
else
    echo "=== Table: $TABLE ==="
    echo ""
    echo "--- Columns ---"
    sqlite3 -header -column "$DB" "PRAGMA table_info('$TABLE');"
    echo ""
    echo "--- Indexes ---"
    sqlite3 -header -column "$DB" "PRAGMA index_list('$TABLE');"
    echo ""
    echo "--- Foreign Keys ---"
    sqlite3 -header -column "$DB" "PRAGMA foreign_key_list('$TABLE');"
    echo ""
    echo "--- CREATE Statement ---"
    sqlite3 "$DB" "SELECT sql FROM sqlite_master WHERE name='$TABLE';"
fi
