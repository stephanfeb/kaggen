# OAuth Integration Guide

This guide explains how to configure OAuth providers and build skills that access OAuth-protected APIs like Gmail, Google Calendar, and GitHub.

## Overview

Kaggen's OAuth system enables skills to securely access APIs that require OAuth 2.0 authentication. The key principles:

- **Skills are declarative** - They reference OAuth providers by name, never handling tokens directly
- **Tokens are encrypted** - Stored securely with AES-256-GCM encryption
- **Automatic refresh** - Tokens are refreshed before expiration
- **User-scoped** - Each user authorizes their own accounts

## Quick Start

### 1. Configure an OAuth Provider

Add the provider to `~/.kaggen/config.json`:

```json
{
  "oauth": {
    "providers": {
      "google": {
        "client_id": "secret:google-oauth-client-id",
        "client_secret": "secret:google-oauth-client-secret",
        "scopes": [
          "https://www.googleapis.com/auth/gmail.readonly",
          "https://www.googleapis.com/auth/gmail.send"
        ]
      }
    }
  }
}
```

### 2. Store Client Credentials

Store the OAuth app credentials in the secrets store:

```bash
# Via dashboard (recommended)
# Navigate to Settings > Secrets in the dashboard UI

# Or via environment if dashboard not available
export KAGGEN_MASTER_KEY="your-master-key"
```

### 3. Create a Skill

Create a skill that uses the OAuth provider:

```yaml
---
name: gmail
description: Read and send emails via Gmail API
oauth_providers: [google]
---

# Gmail Skill

You can read emails, search the inbox, and send messages using Gmail API.

## Reading Emails

To list recent emails:
- Call http_request with oauth_provider: google
- URL: https://gmail.googleapis.com/gmail/v1/users/me/messages
- Method: GET
```

### 4. Authorize via Dashboard

1. Open the kaggen dashboard
2. Navigate to **Settings > OAuth Connections**
3. Click **Connect** next to Google
4. Complete the Google authorization flow
5. Return to dashboard - status shows "Connected"

### 5. Use the Skill

Now the skill can make authenticated API calls:

```
User: Check my recent emails
Bot: [Uses gmail skill, calls Gmail API with OAuth token]
```

---

## Detailed Configuration

### Provider Configuration

Each OAuth provider needs these fields:

| Field | Required | Description |
|-------|----------|-------------|
| `client_id` | Yes | OAuth client ID (use `secret:key` reference) |
| `client_secret` | Yes | OAuth client secret (use `secret:key` reference) |
| `scopes` | Yes | List of OAuth scopes to request |
| `auth_url` | No* | Authorization endpoint URL |
| `token_url` | No* | Token exchange endpoint URL |
| `pkce` | No* | Enable PKCE (code challenge) |
| `redirect_uri` | No | Override callback URI |

*Auto-populated for known providers (google, github)

### Known Providers

Google and GitHub have pre-configured endpoints. You only need to provide credentials and scopes:

**Google:**
```json
{
  "google": {
    "client_id": "secret:google-oauth-client-id",
    "client_secret": "secret:google-oauth-client-secret",
    "scopes": ["https://www.googleapis.com/auth/gmail.readonly"]
  }
}
```

**GitHub:**
```json
{
  "github": {
    "client_id": "secret:github-oauth-client-id",
    "client_secret": "secret:github-oauth-client-secret",
    "scopes": ["repo", "read:user"]
  }
}
```

### Custom Providers

For other OAuth 2.0 providers, specify all endpoints:

```json
{
  "slack": {
    "client_id": "secret:slack-client-id",
    "client_secret": "secret:slack-client-secret",
    "auth_url": "https://slack.com/oauth/v2/authorize",
    "token_url": "https://slack.com/api/oauth.v2.access",
    "scopes": ["channels:read", "chat:write"],
    "pkce": false
  }
}
```

### Callback URL

The default callback path is `/api/oauth/callback`. The full URL depends on your gateway configuration:

- **Local development:** `http://localhost:18789/api/oauth/callback`
- **With tunnel:** `https://your-tunnel-url.trycloudflare.com/api/oauth/callback`
- **Production:** `https://your-domain.com/api/oauth/callback`

Register this URL in your OAuth app's allowed redirect URIs.

---

## Building OAuth-Enabled Skills

### Skill Frontmatter

Declare OAuth providers in the skill's `SKILL.md` frontmatter:

