# Mobile Threading Guide

Integration guide for adding threaded conversations to the Flutter mobile app.

## Overview

Kaggen now supports threaded conversations. Replying to a specific message forks the session at that point, creating an independent conversation branch with full context up to the replied-to message. The agent is aware it's in a thread and stays focused on the topic.

## Protocol Changes

### WebSocket Message Format

**Sending a threaded reply:**

```json
{
  "content": "Tell me more about that approach",
  "session_id": "parent-session-uuid",
  "reply_to_event_id": "evt-abc-123"
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `content` | yes | The message text |
| `session_id` | yes | The parent session ID (the session containing the message being replied to) |
| `reply_to_event_id` | yes | The `event_id` of the message to reply to |

**Normal (non-threaded) message** — unchanged:

```json
{
  "content": "Hello",
  "session_id": "session-uuid"
}
```

### New Response Type: `thread_created`

When the server successfully forks a thread, it sends a `thread_created` response **before** the agent's reply:

```json
{
  "id": "resp-uuid",
  "message_id": "msg-uuid",
  "session_id": "new-thread-session-uuid",
  "type": "thread_created",
  "done": false,
  "metadata": {
    "thread_session_id": "new-thread-session-uuid",
    "parent_session_id": "original-session-uuid"
  }
}
```

All subsequent responses for this turn carry the **thread's session ID**, not the parent's. The client must track this to route messages correctly.

### Event IDs on All Responses

Every response now includes `event_id` in metadata:

```json
{
  "type": "text",
  "content": "Here's what I think...",
  "metadata": {
    "event_id": "evt-xyz-789"
  }
}
```

Use this `event_id` as the `reply_to_event_id` when the user long-presses or swipes to reply.

### Session Welcome Message

On WebSocket connect, the server sends a session assignment message:

```json
{
  "type": "session",
  "session_id": "assigned-uuid"
}
```

Store this — it's the session ID for the connection. If you connect with `?session=<uuid>`, it echoes back the same ID.

## REST API Changes

### GET /api/sessions

Sessions listing now nests threads under their parent:

```json
[
  {
    "id": "session-uuid",
    "name": "Fix login authentication",
    "user_id": "client-uuid",
    "updated_at": "2026-02-02T10:30:00Z",
    "message_count": 42,
    "threads": [
      {
        "id": "thread-uuid",
        "name": "Re: Let me look at the auth middleware",
        "is_thread": true,
        "parent_session_id": "session-uuid",
        "updated_at": "2026-02-02T11:00:00Z",
        "message_count": 8
      }
    ]
  }
]
```

Threads are excluded from the top level by default. To get a flat list of everything, add `?include_threads=true`.

### GET /api/sessions/messages

Messages now include `event_id`:

```json
{
  "messages": [
    {
      "event_id": "evt-abc-123",
      "role": "assistant",
      "content": "Let me look at the auth middleware...",
      "timestamp": "2026-02-02T10:05:00Z"
    }
  ]
}
```

## Flutter Implementation Guide

### 1. Store Event IDs on Messages

When receiving WebSocket responses, extract and store the `event_id` from metadata on each message model:

```dart
class ChatMessage {
  final String id;
  final String? eventId;  // from metadata["event_id"]
  final String role;
  final String content;
  final DateTime timestamp;
  final String sessionId;

