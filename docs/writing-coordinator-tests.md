# Writing Coordinator Tests (V2)

A comprehensive guide to writing evaluation tests for kaggen's coordinator behavior.

## Table of Contents

- [Overview](#overview)
- [Understanding the Architecture](#understanding-the-architecture)
- [Test Case Structure](#test-case-structure)
- [Assertion Types](#assertion-types)
- [Writing Test Skills](#writing-test-skills)
- [Test Categories](#test-categories)
- [Best Practices](#best-practices)
- [Debugging Failing Tests](#debugging-failing-tests)
- [Complete Examples](#complete-examples)

---

## Overview

Coordinator tests (V2) evaluate the **production kaggen system** — not a simplified agent, but the full coordinator + skills architecture. These tests verify:

| Behavior | What It Tests |
|----------|---------------|
| **Skill Selection** | Does the coordinator pick the right skill for each task? |
| **Clarification** | Does the coordinator ask for clarification when instructions are ambiguous? |
| **Delegation** | Does the coordinator delegate appropriately (async vs sync)? |
| **Reasoning** | Does the coordinator plan and synthesize multi-step solutions? |

### Why Coordinator Tests Matter

Kaggen is an **open-ended personal assistant**, not a simple tool-calling agent. The coordinator:

- Receives natural language instructions from users
- Assesses the instruction and selects from available skills
- Asks for clarification when instructions are ambiguous
- Delegates to sub-agents (skills) for execution
- Synthesizes results and responds to the user

Testing only tool calling misses the core intelligence of the system.

### Running Coordinator Tests

```bash
# Run all coordinator tests
kaggen eval -s testdata/eval/coordinator --coordinator --skills testdata/eval/skills

# Run specific category
kaggen eval -s testdata/eval/coordinator --coordinator --category skill_selection

# Run specific test case
kaggen eval -s testdata/eval/coordinator --coordinator --case skill-001

# Verbose output for debugging
kaggen eval -s testdata/eval/coordinator --coordinator -v
```

---

## Understanding the Architecture

Before writing tests, understand how kaggen works:

```
┌─────────────────────────────────────────────────────────────┐
│                         User                                │
│                    "Calculate 15 * 23"                      │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                      Coordinator                            │
│  • Assesses instruction                                     │
│  • Selects skill: "calculator"                              │
│  • Delegates via dispatch_task                              │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                   Calculator Skill                          │
│  • Has tools: [exec]                                        │
│  • Runs: python3 -c "print(15 * 23)"                        │
│  • Returns: 345                                             │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                      Coordinator                            │
│  • Receives result from skill                               │
│  • Synthesizes response                                     │
│  • Returns: "15 multiplied by 23 equals 345"                │
└─────────────────────────────────────────────────────────────┘
```

### Key Concepts

| Concept | Description |
|---------|-------------|
| **Coordinator** | The orchestrating agent that receives user instructions and delegates to skills |
| **Skill** | A specialized sub-agent with specific tools and instructions (defined in `SKILL.md`) |
| **dispatch_task** | The tool the coordinator uses to delegate work to skills |
| **Clarification** | When the coordinator asks the user for more information |

---

## Test Case Structure

Test cases are defined in YAML files. There are two formats: **single-turn** (simple) and **multi-turn** (conversational).

### Single-Turn Format (Default)

```yaml
- id: "unique-id"                    # Required: Unique identifier
  name: "Human-readable name"        # Required: Descriptive name
  description: "Longer description"  # Optional: What this tests
  category: "skill_selection"        # Optional: Category for filtering
  user_message: "The user input"     # Required: What the user says
  timeout: 2m                        # Optional: Override default timeout
  context:                           # Optional: Test environment setup
    files:                           # Files to create in workspace
      "path/to/file.txt": "content"
    env:                             # Environment variables
      KEY: "value"
  assert:                            # Required: List of assertions
    - type: skill-selected
      skill: calculator
      required: true
```

### Multi-Turn Format (Conversational)

For testing conversations where the coordinator may ask for clarification:

```yaml
- id: "context-001"
  name: "Multi-turn clarification flow"
  description: "Coordinator asks clarification, then answers"
  category: context
  context:
    files:
      "config.yaml": |
        server:
          port: 8080
          debug: true
  turns:
    - user: "Is debug mode enabled in the config?"
      assert:
        - type: asked-clarification
          required: true
    - user: "config.yaml"
      assert:
        - type: llm-rubric
          rubric: "Response identifies debug mode is enabled"
          min_score: 0.7
```

**How multi-turn works:**
1. Each turn sends a user message and waits for the coordinator to respond
2. Assertions are evaluated after each turn completes
3. If a turn's assertions fail, the test stops (subsequent turns are not executed)
4. The session context is preserved across turns (the coordinator remembers previous messages)

**When to use multi-turn:**
- Testing clarification flows (coordinator asks a question, user provides answer)
- Testing conversational context (follow-up questions)
- Testing multi-step interactions where the user guides the coordinator

### Field Reference

| Field | Required | Type | Description |
|-------|----------|------|-------------|
| `id` | Yes | string | Unique identifier (e.g., "skill-001") |
| `name` | Yes | string | Human-readable test name |
| `description` | No | string | Detailed description of what's being tested |
| `category` | No | string | Category for filtering (e.g., "skill_selection") |
| `user_message` | * | string | The instruction sent to the coordinator (single-turn) |
| `turns` | * | list | List of conversation turns (multi-turn) |
| `turns[].user` | Yes | string | User message for this turn |
| `turns[].assert` | No | list | Assertions to run after this turn |
| `timeout` | No | duration | Test timeout (default: 5m) |
| `context.files` | No | map | Files to create in the test workspace |
| `context.env` | No | map | Environment variables to set |
| `assert` | * | list | Assertions to evaluate (single-turn) |

\* Either `user_message` + `assert` (single-turn) OR `turns` (multi-turn) is required

---

## Assertion Types

### skill-selected

Tests whether the coordinator selected (or avoided) a specific skill.

```yaml
# Skill MUST be selected
- type: skill-selected
  skill: calculator
  required: true

# Skill must NOT be selected
- type: skill-selected
  skill: file_writer
  forbidden: true
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `skill` | string | — | The skill name to check |
| `required` | bool | true | Skill must be selected |
| `forbidden` | bool | false | Skill must NOT be selected |

**When to use:**
- Testing skill selection logic
- Verifying the coordinator doesn't use inappropriate skills
- Ensuring direct responses for questions that don't need skills

### asked-clarification

Tests whether the coordinator asked for clarification.

```yaml
# MUST ask for clarification
- type: asked-clarification
  required: true

# Must NOT ask for clarification
- type: asked-clarification
  forbidden: true

# MUST ask about a specific topic
- type: asked-clarification
  required: true
  about: "which file"

# Accept either behavior (pass whether or not clarification was asked)
- type: asked-clarification
  optional: true
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `required` | bool | true | Must ask for clarification |
| `forbidden` | bool | false | Must NOT ask for clarification |
| `optional` | bool | false | Pass either way (for variable LLM behavior) |
| `about` | string | "" | Clarification must mention this topic (case-insensitive) |

**When to use:**
- Testing ambiguous instruction handling
- Verifying clear instructions proceed without interruption
- Ensuring clarification questions are relevant
- Use `optional: true` when LLM behavior varies (may or may not ask for clarification)

### contains

Tests whether the final response contains a string.

```yaml
- type: contains
  value: "345"

# Case-insensitive
- type: contains
  value: "success"
  case_insensitive: true
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `value` | string | — | String that must appear in response |
| `case_insensitive` | bool | false | Ignore case when matching |

### not-contains

Tests that the final response does NOT contain a string.

```yaml
- type: not-contains
  value: "error"

- type: not-contains
  value: "failed"
  case_insensitive: true
```

### regex

Tests whether the response matches a regular expression.

```yaml
- type: regex
  value: "\\d{3}"  # Contains 3 digits

- type: regex
  value: "^Success:"  # Starts with "Success:"
```

### tool-called

Tests whether a specific tool was called with expected parameters.

```yaml
- type: tool-called
  tool: read
  params:
    path:
      contains: "config.yaml"

- type: tool-called
  tool: exec
  params:
    command:
      regex: "python.*print"
```

| Field | Type | Description |
|-------|------|-------------|
| `tool` | string | Tool name to check |
| `params` | map | Parameter matchers (contains, equals, regex) |

### tool-sequence

Tests that tools were called in a specific order.

```yaml
- type: tool-sequence
  sequence: ["read", "write"]
  strict: false  # Other tools can appear between
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `sequence` | list | — | Ordered list of tool names |
| `strict` | bool | false | If true, no other tools between |

### llm-rubric

Uses LLM-as-judge to evaluate response quality.

```yaml
- type: llm-rubric
  rubric: "Response correctly identifies 8080 as the configured port and explains it's for the server"
  min_score: 0.7

- type: llm-rubric
  rubric: |
    The response should:
    1. Acknowledge the user's request
    2. Explain what was done
    3. Provide the calculated result
  min_score: 0.8
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `rubric` | string | — | Evaluation criteria for the judge |
| `min_score` | float | 0.7 | Minimum passing score (0.0-1.0) |

**When to use:**
- Evaluating response quality beyond simple string matching
- Testing explanation clarity
- Verifying comprehensive responses

---

## Writing Test Skills

Test skills are minimal skill definitions used specifically for evaluation. They should be:

- **Simple**: Easy to understand and predict
- **Deterministic**: Produce consistent results
- **Focused**: Do one thing well

### Skill File Structure

```markdown
<!-- testdata/eval/skills/skill_name/SKILL.md -->
---
name: skill_name
description: One-line description of what this skill does
tools: [tool1, tool2]
---

Instructions for the skill agent.
```

### Example Test Skills

#### Calculator

```markdown
---
name: calculator
description: Performs mathematical calculations
tools: [exec]
---

You are a calculator assistant. Perform mathematical calculations as requested.

## How to Calculate

Use Python for calculations:

```bash
python3 -c "print(expression)"
```

## Guidelines

- Return only the numeric result
- For complex expressions, break them down step by step
- Always verify your calculation is correct
```

#### File Reader

```markdown
---
name: file_reader
description: Reads and summarizes file contents
tools: [read]
---

You are a file reading assistant. Read files and extract or summarize information.

## Guidelines

- Use the read tool to view file contents
- When asked to summarize, provide a concise summary
- When asked for specific information, extract exactly what was requested
- Report clearly if a file does not exist
```

#### File Writer

```markdown
---
name: file_writer
description: Creates or modifies files
tools: [read, write]
---

You are a file writing assistant. Create or modify files as requested.

## Guidelines

- Always read a file before modifying it
- Use the write tool to create new files or update existing ones
- Preserve existing content structure when modifying
- Report what changes were made
```

#### Summarizer

```markdown
---
name: summarizer
description: Summarizes content and provides analysis
tools: [read]
---

You are a summarization assistant. Read content and provide clear, concise summaries.

## Guidelines

- Read the content thoroughly before summarizing
- Identify key points and main themes
- Provide structured summaries with clear sections
- Highlight important findings or conclusions
```

### Skills Directory Structure

```
testdata/eval/skills/
├── calculator/
│   └── SKILL.md
├── file_reader/
│   └── SKILL.md
├── file_writer/
│   └── SKILL.md
└── summarizer/
    └── SKILL.md
```

---

## Test Categories

Organize tests into categories for clarity and selective execution.

### Skill Selection (`skill_selection`)

Tests whether the coordinator picks the appropriate skill for each task.

```yaml
# testdata/eval/coordinator/skill_selection.yaml

- id: "skill-001"
  name: "Select calculator for math"
  category: skill_selection
  user_message: "What is 15 multiplied by 23?"
  assert:
    - type: skill-selected
      skill: calculator
      required: true
    - type: contains
      value: "345"

- id: "skill-002"
  name: "Select file_reader for file inspection"
  category: skill_selection
  user_message: "Read config.yaml and tell me the port number"
  context:
    files:
      "config.yaml": |
        server:
          port: 8080
  assert:
    - type: skill-selected
      skill: file_reader
      required: true
    - type: contains
      value: "8080"

- id: "skill-003"
  name: "Handle directly for knowledge questions"
  category: skill_selection
  user_message: "What is the capital of France?"
  assert:
    - type: contains
      value: "Paris"
    - type: skill-selected
      skill: calculator
      forbidden: true
    - type: skill-selected
      skill: file_reader
      forbidden: true
```

### Clarification (`clarification`)

Tests whether the coordinator asks for clarification when appropriate.

```yaml
# testdata/eval/coordinator/clarification.yaml

- id: "clarify-001"
  name: "Ask clarification for ambiguous file"
  category: clarification
  user_message: "Update the config file"
  context:
    files:
      "config.yaml": "port: 8080"
      "config.json": '{"port": 8080}'
      "config.toml": "port = 8080"
  assert:
    - type: asked-clarification
      required: true
      about: "which"

- id: "clarify-002"
  name: "Ask clarification for incomplete request"
  category: clarification
  user_message: "Create a new file"
  assert:
    - type: asked-clarification
      required: true

- id: "clarify-003"
  name: "Don't ask for clear file read"
  category: clarification
  user_message: "Read README.md and tell me what it says"
  context:
    files:
      "README.md": "# Hello World\nThis is a test."
  assert:
    - type: asked-clarification
      forbidden: true
    - type: skill-selected
      skill: file_reader
      required: true

- id: "clarify-004"
  name: "Don't ask for clear math"
  category: clarification
  user_message: "Calculate 100 divided by 4"
  assert:
    - type: asked-clarification
      forbidden: true
    - type: contains
      value: "25"
```

### Delegation (`delegation`)

Tests delegation patterns and multi-skill coordination.

```yaml
# testdata/eval/coordinator/delegation.yaml

- id: "delegate-001"
  name: "Single skill delegation"
  category: delegation
  user_message: "What is 2 + 2?"
  assert:
    - type: skill-selected
      skill: calculator
      required: true
    - type: contains
      value: "4"

- id: "delegate-002"
  name: "Read then summarize"
  category: delegation
  user_message: "Read the README and give me a brief summary"
  context:
    files:
      "README.md": |
        # Project X

        A web application for managing tasks.

        ## Features
        - Task creation
        - Due dates
        - Notifications
  assert:
    - type: skill-selected
      skill: file_reader
      required: true
    - type: llm-rubric
      rubric: "Summary mentions it's a task management web application with features like task creation, due dates, and notifications"
      min_score: 0.7
```

### Reasoning (`reasoning`)

Tests multi-step planning and problem-solving.

```yaml
# testdata/eval/coordinator/reasoning.yaml

- id: "reason-001"
  name: "Multi-step calculation"
  category: reasoning
  user_message: "Calculate the total cost: 3 items at $15 each, plus 8% tax"
  assert:
    - type: skill-selected
      skill: calculator
      required: true
    - type: contains
      value: "48.6"
    - type: llm-rubric
      rubric: "Shows the calculation steps: base cost ($45), tax calculation (8%), and final total ($48.60)"
      min_score: 0.6

- id: "reason-002"
  name: "File analysis with context"
  category: reasoning
  user_message: "Look at the config file and tell me if the server is set up for production or development"
  context:
    files:
      "config.yaml": |
        server:
          port: 8080
          debug: true
          log_level: debug
          environment: development
  assert:
    - type: skill-selected
      skill: file_reader
      required: true
    - type: contains
      value: "development"
    - type: llm-rubric
      rubric: "Correctly identifies it's a development configuration, citing evidence like debug=true or environment=development"
      min_score: 0.7
```

---

## Best Practices

### 1. Use Unique, Descriptive IDs

```yaml
# Good
- id: "skill-calc-basic-multiplication"
- id: "clarify-ambiguous-file-multiple-configs"

# Bad
- id: "test1"
- id: "skill-001"  # OK but less descriptive
```

### 2. Test One Behavior at a Time

```yaml
# Good: Focused test
- id: "skill-001"
  name: "Select calculator for basic math"
  user_message: "What is 5 + 3?"
  assert:
    - type: skill-selected
      skill: calculator
      required: true

# Bad: Testing too many things
- id: "skill-001"
  name: "Math and file operations"
  user_message: "Calculate 5 + 3 and save to result.txt"
  assert:
    - type: skill-selected
      skill: calculator
      required: true
    - type: skill-selected
      skill: file_writer
      required: true
    - type: contains
      value: "8"
    - type: tool-called
      tool: write
```

### 3. Use Realistic User Messages

```yaml
# Good: Natural language
- user_message: "What's 15 times 23?"
- user_message: "Read the config file and tell me the port"
- user_message: "Update the config file"  # Intentionally ambiguous

# Bad: Overly formal or robotic
- user_message: "Execute multiplication operation: 15 * 23"
- user_message: "Invoke file_reader skill on config.yaml"
```

### 4. Provide Minimal Context Files

Only include files necessary for the test:

```yaml
# Good: Only what's needed
context:
  files:
    "config.yaml": "port: 8080"

# Bad: Unnecessary complexity
context:
  files:
    "config.yaml": |
      # Server Configuration
      # Last updated: 2024-01-15
      # Author: John Doe

      server:
        port: 8080
        host: localhost
        timeout: 30s
        max_connections: 100

      database:
        host: localhost
        port: 5432
        # ... 50 more lines
```

### 5. Combine Assertions Wisely

Use multiple assertions to thoroughly validate behavior:

```yaml
assert:
  # Check the right skill was used
  - type: skill-selected
    skill: calculator
    required: true

  # Check the answer is present
  - type: contains
    value: "345"

  # Check no errors
  - type: not-contains
    value: "error"

  # Check quality of explanation
  - type: llm-rubric
    rubric: "Explains that 15 * 23 = 345"
    min_score: 0.7
```

### 6. Test Edge Cases

```yaml
# Empty input handling
- id: "edge-empty-file"
  user_message: "Read the config file"
  context:
    files:
      "config.yaml": ""
  assert:
    - type: skill-selected
      skill: file_reader
      required: true
    - type: not-contains
      value: "error"

# Missing file handling
- id: "edge-missing-file"
  user_message: "Read nonexistent.txt"
  assert:
    - type: skill-selected
      skill: file_reader
      required: true
    - type: llm-rubric
      rubric: "Clearly indicates the file does not exist"
      min_score: 0.8
```

### 7. Use `about` for Clarification Tests

```yaml
# Good: Specific about what clarification should mention
- type: asked-clarification
  required: true
  about: "which file"

# Less specific but still valid
- type: asked-clarification
  required: true
```

---

## Debugging Failing Tests

### Enable Verbose Output

```bash
kaggen eval -s testdata/eval/coordinator --coordinator -v --case skill-001
```

### Use Execution Traces

For deeper debugging, write execution traces to a directory:

```bash
# Write traces to ./traces directory
kaggen eval -s testdata/eval/coordinator --coordinator --trace ./traces --case skill-001

# View the trace
cat traces/skill-001.json | jq '.execution_trace'
```

Traces include:
- Every text response from the coordinator
- Every tool call with arguments
- Turn counts and timestamps
- Final output and all tool calls

**Example trace analysis:**
```bash
# See which tools were called
cat traces/skill-001.json | jq '.tool_calls[].name'

# Check for repeated tool calls (looping)
cat traces/skill-001.json | jq '.execution_trace[] | select(.type == "tool_call") | .tool_name' | sort | uniq -c

# View the final output
cat traces/skill-001.json | jq '.final_output'
```

### Control Test Timeout

Use `--timeout` to control per-test timeout (default is 5 minutes):

```bash
# Short timeout for quick iteration
kaggen eval -s testdata/eval/coordinator --coordinator --timeout 30s --case skill-001

# Longer timeout for complex tests
kaggen eval -s testdata/eval/coordinator --coordinator --timeout 2m
```

### Check the Observer Data

The runner tracks:
- Which skills were dispatched (`SkillsDispatched`)
- What clarification questions were asked (`Clarifications`)
- All responses (`AllResponses`)

### Common Failure Patterns

#### "coordinator should have asked for clarification but didn't"

**Cause:** The instruction was clear enough that the coordinator proceeded.

**Fix:** Make the instruction more ambiguous:
```yaml
# Too clear
user_message: "Update config.yaml with port 9000"

# More ambiguous
user_message: "Update the config"
```

#### "skill X was not selected"

**Cause:** The coordinator either handled directly or chose a different skill.

**Debug:** Check if:
1. The skill exists in `testdata/eval/skills/`
2. The skill's description matches the task
3. Another skill might be more appropriate
4. The coordinator used its own tools (e.g., `read`, `ls`) instead of delegating

**Note on tool names:** The underlying framework may prefix tool names (e.g., `team-members-kaggen_calculator` instead of `calculator`). The eval system automatically handles this by matching skill names as suffixes of tool names.

#### "response doesn't contain X"

**Cause:** The skill or coordinator didn't include expected content.

**Fix:**
1. Check the skill's instructions
2. Verify the calculation or operation
3. Consider using `llm-rubric` for flexible matching

### Isolate the Problem

1. Run just the failing test:
   ```bash
   kaggen eval --coordinator --case failing-001 -v
   ```

2. Check skill definitions:
   ```bash
   cat testdata/eval/skills/calculator/SKILL.md
   ```

3. Test the skill directly (if possible)

---

## Complete Examples

### Comprehensive Skill Selection Test Suite

```yaml
# testdata/eval/coordinator/skill_selection.yaml

# Math operations → calculator
- id: "skill-calc-addition"
  name: "Calculator for addition"
  category: skill_selection
  user_message: "What is 42 + 58?"
  assert:
    - type: skill-selected
      skill: calculator
      required: true
    - type: contains
      value: "100"

- id: "skill-calc-multiplication"
  name: "Calculator for multiplication"
  category: skill_selection
  user_message: "Calculate 15 times 23"
  assert:
    - type: skill-selected
      skill: calculator
      required: true
    - type: contains
      value: "345"

- id: "skill-calc-complex"
  name: "Calculator for complex expression"
  category: skill_selection
  user_message: "What is (10 + 5) * 3?"
  assert:
    - type: skill-selected
      skill: calculator
      required: true
    - type: contains
      value: "45"

# File reading → file_reader
- id: "skill-read-yaml"
  name: "File reader for YAML"
  category: skill_selection
  user_message: "Show me what's in config.yaml"
  context:
    files:
      "config.yaml": "port: 8080"
  assert:
    - type: skill-selected
      skill: file_reader
      required: true
    - type: contains
      value: "8080"

- id: "skill-read-json"
  name: "File reader for JSON"
  category: skill_selection
  user_message: "Read the package.json file"
  context:
    files:
      "package.json": '{"name": "test", "version": "1.0.0"}'
  assert:
    - type: skill-selected
      skill: file_reader
      required: true

# File writing → file_writer
- id: "skill-write-new"
  name: "File writer for creating files"
  category: skill_selection
  user_message: "Create a file called hello.txt with 'Hello World' in it"
  assert:
    - type: skill-selected
      skill: file_writer
      required: true

# Summarization → summarizer
- id: "skill-summarize"
  name: "Summarizer for content analysis"
  category: skill_selection
  user_message: "Summarize the README.md file"
  context:
    files:
      "README.md": |
        # My Project
        This is a test project for demonstration.
        ## Features
        - Feature A
        - Feature B
  assert:
    - type: skill-selected
      skill: summarizer
      required: true

# Direct response (no skill needed)
- id: "skill-direct-knowledge"
  name: "Direct response for general knowledge"
  category: skill_selection
  user_message: "What is the capital of Japan?"
  assert:
    - type: contains
      value: "Tokyo"
    - type: skill-selected
      skill: calculator
      forbidden: true
    - type: skill-selected
      skill: file_reader
      forbidden: true
    - type: skill-selected
      skill: file_writer
      forbidden: true

- id: "skill-direct-greeting"
  name: "Direct response for greetings"
  category: skill_selection
  user_message: "Hello, how are you?"
  assert:
    - type: skill-selected
      skill: calculator
      forbidden: true
    - type: skill-selected
      skill: file_reader
      forbidden: true
```

### Comprehensive Clarification Test Suite

```yaml
# testdata/eval/coordinator/clarification.yaml

# Should ask clarification
- id: "clarify-multiple-files"
  name: "Ask which file when multiple exist"
  category: clarification
  user_message: "Update the config file"
  context:
    files:
      "config.yaml": "a: 1"
      "config.json": '{"a": 1}'
  assert:
    - type: asked-clarification
      required: true
      about: "which"

- id: "clarify-incomplete-create"
  name: "Ask what to create"
  category: clarification
  user_message: "Create a new file"
  assert:
    - type: asked-clarification
      required: true

- id: "clarify-vague-task"
  name: "Ask for details on vague task"
  category: clarification
  user_message: "Fix it"
  assert:
    - type: asked-clarification
      required: true

- id: "clarify-missing-value"
  name: "Ask what value to update"
  category: clarification
  user_message: "Update the port in the config"
  context:
    files:
      "config.yaml": "port: 8080"
  assert:
    - type: asked-clarification
      required: true

- id: "clarify-save-what"
  name: "Ask what to save"
  category: clarification
  user_message: "Save this information"
  assert:
    - type: asked-clarification
      required: true

# Should NOT ask clarification
- id: "no-clarify-clear-read"
  name: "Don't ask for clear file read"
  category: clarification
  user_message: "Read README.md and show me its contents"
  context:
    files:
      "README.md": "# Test"
  assert:
    - type: asked-clarification
      forbidden: true
    - type: skill-selected
      skill: file_reader
      required: true

- id: "no-clarify-clear-math"
  name: "Don't ask for clear math"
  category: clarification
  user_message: "What is 100 / 4?"
  assert:
    - type: asked-clarification
      forbidden: true
    - type: contains
      value: "25"

- id: "no-clarify-specific-create"
  name: "Don't ask when file creation is specific"
  category: clarification
  user_message: "Create a file called notes.txt with the text 'Meeting notes'"
  assert:
    - type: asked-clarification
      forbidden: true
    - type: skill-selected
      skill: file_writer
      required: true

- id: "no-clarify-single-config"
  name: "Don't ask when only one config file exists"
  category: clarification
  user_message: "Read the config file"
  context:
    files:
      "config.yaml": "port: 8080"
  assert:
    - type: asked-clarification
      forbidden: true
    - type: skill-selected
      skill: file_reader
      required: true
```

---

## Multi-Turn Conversation Examples

Multi-turn tests are powerful for testing conversational flows where the coordinator may ask for clarification or require follow-up information.

### Basic Clarification Flow

```yaml
# Test that coordinator asks for clarification, then answers correctly
- id: "multi-clarify-001"
  name: "Clarification then answer"
  category: multi_turn
  context:
    files:
      "config.yaml": |
        debug: true
        port: 8080
  turns:
    - user: "Is debug mode enabled in the config?"
      assert:
        - type: asked-clarification
          required: true
          about: "which"
    - user: "config.yaml"
      assert:
        - type: contains
          value: "debug"
        - type: llm-rubric
          rubric: "Response indicates debug mode is enabled or true"
          min_score: 0.7
```

### Multi-Step Task with Confirmation

```yaml
# Test a multi-step interaction
- id: "multi-step-001"
  name: "Create file with confirmation"
  category: multi_turn
  turns:
    - user: "Create a new configuration file"
      assert:
        - type: asked-clarification
          required: true
    - user: "Name it app-config.yaml with port 3000"
      assert:
        - type: skill-selected
          skill: file_writer
          required: true
    - user: "What port did you configure?"
      assert:
        - type: contains
          value: "3000"
```

### Context-Aware Follow-Up

```yaml
# Test that coordinator remembers previous context
- id: "multi-context-001"
  name: "Context-aware follow-up"
  category: multi_turn
  context:
    files:
      "users.json": |
        [
          {"name": "Alice", "role": "admin"},
          {"name": "Bob", "role": "user"}
        ]
  turns:
    - user: "Read the users.json file"
      assert:
        - type: contains
          value: "Alice"
        - type: contains
          value: "Bob"
    - user: "Who is the admin?"
      assert:
        - type: contains
          value: "Alice"
        - type: llm-rubric
          rubric: "Response correctly identifies Alice as the admin based on previously read file"
          min_score: 0.8
```

### Error Recovery Flow

```yaml
# Test handling of errors with user guidance
- id: "multi-error-001"
  name: "Handle missing file then retry"
  category: multi_turn
  context:
    files:
      "backup-config.yaml": "port: 9000"
  turns:
    - user: "Read the main config file"
      assert:
        - type: llm-rubric
          rubric: "Response indicates the file was not found or doesn't exist"
          min_score: 0.7
    - user: "Try backup-config.yaml instead"
      assert:
        - type: contains
          value: "9000"
```

### Best Practices for Multi-Turn Tests

1. **Keep conversations focused**: Each multi-turn test should verify one conversational pattern
2. **Use meaningful assertions per turn**: Don't just check the final turn - verify intermediate states
3. **Test the happy path and error cases**: Multi-turn is great for testing error recovery
4. **Consider context preservation**: The coordinator should remember what was discussed

---

## Summary

Writing effective coordinator tests requires understanding:

1. **Kaggen's architecture**: Coordinator + Skills, not simple tool calling
2. **Test formats**: Single-turn (simple) and multi-turn (conversational) YAML formats
3. **Assertion types**: `skill-selected`, `asked-clarification`, `contains`, `llm-rubric`, etc.
4. **Test skills**: Minimal, focused skill definitions
5. **Categories**: Skill selection, clarification, delegation, reasoning
6. **Multi-turn tests**: For clarification flows and conversational interactions
7. **Best practices**: One behavior per test, realistic messages, minimal context

Start with the examples in this guide, then expand to cover your specific use cases.