```yaml
---
name: my-skill
description: Skill that uses OAuth APIs
oauth_providers: [google, github]
secrets: [api_key]  # Can combine with regular secrets
---
```

### Using http_request with OAuth

The `http_request` tool gains an `oauth_provider` field when OAuth is configured:

```yaml
http_request:
  url: https://api.example.com/endpoint
  method: GET
  oauth_provider: google  # Uses stored OAuth token
```

The tool automatically:
1. Retrieves the user's token from the store
2. Refreshes if near expiration
3. Injects the `Authorization: Bearer <token>` header
4. Returns a helpful message if authorization is needed

### Handling Authorization Required

When a user hasn't authorized yet, the tool returns a message like:

> OAuth authorization required for google. Please authorize via dashboard.

Your skill should handle this gracefully:

```markdown
## When Authorization is Needed

If the user hasn't connected their Google account, inform them:
"I need access to your Gmail. Please connect your Google account
in the kaggen dashboard under Settings > OAuth Connections."
```

### Error Handling

| Scenario | Tool Response |
|----------|---------------|
| No token | "OAuth authorization required for {provider}" |
| Token expired | "OAuth token for {provider} has expired. Please re-authorize" |
| Provider not configured | "OAuth provider {name} not available to this skill" |
| API error | Standard HTTP error with status code and body |

---

## Example: Gmail Integration

This complete example shows how to set up Gmail access.

### Step 1: Create Google OAuth App

