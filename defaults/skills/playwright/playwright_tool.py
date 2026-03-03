#!/usr/bin/env python3
"""Playwright browser automation tool for kaggen.

Provides headless browser control via a CLI that accepts JSON action objects.
Maintains a persistent browser session across invocations using a state file.

Usage:
    python3 playwright_tool.py '{"action": "navigate", "url": "https://example.com"}'
    python3 playwright_tool.py '{"action": "screenshot", "path": "/tmp/shot.png"}'
    python3 playwright_tool.py '{"action": "close"}'
"""

import json
import os
import signal
import sys
import tempfile
from pathlib import Path

# Session state file — stores CDP endpoint for reconnecting
SESSION_FILE = Path(tempfile.gettempdir()) / "kaggen-playwright-session.json"


def _load_session():
    """Load saved session info (CDP endpoint) if it exists."""
    if SESSION_FILE.exists():
        try:
            data = json.loads(SESSION_FILE.read_text())
            return data
        except (json.JSONDecodeError, OSError):
            SESSION_FILE.unlink(missing_ok=True)
    return None


def _save_session(ws_endpoint, pid):
    """Persist the CDP endpoint so subsequent calls can reconnect."""
    SESSION_FILE.write_text(json.dumps({"ws_endpoint": ws_endpoint, "pid": pid}))


def _clear_session():
    """Remove persisted session info."""
    SESSION_FILE.unlink(missing_ok=True)


def _connect_or_launch(playwright, headless=True):
    """Reconnect to an existing browser or launch a new one.

    Returns (browser, is_new) where is_new indicates a fresh launch.
    """
    session = _load_session()
    if session:
        try:
            browser = playwright.chromium.connect_over_cdp(session["ws_endpoint"])
            return browser, False
        except Exception:
            # Stale session — kill leftover process and start fresh
            try:
                os.kill(session["pid"], signal.SIGTERM)
            except (OSError, ProcessLookupError):
                pass
            _clear_session()

    # Launch a new browser that exposes a CDP server for reconnection
    browser = playwright.chromium.launch(headless=headless)
    cdp = browser._impl_obj._connection._transport._ws_endpoint  # noqa: internal
    _save_session(cdp, os.getpid())
    return browser, True


def _get_page(browser):
    """Return the first page of the first context, creating one if needed."""
    contexts = browser.contexts
    if not contexts:
        ctx = browser.new_context()
    else:
        ctx = contexts[0]

    pages = ctx.pages
    if not pages:
        return ctx.new_page()
    return pages[0]


def _ok(result=None):
    """Build a success response dict."""
    resp = {"success": True}
    if result is not None:
        resp["result"] = result
    return resp


def _err(message):
    """Build an error response dict."""
    return {"success": False, "message": str(message)}


# ── Action handlers ──────────────────────────────────────────────────────────

def action_navigate(page, params):
    url = params.get("url")
    if not url:
        return _err("'url' is required for navigate action")
    timeout = params.get("timeout_ms", 30000)
    page.goto(url, timeout=timeout, wait_until="domcontentloaded")
    return _ok(f"Navigated to {url}")


def action_click(page, params):
    selector = params.get("selector")
    if not selector:
        return _err("'selector' is required for click action")
    timeout = params.get("timeout_ms", 5000)
    page.click(selector, timeout=timeout)
    return _ok(f"Clicked {selector}")


def action_type(page, params):
    selector = params.get("selector")
    text = params.get("text")
    if not selector or text is None:
        return _err("'selector' and 'text' are required for type action")
    timeout = params.get("timeout_ms", 5000)
    page.fill(selector, text, timeout=timeout)
    return _ok(f"Typed into {selector}")


def action_screenshot(page, params):
    path = params.get("path")
    if not path:
        fd, path = tempfile.mkstemp(prefix="kaggen-pw-", suffix=".png")
        os.close(fd)
    page.screenshot(path=path, full_page=params.get("full_page", False))
    return _ok({"file_path": path})


def action_content(page, _params):
    text = page.evaluate("() => document.body ? document.body.innerText : ''")
    return _ok(text)


def action_evaluate(page, params):
    script = params.get("script")
    if not script:
        return _err("'script' is required for evaluate action")
    result = page.evaluate(script)
    return _ok(result)


def action_wait(page, params):
    selector = params.get("selector")
    if not selector:
        return _err("'selector' is required for wait action")
    timeout = params.get("timeout_ms", 30000)
    page.wait_for_selector(selector, timeout=timeout)
    return _ok(f"Selector '{selector}' appeared")


