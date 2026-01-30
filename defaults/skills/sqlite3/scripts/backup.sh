#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 2 ]] || [[ "$1" == "--help" ]]; then
    echo "Usage: backup.sh <database> <backup_path>"
    echo "       backup.sh --restore <backup_path> <database>"
    echo ""
    echo "Backup or restore a SQLite database using the online backup API."
    exit 0
fi

if [[ "$1" == "--restore" ]]; then
    BACKUP="$2"
    TARGET="$3"
    if [[ ! -f "$BACKUP" ]]; then
        echo "Error: backup file not found: $BACKUP" >&2
        exit 1
    fi
    echo "Restoring: $BACKUP -> $TARGET"
    sqlite3 "$TARGET" ".restore $BACKUP"
    echo "Done."
else
    DB="$1"
    BACKUP="$2"
    if [[ ! -f "$DB" ]]; then
        echo "Error: database not found: $DB" >&2
        exit 1
    fi
    echo "Backing up: $DB -> $BACKUP"
    sqlite3 "$DB" ".backup $BACKUP"
    SIZE=$(wc -c < "$BACKUP" | tr -d ' ')
    echo "Done. Backup: $BACKUP ($SIZE bytes)"
fi