  // ...
}
```

When processing incoming WebSocket JSON:

```dart
void _onMessage(Map<String, dynamic> data) {
  final type = data['type'] as String?;
  final metadata = data['metadata'] as Map<String, dynamic>? ?? {};

  if (type == 'session') {
    _currentSessionId = data['session_id'] as String;
    return;
  }

  if (type == 'thread_created') {
    _handleThreadCreated(metadata);
    return;
  }

  final message = ChatMessage(
    id: data['id'],
    eventId: metadata['event_id'] as String?,
    role: 'assistant',
    content: data['content'] ?? '',
    timestamp: DateTime.now(),
    sessionId: data['session_id'],
  );

  // Add to appropriate chat view...
}
```

### 2. Thread Creation Flow

When the user replies to a specific message (long-press, swipe-to-reply, etc.):

```dart
void replyToMessage(ChatMessage originalMessage, String replyText) {
  final payload = {
    'content': replyText,
    'session_id': originalMessage.sessionId,
    'reply_to_event_id': originalMessage.eventId,
  };
  _webSocket.send(jsonEncode(payload));
}
```

### 3. Handle `thread_created` Response

When the server responds with `thread_created`, the client should:

1. Store the new thread session ID
2. Navigate to or open a thread view
3. Route subsequent responses (same turn) to the thread view

```dart
void _handleThreadCreated(Map<String, dynamic> metadata) {
  final threadSessionId = metadata['thread_session_id'] as String;
  final parentSessionId = metadata['parent_session_id'] as String;

  // Create a thread entry in the local session list.
  final thread = ChatThread(
    sessionId: threadSessionId,
    parentSessionId: parentSessionId,
  );

  // Navigate to thread view or show thread indicator.
  _navigateToThread(thread);

  // Subsequent responses in this turn will carry threadSessionId.
  // Route them to the thread view.
}
```

### 4. Thread UI Patterns

**Session list**: Show threads nested under their parent session. The `/api/sessions` response already provides this nesting in the `threads` array.

```dart
Widget buildSessionList(List<Session> sessions) {
  return ListView.builder(
    itemCount: sessions.length,
    itemBuilder: (context, index) {
      final session = sessions[index];
      return Column(
        children: [
          SessionTile(session: session),
          if (session.threads.isNotEmpty)
            Padding(
              padding: EdgeInsets.only(left: 24),
              child: Column(
                children: session.threads
                    .map((t) => ThreadTile(thread: t))
                    .toList(),
              ),
            ),
        ],
      );
    },
  );
}
```

**In-chat thread indicator**: When viewing a parent session's messages, show a thread badge on messages that have spawned threads.

**Reply gesture**: Long-press or swipe-right on a message to open a reply input. The reply input should show the quoted message and send with `reply_to_event_id`.

### 5. Thread Session Continuation

Once a thread is created, continuing the conversation in that thread uses the thread's session ID — same as any normal session:

```dart
void sendMessageInThread(String threadSessionId, String text) {
  final payload = {
    'content': text,
    'session_id': threadSessionId,
    // No reply_to_event_id — this is a continuation, not a new fork.
  };
  _webSocket.send(jsonEncode(payload));
}
```

### 6. Loading Thread History

Threads are regular sessions. Load their messages the same way:

```
GET /api/sessions/messages?user_id=<uid>&session_id=<thread-session-id>&detail=chat
```

The response includes a `summary` field if the thread was forked from a long conversation and the parent history was summarized.

### 7. WebSocket Connection for Threads

Two options for receiving thread responses:

**Option A — Single connection (recommended)**: Keep one WebSocket connection. Thread responses arrive on it because the server routes thread responses to clients matching the parent session ID via metadata. After the `thread_created` message, responses for that turn carry the thread's session ID.

**Option B — Dedicated connection**: Open a second WebSocket with `?session=<thread-session-id>` for the thread. This cleanly separates message streams but uses an extra connection.

## Data Model Reference

### Session Metadata (metadata.json)

Thread sessions have these additional fields:

```json
{
  "id": "thread-uuid",
  "name": "Re: Let me look at the auth middleware",
  "is_thread": true,
  "parent_session_id": "parent-uuid",
  "parent_event_id": "evt-abc-123",
  "created_at": "2026-02-02T11:00:00Z",
  "updated_at": "2026-02-02T11:05:00Z",
  "user_id": "client-uuid"
}
```

### Thread Naming

Thread names are auto-generated:
- Default: `"Re: <first 50 chars of the replied-to message>"`
- If Ollama is available, a short inferred name replaces this asynchronously
- The session rename endpoint can override: `POST /api/sessions/rename`

## Edge Cases

| Scenario | Behavior |
|----------|----------|
| `reply_to_event_id` not found | Server logs warning, message processed in original session (no fork) |
| Nested threads (reply within a thread) | Works — creates a new fork of the thread session |
| Parent session deleted | Thread continues to work independently (orphaned thread appears at top level in listings) |
| Very long parent history | Only the last 100 events are copied to the thread, along with the parent's conversation summary |
| `reply_to_event_id` without `session_id` | Uses the WebSocket connection's current session ID as the parent |
