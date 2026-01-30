---
name: beads
description: Git-backed issue tracking with dependency graphs using the bd CLI (create, list, update, close, search tasks)
---

# Beads — Git-Backed Issue Tracker

Use this skill when the user asks to manage issues, tasks, bugs, or epics using the `bd` (beads) CLI. Beads stores issues as JSONL files in a `.beads/` directory, versioned through git, with first-class dependency support.

## Available Commands

All scripts wrap the `bd` CLI at `/Users/stephanfeb/.local/bin/bd`. Run any script with `--help` for usage.

### ready.sh — Show tasks ready for work

```bash
bash scripts/ready.sh [options...]
```

Shows unblocked issues ready to be worked on.

Options:
- `--assignee <name>` — Filter by assignee
- `--priority <n>` — Filter by priority (0=highest, 4=lowest)
- `--limit <n>` — Max issues to show (default 10)
- `--label <label>` — Filter by label
- `--unassigned` — Show only unassigned issues
- `--pretty` — Tree format with status symbols
- `--json` — JSON output

Examples:

```bash
bash scripts/ready.sh
bash scripts/ready.sh --priority 0 --limit 5
bash scripts/ready.sh --unassigned --pretty
```

### create.sh — Create a new issue

```bash
bash scripts/create.sh <title> [options...]
```

Options:
- `-d <desc>` — Description
- `-p <0-4>` — Priority (default 2)
- `-t <type>` — Type: task, bug, feature, epic, chore
- `-a <assignee>` — Assignee
- `-l <labels>` — Comma-separated labels
- `--parent <id>` — Parent issue (for sub-tasks)
- `--deps <ids>` — Dependencies (comma-separated)
- `--due <date>` — Due date (+6h, +1d, tomorrow, 2025-01-15)
- `--body-file <file>` — Read description from file

Examples:

```bash
bash scripts/create.sh "Fix login timeout" -p 1 -t bug -l backend,auth
bash scripts/create.sh "Refactor DB layer" -d "Migrate from raw SQL to ORM" --parent bd-a3f8
bash scripts/create.sh "Add user export" -t feature --due "+2w"
```

### show.sh — Show issue details

```bash
bash scripts/show.sh <id> [options...]
```

Options:
- `--json` — JSON output
- `--refs` — Show issues referencing this one
- `--short` — Compact one-line output

Examples:

```bash
bash scripts/show.sh bd-a1b2
bash scripts/show.sh bd-a1b2 --json
```

### list.sh — List issues with filters

```bash
bash scripts/list.sh [options...]
```

Options:
- `-s <status>` — Filter: open, in_progress, blocked, closed
- `-p <priority>` — Filter by priority
- `-t <type>` — Filter by type
- `-a <assignee>` — Filter by assignee
- `-l <labels>` — Filter by labels
- `--all` — Include closed issues
- `--limit <n>` — Max results (default 50)
- `--sort <field>` — Sort: priority, created, updated, status, title
- `--pretty` — Tree format with status symbols
- `--long` — Detailed multi-line output
- `--json` — JSON output

Examples:

```bash
bash scripts/list.sh -s open -p 0
bash scripts/list.sh -t bug --sort priority --pretty
bash scripts/list.sh -a alice --all --limit 20
```

### update.sh — Update an issue

```bash
bash scripts/update.sh <id> [options...]
```

Options:
- `--title <text>` — New title
- `-d <desc>` — New description
- `-s <status>` — New status (open, in_progress, blocked, closed)
- `-p <0-4>` — New priority
- `-a <assignee>` — New assignee
- `--add-label <label>` — Add a label
- `--remove-label <label>` — Remove a label
- `--claim` — Atomically claim (set assignee to you + in_progress)
- `--due <date>` — Set due date
- `--notes <text>` — Additional notes

Examples:

```bash
bash scripts/update.sh bd-a1b2 -s in_progress -a steve
bash scripts/update.sh bd-a1b2 --claim
bash scripts/update.sh bd-a1b2 -p 0 --add-label urgent
```

### close.sh — Close issues

```bash
bash scripts/close.sh [id] [options...]
```

If no ID is given, closes the last touched issue.

Options:
- `-r <reason>` — Reason for closing
- `--suggest-next` — Show newly unblocked issues after closing

Examples:

```bash
bash scripts/close.sh bd-a1b2 -r "Fixed in commit abc123"
bash scripts/close.sh --suggest-next
```

### search.sh — Search issues

```bash
bash scripts/search.sh <query> [options...]
```

Searches across title, description, and ID.

Options:
- `-s <status>` — Filter by status
- `-t <type>` — Filter by type
- `-a <assignee>` — Filter by assignee
- `-l <labels>` — Filter by labels
- `--limit <n>` — Max results
- `--sort <field>` — Sort: priority, created, updated
- `--long` — Detailed output
- `--json` — JSON output

Examples:

```bash
bash scripts/search.sh "authentication"
bash scripts/search.sh "login" -s open -t bug
bash scripts/search.sh "database" --sort priority --limit 10
```

### dep.sh — Manage dependencies

```bash
bash scripts/dep.sh <action> <args...>
```

Actions:
- `add <child> <parent>` — child depends on parent
- `remove <child> <parent>` — Remove dependency
- `list <id>` — List dependencies of an issue
- `tree <id>` — Show dependency tree

Examples:

```bash
bash scripts/dep.sh add bd-abc bd-xyz
bash scripts/dep.sh tree bd-a1b2
bash scripts/dep.sh list bd-abc
```

### status.sh — Database overview

```bash
bash scripts/status.sh [options...]
```

Shows issue counts by state, ready work, and recent activity.

Options:
- `--assigned` — Show only issues assigned to current user
- `--no-activity` — Skip git activity (faster)
- `--json` — JSON output

### comments.sh — View and add comments

```bash
bash scripts/comments.sh <id>
bash scripts/comments.sh add <id> <text>
```

Examples:

```bash
bash scripts/comments.sh bd-a1b2
bash scripts/comments.sh add bd-a1b2 "Needs design review before implementation"
```

### epic.sh — Epic management

```bash
bash scripts/epic.sh status <id>
bash scripts/epic.sh close-eligible
```

Shows epic completion status or closes epics where all children are done.

### graph.sh — Dependency visualization

```bash
bash scripts/graph.sh <id>
```

Shows ASCII dependency graph. Left-to-right execution order; same column = parallel.

## Tips

- Use `bd ready` as your starting point — it shows what can be worked on now
- Priority 0 is highest (critical), 4 is lowest
- Use `--claim` on update to atomically assign yourself and set in_progress
- Use `--suggest-next` when closing to see what gets unblocked
- Add `--json` to any command for machine-readable output
- Issues are stored in `.beads/` and sync through git — no external server needed
- Use `--pretty` on list/ready for a visual tree view
