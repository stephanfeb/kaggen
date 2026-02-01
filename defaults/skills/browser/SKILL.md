---
name: browser
description: Automate browser interactions — navigate, click, type, screenshot, extract content, and run JavaScript via the built-in browser tool
tools: [browser]
---

# Browser — Headless Browser Automation

Use this skill to automate browser interactions using the built-in `browser` tool. The tool controls a Chromium-based browser via Chrome DevTools Protocol (CDP), configured in `~/.kaggen/config.json` under the `browser` section.

## Available Actions

Use the `browser` tool with the `action` field set to one of:

| Action | Required Fields | Description |
|--------|----------------|-------------|
| `navigate` | `url` | Navigate to a URL |
| `click` | `selector` | Click an element matching a CSS selector |
| `type` | `selector`, `text` | Type text into an input field |
| `screenshot` | — | Capture a screenshot and save as PNG file (optional `path` field) |
| `content` | — | Extract all visible text from the current page |
| `evaluate` | `script` | Run JavaScript in the page context and return the result |
| `scroll` | — | Scroll the page (`direction`: "up" or "down", `amount`: pixels) |
| `wait` | `selector` | Wait for an element to appear on the page |
| `setViewport` | `width`, `height` | Set browser viewport dimensions (pixels) |
| `getTitle` | — | Get the current page title |
| `getCurrentUrl` | — | Get the current page URL |
| `getText` | `selector` | Extract visible text from a specific element |
| `getHTML` | `selector` | Get outer HTML of a specific element |
| `getAttribute` | `selector`, `attribute` | Read an attribute value from an element |
| `goBack` | — | Navigate back in browser history |
| `goForward` | — | Navigate forward in browser history |
| `reload` | — | Reload the current page |
| `close` | — | Close the browser session. Always do this when finished. |

### Optional Fields

- `profile` — Browser profile name (uses the first configured profile if omitted)
- `path` — File path to save screenshot to (screenshot action only; a temp file is created if omitted)
- `timeout_seconds` — Timeout in seconds (default: 30)

## Sending Screenshots and Files to the User

When you take a screenshot, the tool returns a `file_path` in the result. To deliver the image to the user through chat, include the file path in your response using the `[send_file:]` directive:

```
[send_file: /path/to/screenshot.png]
Here is the screenshot of the page.
```

This is required — without `[send_file:]`, the user will not receive the image. Always include it when the user asks for a screenshot.

## Examples

### Navigate, screenshot, and send to user
```json
{"action": "navigate", "url": "https://example.com"}
```
```json
{"action": "screenshot"}
```
Then include in your response:
```
[send_file: /tmp/kaggen-screenshot-xxxxx.png]
Here is the screenshot of example.com.
```

### Set viewport for responsive testing
```json
{"action": "setViewport", "width": 375, "height": 812}
```
```json
{"action": "screenshot", "path": "/tmp/mobile.png"}
```

### Extract text from a specific element
```json
{"action": "getText", "selector": "h1.title"}
```
Returns: `content` (visible text of the matched element)

### Get an element's attribute
```json
{"action": "getAttribute", "selector": "a.main-link", "attribute": "href"}
```
Returns: `content` (the attribute value)

### Get page title and URL
```json
{"action": "getTitle"}
```
```json
{"action": "getCurrentUrl"}
```

### Extract page text
```json
{"action": "content"}
```
Returns: `content` (visible text of the entire page)

### Wait for content to load, then extract it
```json
{"action": "wait", "selector": ".content", "timeout_seconds": 10}
```
```json
{"action": "content"}
```

### Evaluate JavaScript
```json
{"action": "evaluate", "script": "document.querySelectorAll('a').length"}
```
Returns: `content` (stringified JS result)

### Scroll down the page
```json
{"action": "scroll", "direction": "down", "amount": 500}
```

### Close the browser when done
```json
{"action": "close"}
```

## Standard Workflow

For most browser tasks, follow this pattern:

1. **Navigate** to the target URL
2. **Wait** for dynamic content if needed
3. **Interact** (click, type, scroll) as required
4. **Extract** content or take a **screenshot**
5. **Send** any files to the user with `[send_file: /path]`
6. **Close** the browser session

## Tips

- **Always close the browser** when your task is done using the `close` action
- **Always use `[send_file:]`** to deliver screenshots and files to the user
- **Selectors**: Use CSS selectors (e.g., `"button.primary"`, `"input[type=text]"`, `"#header > h1"`)
- **Dynamic content**: Use the `wait` action before interacting with elements that load asynchronously
- **Viewport**: Default is 1280×720. Use `setViewport` to test responsive layouts (e.g., 375×812 for mobile)
- **JavaScript**: The `evaluate` action runs code in the page context; return JSON-serializable values
- **Profiles**: Use different browser profiles for isolated sessions (e.g., separate cookies/state)
- **Errors**: Check the `success` field in the response; `message` describes what happened
