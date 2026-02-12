# Third-Party Conversations Integration Guide

This guide describes how to integrate the third-party conversation browser into a mobile client. Third-party conversations are messages from unknown senders that are handled by a local LLM (separate from the main agent context). The mobile app can browse these conversations while the owner receives batched digest notifications via Telegram.

## Overview

When a message arrives from an unknown sender (not in the trust allowlist), kaggen routes it to a local LLM (Ollama) for handling. These conversations are:

- **Persisted** in a separate SQLite database (`~/.kaggen/thirdparty.db`)
- **Isolated** from the main agent context (no data leakage between contexts)
- **Browsable** via the P2P `/kaggen/thirdparty/1.0.0` protocol
- **Summarized** and sent to the owner via Telegram digest (every 5 minutes)

### Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                     Third-Party Message Flow                     │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  Unknown Sender                                                  │
│       │                                                          │
│       ▼                                                          │
│  ┌─────────────────┐                                             │
│  │ Trust Classifier │──────► TrustTierThirdParty                 │
│  └─────────────────┘                                             │
│       │                                                          │
│       ▼                                                          │
│  ┌─────────────────┐    ┌──────────────────────┐                │
│  │   Local LLM     │───►│  ThirdPartyStore     │                │
│  │   (Ollama)      │    │  (SQLite)            │                │
│  └─────────────────┘    └──────────────────────┘                │
│       │                          │                               │
│       │                          ▼                               │
│       │                 ┌──────────────────────┐                │
│       │                 │ TelegramOwnerNotifier│                │
│       │                 │ (5-min batched digest)│               │
│       │                 └──────────────────────┘                │
│       │                          │                               │
│       ▼                          ▼                               │
│  Response to Sender       Owner Notification                     │
│                                                                  │
│  ─────────────────────────────────────────────────────────────  │
│                                                                  │
│  Mobile App                                                      │
│       │                                                          │
│       ▼                                                          │
│  ┌─────────────────────────────────────────┐                    │
│  │ P2P Protocol: /kaggen/thirdparty/1.0.0  │                    │
│  │                                          │                    │
│  │  Methods:                                │                    │
│  │  • sessions    - List all conversations  │                    │
│  │  • messages    - Get conversation detail │                    │
│  │  • unread_count - Badge count           │                    │
│  │  • mark_read   - Clear unread status    │                    │
│  └─────────────────────────────────────────┘                    │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

## Protocol Details

| Property | Value |
|----------|-------|
| Protocol ID | `/kaggen/thirdparty/1.0.0` |
| Type | Request/Response (API pattern) |
| Wire Format | 4-byte length prefix (big-endian) + protobuf bytes |
| Authentication | PeerID (via Noise handshake) |

### Available Methods

| Method | Description |
|--------|-------------|
| `sessions` | List all third-party conversation sessions with summaries |
| `messages` | Get messages for a specific session (paginated) |
| `unread_count` | Get total count of unread (unnotified) messages |
| `mark_read` | Mark all messages in a session as read |

---

## API Reference

### sessions

List all third-party conversation sessions, ordered by most recent activity.

**Request:**
```json
{
  "id": "req-123",
  "method": "sessions",
  "params": {}
}
```

**Response:**
```json
{
  "id": "req-123",
  "success": true,
  "data": {
    "sessions": [
      {
        "session_id": "whatsapp:15551234567",
        "sender_phone": "+15551234567",
        "sender_telegram_id": 0,
        "sender_name": "John Doe",
        "channel": "whatsapp",
        "message_count": 12,
        "unread_count": 3,
        "last_message_at": "2025-02-12T10:30:00Z",
        "first_message_at": "2025-02-10T14:22:00Z"
      },
      {
        "session_id": "telegram:987654321",
        "sender_phone": "",
        "sender_telegram_id": 987654321,
        "sender_name": "Jane Smith",
        "channel": "telegram",
        "message_count": 5,
        "unread_count": 0,
        "last_message_at": "2025-02-11T16:45:00Z",
        "first_message_at": "2025-02-11T16:40:00Z"
      }
    ]
  }
}
```

#### Session Object Fields

| Field | Type | Description |
|-------|------|-------------|
| `session_id` | string | Unique identifier for the conversation session |
| `sender_phone` | string | Phone number (if from WhatsApp) |
| `sender_telegram_id` | int64 | Telegram user ID (if from Telegram) |
| `sender_name` | string | Display name (push_name from WhatsApp, or Telegram name) |
| `channel` | string | Source channel: `"whatsapp"`, `"telegram"` |
| `message_count` | int | Total number of message exchanges in this session |
| `unread_count` | int | Number of messages not yet included in a digest |
| `last_message_at` | string | ISO 8601 timestamp of most recent message |
| `first_message_at` | string | ISO 8601 timestamp of first message |

