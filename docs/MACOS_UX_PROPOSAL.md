# macOS UX Improvement Proposal

> **Status**: Draft
> **Date**: 2026-03-06
> **Goal**: Transform Kaggen into a class-leading macOS experience by wrapping the existing Go backend in a native presentation layer.

## Motivation

Kaggen has an incredibly rich backend — multi-model orchestration, VFS sandboxing, semantic memory, proactive jobs, P2P networking, sub-agent delegation, and tiered reasoning. The user experience hasn't caught up to the capability. Today the primary interface is a bare CLI REPL (`kaggen agent`) and a functional but basic web dashboard on the gateway port.

A native macOS app can make Kaggen **ambient** — always available, context-aware, and deeply integrated with the operating system — without rewriting the engine.

## Architectural Approach

A **thin SwiftUI shell** communicating with the existing Go binary via the **WebSocket gateway** (already built). The Go backend stays as-is. The native app is a presentation layer over the existing protocol. This is the fastest path to a class-leading macOS experience without rewriting the engine.

```
┌─────────────────────────────────┐
│  SwiftUI App (presentation)     │
│  - Menu bar icon                │
│  - Conversation window          │
│  - Spotlight-style input bar    │
│  - Settings / onboarding        │
├─────────────────────────────────┤
│  WebSocket Client               │
│  (connects to gateway)          │
└────────────┬────────────────────┘
             │ ws://127.0.0.1:18789
┌────────────▼────────────────────┐
│  kaggen gateway (Go binary)     │
│  - Agent engine                 │
│  - Session persistence          │
│  - Memory, skills, proactive    │
│  - P2P, channels                │
└─────────────────────────────────┘
```

---

## P0 — Foundational

### Menu Bar App + Hotkey Invocation

Kaggen lives in the macOS menu bar as an always-available icon. This eliminates the friction of opening a terminal and launching a process.

- **Menu bar icon** — click to open a floating conversation panel (NSPopover or detachable window). The agent is always one click away.
- **Global hotkey** (e.g. `⌥ Space`) — summons a Spotlight-style floating input bar. Type a request, get a response, dismiss with Escape. This is the "I need Kaggen for 5 seconds" flow.
- **Compact and full modes** — menu bar popover for quick queries, full window for deep work. Seamless transition between them.
- The Go binary runs as a background process managed by the app (or launchd). The app connects via WebSocket on startup.

### Native Conversation UI

Replace the bare REPL with a proper chat interface:

- Markdown rendering with full CommonMark support.
- Code blocks with syntax highlighting, copy-to-clipboard buttons, and language labels.
- Inline file previews (images, PDFs, text files).
- Typing indicators and smooth message appearance animations.
- Tool-use progress: show which tools the agent is calling, with expandable detail for power users.
- Conversation search (full-text across the current session).

---

## P1 — System Integration

### Notification Integration

Surface the agent's autonomous work through native macOS notifications:

- **Proactive job completion** — cron jobs, webhook-triggered tasks, and heartbeat results appear as notifications.
- **Async task completion** — when a dispatched sub-agent finishes work, notify the user with a summary and a "View" action.
- **Approval requests** — P2P maker-checker approvals surface as actionable notifications (Approve / Deny buttons).
- Uses `UserNotifications` framework so notifications appear in Notification Center, respect Focus modes, and can be grouped.

### Guided Onboarding Wizard

First-run experience determines retention. Replace `kaggen init` with a native setup flow:

1. **Welcome screen** — "Meet Kaggen" with the logo and a one-sentence description.
2. **API key setup** — "Which AI provider do you use?" Radio buttons for Anthropic / Gemini / ZAI. Paste API key with a "Test Connection" button. Support for multiple providers.
3. **Identity customization** — "What should Kaggen call you?" Pre-fill from macOS user info. Timezone auto-detected. Preferences (communication style, verbosity).
4. **Personality preview** — live preview of the agent's greeting based on SOUL.md / IDENTITY.md defaults. Option to customize.
5. **Feature opt-in** — progressive disclosure of advanced features (memory search, browser automation, proactive jobs). Each with a one-sentence explanation and toggle. Start simple.
6. **Done** — "Kaggen is ready. Press `⌥ Space` anytime." Opens the menu bar popover with a welcome message from the agent.

The wizard writes `config.json`, bootstrap files, and API keys to the secrets store — the same artifacts `kaggen init` produces today.

---

## P2 — Context & Memory

### Session Sidebar + Management

The session model is powerful but currently invisible to users:

- **Session list** — sidebar showing all sessions with auto-generated names, timestamps, and two-line preview snippets.
- **Session actions** — pin, archive, delete, rename. Search across all sessions (leveraging existing FTS5).
- **Session forking** — visually branch a conversation. "Let me explore this idea in a fork." Show the fork tree as a visual graph.
- **Active session indicator** — show which session is active, with quick-switch support.

### Memory Surface

Let users see and curate what the agent remembers:

- **Memory browser** — list of extracted memories with source attribution (which conversation, when).
- **Edit / delete** — users can correct or remove memories. "You think I prefer Python, but I've switched to Go."
- **Memory insights** — show synthesized patterns ("You frequently work on API integrations on Mondays").
- **Trust indicator** — show confidence and recency of each memory.
- Think of it as Apple's contact card, but for the AI's model of you.

### Clipboard and File Drop Integration

Context without context-switching:

- **Clipboard hotkey** — global shortcut (e.g. `⌥ ⇧ Space`) sends clipboard contents to Kaggen with a prompt: "Explain this" / "Summarize" / "Rewrite" / custom.
- **Services menu** — register Kaggen as a macOS Services provider. Select text in any app → right-click → Services → "Ask Kaggen."
- **File drop** — drag a file onto the menu bar icon or conversation window. Kaggen reads it via VFS and can discuss, summarize, or transform it.
- **Screenshot capture** — hotkey to capture a screen region and send it to the agent (multimodal models support image input). "What's wrong with this UI?"

---

## P3 — Automation & Voice

### Shortcuts and Quick Actions

Deep macOS ecosystem integration for power users:

- **Shortcuts provider** — register Kaggen actions with the Shortcuts app. Users build automations:
  - "When I arrive at the office, ask Kaggen to summarize my email."
  - "Every morning at 8am, have Kaggen prepare my daily brief."
  - "When I connect to my monitor, start a new coding session."
- **Finder Quick Actions** — right-click a file in Finder → "Ask Kaggen about this file." Works via the existing VFS read capability.
- **Share extension** — share a URL, image, or text to Kaggen from any app's share sheet.

### Voice Interface

Hands-free interaction for specific use cases:

- **Voice input** — hold a hotkey, speak, transcribe via Whisper (local) or a cloud API, send to the agent. Release to submit.
- **Text-to-speech output** — optionally read responses aloud using macOS `AVSpeechSynthesizer` or a higher-quality model.
- **Conversation mode** — continuous voice back-and-forth for extended hands-free sessions (cooking, walking, whiteboarding).
- Combined with the menu bar, this becomes a conversational assistant with real capability.

---

## P4 — Developer & Power User

### Live Task Panel

Surface sub-agent and proactive work in real-time:

- **Activity view** — see dispatched sub-agent tasks running with status, elapsed time, and logs. Like Activity Monitor for AI work.
- **Cancel / retry** — stop a runaway task or retry a failed one with one click.
- **History** — completed tasks with outcome summaries, duration, and token usage.

### Skill Browser

Make the skill system visible and manageable:

- **Installed skills** — visual cards showing name, description, protocol tools, and status (enabled/disabled).
- **One-click toggle** — enable or disable skills without editing files.
- **Skill editor** — in-app SKILL.md editor with live validation and preview.
- **Skill gallery** (future) — community-contributed skills with install button.

### Editor Integration

For developer-centric workflows:

- **VS Code extension** — connects to the gateway WebSocket. Inline code explanations, refactoring suggestions, and error analysis.
- **Xcode source editor extension** — same capabilities for Swift/Objective-C development.
- **Terminal embedding** — for users who want the CLI feel, embed a terminal view that runs `kaggen agent` with rich formatting inside the native app.

### API Playground

For debugging and development:

- **Tool tester** — invoke any tool manually and inspect the result.
- **VFS inspector** — browse the sandboxed workspace filesystem.
- **Session debugger** — view raw JSONL events, context cache state, and token counts.

---

## P5 — Cross-Device Continuity

### P2P and Mobile Handoff

The P2P layer already exists — lean into Apple ecosystem integration:

- **Handoff** — start a conversation on Mac, continue on iPhone (via the existing P2P mobile integration). Show a Handoff icon in the Dock when a session is active on another device.
- **Universal Clipboard** — copy on phone, paste into Kaggen on Mac, or vice versa.
- **iCloud sync** (optional) — sync config, memories, and session metadata across devices. Session content stays local for privacy.

---

## Visual Design Principles

- **Adaptive theming** — follow macOS light/dark mode and accent color. Use vibrancy and blur effects (`NSVisualEffectView`) for the floating panel.
- **Subtle animations** — typing indicators, smooth message appearance, tool-use progress spinners. Nothing gratuitous.
- **Information density controls** — compact mode for quick answers, expanded mode for detailed work. User-configurable.
- **Accessibility** — full VoiceOver support, Dynamic Type, keyboard navigation throughout.
- **Native feel** — use standard macOS controls (NSToolbar, NSSplitView, SF Symbols). Kaggen should feel like it belongs on macOS, not like an Electron wrapper.

---

## Priority Summary

| Priority | Initiative | Rationale |
|----------|-----------|-----------|
| **P0** | Menu bar app + hotkey invocation | Eliminates friction; makes Kaggen ambient |
| **P0** | Native conversation UI | Chat with markdown/code rendering replaces bare REPL |
| **P1** | Notification integration | Proactive jobs and async tasks become visible |
| **P1** | Guided onboarding wizard | First-run experience determines retention |
| **P2** | Session sidebar + memory surface | Users need to see and trust persistent state |
| **P2** | Clipboard / file drop integration | Context without context-switching |
| **P3** | Shortcuts / Quick Actions | Power users automate; ecosystem integration |
| **P3** | Voice I/O | Hands-free use case |
| **P4** | Live task panel + skill browser | Visibility into autonomous agent work |
| **P4** | Editor integrations | Developer-specific workflows |
| **P5** | Cross-device continuity | Leverage existing P2P for Apple ecosystem |

---

## Open Questions

1. **App distribution** — Mac App Store vs. direct download (DMG)? App Store provides auto-updates and trust but imposes sandboxing constraints. Direct download offers more freedom.
2. **Background process model** — should the Go binary run as a launchd daemon, or should the SwiftUI app manage its lifecycle? Daemon is more reliable; app-managed is simpler.
3. **Gateway authentication** — the current token-based auth is designed for remote access. For local app-to-gateway communication, should we use a Unix socket or a local-only bearer token?
4. **Electron fallback** — should there be a cross-platform option (Electron/Tauri) for Linux users, or is macOS-native the exclusive focus?
5. **Memory privacy** — how much of the memory surface should be visible? Users may be surprised by what the agent extracts. Consider an opt-in model for memory visibility.
