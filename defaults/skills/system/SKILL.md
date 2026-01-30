---
name: system
description: A skill to manage the AI assistant's system, such as reloading components.
---

# System Management

Use this skill to perform system-level operations on the agent itself.

## Available Commands

### reload.sh — Reload Skills

Triggers a dynamic reload of the agent's skill repository. This allows newly created skills to become active without restarting the entire application.

```bash
bash scripts/reload.sh
```
