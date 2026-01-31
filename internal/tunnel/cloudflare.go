// Package tunnel manages reverse tunnels for exposing local services to the internet.
package tunnel

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"sync"
	"time"
)

// urlPattern matches the quick-tunnel URL from cloudflared output.
var urlPattern = regexp.MustCompile(`https://[a-zA-Z0-9-]+\.trycloudflare\.com`)

// CloudflareTunnel manages a cloudflared subprocess that exposes a local port
// to the internet via Cloudflare's tunnel infrastructure.
type CloudflareTunnel struct {
	localPort   int
	namedTunnel string // empty = quick tunnel (random URL)
	logger      *slog.Logger

	mu        sync.Mutex
	cmd       *exec.Cmd
	publicURL string
	urlReady  chan struct{}
	done      chan struct{}
}

// NewCloudflareTunnel creates a new tunnel manager.
// If namedTunnel is empty, a quick tunnel with a random URL is used.
func NewCloudflareTunnel(localPort int, namedTunnel string, logger *slog.Logger) *CloudflareTunnel {
	return &CloudflareTunnel{
		localPort:   localPort,
		namedTunnel: namedTunnel,
		logger:      logger,
		urlReady:    make(chan struct{}),
		done:        make(chan struct{}),
	}
}

// Start launches the cloudflared subprocess. It returns once the process is
// started; use PublicURL() to wait for the tunnel URL to become available.
func (t *CloudflareTunnel) Start(ctx context.Context) error {
	// Verify cloudflared is installed.
	path, err := exec.LookPath("cloudflared")
	if err != nil {
		return fmt.Errorf("cloudflared not found in PATH: install with 'brew install cloudflared' or download from https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/")
	}

	var args []string
	if t.namedTunnel != "" {
		args = []string{"tunnel", "run", t.namedTunnel}
	} else {
		args = []string{"tunnel", "--url", fmt.Sprintf("http://localhost:%d", t.localPort)}
	}

	t.mu.Lock()
	t.cmd = exec.CommandContext(ctx, path, args...)
	// cloudflared writes tunnel info to stderr.
	stderr, err := t.cmd.StderrPipe()
	if err != nil {
		t.mu.Unlock()
		return fmt.Errorf("stderr pipe: %w", err)
	}
	if err := t.cmd.Start(); err != nil {
		t.mu.Unlock()
		return fmt.Errorf("start cloudflared: %w", err)
	}
	t.mu.Unlock()

	t.logger.Info("cloudflared started", "pid", t.cmd.Process.Pid, "named_tunnel", t.namedTunnel)

	// Scan stderr for the tunnel URL.
	go func() {
		defer close(t.done)
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			t.logger.Debug("cloudflared", "output", line)

			if t.namedTunnel == "" {
				if match := urlPattern.FindString(line); match != "" {
					t.mu.Lock()
					if t.publicURL == "" {
						t.publicURL = match
						close(t.urlReady)
						t.logger.Info("tunnel URL discovered", "url", match)
					}
					t.mu.Unlock()
				}
			}
		}
		// Wait for process to exit.
		if err := t.cmd.Wait(); err != nil {
			t.logger.Warn("cloudflared exited", "error", err)
		} else {
			t.logger.Info("cloudflared exited")
		}
	}()

	return nil
}

// PublicURL blocks until the tunnel URL is discovered or the context is
// cancelled. Returns the public URL (e.g. "https://abc-xyz.trycloudflare.com").
// For named tunnels, this returns an empty string (the URL is configured externally).
func (t *CloudflareTunnel) PublicURL(ctx context.Context) (string, error) {
	if t.namedTunnel != "" {
		return "", fmt.Errorf("named tunnels don't expose a URL via stdout; configure callback_base_url manually")
	}

	// Wait up to 30 seconds for the URL.
	timeout, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	select {
	case <-t.urlReady:
		t.mu.Lock()
		defer t.mu.Unlock()
		return t.publicURL, nil
	case <-timeout.Done():
		return "", fmt.Errorf("timed out waiting for tunnel URL")
	case <-t.done:
		return "", fmt.Errorf("cloudflared exited before URL was discovered")
	}
}

// Stop terminates the cloudflared subprocess.
func (t *CloudflareTunnel) Stop() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cmd != nil && t.cmd.Process != nil {
		t.logger.Info("stopping cloudflared", "pid", t.cmd.Process.Pid)
		return t.cmd.Process.Kill()
	}
	return nil
}
