#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 3 ]] || [[ "$1" == "--help" ]]; then
    echo "Usage: import.sh <database> <table> <csv_file> [--create] [--separator <char>]"
    echo ""
    echo "Import CSV data into a SQLite table."
    echo "  --create     Create the table from CSV headers (all TEXT columns)"
    echo "  --separator  Column separator (default: comma)"
    exit 0
fi

DB="$1"
TABLE="$2"
CSV="$3"
shift 3

CREATE=false
SEP=","

while [[ $# -gt 0 ]]; do
    case "$1" in
        --create)    CREATE=true; shift ;;
        --separator) SEP="$2"; shift 2 ;;
        *)           shift ;;
    esac
done

if [[ ! -f "$CSV" ]]; then
    echo "Error: CSV file not found: $CSV" >&2
    exit 1
fi

if [[ "$CREATE" == true ]]; then
    # Read header line and create table with TEXT columns
    HEADER=$(head -1 "$CSV")
    IFS="$SEP" read -ra COLS <<< "$HEADER"
    COL_DEFS=""
    for COL in "${COLS[@]}"; do
        COL=$(echo "$COL" | tr -d '"' | xargs)
        [[ -n "$COL_DEFS" ]] && COL_DEFS+=", "
        COL_DEFS+="\"$COL\" TEXT"
    done
    echo "Creating table: $TABLE ($COL_DEFS)"
    sqlite3 "$DB" "CREATE TABLE IF NOT EXISTS \"$TABLE\" ($COL_DEFS);"
fi

echo "Importing: $CSV -> $TABLE"
sqlite3 "$DB" <<EOF
.mode csv
.separator "$SEP"
.import --skip 1 $CSV $TABLE
EOF

COUNT=$(sqlite3 "$DB" "SELECT COUNT(*) FROM \"$TABLE\";")
echo "Done. Table $TABLE now has $COUNT rows."