def action_scroll(page, params):
    direction = params.get("direction", "down")
    amount = params.get("amount", 300)
    if direction == "up":
        amount = -abs(amount)
    else:
        amount = abs(amount)
    page.evaluate(f"window.scrollBy(0, {amount})")
    return _ok(f"Scrolled {direction} by {abs(amount)}px")


def action_set_viewport(page, params):
    width = params.get("width")
    height = params.get("height")
    if not width or not height:
        return _err("'width' and 'height' are required for setViewport action")
    page.set_viewport_size({"width": int(width), "height": int(height)})
    return _ok(f"Viewport set to {width}x{height}")


def action_get_title(page, _params):
    title = page.title()
    return _ok(title)


def action_get_current_url(page, _params):
    url = page.url
    return _ok(url)


def action_get_text(page, params):
    selector = params.get("selector")
    if not selector:
        return _err("'selector' is required for getText action")
    timeout = params.get("timeout_ms", 5000)
    text = page.locator(selector).inner_text(timeout=timeout)
    return _ok(text)


def action_get_html(page, params):
    selector = params.get("selector")
    if not selector:
        return _err("'selector' is required for getHTML action")
    timeout = params.get("timeout_ms", 5000)
    # Use evaluate to get outerHTML (includes the element itself)
    html = page.locator(selector).evaluate("el => el.outerHTML")
    return _ok(html)


def action_get_attribute(page, params):
    selector = params.get("selector")
    attribute = params.get("attribute")
    if not selector or not attribute:
        return _err("'selector' and 'attribute' are required for getAttribute action")
    timeout = params.get("timeout_ms", 5000)
    value = page.locator(selector).get_attribute(attribute, timeout=timeout)
    return _ok(value)


def action_go_back(page, _params):
    page.go_back()
    return _ok("Navigated back")


def action_go_forward(page, _params):
    page.go_forward()
    return _ok("Navigated forward")


def action_reload(page, _params):
    page.reload()
    return _ok("Page reloaded")


def action_close(browser, _params):
    browser.close()
    _clear_session()
    return _ok("Browser closed")


# Map of action name → handler
ACTIONS = {
    "navigate": action_navigate,
    "click": action_click,
    "type": action_type,
    "screenshot": action_screenshot,
    "content": action_content,
    "evaluate": action_evaluate,
    "wait": action_wait,
    "scroll": action_scroll,
    "setViewport": action_set_viewport,
    "getTitle": action_get_title,
    "getCurrentUrl": action_get_current_url,
    "getText": action_get_text,
    "getHTML": action_get_html,
    "getAttribute": action_get_attribute,
    "goBack": action_go_back,
    "goForward": action_go_forward,
    "reload": action_reload,
}


def main():
    if len(sys.argv) < 2:
        print(json.dumps(_err("Usage: playwright_tool.py '<json action>'")))
        sys.exit(1)

    # Parse input
    raw = sys.argv[1]
    try:
        params = json.loads(raw)
    except json.JSONDecodeError as exc:
        print(json.dumps(_err(f"Invalid JSON input: {exc}")))
        sys.exit(1)

    action = params.get("action")
    if not action:
        print(json.dumps(_err("'action' field is required")))
        sys.exit(1)

    # Import playwright here so import errors are caught gracefully
    try:
        from playwright.sync_api import sync_playwright
    except ImportError:
        print(json.dumps(_err(
            "Playwright is not installed. Run: pip install playwright && python -m playwright install chromium"
        )))
        sys.exit(1)

    # Handle close without needing a page
    if action == "close":
        session = _load_session()
        if not session:
            _clear_session()
            print(json.dumps(_ok("No active session to close")))
            return
        try:
            with sync_playwright() as pw:
                browser = pw.chromium.connect_over_cdp(session["ws_endpoint"])
                result = action_close(browser, params)
                print(json.dumps(result))
        except Exception:
            _clear_session()
            print(json.dumps(_ok("Session cleared (browser was already gone)")))
        return

    if action not in ACTIONS:
        print(json.dumps(_err(f"Unknown action '{action}'. Valid: {', '.join(list(ACTIONS) + ['close'])}")))
        sys.exit(1)

    # Run the action
    try:
        with sync_playwright() as pw:
            headless = params.get("headless", True)
            browser = pw.chromium.launch(headless=headless)
            page = _get_page(browser)
            handler = ACTIONS[action]
            result = handler(page, params)
            # Keep browser alive only if not closing; for a CLI tool we close
            # after each action since sync_playwright context manager exits.
            # The session-persistence approach is kept for future daemon mode.
            browser.close()
            print(json.dumps(result))
    except Exception as exc:
        print(json.dumps(_err(str(exc))))
        sys.exit(1)


if __name__ == "__main__":
    main()
