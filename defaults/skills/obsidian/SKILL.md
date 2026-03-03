---
name: obsidian
description: Interact with your Obsidian vault via the Local REST API
secrets: [obsidian-api-key]
---

# Obsidian — Local REST API Integration

This skill allows Kaggen to interact with an Obsidian vault using the Obsidian Local REST API plugin. It supports listing files, reading/writing notes, searching, and triggering Obsidian commands.

**Prerequisites**: Obsidian must be running with the Local REST API plugin enabled.

## Configuration

- **Base URL**: `https://127.0.0.1:27124`
- **Auth**: Bearer token via `auth_secret: obsidian-api-key`
- **TLS**: Uses `insecure: true` for self-signed certificate

## Operations

### List Files and Folders

List files in the vault root or a specific path.

```
http_request:
  url: https://127.0.0.1:27124/vault/
  method: GET
  auth_secret: obsidian-api-key
  insecure: true
```

To list a specific folder, append the path with trailing slash:
```
http_request:
  url: https://127.0.0.1:27124/vault/Projects/
  method: GET
  auth_secret: obsidian-api-key
  insecure: true
```

### Read File Content

Read the content of a note or file.

```
http_request:
  url: https://127.0.0.1:27124/vault/{path}
  method: GET
  auth_secret: obsidian-api-key
  insecure: true
```

**Example** - Read `notes/meeting.md`:
```
http_request:
  url: https://127.0.0.1:27124/vault/notes/meeting.md
  method: GET
  auth_secret: obsidian-api-key
  insecure: true
```

### Write/Create File

Create a new file or overwrite an existing one.

```
http_request:
  url: https://127.0.0.1:27124/vault/{path}
  method: PUT
  auth_secret: obsidian-api-key
  insecure: true
  content_type: text/markdown
  body: |
    # My Note

    Content here...
```

### Append to File

Append content to the end of an existing file.

```
http_request:
  url: https://127.0.0.1:27124/vault/{path}
  method: POST
  auth_secret: obsidian-api-key
  insecure: true
  content_type: text/markdown
  body: |

    ## New Section
    Additional content...
```

### Search Vault

Search for text across all notes in the vault.

```
http_request:
  url: https://127.0.0.1:27124/search/simple/
  method: POST
  auth_secret: obsidian-api-key
  insecure: true
  body: {"query": "search term"}
```

### Execute Obsidian Command

Trigger an Obsidian command by its ID.

```
http_request:
  url: https://127.0.0.1:27124/commands/{commandId}/
  method: POST
  auth_secret: obsidian-api-key
  insecure: true
```

**Example** - Open settings:
```
http_request:
  url: https://127.0.0.1:27124/commands/app:open-settings/
  method: POST
  auth_secret: obsidian-api-key
  insecure: true
```

### Open Daily Note

Access the current daily note (requires Periodic Notes plugin).

```
http_request:
  url: https://127.0.0.1:27124/periodic/daily/
  method: POST
  auth_secret: obsidian-api-key
  insecure: true
```

## Tips

- Use forward slashes `/` for paths
- File paths should include the extension (e.g., `folder/note.md`)
- Directory paths should end with `/` for listing
- Command IDs can be found in Obsidian settings or via the command palette
- Common commands: `app:open-settings`, `app:reload`, `daily-notes:goto-today`

## Setup

1. Install the "Local REST API" plugin in Obsidian
2. Enable the plugin and copy the API key
3. Add the secret via the Kaggen dashboard:
   - Key: `obsidian-api-key`
   - Value: Your API key from the plugin settings