---

### messages

Get messages for a specific session with pagination support.

**Request:**
```json
{
  "id": "req-124",
  "method": "messages",
  "params": {
    "session_id": "whatsapp:15551234567",
    "limit": 50,
    "offset": 0
  }
}
```

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `session_id` | string | Yes | - | The session ID to fetch messages for |
| `limit` | int | No | 50 | Maximum number of messages to return |
| `offset` | int | No | 0 | Number of messages to skip (for pagination) |

**Response:**
```json
{
  "id": "req-124",
  "success": true,
  "data": {
    "session_id": "whatsapp:15551234567",
    "messages": [
      {
        "id": "msg-uuid-1",
        "user_message": "Hi, is this the support line?",
        "llm_response": "Hello! I'm an AI assistant. How can I help you today?",
        "created_at": "2025-02-10T14:22:00Z",
        "notified": true
      },
      {
        "id": "msg-uuid-2",
        "user_message": "I need help with my order #12345",
        "llm_response": "I'd be happy to help with your order. Could you tell me more about the issue you're experiencing?",
        "created_at": "2025-02-10T14:23:30Z",
        "notified": true
      },
      {
        "id": "msg-uuid-3",
        "user_message": "The package never arrived",
        "llm_response": "I'm sorry to hear that. Let me note this for the owner. In the meantime, do you have a tracking number?",
        "created_at": "2025-02-12T10:30:00Z",
        "notified": false
      }
    ],
    "total": 12,
    "limit": 50,
    "offset": 0
  }
}
```

#### Message Object Fields

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Unique message identifier (UUID) |
| `user_message` | string | The message sent by the third-party user |
| `llm_response` | string | The response generated by the local LLM |
| `created_at` | string | ISO 8601 timestamp when the exchange occurred |
| `notified` | bool | Whether this message was included in a Telegram digest |

#### Pagination

Messages are returned in chronological order (oldest first). Use `offset` and `limit` for pagination:

```
Page 1: offset=0,  limit=50  → messages 0-49
Page 2: offset=50, limit=50  → messages 50-99
...
```

The `total` field indicates the total number of messages in the session.

---

### unread_count

Get the total count of unread (unnotified) messages across all sessions. Use this for badge counts.

**Request:**
```json
{
  "id": "req-125",
  "method": "unread_count",
  "params": {}
}
```

**Response:**
```json
{
  "id": "req-125",
  "success": true,
  "data": {
    "unread_count": 7
  }
}
```

---

### mark_read

Mark all messages in a session as read/notified. Call this when the user views a conversation.

**Request:**
```json
{
  "id": "req-126",
  "method": "mark_read",
  "params": {
    "session_id": "whatsapp:15551234567"
  }
}
```

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `session_id` | string | Yes | The session ID to mark as read |

**Response:**
```json
{
  "id": "req-126",
  "success": true,
  "data": {
    "success": true,
    "session_id": "whatsapp:15551234567"
  }
}
```

---

## Implementation Examples

### Go Client

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"

    "github.com/libp2p/go-libp2p"
    "github.com/libp2p/go-libp2p/core/peer"
    "github.com/libp2p/go-libp2p/core/protocol"
)

const ThirdPartyProtocolID = "/kaggen/thirdparty/1.0.0"

// Session represents a third-party conversation session.
type Session struct {
    SessionID        string `json:"session_id"`
    SenderPhone      string `json:"sender_phone,omitempty"`
    SenderTelegramID int64  `json:"sender_telegram_id,omitempty"`
    SenderName       string `json:"sender_name,omitempty"`
    Channel          string `json:"channel"`
    MessageCount     int    `json:"message_count"`
    UnreadCount      int    `json:"unread_count"`
    LastMessageAt    string `json:"last_message_at"`
    FirstMessageAt   string `json:"first_message_at"`
}

// Message represents a single message exchange.
type Message struct {
    ID          string `json:"id"`
    UserMessage string `json:"user_message"`
    LLMResponse string `json:"llm_response"`
    CreatedAt   string `json:"created_at"`
    Notified    bool   `json:"notified"`
}

// ThirdPartyClient provides access to third-party conversations.
type ThirdPartyClient struct {
    host   host.Host
    peerID peer.ID
}

// ListSessions returns all third-party conversation sessions.
func (c *ThirdPartyClient) ListSessions(ctx context.Context) ([]Session, error) {
    data, err := c.call(ctx, "sessions", nil)
    if err != nil {
        return nil, err
    }

    var result struct {
        Sessions []Session `json:"sessions"`
    }
    if err := json.Unmarshal(data, &result); err != nil {
        return nil, err
    }
    return result.Sessions, nil
}

