---
name: playwright
description: Headless browser automation via Playwright — navigate, click, type, screenshot, extract content, run JavaScript
---

# Playwright — Browser Automation

Use this skill for browser automation tasks: navigating websites, clicking elements, filling forms, taking screenshots, and extracting content. Powered by Playwright with Chromium.

## Prerequisites

Requires Playwright to be installed:

```bash
pip install playwright && python -m playwright install chromium
```

## Available Commands

Run operations via `skill_run` with the scripts below. All scripts accept `--help`.

### Navigation

| Script | Usage | Description |
|--------|-------|-------------|
| `navigate.sh` | `<url> [--timeout <ms>]` | Navigate to a URL |
| `history.sh` | `<back\|forward\|reload>` | Browser history navigation |

### Interaction

| Script | Usage | Description |
|--------|-------|-------------|
| `click.sh` | `<selector> [--timeout <ms>]` | Click an element |
| `type.sh` | `<selector> <text> [--timeout <ms>]` | Type into an input field |
| `scroll.sh` | `<up\|down> [amount]` | Scroll the page (default: 300px) |
| `wait.sh` | `<selector> [--timeout <ms>]` | Wait for element to appear |

### Content Extraction

| Script | Usage | Description |
|--------|-------|-------------|
| `content.sh` | — | Get all visible page text |
| `get_text.sh` | `<selector>` | Get text from specific element |
| `get_html.sh` | `<selector>` | Get outer HTML of element |
| `get_attr.sh` | `<selector> <attribute>` | Get attribute value |
| `info.sh` | `<title\|url>` | Get page title or current URL |

### Screenshot & Viewport

| Script | Usage | Description |
|--------|-------|-------------|
| `screenshot.sh` | `[path] [--full-page]` | Capture screenshot |
| `viewport.sh` | `<width> <height>` | Set viewport dimensions |

### JavaScript & Session

| Script | Usage | Description |
|--------|-------|-------------|
| `evaluate.sh` | `<script>` | Run JavaScript in page context |
| `close.sh` | — | Close the browser session |

## Examples

### Navigate and screenshot

```bash
bash scripts/navigate.sh "https://example.com"
bash scripts/screenshot.sh /tmp/example.png
bash scripts/close.sh
```

### Fill a form

```bash
bash scripts/navigate.sh "https://example.com/login"
bash scripts/type.sh "input[name=email]" "user@example.com"
bash scripts/type.sh "input[name=password]" "secret123"
bash scripts/click.sh "button[type=submit]"
bash scripts/wait.sh ".dashboard"
bash scripts/close.sh
```

### Extract content

```bash
bash scripts/navigate.sh "https://news.ycombinator.com"
bash scripts/content.sh
bash scripts/close.sh
```

### Get specific element text

```bash
bash scripts/navigate.sh "https://example.com"
bash scripts/get_text.sh "h1"
bash scripts/get_attr.sh "a.main-link" "href"
bash scripts/close.sh
```

### Mobile viewport testing

```bash
bash scripts/navigate.sh "https://example.com"
bash scripts/viewport.sh 375 812
bash scripts/screenshot.sh /tmp/mobile.png --full-page
bash scripts/close.sh
```

### Run JavaScript

```bash
bash scripts/navigate.sh "https://example.com"
bash scripts/evaluate.sh "document.querySelectorAll('a').length"
bash scripts/close.sh
```

### Wait for dynamic content

```bash
bash scripts/navigate.sh "https://example.com/spa"
bash scripts/wait.sh ".loaded-content" --timeout 10000
bash scripts/content.sh
bash scripts/close.sh
```

## Sending Screenshots to the User

When you take a screenshot, include the file path in your response using `[send_file:]`:

```
[send_file: /tmp/example.png]
Here is the screenshot of the page.
```

## Standard Workflow

1. **Navigate** to the target URL
2. **Wait** for dynamic content if needed
3. **Interact** (click, type, scroll) as required
4. **Extract** content or take a **screenshot**
5. **Send** files to user with `[send_file: /path]`
6. **Close** the browser session

## Tips

- **Always close the browser** when done using `close.sh`
- **Always use `[send_file:]`** to deliver screenshots to the user
- **Selectors**: Use CSS selectors (e.g., `"button.primary"`, `"input[type=text]"`, `"#header > h1"`)
- **Dynamic content**: Use `wait.sh` before interacting with async-loaded elements
- **Viewport**: Default is browser default. Use `viewport.sh` for responsive testing
- **Timeouts**: Most scripts accept `--timeout <ms>` (default varies by action)
- **Full page screenshots**: Use `--full-page` flag with `screenshot.sh`
- **JSON output**: All commands return JSON with `success` and `result` or `message` fields
