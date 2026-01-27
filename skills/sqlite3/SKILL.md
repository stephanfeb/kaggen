---
name: sqlite3
description: Query and manage SQLite databases (inspect schema, run queries, export data, backup)
---

# SQLite3 — Local Database Management

Use this skill when the user asks to inspect, query, export, or manage SQLite databases. Kaggen's own data (memory, workflows) is stored in SQLite, so this skill is also useful for introspection.

## Available Commands

Run operations via `skill_run` with the scripts below. All scripts accept `--help`.

### query.sh — Run SQL queries

```bash
bash scripts/query.sh <database> <sql> [--format csv|json|table|markdown] [--headers]
```

Defaults to table format with headers. The query is read-only by default. Use `--write` to enable INSERT/UPDATE/DELETE.

Examples:

```bash
# List all tables
bash scripts/query.sh app.db "SELECT name FROM sqlite_master WHERE type='table'"

# Query with JSON output
bash scripts/query.sh app.db "SELECT * FROM users LIMIT 10" --format json

# Export to CSV
bash scripts/query.sh app.db "SELECT * FROM orders WHERE date > '2025-01-01'" --format csv > orders.csv

# Count records
bash scripts/query.sh app.db "SELECT COUNT(*) FROM events"

# Write operation (requires --write flag)
bash scripts/query.sh app.db "UPDATE config SET value='dark' WHERE key='theme'" --write
```

### schema.sh — Inspect database structure

```bash
bash scripts/schema.sh <database> [table_name]
```

Without a table name, lists all tables with row counts. With a table name, shows column definitions, indexes, and foreign keys.

Examples:

```bash
# Overview of all tables
bash scripts/schema.sh ~/.kaggen/memory.db

# Detailed schema for one table
bash scripts/schema.sh ~/.kaggen/memory.db memories

# Show Kaggen's entity graph schema
bash scripts/schema.sh ~/.kaggen/memory.db entities
```

### export.sh — Export tables or queries to files

```bash
bash scripts/export.sh <database> <output_file> [--table <name> | --query <sql>] [--format csv|json|sql]
```

Examples:

```bash
# Export entire table to CSV
bash scripts/export.sh app.db users.csv --table users --format csv

# Export query results to JSON
bash scripts/export.sh app.db results.json --query "SELECT * FROM logs WHERE level='error'" --format json

# Dump table as SQL INSERT statements
bash scripts/export.sh app.db backup.sql --table users --format sql

# Export all tables to SQL dump
bash scripts/export.sh app.db full_backup.sql --format sql
```

### backup.sh — Backup and restore databases

```bash
bash scripts/backup.sh <database> <backup_path>
bash scripts/backup.sh --restore <backup_path> <database>
```

Uses SQLite's online backup API for a consistent snapshot.

Examples:

```bash
# Backup Kaggen's memory database
bash scripts/backup.sh ~/.kaggen/memory.db ~/backups/memory_$(date +%Y%m%d).db

# Restore from backup
bash scripts/backup.sh --restore ~/backups/memory_20250115.db ~/.kaggen/memory.db
```

### import.sh — Import data from CSV

```bash
bash scripts/import.sh <database> <table> <csv_file> [--create] [--separator <char>]
```

Options:
- `--create` — Create the table from CSV headers (all TEXT columns)
- `--separator` — Column separator (default: comma)

Examples:

```bash
# Import CSV into existing table
bash scripts/import.sh app.db contacts contacts.csv

# Create table from CSV and import
bash scripts/import.sh app.db expenses expenses.csv --create

# Import TSV file
bash scripts/import.sh app.db data export.tsv --separator "	"
```

## Kaggen Databases

| Database | Path | Contents |
|----------|------|----------|
| Memory | `~/.kaggen/memory.db` | Memories, entities, entity relations, vector index |
| Sessions | `~/.kaggen/sessions/` | Conversation history (one file per session) |

### Useful Kaggen queries

```sql
-- List all memories with type and confidence
SELECT id, memory_type, confidence, content FROM memories ORDER BY updated_at DESC LIMIT 20;

-- Show entity graph
SELECT e.name, COUNT(me.memory_id) as memory_count
FROM entities e JOIN memory_entities me ON e.id = me.entity_id
GROUP BY e.id ORDER BY memory_count DESC;

-- Find entity relationships
SELECT a.name, b.name, er.weight
FROM entity_relations er
JOIN entities a ON er.entity_a = a.id
JOIN entities b ON er.entity_b = b.id
ORDER BY er.weight DESC LIMIT 20;

-- Show recent observations (synthesized memories)
SELECT content, confidence, updated_at FROM memories
WHERE memory_type = 'observation' ORDER BY updated_at DESC;
```

## Tips

- Always use parameterized queries or proper escaping for user-provided values.
- Use `--format json` when the output will be processed programmatically.
- Use `--format markdown` when presenting results to the user in chat.
- For large exports, stream to a file rather than capturing in memory.
- The `--write` flag on query.sh is a safety mechanism — never enable it without user intent.
- Use `PRAGMA journal_mode=WAL` for databases that are read while being written to.
- SQLite databases can be safely backed up while in use via the backup API.
