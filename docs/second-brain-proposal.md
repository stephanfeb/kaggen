# Proposal: Building a Second Brain with Kaggen + Obsidian

## Overview

This proposal describes how Kaggen can implement the Building a Second Brain (BASB) methodology by combining Obsidian as the primary knowledge UI with Telegram as a mobile capture and notification channel. A custom Obsidian plugin bridges the two, connecting to the existing Kaggen gateway over WebSocket.

The result is an intelligent, proactive second brain — not just a notes app, but a system that captures, organizes, distills, and surfaces knowledge on your behalf.

---

## Architecture

```
┌─────────────┐     ┌──────────────────┐     ┌─────────────┐
│  Telegram    │────>│                  │────>│  Obsidian   │
│  (mobile     │<────│  Kaggen Agent    │<────│  (desktop   │
│   capture)   │     │  (gateway)       │     │   brain UI) │
└─────────────┘     └──────────────────┘     └─────────────┘
                           │                        │
                           v                        v
                    ┌──────────────┐          ┌───────────┐
                    │ Vector Index │          │ Vault (fs) │
                    │ + SQLite     │          │ markdown   │
                    └──────────────┘          └───────────┘
```

- **Obsidian** is the full second brain UI: browsing, editing, reviewing, and receiving proactive suggestions.
- **Telegram** is the mobile capture and notification layer: quick saves, voice notes, photo captures, and agent-pushed alerts.
- **Kaggen agent** is the intelligence layer: organizing, summarizing, linking, surfacing.
- **Obsidian vault** (a folder of markdown files) is the single source of truth.

Both Obsidian and Telegram connect to the same Kaggen gateway as channels. The agent operates on the vault filesystem directly.

---

## PARA Vault Structure

The vault follows the PARA organizational method:

```
vault/
├── 0-Inbox/           # Unprocessed captures land here
├── 1-Projects/        # Active work with a deadline or goal
│   ├── ProjectName/
│   │   └── notes...
├── 2-Areas/           # Ongoing responsibilities (no end date)
│   ├── Health/
│   ├── Finance/
├── 3-Resources/       # Reference material by topic
│   ├── Programming/
│   ├── Cooking/
└── 4-Archive/         # Completed or inactive items
```

All captures enter through `0-Inbox/`. The agent assists in filing them into the appropriate PARA category during inbox processing.

---

## Capture

### Design Principle

Not everything becomes a note. Capture is selective. The system supports explicit capture commands and light agent judgment, but casual conversation stays out of the brain.

### Capture Flows

| Source | Mechanism | Destination |
|--------|-----------|-------------|
| Telegram text | `/save` command or forward to bot | `0-Inbox/` as `.md` |
| Telegram photo | Upload with caption | `0-Inbox/` with embedded image |
| Telegram voice | Voice message | Transcribed to `0-Inbox/` as `.md` |
| Web article | `/clip <URL>` from Telegram or Obsidian | `0-Inbox/` as distilled `.md` |
| In Obsidian | Write directly — file watcher indexes it | Wherever user places it |
| Obsidian highlight | Select text, "Capture to inbox" command | `0-Inbox/` as excerpt with backlink |

### Capture Behavior

- **Explicit commands** (`/save`, `/clip`, forwarded messages) go straight to inbox.
- **Agent judgment** (light): during normal conversation, if the agent detects something worth keeping (a decision, reference, insight), it saves to inbox and mentions it: "Noted that in your inbox."
- **What is NOT captured**: casual chat, greetings, operational commands ("rotate this image"), duplicates of existing notes.

### Capture Format

Each captured note includes YAML frontmatter:

```yaml
---
captured: 2025-06-15T10:30:00Z
source: telegram
tags: []
status: inbox
---
```

---

## Organize

### Inbox Processing

The `inbox-process` skill reviews `0-Inbox/` and for each item:

1. Suggests a PARA category and subfolder
2. Suggests tags based on content analysis
3. Identifies related existing notes
4. Waits for user confirmation (via Telegram prompt or Obsidian command)
5. Moves the file and updates frontmatter

