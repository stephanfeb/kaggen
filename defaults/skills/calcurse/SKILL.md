---
name: calcurse
description: Terminal-based calendar and todo management (query appointments, events, todos, import/export iCal)
---

# Calcurse — Terminal Calendar & Todo Management

Use this skill when the user asks to manage calendar appointments, events, or todo items using calcurse. Calcurse is a terminal-based organizer that stores data as text files, making it ideal for AI-assisted calendar management.

## Available Commands

Run operations via the scripts below. All scripts accept `--help`.

### query.sh — Query appointments, events, and todos

```bash
bash scripts/query.sh [--from <date>] [--to <date>] [--days <num>] [--type <type>] [--search <pattern>] [--limit <num>] [--format json|raw]
```

Query appointments, events, and todos within a date range.

Options:
- `--from <date>` — Start date (default: today). Accepts: YYYY-MM-DD, today, tomorrow, monday, etc.
- `--to <date>` — End date (cannot combine with --days)
- `--days <num>` — Number of days from start (negative for past)
- `--type <type>` — Filter by type: apt, event, todo, cal (apt+event), recur, all
- `--search <pattern>` — Filter by regex pattern in description
- `--limit <num>` — Limit number of results
- `--format json|raw` — Output format (default: human-readable)

Examples:

```bash
# Today's appointments and events
bash scripts/query.sh

# This week's calendar
bash scripts/query.sh --days 7

# Next month
bash scripts/query.sh --from today --days 30

# All todos
bash scripts/query.sh --type todo

# High-priority todos only (priority 1-3)
bash scripts/query.sh --type todo --priority 1,2,3

# Search for meetings
bash scripts/query.sh --days 14 --search "meeting|standup"

# Tomorrow's schedule as JSON
bash scripts/query.sh --from tomorrow --days 1 --format json
```

### add.sh — Add appointments, events, or todos

```bash
bash scripts/add.sh --type <apt|event|todo> --desc <description> [options]
```

Add a new calendar item.

**For appointments (--type apt):**
- `--date <date>` — Date (required)
- `--start <HH:MM>` — Start time (required)
- `--end <HH:MM>` — End time (required)
- `--desc <text>` — Description (required)

**For events (--type event):**
- `--date <date>` — Date (required)
- `--desc <text>` — Description (required)

**For todos (--type todo):**
- `--desc <text>` — Description (required)
- `--priority <1-9>` — Priority (1=highest, 9=lowest, default: 0=none)

Examples:

```bash
# Add an appointment
bash scripts/add.sh --type apt --date 2026-02-01 --start 10:00 --end 11:00 --desc "Team standup"

# Add an all-day event
bash scripts/add.sh --type event --date 2026-02-14 --desc "Valentine's Day"

# Add a high-priority todo
bash scripts/add.sh --type todo --priority 1 --desc "Prepare quarterly report"

# Add Indonesian holiday as event
bash scripts/add.sh --type event --date 2026-03-30 --desc "🇮🇩 Eid al-Fitr (Indonesia) - Servicing team holiday"
```

### todo.sh — Manage todo items

```bash
bash scripts/todo.sh list [--priority <num>] [--completed|--uncompleted]
bash scripts/todo.sh add <description> [--priority <1-9>]
bash scripts/todo.sh complete <pattern>
bash scripts/todo.sh delete <pattern>
```

Specialized todo management.

Examples:

```bash
# List all todos
bash scripts/todo.sh list

# List only high-priority uncompleted todos
bash scripts/todo.sh list --priority 1,2,3 --uncompleted

# Add a todo
bash scripts/todo.sh add "Review PR #123" --priority 2

# Mark todo as complete (by pattern match)
bash scripts/todo.sh complete "Review PR"

# Delete a todo
bash scripts/todo.sh delete "old task"
```

### import.sh — Import iCal data

```bash
bash scripts/import.sh <file.ics> [--dry-run]
```

Import appointments and events from an iCal (.ics) file.

Options:
- `--dry-run` — Show what would be imported without saving

Examples:

```bash
# Import calendar export
bash scripts/import.sh ~/Downloads/calendar.ics

# Preview import without saving
bash scripts/import.sh holidays.ics --dry-run
```

### export.sh — Export calendar data

```bash
bash scripts/export.sh [--from <date>] [--to <date>] [--format ical|pcal] [--output <file>]
```

Export calendar data to iCal or pcal format.

Examples:

```bash
# Export all data to iCal
bash scripts/export.sh --output backup.ics

# Export next 30 days
bash scripts/export.sh --from today --days 30 --output upcoming.ics
```

### recur.sh — Manage recurring items

```bash
bash scripts/recur.sh add --type <apt|event> --desc <text> --date <start> --recur <daily|weekly|monthly|yearly> [--until <date>]
bash scripts/recur.sh list
```

Create and manage recurring appointments and events.

Examples:

```bash
# Weekly team standup
bash scripts/recur.sh add --type apt --desc "Team standup" --date 2026-02-02 --start 09:00 --end 09:30 --recur weekly

# Monthly 1-on-1
bash scripts/recur.sh add --type apt --desc "1-on-1 with Dennis" --date 2026-02-05 --start 14:00 --end 15:00 --recur monthly

# Annual event
bash scripts/recur.sh add --type event --desc "🇮🇩 Independence Day (Indonesia)" --date 2026-08-17 --recur yearly
```

## Data Files

Calcurse stores data in text files:

| File | Path | Contents |
|------|------|----------|
| Appointments | `~/.local/share/calcurse/apts` | Appointments and events |
| Todos | `~/.local/share/calcurse/todo` | Todo items |
| Notes | `~/.local/share/calcurse/notes/` | Notes attached to items |
| Config | `~/.config/calcurse/conf` | User configuration |

Alternative: `~/.calcurse/` if it exists (legacy location).

## Date Formats

Calcurse accepts these date formats:
- ISO format: `2026-01-28` or `2026/01/28`
- Relative: `today`, `tomorrow`, `yesterday`
- Weekdays: `monday`, `tuesday`, etc. (next occurrence)

## Tips for Team Management

- **Indonesian holidays**: Add as all-day events with 🇮🇩 emoji prefix for visibility
- **1-on-1s**: Use recurring appointments with team member names
- **Reminders**: Calcurse daemon can send notifications (run `calcurse --daemon`)
- **Sync**: Export to iCal for sharing or syncing with other calendars
- **Backup**: The apts and todo files are plain text — easy to backup and version control

## Integration Ideas

```bash
# Morning briefing: today's schedule
bash scripts/query.sh --days 1

# Weekly planning: next 7 days
bash scripts/query.sh --days 7

# Check Indonesian team availability (search for Indonesia events)
bash scripts/query.sh --days 30 --search "Indonesia|🇮🇩"

# Export for sharing with team
bash scripts/export.sh --days 30 --output team-calendar.ics
```