// GetMessages returns messages for a specific session.
func (c *ThirdPartyClient) GetMessages(ctx context.Context, sessionID string, limit, offset int) ([]Message, int, error) {
    params := map[string]any{
        "session_id": sessionID,
        "limit":      limit,
        "offset":     offset,
    }

    data, err := c.call(ctx, "messages", params)
    if err != nil {
        return nil, 0, err
    }

    var result struct {
        Messages []Message `json:"messages"`
        Total    int       `json:"total"`
    }
    if err := json.Unmarshal(data, &result); err != nil {
        return nil, 0, err
    }
    return result.Messages, result.Total, nil
}

// GetUnreadCount returns the total unread message count.
func (c *ThirdPartyClient) GetUnreadCount(ctx context.Context) (int, error) {
    data, err := c.call(ctx, "unread_count", nil)
    if err != nil {
        return 0, err
    }

    var result struct {
        UnreadCount int `json:"unread_count"`
    }
    if err := json.Unmarshal(data, &result); err != nil {
        return 0, err
    }
    return result.UnreadCount, nil
}

// MarkRead marks all messages in a session as read.
func (c *ThirdPartyClient) MarkRead(ctx context.Context, sessionID string) error {
    _, err := c.call(ctx, "mark_read", map[string]any{
        "session_id": sessionID,
    })
    return err
}

// call makes an API call to the third-party protocol.
func (c *ThirdPartyClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
    stream, err := c.host.NewStream(ctx, c.peerID, protocol.ID(ThirdPartyProtocolID))
    if err != nil {
        return nil, err
    }
    defer stream.Close()

    // Send request
    paramsJSON, _ := json.Marshal(params)
    req := &APIRequest{
        ID:     uuid.New().String(),
        Method: method,
        Params: paramsJSON,
    }
    if err := writeMessage(stream, req); err != nil {
        return nil, err
    }

    // Read response
    var resp APIResponse
    if err := readMessage(stream, &resp); err != nil {
        return nil, err
    }
    if !resp.Success {
        return nil, fmt.Errorf("API error: %s", resp.Error)
    }
    return resp.Data, nil
}
```

### Dart/Flutter Client

```dart
import 'dart:convert';

class ThirdPartySession {
  final String sessionId;
  final String? senderPhone;
  final int? senderTelegramId;
  final String? senderName;
  final String channel;
  final int messageCount;
  final int unreadCount;
  final DateTime lastMessageAt;
  final DateTime firstMessageAt;

  ThirdPartySession.fromJson(Map<String, dynamic> json)
      : sessionId = json['session_id'],
        senderPhone = json['sender_phone'],
        senderTelegramId = json['sender_telegram_id'],
        senderName = json['sender_name'],
        channel = json['channel'],
        messageCount = json['message_count'],
        unreadCount = json['unread_count'],
        lastMessageAt = DateTime.parse(json['last_message_at']),
        firstMessageAt = DateTime.parse(json['first_message_at']);

  /// Display name for the session (name, phone, or telegram ID).
  String get displayName {
    if (senderName != null && senderName!.isNotEmpty) return senderName!;
    if (senderPhone != null && senderPhone!.isNotEmpty) return senderPhone!;
    if (senderTelegramId != null && senderTelegramId! > 0) {
      return 'Telegram:$senderTelegramId';
    }
    return 'Unknown';
  }

  /// Icon based on channel type.
  IconData get channelIcon {
    switch (channel) {
      case 'whatsapp':
        return Icons.chat;
      case 'telegram':
        return Icons.send;
      default:
        return Icons.message;
    }
  }
}

class ThirdPartyMessage {
  final String id;
  final String userMessage;
  final String llmResponse;
  final DateTime createdAt;
  final bool notified;

  ThirdPartyMessage.fromJson(Map<String, dynamic> json)
      : id = json['id'],
        userMessage = json['user_message'],
        llmResponse = json['llm_response'],
        createdAt = DateTime.parse(json['created_at']),
        notified = json['notified'];
}

class ThirdPartyClient {
  final P2PConnection _connection;

  ThirdPartyClient(this._connection);

  static const protocolId = '/kaggen/thirdparty/1.0.0';

  /// List all third-party conversation sessions.
  Future<List<ThirdPartySession>> listSessions() async {
    final response = await _connection.apiCall(protocolId, 'sessions', {});
    final sessions = response['sessions'] as List;
    return sessions.map((s) => ThirdPartySession.fromJson(s)).toList();
  }

  /// Get messages for a specific session.
  Future<({List<ThirdPartyMessage> messages, int total})> getMessages(
    String sessionId, {
    int limit = 50,
    int offset = 0,
  }) async {
    final response = await _connection.apiCall(protocolId, 'messages', {
      'session_id': sessionId,
      'limit': limit,
      'offset': offset,
    });
    final messages = (response['messages'] as List)
        .map((m) => ThirdPartyMessage.fromJson(m))
        .toList();
    return (messages: messages, total: response['total'] as int);
  }