### Auto-tagging

On note creation or save, the agent analyzes content and suggests frontmatter tags. In Obsidian, these appear as a non-intrusive suggestion. Via Telegram, the agent mentions them during inbox review.

### Link Discovery

The agent identifies connections between notes and suggests `[[wikilinks]]`. This runs:
- On new note creation
- During inbox processing
- On demand via "Find related notes" command

---

## Distill (Progressive Summarization)

Progressive summarization is applied in layers, using standard Obsidian markdown:

| Layer | What | Obsidian Syntax |
|-------|------|-----------------|
| 1 | Full captured text | The note itself |
| 2 | Bold key passages | `**bolded text**` |
| 3 | Highlighted core ideas | `==highlighted text==` |
| 4 | Executive summary | Callout block at top of note |

### Layer 4 Example

```markdown
> [!summary] Agent Summary
> This article argues that spaced repetition works best when combined
> with active recall. Key finding: 3-day intervals outperform daily review
> for long-term retention.
```

### When Distillation Happens

- **On demand**: user triggers "Summarize this note" from Obsidian or Telegram
- **On repeated access**: if a note is opened/referenced multiple times without summarization, the agent offers to distill it
- **During weekly review**: agent identifies frequently accessed but unsummarized notes

---

## Express (Intermediate Packets)

The agent helps compose outputs from collected notes:

- **Draft**: select several notes, agent composes a document pulling from them
- **Outline**: agent generates a structured outline from materials on a topic
- **Export**: render to PDF, HTML, or other formats (using the existing pandoc skill)

These operations work from both Obsidian (command palette) and Telegram (e.g., "draft a summary of my notes on X").

---

## Proactive Surfacing

This is the core differentiator — the brain actively works for you.

### Daily Briefing

On vault open (or at a scheduled time via Telegram), the agent generates a daily note:

- Active projects and their status
- Items in inbox awaiting processing
- Notes scheduled for review (spaced recall)
- Any calendar/deadline awareness (if integrated)

### Weekly Review

A scheduled proactive job generates a weekly review note and pushes a summary to Telegram:

- New captures this week
- Project progress
- Stale inbox items (>7 days unprocessed)
- Notes that were accessed but not distilled
- Suggestions for archival (completed projects, unused resources)

### Spaced Recall

The agent tracks note access patterns and surfaces notes at increasing intervals for review:

- New note: surface after 1 day, 3 days, 7 days, 14 days, 30 days
- Each review resets the interval
- Surfaced via Telegram notification or Obsidian inbox pane
- User can dismiss, snooze, or mark as "known"

Tracking is stored in SQLite alongside the vector index.

### Contextual Suggestions

While writing in Obsidian, the agent monitors the active note and surfaces related notes in a sidebar panel. This runs on a debounced timer (e.g., 2 seconds after typing stops) to avoid distraction.

---

## Obsidian Plugin

### Purpose

A custom Obsidian plugin that connects to the Kaggen gateway via WebSocket, providing an in-editor agent experience and proactive surfacing UI.

### Structure

```
kaggen-obsidian-plugin/
├── main.ts              # Plugin entry, WebSocket connection to gateway
├── views/
│   ├── InboxView.ts     # Sidebar: unprocessed captures, filing UI
│   ├── ChatView.ts      # Agent chat panel (in-editor)
│   └── BriefingView.ts  # Daily/weekly synthesis display
├── commands/
│   ├── capture.ts       # "Save to inbox", "Clip URL"
│   ├── organize.ts      # "File to PARA", "Suggest tags"
│   ├── distill.ts       # "Summarize", "Extract insights"
│   └── search.ts        # "Find related notes", semantic search
├── handlers/
│   ├── fileWatcher.ts   # React to note create/edit/move events
│   └── notification.ts  # Display agent-pushed alerts/banners
└── manifest.json
```

### Key Features

