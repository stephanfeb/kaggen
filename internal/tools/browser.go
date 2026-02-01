package tools

import (
	"context"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"

	"github.com/yourusername/kaggen/internal/browser"
)

// BrowserArgs defines the input arguments for the browser tool.
type BrowserArgs struct {
	Action    string `json:"action" jsonschema:"required,enum=navigate|click|type|screenshot|content|evaluate|scroll|wait|close|setViewport|getTitle|getCurrentUrl|goBack|goForward|reload|getText|getHTML|getAttribute,description=The browser action to perform."`
	Profile   string `json:"profile,omitempty" jsonschema:"description=Browser profile name. Uses first configured profile if omitted."`
	URL       string `json:"url,omitempty" jsonschema:"description=URL to navigate to (required for navigate action)."`
	Selector  string `json:"selector,omitempty" jsonschema:"description=CSS selector for click/type/wait/getText/getHTML/getAttribute actions."`
	Text      string `json:"text,omitempty" jsonschema:"description=Text to type (required for type action)."`
	Script    string `json:"script,omitempty" jsonschema:"description=JavaScript code to evaluate (required for evaluate action)."`
	Path      string `json:"path,omitempty" jsonschema:"description=File path to save screenshot to. If omitted a temp file is created. Used only for screenshot action."`
	Direction string `json:"direction,omitempty" jsonschema:"description=Scroll direction: up or down (default down)."`
	Amount    int    `json:"amount,omitempty" jsonschema:"description=Pixels to scroll (default 300)."`
	Width     int    `json:"width,omitempty" jsonschema:"description=Viewport width in pixels (required for setViewport action)."`
	Height    int    `json:"height,omitempty" jsonschema:"description=Viewport height in pixels (required for setViewport action)."`
	Attribute string `json:"attribute,omitempty" jsonschema:"description=Element attribute name (required for getAttribute action)."`
	Timeout   int    `json:"timeout_seconds,omitempty" jsonschema:"description=Timeout in seconds (default 30)."`
}

// BrowserResult defines the output of the browser tool.
type BrowserResult struct {
	Success  bool   `json:"success"`
	Message  string `json:"message"`
	Content  string `json:"content,omitempty"`
	FilePath string `json:"file_path,omitempty"`
	Title    string `json:"title,omitempty"`
	URL      string `json:"url,omitempty"`
}

const defaultBrowserTimeout = 30 * time.Second

// BrowserTools returns browser tools if a manager is provided, nil otherwise.
func BrowserTools(mgr *browser.Manager) []tool.Tool {
	if mgr == nil {
		return nil
	}
	return []tool.Tool{newBrowserTool(mgr)}
}

func newBrowserTool(mgr *browser.Manager) tool.CallableTool {
	return function.NewFunctionTool(
		func(ctx context.Context, args BrowserArgs) (*BrowserResult, error) {
			return executeBrowser(ctx, mgr, args)
		},
		function.WithName("browser"),
		function.WithDescription("Control a browser via Chrome DevTools Protocol. Actions: navigate, click, type, screenshot, content, evaluate, scroll, wait, close, setViewport, getTitle, getCurrentUrl, goBack, goForward, reload, getText, getHTML, getAttribute. Always close the browser when done."),
	)
}