1. Go to [Google Cloud Console](https://console.cloud.google.com/)
2. Create a new project or select existing
3. Navigate to **APIs & Services > Credentials**
4. Click **Create Credentials > OAuth client ID**
5. Select **Web application**
6. Add authorized redirect URI: `http://localhost:18789/api/oauth/callback`
7. Copy the Client ID and Client Secret

### Step 2: Enable Gmail API

1. In Google Cloud Console, go to **APIs & Services > Library**
2. Search for "Gmail API"
3. Click **Enable**

### Step 3: Configure Kaggen

Add to `~/.kaggen/config.json`:

```json
{
  "oauth": {
    "providers": {
      "google": {
        "client_id": "secret:google-oauth-client-id",
        "client_secret": "secret:google-oauth-client-secret",
        "scopes": [
          "https://www.googleapis.com/auth/gmail.readonly",
          "https://www.googleapis.com/auth/gmail.send",
          "https://www.googleapis.com/auth/gmail.labels"
        ]
      }
    }
  }
}
```

Store the credentials (via dashboard or directly):

```bash
# Store in secrets (requires KAGGEN_MASTER_KEY)
# Best done via dashboard UI at Settings > Secrets
```

### Step 4: Create Gmail Skill

Create `~/.kaggen/workspace/skills/gmail/SKILL.md`:

```markdown
---
name: gmail
description: Read, search, and send emails via Gmail API
oauth_providers: [google]
---

# Gmail Skill

You can help the user manage their Gmail inbox.

## Capabilities

- List recent emails
- Search for specific emails
- Read email content
- Send new emails
- Manage labels

## API Reference

### List Messages

Get recent messages from inbox:

\`\`\`
http_request:
  url: https://gmail.googleapis.com/gmail/v1/users/me/messages?maxResults=10
  method: GET
  oauth_provider: google
\`\`\`

Response contains message IDs. Use Get Message to retrieve full content.

### Get Message

Get full message content by ID:

\`\`\`
http_request:
  url: https://gmail.googleapis.com/gmail/v1/users/me/messages/{messageId}?format=full
  method: GET
  oauth_provider: google
\`\`\`

### Search Messages

Search with Gmail query syntax:

\`\`\`
http_request:
  url: https://gmail.googleapis.com/gmail/v1/users/me/messages?q=from:boss@company.com+is:unread
  method: GET
  oauth_provider: google
\`\`\`

### Send Message

Send a new email (body must be base64url encoded RFC 2822 message):

\`\`\`
http_request:
  url: https://gmail.googleapis.com/gmail/v1/users/me/messages/send
  method: POST
  oauth_provider: google
  body: {"raw": "<base64url-encoded-email>"}
\`\`\`

### List Labels

Get all labels in the mailbox:

\`\`\`
http_request:
  url: https://gmail.googleapis.com/gmail/v1/users/me/labels
  method: GET
  oauth_provider: google
\`\`\`

## Notes

- Message bodies are base64url encoded
- Use the `payload.parts` array for multipart messages
- The `snippet` field contains a preview of the message
- Query syntax: https://support.google.com/mail/answer/7190

## Authorization

If the user hasn't connected their Google account, they need to:
1. Open the kaggen dashboard
2. Go to Settings > OAuth Connections
3. Click "Connect" next to Google
4. Complete the authorization flow
```

### Step 5: Authorize and Test

1. Start kaggen gateway: `kaggen gateway`
2. Open dashboard: `http://localhost:18789/`
3. Go to Settings > OAuth Connections
4. Click "Connect" next to Google
5. Authorize with your Google account
6. Test: "Show me my recent emails"

---

## Dashboard API Reference

### List Providers

```
GET /api/oauth/providers?user_id=default
```

Response:
```json
{
  "available": true,
  "providers": [
    {"name": "google", "connected": true, "scopes": ["gmail.readonly"]},
    {"name": "github", "connected": false}
  ],
  "user_id": "default"
}
```

### Start Authorization

```
POST /api/oauth/authorize
Content-Type: application/json

{"provider": "google", "user_id": "default"}
```

Response:
```json
{
  "auth_url": "https://accounts.google.com/o/oauth2/v2/auth?...",
  "provider": "google",
  "user_id": "default"
}
```

### Check Status

```
GET /api/oauth/status?provider=google&user_id=default
```

Response:
```json
{
  "provider": "google",
  "user_id": "default",
  "connected": true,
  "expires_at": "2024-01-15T14:30:00Z",
  "scopes": ["gmail.readonly", "gmail.send"]
}
```

### Revoke Token

```
POST /api/oauth/revoke
Content-Type: application/json

{"provider": "google", "user_id": "default"}
```

Response:
```json
{
  "status": "revoked",
  "provider": "google",
  "user_id": "default"
}
```

---

## Security Considerations

### Token Storage

- Tokens are encrypted with AES-256-GCM
- Encryption key derived via Argon2id from `KAGGEN_MASTER_KEY`
- Stored in `~/.kaggen/oauth_tokens.db` (SQLite)
- File permissions set to 0600 (owner only)

### Client Secrets

- Use `secret:key-name` references in config
- Never store raw secrets in config.json
- Secrets resolved at runtime from secure store

### PKCE

- Enabled by default for Google and Microsoft
- Prevents authorization code interception attacks
- Code verifier stored only in memory during flow

### Callback Security

- State parameter validated to prevent CSRF
- Pending flows expire after 10 minutes
- Callback page auto-closes after success

### Scope Limitation

- Request only necessary scopes
- Users see requested scopes during authorization
- Can't request scopes not in config

---

## Troubleshooting

### "OAuth not configured"

- Check `~/.kaggen/config.json` has the `oauth` section
- Ensure provider name matches exactly (case-sensitive)
- Verify KAGGEN_MASTER_KEY is set for encrypted storage

### "OAuth authorization required"

- User hasn't authorized yet
- Token was revoked
- Navigate to dashboard OAuth Connections to authorize

### "OAuth token expired"

- Refresh token is invalid or revoked
- User needs to re-authorize via dashboard
- Check if app was disconnected in provider's account settings

### Callback URL mismatch

- Ensure redirect URI in Google/GitHub app matches exactly
- Include port number if not 80/443
- Check http vs https

### "Provider not available to this skill"

- Skill's `oauth_providers` doesn't include this provider
- Add the provider to skill's frontmatter

---

## Common OAuth Scopes

### Google

| Scope | Description |
|-------|-------------|
| `gmail.readonly` | Read emails |
| `gmail.send` | Send emails |
| `gmail.modify` | Read, send, delete, manage labels |
| `calendar.readonly` | Read calendar events |
| `calendar.events` | Read and write calendar events |
| `drive.readonly` | Read Google Drive files |
| `drive.file` | Access files created by the app |

### GitHub

| Scope | Description |
|-------|-------------|
| `repo` | Full repository access |
| `read:user` | Read user profile |
| `user:email` | Read user email addresses |
| `gist` | Create and read gists |
| `workflow` | Update GitHub Actions workflows |

---

## Related Documentation

- [Protocol Tools Roadmap](./PROTOCOL_TOOLS.md) - Future protocol integrations
- [Skills Guide](./skills-guide.md) - How to write skills
- [Security Guide](./Golang_AI_Bot_Security_Hardening_Guide.md) - Security best practices
