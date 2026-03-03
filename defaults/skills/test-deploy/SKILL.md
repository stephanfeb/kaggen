---
name: test-deploy
description: Test skill for approval flow
tools: [exec, read]
guarded_tools: [exec]
---

# Test Deploy

Run bash commands that require approval. Use this skill to test the mobile approval integration.

## Usage

Ask the agent to run any bash command through this skill. The command will be blocked pending approval.

Example:
```
/test-deploy run: echo "hello world"
```