func executeBrowser(ctx context.Context, mgr *browser.Manager, args BrowserArgs) (*BrowserResult, error) {
	result := &BrowserResult{}

	timeout := defaultBrowserTimeout
	if args.Timeout > 0 {
		timeout = time.Duration(args.Timeout) * time.Second
	}

	// Handle close before acquiring a session (avoids creating one just to close it).
	if args.Action == "close" {
		mgr.CloseSession(args.Profile)
		result.Success = true
		result.Message = "Browser session closed"
		return result, nil
	}

	// Get persistent browser session. Do NOT wrap this context with
	// context.WithTimeout — cancelling a child of a chromedp context kills
	// the browser process. Timeouts are enforced via runWithTimeout below.
	bctx, err := mgr.GetSession(args.Profile)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to get browser session: %v", err)
		return result, err
	}

	// runWithTimeout executes fn with a deadline. If the function doesn't
	// return in time we report a timeout error but do NOT cancel the chromedp
	// context, keeping the browser session alive for subsequent calls.
	runWithTimeout := func(fn func() error) error {
		done := make(chan error, 1)
		go func() { done <- fn() }()
		select {
		case err := <-done:
			return err
		case <-time.After(timeout):
			return fmt.Errorf("action timed out after %s", timeout)
		}
	}

	switch args.Action {
	case "navigate":
		if args.URL == "" {
			result.Message = "url is required for navigate action"
			return result, fmt.Errorf("url is required for navigate action")
		}
		var title, currentURL string
		err = runWithTimeout(func() error {
			var e error
			title, currentURL, e = browser.Navigate(bctx, args.URL)
			return e
		})
		if err != nil {
			result.Message = fmt.Sprintf("Navigate failed: %v", err)
			return result, nil
		}
		result.Success = true
		result.Title = title
		result.URL = currentURL
		result.Message = fmt.Sprintf("Navigated to %s", currentURL)

	case "click":
		if args.Selector == "" {
			result.Message = "selector is required for click action"
			return result, fmt.Errorf("selector is required for click action")
		}
		err = runWithTimeout(func() error {
			return browser.Click(bctx, args.Selector)
		})
		if err != nil {
			result.Message = fmt.Sprintf("Click failed: %v", err)
			return result, nil
		}
		result.Success = true
		result.Message = fmt.Sprintf("Clicked %s", args.Selector)

	case "type":
		if args.Selector == "" || args.Text == "" {
			result.Message = "selector and text are required for type action"
			return result, fmt.Errorf("selector and text are required for type action")
		}
		err = runWithTimeout(func() error {
			return browser.Type(bctx, args.Selector, args.Text)
		})
		if err != nil {
			result.Message = fmt.Sprintf("Type failed: %v", err)
			return result, nil
		}
		result.Success = true
		result.Message = fmt.Sprintf("Typed into %s", args.Selector)

	case "screenshot":
		var filePath string
		err = runWithTimeout(func() error {
			var e error
			filePath, e = browser.Screenshot(bctx, args.Path)
			return e
		})
		if err != nil {
			result.Message = fmt.Sprintf("Screenshot failed: %v", err)
			return result, nil
		}
		result.Success = true
		result.FilePath = filePath
		result.Message = fmt.Sprintf("Screenshot saved to %s", filePath)

	case "content":
		var text string
		err = runWithTimeout(func() error {
			var e error
			text, e = browser.Content(bctx)
			return e
		})
		if err != nil {
			result.Message = fmt.Sprintf("Content extraction failed: %v", err)
			return result, nil
		}
		result.Success = true
		result.Content = text
		result.Message = "Page content extracted"

	case "evaluate":
		if args.Script == "" {
			result.Message = "script is required for evaluate action"
			return result, fmt.Errorf("script is required for evaluate action")
		}
		var val string
		err = runWithTimeout(func() error {
			var e error
			val, e = browser.Evaluate(bctx, args.Script)
			return e
		})
		if err != nil {
			result.Message = fmt.Sprintf("Evaluate failed: %v", err)
			return result, nil
		}
		result.Success = true
		result.Content = val
		result.Message = "JavaScript evaluated"

	case "scroll":
		dir := args.Direction
		if dir == "" {
			dir = "down"
		}
		err = runWithTimeout(func() error {
			return browser.Scroll(bctx, dir, args.Amount)
		})
		if err != nil {
			result.Message = fmt.Sprintf("Scroll failed: %v", err)
			return result, nil
		}
		result.Success = true
		result.Message = fmt.Sprintf("Scrolled %s", dir)

	case "wait":
		if args.Selector == "" {
			result.Message = "selector is required for wait action"
			return result, fmt.Errorf("selector is required for wait action")
		}
		err = runWithTimeout(func() error {
			return browser.Wait(bctx, args.Selector, timeout)
		})
		if err != nil {
			result.Message = fmt.Sprintf("Wait failed: %v", err)
			return result, nil
		}
		result.Success = true
		result.Message = fmt.Sprintf("Element %s appeared", args.Selector)

	case "setViewport":
		if args.Width <= 0 || args.Height <= 0 {
			result.Message = "width and height are required for setViewport action"
			return result, fmt.Errorf("width and height are required for setViewport action")
		}
		err = runWithTimeout(func() error {
			return browser.SetViewport(bctx, args.Width, args.Height)
		})
		if err != nil {
			result.Message = fmt.Sprintf("SetViewport failed: %v", err)
			return result, nil
		}
		result.Success = true
		result.Message = fmt.Sprintf("Viewport set to %dx%d", args.Width, args.Height)

	case "getTitle":
		var title string
		err = runWithTimeout(func() error {
			var e error
			title, e = browser.GetTitle(bctx)
			return e
		})
		if err != nil {
			result.Message = fmt.Sprintf("GetTitle failed: %v", err)
			return result, nil
		}
		result.Success = true
		result.Title = title
		result.Message = "Page title retrieved"

	case "getCurrentUrl":
		var currentURL string
		err = runWithTimeout(func() error {
			var e error
			currentURL, e = browser.GetCurrentURL(bctx)
			return e
		})
		if err != nil {
			result.Message = fmt.Sprintf("GetCurrentUrl failed: %v", err)
			return result, nil
		}
		result.Success = true
		result.URL = currentURL
		result.Message = "Current URL retrieved"

	case "goBack":
		err = runWithTimeout(func() error {
			return browser.GoBack(bctx)
		})
		if err != nil {
			result.Message = fmt.Sprintf("GoBack failed: %v", err)
			return result, nil
		}
		result.Success = true
		result.Message = "Navigated back"

	case "goForward":
		err = runWithTimeout(func() error {
			return browser.GoForward(bctx)
		})
		if err != nil {
			result.Message = fmt.Sprintf("GoForward failed: %v", err)
			return result, nil
		}
		result.Success = true
		result.Message = "Navigated forward"

	case "reload":
		err = runWithTimeout(func() error {
			return browser.Reload(bctx)
		})
		if err != nil {
			result.Message = fmt.Sprintf("Reload failed: %v", err)
			return result, nil
		}
		result.Success = true
		result.Message = "Page reloaded"

	case "getText":
		if args.Selector == "" {
			result.Message = "selector is required for getText action"
			return result, fmt.Errorf("selector is required for getText action")
		}
		var text string
		err = runWithTimeout(func() error {
			var e error
			text, e = browser.GetText(bctx, args.Selector)
			return e
		})
		if err != nil {
			result.Message = fmt.Sprintf("GetText failed: %v", err)
			return result, nil
		}
		result.Success = true
		result.Content = text
		result.Message = fmt.Sprintf("Text extracted from %s", args.Selector)

	case "getHTML":
		if args.Selector == "" {
			result.Message = "selector is required for getHTML action"
			return result, fmt.Errorf("selector is required for getHTML action")
		}
		var html string
		err = runWithTimeout(func() error {
			var e error
			html, e = browser.GetHTML(bctx, args.Selector)
			return e
		})
		if err != nil {
			result.Message = fmt.Sprintf("GetHTML failed: %v", err)
			return result, nil
		}
		result.Success = true
		result.Content = html
		result.Message = fmt.Sprintf("HTML extracted from %s", args.Selector)

	case "getAttribute":
		if args.Selector == "" || args.Attribute == "" {
			result.Message = "selector and attribute are required for getAttribute action"
			return result, fmt.Errorf("selector and attribute are required for getAttribute action")
		}
		var val string
		err = runWithTimeout(func() error {
			var e error
			val, e = browser.GetAttribute(bctx, args.Selector, args.Attribute)
			return e
		})
		if err != nil {
			result.Message = fmt.Sprintf("GetAttribute failed: %v", err)
			return result, nil
		}
		result.Success = true
		result.Content = val
		result.Message = fmt.Sprintf("Attribute %q from %s", args.Attribute, args.Selector)

	default:
		result.Message = fmt.Sprintf("Unknown action: %s", args.Action)
		return result, fmt.Errorf("unknown browser action: %s", args.Action)
	}

	return result, nil
}
