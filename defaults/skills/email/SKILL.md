---
name: email
description: Send, read, and manage emails using the kaggenbot@gmail.com account
tools: [email]
guarded_tools: [email]
oauth_providers: [google]
---

# Email — Gmail Account Access

Use this skill to send and manage emails using the **kaggenbot@gmail.com** account. All email sends require user approval before execution.

## Account Details

- **Email**: kaggenbot@gmail.com
- **Provider**: google
- **Auth**: OAuth 2.0 via Google

## Available Actions

Use the `email` tool with the following actions. Always include:
- `provider`: `"google"`
- `email`: `"kaggenbot@gmail.com"`

### Send Email

Send an email. Requires approval before sending.

```json
{
  "action": "send",
  "provider": "google",
  "email": "kaggenbot@gmail.com",
  "to": ["recipient@example.com"],
  "cc": ["optional-cc@example.com"],
  "subject": "Email subject",
  "body": "Plain text email body"
}
```

**Required fields**: `to`, `subject`, `body`
**Optional fields**: `cc`, `bcc`

### List Emails

List recent emails from a folder.

```json
{
  "action": "list",
  "provider": "google",
  "email": "kaggenbot@gmail.com",
  "folder": "INBOX",
  "limit": 10
}
```

**Optional fields**:
- `folder`: IMAP folder name (default: "INBOX")
- `limit`: Max messages to return (default: 10, max: 50)

### Read Email

Read a specific email by sequence number.

```json
{
  "action": "read",
  "provider": "google",
  "email": "kaggenbot@gmail.com",
  "folder": "INBOX",
  "message_id": 123
}
```

**Required fields**: `message_id` (sequence number from list results)

### Search Emails

Search emails using IMAP search format.

```json
{
  "action": "search",
  "provider": "google",
  "email": "kaggenbot@gmail.com",
  "folder": "INBOX",
  "query": "FROM sender@example.com",
  "limit": 20
}
```

**Common search queries**:
- `FROM sender@example.com` — Emails from a specific sender
- `TO recipient@example.com` — Emails to a specific recipient
- `SUBJECT "meeting"` — Emails with subject containing "meeting"
- `SINCE 01-Jan-2024` — Emails since a date
- `UNSEEN` — Unread emails
- `SEEN` — Read emails

## Gmail Folders

Common Gmail IMAP folder names:
- `INBOX` — Primary inbox
- `[Gmail]/Sent Mail` — Sent messages
- `[Gmail]/Drafts` — Draft messages
- `[Gmail]/Spam` — Spam folder
- `[Gmail]/Trash` — Deleted messages
- `[Gmail]/All Mail` — All messages

## Example Workflows

### Check inbox and summarize

```json
{"action": "list", "provider": "google", "email": "kaggenbot@gmail.com", "limit": 5}
```

Then read specific emails:
```json
{"action": "read", "provider": "google", "email": "kaggenbot@gmail.com", "message_id": 1}
```

### Send a reply

After reading an email, compose and send a response:
```json
{
  "action": "send",
  "provider": "google",
  "email": "kaggenbot@gmail.com",
  "to": ["original-sender@example.com"],
  "subject": "Re: Original subject",
  "body": "Your reply message here..."
}
```

### Find unread emails from a specific sender

```json
{
  "action": "search",
  "provider": "google",
  "email": "kaggenbot@gmail.com",
  "query": "FROM important@company.com UNSEEN"
}
```

## Notes

- All email sends are guarded and require explicit user approval in the dashboard
- OAuth authorization must be completed via the dashboard before use
- **IMPORTANT**: The `https://mail.google.com/` scope is REQUIRED in your config.json. The limited `gmail.send` scope does NOT work with SMTP/IMAP - it only works with the Gmail REST API.
