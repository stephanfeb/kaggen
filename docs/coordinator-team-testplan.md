# Coordinator Team Architecture Test Plan

This document contains manual test scenarios for validating the coordinator team architecture, including async task dispatch, sub-agent delegation, backlog management, and concurrent message processing.

## Prerequisites

- Gateway running with Gemini or Anthropic model configured
- Telegram channel connected
- Skills loaded: pandoc, imagemagick, calcurse, sqlite3

## Test Scenarios

---

### Scenario 1: Parallel Sub-Agent Dispatch

**Tests:** Async dispatch, multiple sub-agents, completion callbacks

**Input:**
```
Create three things for me simultaneously:
1. A markdown file called "weekly_report.md" with a template for a weekly status report
2. Convert it to PDF using pandoc
3. Create a 400x300 banner image with ImageMagick that says "Weekly Report"

Work on all three in parallel and notify me when each completes.
```

**Expected Behavior:**
- Coordinator creates the markdown file directly
- Coordinator dispatches pandoc sub-agent for PDF conversion
- Coordinator dispatches imagemagick sub-agent for banner creation
- User receives completion notifications as each task finishes

**Success Indicators:**
- Logs show `dispatched async task` for both pandoc and imagemagick
- Files are created in workspace directory
- User receives status updates

---

### Scenario 2: Backlog + Deferred Execution

**Tests:** Backlog tools, priority ordering, task tracking

**Input (Message 1):**
```
Add these tasks to my backlog:
- HIGH priority: Research competitor pricing strategies
- MEDIUM priority: Draft Q1 marketing plan outline
- LOW priority: Clean up old project files

Then show me the backlog sorted by priority.
```

**Input (Message 2 - send while Message 1 is processing):**
```
What's the current weather like?
```

**Expected Behavior:**
- Three backlog items created with correct priorities
- Backlog list returned sorted by priority (HIGH first)
- Weather question answered quickly, not blocked by Message 1

**Success Indicators:**
- `backlog_add` tool called 3 times
- `backlog_list` returns items in priority order
- Both messages get responses without significant delay between them

---

### Scenario 3: Multi-Step Document Pipeline

**Tests:** Sequential dependencies, file operations, multiple sub-agents

**Input:**
```
I need you to:
1. Create a file called "meeting_agenda.md" with a sample meeting agenda
2. After that's done, convert it to PDF
3. Then create a 100x100 thumbnail placeholder for it

Execute these in order and track progress in the backlog.
```

**Expected Behavior:**
- Coordinator creates markdown file first
- Waits for completion, then dispatches pandoc for PDF
- Waits for PDF, then dispatches imagemagick for thumbnail
- Each step tracked in backlog with status updates

**Success Indicators:**
- Tasks execute sequentially (not all dispatched at once)
- Backlog shows progression: pending → in_progress → completed
- Final response summarizes all completed tasks

---

### Scenario 4: Stress Test - Rapid Fire Messages

**Tests:** Concurrent message processing, no blocking

**Input:** Send these 4 messages in quick succession (within 5 seconds):

1. `What is 2+2?`
2. `Name 3 colors`
3. `Create a file called test.txt with "hello world"`
4. `What year is it?`

**Expected Behavior:**
- All messages processed concurrently
- Responses arrive in approximately the order they complete (not necessarily input order)
- No message blocks another

**Success Indicators:**
- Logs show all 4 `handling message` entries within seconds of each other
- All 4 responses received within ~30 seconds total
- Simple questions (2+2, colors, year) respond faster than file creation

---

### Scenario 5: Mixed Sync/Async Workload

**Tests:** Coordinator handles simple queries while async tasks run

**Input (Message 1):**
```
Start a background task: Create a detailed markdown document about "Best Practices for Code Reviews" - make it comprehensive with at least 5 sections.

While that's running, I'll ask you some quick questions.
```

**Input (Message 2 - send immediately after):**
```
What's the capital of France?
```

**Input (Message 3 - send immediately after):**
```
How do you make scrambled eggs?
```

**Expected Behavior:**
- Document creation runs in background (async dispatch or direct)
- France capital question answered quickly
- Scrambled eggs question answered quickly
- Eventually, document completion notification arrives

**Success Indicators:**
- Messages 2 and 3 get fast responses (< 10 seconds)
- Message 1's task continues in background
- No "context deadline exceeded" errors blocking quick responses

---

### Scenario 6: Calendar Integration

**Tests:** Calcurse sub-agent

**Input:**
```
Add a meeting to my calendar for tomorrow at 2pm called "Project Sync" for 1 hour.
```

**Expected Behavior:**
- Coordinator delegates to calcurse sub-agent
- Calendar entry created
- Confirmation returned to user

**Success Indicators:**
- Logs show calcurse agent invoked
- No tool schema errors from Gemini
- Calendar entry verifiable in calcurse

---

### Scenario 7: Error Recovery and Graceful Degradation

**Tests:** Error handling, fallback instructions

**Input:**
```
Convert the file "nonexistent.md" to PDF using pandoc.
```

**Expected Behavior:**
- Sub-agent attempts the task
- Fails gracefully when file not found
- Coordinator reports failure with helpful message
- Optionally provides manual instructions

**Success Indicators:**
- No crash or hang
- Clear error message to user
- Backlog item marked as failed (if tracked)

---

### Scenario 8: Backlog Workflow Complete Cycle

**Tests:** Full backlog lifecycle

**Input (Message 1):**
```
Add a task to the backlog: "Write unit tests for the auth module" with high priority
```

**Input (Message 2):**
```
Show me my backlog
```

**Input (Message 3):**
```
Mark the auth module task as completed with summary "Added 15 unit tests covering login, logout, and token refresh"
```

**Input (Message 4):**
```
Show me my backlog again
```

**Expected Behavior:**
- Task created, listed, completed, and reflected in final list
- Completed task shows summary

**Success Indicators:**
- Task appears in list after creation
- Task status changes to completed
- Summary is stored and displayed

---

## Log Patterns to Monitor

| Pattern | Meaning |
|---------|---------|
| `handling message` | New message received and processing started |
| `dispatched async task` | Sub-agent task dispatched successfully |
| `auto_memory: job failed` | Memory extraction timeout (background, non-blocking) |
| `context deadline exceeded` | API timeout (may indicate Gemini slowness) |
| `Invalid JSON payload` | Tool schema incompatibility with Gemini |
| `queue full, skipping` | Memory queue at capacity (non-blocking) |

## Common Issues and Fixes

| Issue | Cause | Fix |
|-------|-------|-----|
| Messages blocked | Serial processing in router | Fixed: goroutine per message |
| Sub-agent schema error | `additionalProperties` in schema | Fixed: removed from Gemini adapter |
| Memory extraction timeout | Gemini API slow | Non-blocking, runs in background |
| Thought signature error | Wrong JSON structure | Fixed: sibling of functionCall |

---

## Test Execution Checklist

- [ ] Scenario 1: Parallel Sub-Agent Dispatch
- [ ] Scenario 2: Backlog + Deferred Execution
- [ ] Scenario 3: Multi-Step Document Pipeline
- [ ] Scenario 4: Stress Test - Rapid Fire Messages
- [ ] Scenario 5: Mixed Sync/Async Workload
- [ ] Scenario 6: Calendar Integration
- [ ] Scenario 7: Error Recovery
- [ ] Scenario 8: Backlog Workflow Complete Cycle

---

*Last updated: 2026-01-28*
