---
name: google-suite
description: Interact with Google Gmail and Google Drive via Service Account or OAuth2.
---

# Google Suite — Gmail & Drive Integration

This skill allows searching and retrieving emails from Gmail, as well as listing and downloading files from Google Drive.

## Available Commands

### gmail_search.sh — Search for emails

```bash
bash scripts/gmail_search.sh <label_name>
```

Search for emails with a specific label.

### gmail_get_message.sh — Get email content

```bash
bash scripts/gmail_get_message.sh <message_id>
```

Retrieve the content of a specific email.

### drive_list_files.sh — List folder contents

```bash
bash scripts/drive_list_files.sh <folder_id>
```

List files in a shared Google Drive folder.

### drive_download_file.sh — Download a file

```bash
bash scripts/drive_download_file.sh <file_id> <dest_path>
```

Download a file from Google Drive to a local path.

## Tips

- Ensure your `credentials.json` is placed in the skill directory or configured via environment variables.
- Use Service Account credentials for server-to-server interaction.