| Feature | Description |
|---------|-------------|
| Chat panel | Conversational agent access inside Obsidian |
| Command palette | "Kaggen: Summarize", "Kaggen: File to PARA", etc. |
| Context menu | Right-click text, "Ask Kaggen about this" |
| Inbox sidebar | Visual list of unprocessed captures with filing actions |
| Briefing view | Daily/weekly synthesis rendered in a pane |
| Notification banner | Agent-pushed alerts (review reminders, suggestions) |
| File watcher | Auto-index new/changed notes for vector search |
| Contextual sidebar | Related notes surfaced while writing |

### Connection Model

The plugin connects to the Kaggen gateway as a channel, identical to how Telegram connects:

- Opens a WebSocket to `ws://localhost:<port>/ws`
- Authenticates with a local token
- Sends user messages (commands, queries, note context)
- Receives agent responses (text, file operations, suggestions)
- Receives proactive pushes (review prompts, briefing updates)

This means the agent logic is shared — the same skills, same memory, same brain — regardless of whether the user interacts via Obsidian or Telegram.

---

## Kaggen Skills

### New Skills

| Skill | Description |
|-------|-------------|
| `inbox-process` | Review `0-Inbox/`, suggest PARA placement, move files |
| `summarize` | Progressive summarization (layers 2-4) on a note |
| `link-notes` | Find and insert `[[backlinks]]` to related notes |
| `web-clip` | Fetch URL, extract readable content, save to inbox |
| `voice-note` | Transcribe audio (whisper), save to inbox |
| `daily-briefing` | Generate daily synthesis note |
| `weekly-review` | Generate weekly review note, push to Telegram |
| `search-brain` | Semantic search across vault (vector index) |
| `tag-suggest` | Analyze content, suggest frontmatter tags |
| `para-move` | Move note between PARA folders, update links |
| `spaced-recall` | Track and schedule note review intervals |
| `screenshot-ocr` | Extract text from images, save to inbox |

### Existing Skills (reused)

| Skill | Use |
|-------|-----|
| `pandoc` | Export notes to PDF/HTML/DOCX |
| `imagemagick` | Process captured screenshots/images |
| `sqlite3` | Query spaced recall schedules, metadata |

---

## Build Phases

### Phase 1: Vault Foundation

- PARA folder structure setup
- `inbox-process` skill (suggest category, move files)
- `tag-suggest` skill
- `para-move` skill
- Capture via Telegram (`/save`, `/clip`)
- File watcher integration with vector index

### Phase 2: Obsidian Plugin (Minimal)

- WebSocket connection to gateway
- Chat panel (send messages, receive responses)
- Command palette integration (summarize, file, search)
- Basic file watcher (index on save)

### Phase 3: Capture Expansion

- `web-clip` skill (URL to readable markdown)
- `voice-note` skill (transcription)
- `screenshot-ocr` skill
- Agent judgment capture during conversation
- Capture from Obsidian (highlight to inbox)

### Phase 4: Distillation

- `summarize` skill (progressive layers)
- `link-notes` skill (backlink discovery)
- `search-brain` skill (semantic search from Obsidian + Telegram)

### Phase 5: Proactive Surfacing

- `daily-briefing` skill + proactive job
- `weekly-review` skill + proactive job
- `spaced-recall` skill + tracking in SQLite
- Obsidian notification banners and briefing view

### Phase 6: Polish

- Obsidian inbox sidebar view
- Contextual related-notes sidebar
- Express workflows (draft, outline, export)
- Onboarding flow for new users

---

## Open Questions

1. **Vault location**: Should the vault be the Kaggen workspace itself, or a separate Obsidian vault that Kaggen accesses? A separate vault is cleaner but means two directories to manage.

2. **Conflict resolution**: If the user manually moves a note in Obsidian while the agent is also organizing, how do we handle conflicts? Likely: agent never moves without confirmation, file watcher detects external changes.

3. **Multi-device sync**: Obsidian Sync or git-based sync for the vault? This affects whether the agent can assume it always has the latest state.

4. **Voice transcription**: Use local Whisper (privacy, offline) or cloud API (accuracy, speed)? Could be configurable.

5. **Plugin distribution**: Obsidian community plugin store (requires review) or manual install? Manual is faster for iteration; community store for eventual public release.