  /// Get the total unread message count (for badge).
  Future<int> getUnreadCount() async {
    final response = await _connection.apiCall(protocolId, 'unread_count', {});
    return response['unread_count'] as int;
  }

  /// Mark all messages in a session as read.
  Future<void> markRead(String sessionId) async {
    await _connection.apiCall(protocolId, 'mark_read', {
      'session_id': sessionId,
    });
  }
}
```

---

## UI Implementation Guidelines

### Session List Screen

Display a list of conversation sessions with:

1. **Sender identification**: Show `sender_name` if available, fall back to phone/telegram ID
2. **Channel indicator**: Icon or badge showing WhatsApp/Telegram origin
3. **Unread badge**: Show `unread_count` as a badge on each session
4. **Timestamp**: Show `last_message_at` as relative time ("2h ago", "Yesterday")
5. **Message count**: Optionally show total `message_count`

```
┌────────────────────────────────────────────┐
│  Third-Party Messages                   ⋮  │
├────────────────────────────────────────────┤
│ ┌──┐                                       │
│ │WA│ John Doe                    2h ago    │
│ └──┘ 12 messages                    [3]    │
├────────────────────────────────────────────┤
│ ┌──┐                                       │
│ │TG│ Jane Smith               Yesterday    │
│ └──┘ 5 messages                            │
├────────────────────────────────────────────┤
│ ┌──┐                                       │
│ │WA│ +1 555 987 6543            3 days     │
│ └──┘ 2 messages                            │
└────────────────────────────────────────────┘
```

### Conversation Detail Screen

Display messages in a chat-like format:

1. **User messages**: Aligned left, showing `user_message`
2. **Bot responses**: Aligned right or differentiated, showing `llm_response`
3. **Timestamps**: Show `created_at` for each message pair
4. **Unread indicator**: Optional visual indicator for unnotified messages
5. **Pagination**: Load more messages as user scrolls (use `offset`)

```
┌────────────────────────────────────────────┐
│  ← John Doe (WhatsApp)                     │
├────────────────────────────────────────────┤
│                                            │
│  ┌─────────────────────────┐               │
│  │ Hi, is this the         │               │
│  │ support line?           │  14:22        │
│  └─────────────────────────┘               │
│                                            │
│               ┌─────────────────────────┐  │
│               │ Hello! I'm an AI        │  │
│               │ assistant. How can I    │  │
│     14:22     │ help you today?         │  │
│               └─────────────────────────┘  │
│                                            │
│  ┌─────────────────────────┐               │
│  │ I need help with my     │               │
│  │ order #12345            │  14:23        │
│  └─────────────────────────┘               │
│                                            │
│               ┌─────────────────────────┐  │
│               │ I'd be happy to help    │  │
│     14:23     │ with your order...      │  │
│               └─────────────────────────┘  │
│                                            │
└────────────────────────────────────────────┘
```

### Badge Count

Poll `unread_count` periodically (e.g., every 30 seconds) or on app foreground:

```dart
// Poll for unread count
Timer.periodic(Duration(seconds: 30), (_) async {
  final count = await client.getUnreadCount();
  updateBadge(count);
});
```

### Mark as Read

Call `mark_read` when the user opens a conversation:

```dart
void onConversationOpened(String sessionId) async {
  // Mark as read when user views the conversation
  await client.markRead(sessionId);

  // Refresh the session list to update unread counts
  await refreshSessions();
}
```

---

## Error Handling

### Common Errors

| Error | Cause | Resolution |
|-------|-------|------------|
| `third-party store not configured` | Server doesn't have third-party enabled | Check server config `trust.third_party.enabled` |
| `session_id is required` | Missing required parameter | Ensure `session_id` is provided |
| `failed to list sessions` | Database error | Retry or check server logs |
| `failed to get messages` | Database error | Retry or check server logs |

### Example Error Handling

```dart
try {
  final sessions = await client.listSessions();
  // Handle success
} on ApiException catch (e) {
  if (e.message.contains('not configured')) {
    showSnackBar('Third-party messages are not enabled on this server');
  } else {
    showSnackBar('Failed to load conversations: ${e.message}');
  }
}
```

---

## Related Documentation

- [P2P Integration Guide](p2p-integration-guide.md) - Complete P2P protocol reference
- [Mobile Approval Integration](mobile_approval_integration.md) - Approval system integration
- [Mobile Security Integration](mobile-security-integration.md) - Authentication and security

---

## Changelog

| Version | Date | Changes |
|---------|------|---------|
| 1.0.0 | 2025-02-12 | Initial release |
