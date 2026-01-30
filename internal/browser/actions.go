package browser

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// Navigate navigates to a URL and returns the page title and current URL.
func Navigate(ctx context.Context, url string) (title, currentURL string, err error) {
	if strings.HasPrefix(strings.ToLower(url), "file://") {
		return "", "", fmt.Errorf("file:// URLs are not allowed")
	}

	err = chromedp.Run(ctx,
		chromedp.Navigate(url),
		chromedp.WaitReady("body"),
		chromedp.Title(&title),
		chromedp.Location(&currentURL),
	)
	return
}

// Click clicks an element matching the CSS selector.
func Click(ctx context.Context, selector string) error {
	return chromedp.Run(ctx,
		chromedp.WaitVisible(selector),
		chromedp.Click(selector, chromedp.ByQuery),
	)
}

// Type types text into an element matching the CSS selector.
func Type(ctx context.Context, selector, text string) error {
	return chromedp.Run(ctx,
		chromedp.WaitVisible(selector),
		chromedp.Clear(selector),
		chromedp.SendKeys(selector, text, chromedp.ByQuery),
	)
}

// Screenshot captures a screenshot and writes it to a temporary PNG file.
// If savePath is non-empty, the file is written there instead.
// Returns the absolute path to the saved PNG file.
func Screenshot(ctx context.Context, savePath string) (string, error) {
	var buf []byte
	err := chromedp.Run(ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			buf, err = page.CaptureScreenshot().
				WithFormat(page.CaptureScreenshotFormatPng).
				WithCaptureBeyondViewport(false).
				Do(ctx)
			return err
		}),
	)
	if err != nil {
		return "", err
	}
	if len(buf) == 0 {
		return "", fmt.Errorf("screenshot returned empty data")
	}

	outPath := savePath
	if outPath == "" {
		f, err := os.CreateTemp("", "kaggen-screenshot-*.png")
		if err != nil {
			return "", fmt.Errorf("creating temp file: %w", err)
		}
		outPath = f.Name()
		f.Close()
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
		return "", fmt.Errorf("creating directory: %w", err)
	}
	if err := os.WriteFile(outPath, buf, 0644); err != nil {
		return "", fmt.Errorf("writing screenshot: %w", err)
	}

	return outPath, nil
}

// Content extracts the visible text content of the current page.
func Content(ctx context.Context) (string, error) {
	var text string
	err := chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.body.innerText`, &text),
	)
	return text, err
}

// Evaluate runs JavaScript in the page context and returns the stringified result.
func Evaluate(ctx context.Context, script string) (string, error) {
	var result interface{}
	err := chromedp.Run(ctx,
		chromedp.Evaluate(script, &result),
	)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%v", result), nil
}

// Scroll scrolls the page in the given direction by the specified amount in pixels.
func Scroll(ctx context.Context, direction string, amount int) error {
	if amount <= 0 {
		amount = 300
	}
	dy := amount
	if direction == "up" {
		dy = -amount
	}
	return chromedp.Run(ctx,
		chromedp.Evaluate(fmt.Sprintf(`window.scrollBy(0, %d)`, dy), nil),
	)
}

// Wait waits for an element matching the CSS selector to appear within the timeout.
func Wait(ctx context.Context, selector string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return chromedp.Run(ctx,
		chromedp.WaitVisible(selector),
	)
}
