#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 2 ]] || [[ "$1" == "--help" ]]; then
    echo "Usage: export.sh <database> <output_file> [--table <name> | --query <sql>] [--format csv|json|sql]"
    echo ""
    echo "Export tables or query results to a file."
    exit 0
fi

DB="$1"
OUTPUT="$2"
shift 2

TABLE=""
QUERY=""
FORMAT="csv"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --table)  TABLE="$2"; shift 2 ;;
        --query)  QUERY="$2"; shift 2 ;;
        --format) FORMAT="$2"; shift 2 ;;
        *)        shift ;;
    esac
done

if [[ ! -f "$DB" ]]; then
    echo "Error: database not found: $DB" >&2
    exit 1
fi

if [[ "$FORMAT" == "sql" ]]; then
    if [[ -n "$TABLE" ]]; then
        echo "Exporting table $TABLE as SQL..."
        sqlite3 "$DB" ".dump $TABLE" > "$OUTPUT"
    else
        echo "Exporting full database as SQL..."
        sqlite3 "$DB" ".dump" > "$OUTPUT"
    fi
else
    if [[ -z "$QUERY" && -n "$TABLE" ]]; then
        QUERY="SELECT * FROM \"$TABLE\""
    fi
    if [[ -z "$QUERY" ]]; then
        echo "Error: specify --table or --query" >&2
        exit 1
    fi

    case "$FORMAT" in
        csv)
            sqlite3 -header -csv "$DB" "$QUERY" > "$OUTPUT"
            ;;
        json)
            sqlite3 -json "$DB" "$QUERY" > "$OUTPUT"
            ;;
        *)
            echo "Error: unknown format: $FORMAT" >&2
            exit 1
            ;;
    esac
fi

SIZE=$(wc -c < "$OUTPUT" | tr -d ' ')
echo "Done. Output: $OUTPUT ($SIZE bytes)"
