# P2P Integration Guide

This guide provides everything you need to build a client application that communicates with kaggen via libp2p protocols. The P2P interface mirrors the WebSocket API, enabling mobile and desktop clients to connect directly to a kaggen node.

## Table of Contents

- [Overview](#overview)
- [Prerequisites](#prerequisites)
- [Connection Setup](#connection-setup)
- [Available Protocols](#available-protocols)
- [Chat Protocol](#chat-protocol)
- [API Protocols](#api-protocols)
  - [Sessions Protocol](#sessions-protocol)
  - [Tasks Protocol](#tasks-protocol)
  - [Approvals Protocol](#approvals-protocol)
  - [System Protocol](#system-protocol)
  - [Secrets Protocol](#secrets-protocol)
  - [Files Protocol](#files-protocol)
- [Wire Format](#wire-format)
- [Complete Examples](#complete-examples)
- [Error Handling](#error-handling)
- [Best Practices](#best-practices)

## Overview

Kaggen exposes a suite of libp2p protocols that mirror the HTTP API, enabling mobile and desktop clients to access all kaggen functionality via P2P:

- **Chat** - Send messages and receive streaming AI responses
- **Sessions** - View and manage conversation history
- **Tasks** - Monitor running tasks and pipelines
- **Approvals** - Handle human-in-the-loop tool approvals
- **System** - Access system status, configuration, and skills
- **Secrets** - Manage secrets and authentication tokens
- **Files** - Download published files

### Architecture

```
┌─────────────────┐                    ┌─────────────────┐
│  Mobile Client  │◄───libp2p stream───►│  Kaggen Server  │
│  (your app)     │                    │                 │
└─────────────────┘                    └─────────────────┘
         │                                      │
         │  Protocols:                          │
         │    /kaggen/chat/1.0.0      (streaming)
         │    /kaggen/sessions/1.0.0  (request/response)
         │    /kaggen/tasks/1.0.0     (request/response)
         │    /kaggen/approvals/1.0.0 (request/response)
         │    /kaggen/system/1.0.0    (request/response)
         │    /kaggen/secrets/1.0.0   (request/response)
         │    /kaggen/files/1.0.0     (request/response)
         │                                      │
         │  Transport: UDP (UDX) or TCP         │
         │  Security: Noise                     │
         │  Muxer: yamux                        │
         └──────────────────────────────────────┘
```

## Prerequisites

### Required Libraries

Choose a libp2p implementation for your platform:

| Platform | Library | Link |
|----------|---------|------|
| Go | go-libp2p | https://github.com/libp2p/go-libp2p |
| Rust | rust-libp2p | https://github.com/libp2p/rust-libp2p |
| JavaScript | js-libp2p | https://github.com/libp2p/js-libp2p |
| Dart/Flutter | dart-libp2p | https://github.com/paideia-ai/dart-libp2p |
| Kotlin | jvm-libp2p | https://github.com/libp2p/jvm-libp2p |
| Swift | swift-libp2p | https://github.com/swift-libp2p/swift-libp2p |

### Protobuf

You'll need protobuf code generation for your platform. The `.proto` file is located at `internal/p2p/proto/chat.proto`.

## Connection Setup

### 1. Obtain the Server's Multiaddr

When kaggen starts with P2P enabled, it prints its multiaddresses:

```
PeerID: 12D3KooWExample...
P2P Listen: /ip4/192.168.1.100/udp/4001/quic-v1/p2p/12D3KooWExample...
P2P Listen: /ip4/192.168.1.100/tcp/4001/p2p/12D3KooWExample...
```

The dashboard also displays these addresses with QR codes for easy mobile scanning.

### 2. Configure Your libp2p Host

Your client needs to support the same protocols as the server:

```go
// Go example
import (
    "github.com/libp2p/go-libp2p"
    "github.com/libp2p/go-libp2p/core/peer"
    "github.com/libp2p/go-libp2p/p2p/security/noise"
    "github.com/libp2p/go-libp2p/p2p/muxer/yamux"
    "github.com/libp2p/go-libp2p/p2p/transport/tcp"
    quic "github.com/libp2p/go-libp2p/p2p/transport/quic"
)

host, err := libp2p.New(
    libp2p.Transport(tcp.NewTCPTransport),
    libp2p.Transport(quic.NewTransport),
    libp2p.Security(noise.ID, noise.New),
    libp2p.Muxer(yamux.ID, yamux.DefaultTransport),
)
```

### 3. Connect to the Server

```go
// Parse the multiaddr
serverAddr, _ := multiaddr.NewMultiaddr(
    "/ip4/192.168.1.100/tcp/4001/p2p/12D3KooWExample...",
)

// Extract peer info
peerInfo, _ := peer.AddrInfoFromP2pAddr(serverAddr)

// Connect
err := host.Connect(ctx, *peerInfo)
```

### 4. Open a Protocol Stream

```go
// For chat (streaming responses)
stream, err := host.NewStream(ctx, peerInfo.ID, "/kaggen/chat/1.0.0")

// For API protocols (request/response)
stream, err := host.NewStream(ctx, peerInfo.ID, "/kaggen/sessions/1.0.0")
```

## Available Protocols

| Protocol ID | Type | Description |
|-------------|------|-------------|
| `/kaggen/chat/1.0.0` | Streaming | AI chat with streaming responses |
| `/kaggen/sessions/1.0.0` | Request/Response | Session management (list, view, rename, delete) |
| `/kaggen/tasks/1.0.0` | Request/Response | Task monitoring (list, cancel, pipelines) |
| `/kaggen/approvals/1.0.0` | Request/Response | Approval workflow (list, approve, reject) |
| `/kaggen/system/1.0.0` | Request/Response | System info (overview, config, skills, backlog) |
| `/kaggen/secrets/1.0.0` | Request/Response | Secret/token management |
| `/kaggen/files/1.0.0` | Request/Response | File downloads |
| `/kaggen/thirdparty/1.0.0` | Request/Response | Third-party conversation browsing |

### Authentication

All P2P protocols use **PeerID as identity** - no additional tokens are required. The PeerID is authenticated via the Noise protocol handshake during the libp2p connection.

---

## Chat Protocol

The chat protocol (`/kaggen/chat/1.0.0`) provides streaming AI chat functionality.

| Property | Value |
|----------|-------|
| Protocol ID | `/kaggen/chat/1.0.0` |
| Message encoding | Protocol Buffers (proto3) |
| Wire format | 4-byte length prefix (big-endian) + protobuf bytes |
| Max message size | 1 MB (1,048,576 bytes) |
| Pattern | Streaming (one request, multiple responses) |

### Chat Message Format

### Protobuf Definitions

```protobuf
syntax = "proto3";
package kaggen.p2p;

// Client → Server
message ChatMessage {
  string id = 1;                        // Unique message ID (UUID recommended)
  string session_id = 2;                // Session for conversation continuity
  string user_id = 3;                   // User identifier (optional)
  string content = 4;                   // Message text
  string reply_to_event_id = 5;         // For thread forking (optional)
  map<string, bytes> metadata = 6;      // JSON-encoded key-value pairs
  repeated Attachment attachments = 7;  // File attachments
}

message Attachment {
  string filename = 1;     // Original filename
  string mime_type = 2;    // MIME type (e.g., "image/jpeg")
  bytes data = 3;          // Raw file bytes
}

// Server → Client
message ChatResponse {
  string id = 1;            // Response ID
  string message_id = 2;    // ID of message being responded to
  string session_id = 3;    // Session ID
  string content = 4;       // Response content
  string type = 5;          // Response type (see below)
  bool done = 6;            // True if final response
  map<string, bytes> metadata = 7;  // Additional data
}
```

### Field Details

#### ChatMessage Fields

| Field | Required | Description |
|-------|----------|-------------|
| `id` | Recommended | Client-generated UUID. Server generates one if empty. |
| `session_id` | Optional | Conversation session. Defaults to first 16 chars of your PeerID. |
| `user_id` | Optional | User identifier. Defaults to session_id. |
| `content` | Yes | The message text to send to the agent. |
| `reply_to_event_id` | Optional | Event ID to reply to, creating a thread fork. |
| `metadata` | Optional | JSON-encoded values for extra context. |
| `attachments` | Optional | Files to include with the message. |

#### ChatResponse Fields

| Field | Description |
|-------|-------------|
| `id` | Unique response identifier. |
| `message_id` | ID of the message this responds to. |
| `session_id` | Session this response belongs to. |
| `content` | Text content, JSON for tool calls, or error message. |
| `type` | Response type (see [Response Types](#response-types)). |
| `done` | `true` if this is the final response for the request. |
| `metadata` | Additional typed data (JSON-encoded values). |

## Wire Format

All messages are length-prefixed for framing:

```
┌──────────────────┬─────────────────────────┐
│  4 bytes         │  N bytes                │
│  (big-endian)    │  (protobuf message)     │
│  length = N      │                         │
└──────────────────┴─────────────────────────┘
```

### Writing a Message

```go
func WriteMessage(w io.Writer, msg proto.Message) error {
    data, err := proto.Marshal(msg)
    if err != nil {
        return err
    }

    // Write 4-byte length prefix
    lenBuf := make([]byte, 4)
    binary.BigEndian.PutUint32(lenBuf, uint32(len(data)))
    if _, err := w.Write(lenBuf); err != nil {
        return err
    }

    // Write message data
    _, err = w.Write(data)
    return err
}
```

### Reading a Message

```go
func ReadMessage(r io.Reader, msg proto.Message) error {
    // Read 4-byte length prefix
    lenBuf := make([]byte, 4)
    if _, err := io.ReadFull(r, lenBuf); err != nil {
        return err
    }

    length := binary.BigEndian.Uint32(lenBuf)
    if length > 1<<20 { // 1MB max
        return errors.New("message too large")
    }

    // Read message data
    data := make([]byte, length)
    if _, err := io.ReadFull(r, data); err != nil {
        return err
    }

    return proto.Unmarshal(data, msg)
}
```

## Session Management

### Default Session Assignment

When you connect, the server automatically assigns a session ID based on your PeerID:

```
Your PeerID: 12D3KooWABCDEFGHIJKL...
Assigned Session: 12D3KooWABCDEFG (first 16 characters)
```

The server sends this as the first response:

```json
{
  "type": "session",
  "session_id": "12D3KooWABCDEFG"
}
```

### Custom Session IDs

To use a specific session (e.g., resuming a previous conversation):

```go
msg := &ChatMessage{
    Id:        uuid.New().String(),
    SessionId: "my-custom-session-id",
    Content:   "Hello!",
}
```

### Session Persistence

Sessions are stored server-side at `~/.kaggen/sessions/kaggen/<user_id>/<session_id>/`. Each session maintains:

- Complete conversation history
- Context for the AI agent
- Thread relationships

### Thread Forking

To create a branch from a specific point in the conversation:

```go
msg := &ChatMessage{
    Id:             uuid.New().String(),
    SessionId:      "original-session",
    Content:        "What if we tried a different approach?",
    ReplyToEventId: "evt_abc123",  // The event ID to branch from
}
```

The server responds with a `thread_created` message containing the new thread's session ID.

## Response Types

The `type` field in `ChatResponse` indicates the kind of response:

| Type | Description | Content Format |
|------|-------------|----------------|
| `session` | Session assignment (first message) | Empty; check `session_id` field |
| `text` | Streaming text chunk | Plain text |
| `done` | Final response marker | Final text (may be empty) |
| `error` | Error occurred | Error message |
| `tool_call` | Agent is calling a tool | JSON: `{"name": "...", "input": {...}}` |
| `tool_result` | Tool execution result | JSON: `{"name": "...", "output": "..."}` |
| `thread_created` | New thread was forked | Empty; check metadata |
| `approval_required` | Human approval needed | JSON with approval details |

### Handling Responses

```go
for {
    var resp ChatResponse
    if err := ReadMessage(stream, &resp); err != nil {
        if err == io.EOF {
            break // Stream closed normally
        }
        return err
    }

    switch resp.Type {
    case "session":
        fmt.Printf("Session: %s\n", resp.SessionId)

    case "text":
        fmt.Print(resp.Content) // Streaming output

    case "done":
        fmt.Println() // Response complete

    case "error":
        fmt.Printf("Error: %s\n", resp.Content)

    case "tool_call":
        var tc struct {
            Name  string         `json:"name"`
            Input map[string]any `json:"input"`
        }
        json.Unmarshal([]byte(resp.Content), &tc)
        fmt.Printf("Tool: %s\n", tc.Name)

    case "tool_result":
        var tr struct {
            Name   string `json:"name"`
            Output string `json:"output"`
        }
        json.Unmarshal([]byte(resp.Content), &tr)
        fmt.Printf("Result from %s\n", tr.Name)

    case "thread_created":
        threadID := string(resp.Metadata["thread_session_id"])
        fmt.Printf("Thread created: %s\n", threadID)

    case "approval_required":
        // Handle human-in-the-loop approval
        handleApprovalRequest(resp)
    }

    if resp.Done {
        break
    }
}
```

### Metadata Fields

Common metadata fields in responses:

| Key | Type | Description |
|-----|------|-------------|
| `thread_session_id` | string | Session ID of newly created thread |
| `parent_session_id` | string | Parent session for thread responses |
| `tool_call_id` | string | ID correlating tool_call with tool_result |
| `event_id` | string | Unique event ID (for reply_to_event_id) |

---

## API Protocols

API protocols use a simple request/response pattern with generic message wrappers. Unlike the chat protocol (which streams multiple responses), API protocols return a single response per request.

### API Message Format

All API protocols use these protobuf messages:

```protobuf
// Request wrapper (Client → Server)
message APIRequest {
  string id = 1;       // Request ID for correlation
  string method = 2;   // Method name (e.g., "list", "get")
  bytes params = 3;    // JSON-encoded parameters
}

// Response wrapper (Server → Client)
message APIResponse {
  string id = 1;       // Correlates to request ID
  bool success = 2;    // True if successful
  bytes data = 3;      // JSON-encoded response data
  string error = 4;    // Error message if !success
}
```

### API Request Flow

```
Client                          Server
   |                               |
   |------ APIRequest ------------>|
   |       method: "list"          |
   |       params: {}              |
   |                               |
   |<----- APIResponse ------------|
   |       success: true           |
   |       data: {...}             |
   |                               |
```

### Generic API Client (Go)

```go
func callAPI(host host.Host, peerID peer.ID, protocol string, method string, params any) (json.RawMessage, error) {
    stream, err := host.NewStream(ctx, peerID, protocol.ID(protocol))
    if err != nil {
        return nil, err
    }
    defer stream.Close()

    // Marshal params
    paramsJSON, _ := json.Marshal(params)

    // Send request
    req := &pb.APIRequest{
        Id:     uuid.New().String(),
        Method: method,
        Params: paramsJSON,
    }
    writeMessage(stream, req)

    // Read response
    var resp pb.APIResponse
    readMessage(stream, &resp)

    if !resp.Success {
        return nil, errors.New(resp.Error)
    }
    return resp.Data, nil
}
```

---

### Sessions Protocol

**Protocol ID:** `/kaggen/sessions/1.0.0`

Manage conversation sessions - list, view history, rename, delete, and archive.

| Method | Parameters | Response |
|--------|------------|----------|
| `list` | `{flat?: bool}` | `{sessions: [...]}` |
| `messages` | `{user_id, session_id, detail?, limit?, offset?}` | `{session_id, name, messages, total}` |
| `rename` | `{user_id, session_id, name}` | `{success: true, name}` |
| `delete` | `{user_id, session_id}` | `{success: true}` |
| `archive` | `{user_id, session_id}` | `{success: true}` |

#### Example: List Sessions

```go
data, _ := callAPI(host, peerID, "/kaggen/sessions/1.0.0", "list", map[string]any{})
var result struct {
    Sessions []struct {
        ID        string `json:"id"`
        Name      string `json:"name"`
        UserID    string `json:"user_id"`
        UpdatedAt string `json:"updated_at"`
    } `json:"sessions"`
}
json.Unmarshal(data, &result)
```

#### Example: Get Chat History

```go
data, _ := callAPI(host, peerID, "/kaggen/sessions/1.0.0", "messages", map[string]any{
    "user_id":    "user123",
    "session_id": "session456",
    "detail":     "chat",  // or "full" for tool calls
    "limit":      50,
})
```

---

### Tasks Protocol

**Protocol ID:** `/kaggen/tasks/1.0.0`

Monitor running tasks and pipelines.

| Method | Parameters | Response |
|--------|------------|----------|
| `list` | `{status?: string}` | `{tasks: [...]}` |
| `cancel` | `{task_id}` | `{success, cancelled, task_id}` |
| `pipelines` | `{}` | `{pipelines: [...]}` |

#### Task Status Values

- `running` - Task is currently executing
- `completed` - Task finished successfully
- `failed` - Task failed with error
- `cancelled` - Task was cancelled
- `pending_approval` - Waiting for human approval

#### Example: List Running Tasks

```go
data, _ := callAPI(host, peerID, "/kaggen/tasks/1.0.0", "list", map[string]any{
    "status": "running",
})
var result struct {
    Tasks []struct {
        ID        string `json:"id"`
        AgentName string `json:"agent_name"`
        Status    string `json:"status"`
        StartedAt string `json:"started_at"`
    } `json:"tasks"`
}
json.Unmarshal(data, &result)
```

---

### Approvals Protocol

**Protocol ID:** `/kaggen/approvals/1.0.0`

Handle human-in-the-loop tool approval requests.

| Method | Parameters | Response |
|--------|------------|----------|
| `list` | `{}` | `{approvals: [...]}` |
| `approve` | `{id, reason?}` | `{success: true, status: "approved", id}` |
| `reject` | `{id, reason?}` | `{success: true, status: "rejected", id}` |

#### Approval Object

```json
{
  "id": "task-123",
  "tool_name": "execute_command",
  "skill_name": "shell",
  "description": "Run: rm -rf /tmp/cache",
  "arguments": "{\"command\": \"rm -rf /tmp/cache\"}",
  "session_id": "session-456",
  "user_id": "user-789",
  "requested_at": "2024-01-15T10:30:00Z",
  "timeout_at": "2024-01-15T10:35:00Z"
}
```

#### Example: Approve a Tool

```go
data, _ := callAPI(host, peerID, "/kaggen/approvals/1.0.0", "approve", map[string]any{
    "id":     "task-123",
    "reason": "Approved by mobile user",
})
```

---

### System Protocol

**Protocol ID:** `/kaggen/system/1.0.0`

Access system status, configuration, skills, and backlog.

| Method | Parameters | Response |
|--------|------------|----------|
| `overview` | `{}` | `{status, uptime, model, tasks, ...}` |
| `config` | `{}` | `{config: {...}}` (sensitive fields redacted) |
| `settings` | `{}` | `{auth_enabled, gateway_bind, p2p, ...}` |
| `skills` | `{}` | `{skills: [{name, description}, ...]}` |
| `backlog` | `{status?, priority?, parent_id?}` | `{items: [...]}` |
| `plan` | `{id}` | `{plan: {...}, subtasks: [...]}` |

#### Example: Get System Overview

```go
data, _ := callAPI(host, peerID, "/kaggen/system/1.0.0", "overview", nil)
var result struct {
    Status           string `json:"status"`
    Uptime           string `json:"uptime"`
    Model            string `json:"model"`
    ConnectedClients int    `json:"connected_clients"`
    InFlightTasks    int    `json:"inflight_tasks"`
    SkillsLoaded     int    `json:"skills_loaded"`
}
json.Unmarshal(data, &result)
```

---

### Secrets Protocol

**Protocol ID:** `/kaggen/secrets/1.0.0`

Manage secrets and authentication tokens.

| Method | Parameters | Response |
|--------|------------|----------|
| `list` | `{}` | `{available, backend?, keys, error?}` |
| `set` | `{key, value}` | `{success: true, key}` |
| `delete` | `{key}` | `{success: true, key}` |
| `tokens` | `{}` | `{tokens: [...]}` |
| `generate_token` | `{name, expires_in?}` | `{success, id, token, name, message}` |
| `revoke_token` | `{id}` | `{success: true, id}` |

#### Example: List Secrets

```go
data, _ := callAPI(host, peerID, "/kaggen/secrets/1.0.0", "list", nil)
var result struct {
    Available bool     `json:"available"`
    Backend   string   `json:"backend"`
    Keys      []string `json:"keys"`
}
json.Unmarshal(data, &result)
```

#### Example: Generate Auth Token

```go
data, _ := callAPI(host, peerID, "/kaggen/secrets/1.0.0", "generate_token", map[string]any{
    "name":       "mobile-app",
    "expires_in": "7d",
})
var result struct {
    ID    string `json:"id"`
    Token string `json:"token"`  // Save this! Cannot be retrieved later
    Name  string `json:"name"`
}
json.Unmarshal(data, &result)
```

---

### Files Protocol

**Protocol ID:** `/kaggen/files/1.0.0`

Download published files from `~/.kaggen/public/`.

| Method | Parameters | Response |
|--------|------------|----------|
| `list` | `{path?}` | `{files: [{name, size, modified, is_dir}, ...]}` |
| `get` | `{path}` | `{filename, mime_type, size, data}` |

**Note:** The `data` field in `get` responses contains raw bytes (base64-encoded in JSON).

#### Example: List Files

```go
data, _ := callAPI(host, peerID, "/kaggen/files/1.0.0", "list", nil)
var result struct {
    Files []struct {
        Name     string `json:"name"`
        Size     int64  `json:"size"`
        Modified string `json:"modified"`
    } `json:"files"`
}
json.Unmarshal(data, &result)
```

#### Example: Download File

```go
data, _ := callAPI(host, peerID, "/kaggen/files/1.0.0", "get", map[string]any{
    "path": "report.pdf",
})
var result struct {
    Filename string `json:"filename"`
    MimeType string `json:"mime_type"`
    Size     int64  `json:"size"`
    Data     []byte `json:"data"`
}
json.Unmarshal(data, &result)
os.WriteFile(result.Filename, result.Data, 0644)
```

---

## Complete Examples

### Go Client Example

```go
package main

import (
    "context"
    "encoding/binary"
    "fmt"
    "io"

    "github.com/google/uuid"
    "github.com/libp2p/go-libp2p"
    "github.com/libp2p/go-libp2p/core/peer"
    "github.com/multiformats/go-multiaddr"
    "google.golang.org/protobuf/proto"

    pb "your-project/proto" // Generated from chat.proto
)

const ChatProtocolID = "/kaggen/chat/1.0.0"

func main() {
    ctx := context.Background()

    // Create libp2p host
    host, _ := libp2p.New()
    defer host.Close()

    // Connect to kaggen server
    serverAddr, _ := multiaddr.NewMultiaddr(
        "/ip4/192.168.1.100/tcp/4001/p2p/12D3KooW...",
    )
    peerInfo, _ := peer.AddrInfoFromP2pAddr(serverAddr)
    host.Connect(ctx, *peerInfo)

    // Open chat stream
    stream, _ := host.NewStream(ctx, peerInfo.ID, ChatProtocolID)
    defer stream.Close()

    // Read session assignment
    var sessionResp pb.ChatResponse
    readMessage(stream, &sessionResp)
    fmt.Printf("Connected to session: %s\n", sessionResp.SessionId)

    // Send a message
    msg := &pb.ChatMessage{
        Id:        uuid.New().String(),
        SessionId: sessionResp.SessionId,
        Content:   "What's the weather like today?",
    }
    writeMessage(stream, msg)

    // Read streaming responses
    for {
        var resp pb.ChatResponse
        if err := readMessage(stream, &resp); err != nil {
            break
        }

        if resp.Type == "text" || resp.Type == "done" {
            fmt.Print(resp.Content)
        }

        if resp.Done {
            fmt.Println()
            break
        }
    }
}

func writeMessage(w io.Writer, msg proto.Message) error {
    data, _ := proto.Marshal(msg)
    lenBuf := make([]byte, 4)
    binary.BigEndian.PutUint32(lenBuf, uint32(len(data)))
    w.Write(lenBuf)
    w.Write(data)
    return nil
}

func readMessage(r io.Reader, msg proto.Message) error {
    lenBuf := make([]byte, 4)
    io.ReadFull(r, lenBuf)
    length := binary.BigEndian.Uint32(lenBuf)
    data := make([]byte, length)
    io.ReadFull(r, data)
    return proto.Unmarshal(data, msg)
}
```

### Dart/Flutter Example

```dart
import 'dart:typed_data';
import 'package:libp2p/libp2p.dart';
import 'package:protobuf/protobuf.dart';
import 'chat.pb.dart'; // Generated from chat.proto

const chatProtocolId = '/kaggen/chat/1.0.0';

class KaggenP2PClient {
  late Libp2pHost host;
  late Stream stream;
  String? sessionId;

  Future<void> connect(String multiaddr) async {
    host = await Libp2pHost.create();
    final peerInfo = PeerInfo.fromMultiaddr(multiaddr);
    await host.connect(peerInfo);
    stream = await host.newStream(peerInfo.peerId, chatProtocolId);

    // Read session assignment
    final sessionResp = await _readMessage();
    sessionId = sessionResp.sessionId;
  }

  Stream<ChatResponse> sendMessage(String content) async* {
    final msg = ChatMessage()
      ..id = Uuid().v4()
      ..sessionId = sessionId ?? ''
      ..content = content;

    await _writeMessage(msg);

    while (true) {
      final resp = await _readMessage();
      yield resp;
      if (resp.done) break;
    }
  }

  Future<void> _writeMessage(ChatMessage msg) async {
    final data = msg.writeToBuffer();
    final lenBuf = ByteData(4)..setUint32(0, data.length, Endian.big);
    await stream.write(lenBuf.buffer.asUint8List());
    await stream.write(data);
  }

  Future<ChatResponse> _readMessage() async {
    final lenBuf = await stream.read(4);
    final length = ByteData.sublistView(lenBuf).getUint32(0, Endian.big);
    final data = await stream.read(length);
    return ChatResponse.fromBuffer(data);
  }
}
```

### Sending an Image Attachment

```go
// Read image file
imageData, _ := os.ReadFile("photo.jpg")

msg := &pb.ChatMessage{
    Id:        uuid.New().String(),
    SessionId: sessionID,
    Content:   "What's in this image?",
    Attachments: []*pb.Attachment{
        {
            Filename: "photo.jpg",
            MimeType: "image/jpeg",
            Data:     imageData,
        },
    },
}
writeMessage(stream, msg)
```

## Error Handling

### Connection Errors

```go
stream, err := host.NewStream(ctx, peerID, ChatProtocolID)
if err != nil {
    switch {
    case errors.Is(err, network.ErrNoConn):
        // Not connected to peer - try reconnecting
    case errors.Is(err, context.DeadlineExceeded):
        // Connection timeout
    default:
        // Other error
    }
}
```

### Protocol Errors

The server sends error responses for protocol-level issues:

```go
if resp.Type == "error" {
    fmt.Printf("Server error: %s\n", resp.Content)
    // Possible errors:
    // - "session not found"
    // - "message too large"
    // - "rate limited"
    // - "unauthorized"
}
```

### Stream Closure

Handle unexpected stream closure:

```go
for {
    var resp pb.ChatResponse
    err := readMessage(stream, &resp)
    if err != nil {
        if err == io.EOF {
            // Normal closure - server done sending
            break
        }
        if errors.Is(err, network.ErrReset) {
            // Stream reset by peer
            break
        }
        // Handle other errors
        return err
    }
    // Process response...
}
```

## Best Practices

### 1. Generate UUIDs for Message IDs

Always provide unique message IDs to enable:
- Response correlation
- Deduplication
- Debugging

```go
msg.Id = uuid.New().String()
```

### 2. Reuse Sessions for Conversations

Store and reuse session IDs to maintain conversation context:

```go
// Save session ID after first connection
prefs.setString("session_id", sessionID)

// Restore on subsequent connections
msg.SessionId = prefs.getString("session_id")
```

### 3. Handle Streaming Responses Incrementally

Don't buffer all responses before displaying:

```go
for {
    resp := readMessage(stream)
    if resp.Type == "text" {
        ui.appendText(resp.Content) // Show immediately
    }
    if resp.Done {
        break
    }
}
```

### 4. Implement Reconnection Logic

P2P connections can be unstable. Implement exponential backoff:

```go
backoff := time.Second
for {
    err := connect(serverAddr)
    if err == nil {
        backoff = time.Second // Reset on success
        handleConnection()
    }
    time.Sleep(backoff)
    backoff = min(backoff*2, time.Minute)
}
```

### 5. Use DHT for Peer Discovery

Instead of hardcoding addresses, use DHT to find the kaggen peer:

```go
// Server publishes to DHT
dht.Provide(ctx, rendezvousKey)

// Client discovers via DHT
peerChan := dht.FindProviders(ctx, rendezvousKey)
for peer := range peerChan {
    host.Connect(ctx, peer)
}
```

### 6. Keep Connections Alive

libp2p handles keepalives at the transport level, but for long-idle connections, consider sending periodic ping messages or reconnecting as needed.

### 7. Validate Server Identity

For production, verify the server's PeerID matches expected values:

```go
expectedPeerID := "12D3KooW..."
if peerInfo.ID.String() != expectedPeerID {
    return errors.New("unexpected peer identity")
}
```

## Troubleshooting

### Cannot Connect

1. **Check firewall**: Ensure UDP/4001 and TCP/4001 are open
2. **Verify multiaddr**: Include the `/p2p/<peer-id>` suffix
3. **Check transports**: Your client must support the same transports (UDX/QUIC or TCP)

### No Response

1. **Check protocol ID**: Must be exactly `/kaggen/chat/1.0.0`
2. **Verify wire format**: 4-byte big-endian length prefix
3. **Check protobuf**: Ensure message serialization matches schema

### Session Not Found

1. Sessions are scoped to user_id and session_id
2. Default user_id is derived from PeerID
3. Explicitly set user_id if using custom session management

## Reference

### Protobuf File Locations

```
internal/p2p/proto/chat.proto   # Chat protocol messages
internal/p2p/proto/api.proto    # API protocol messages (APIRequest, APIResponse)
```

### Generating Client Code

```bash
# Go
protoc --go_out=. --go_opt=paths=source_relative chat.proto api.proto

# Dart
protoc --dart_out=. chat.proto api.proto

# JavaScript
protoc --js_out=. chat.proto api.proto

# Swift
protoc --swift_out=. chat.proto api.proto

# Kotlin
protoc --kotlin_out=. chat.proto api.proto
```

### Related Documentation

- [libp2p Concepts](https://docs.libp2p.io/concepts/)
- [Protocol Buffers](https://developers.google.com/protocol-buffers)
- [Kaggen README](../README.md) - P2P configuration options
- [Third-Party Conversations Integration](thirdparty-conversations-integration.md) - Browse local LLM conversations
