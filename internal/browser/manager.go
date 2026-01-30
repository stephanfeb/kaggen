// Package browser provides browser control via Chrome DevTools Protocol.
package browser

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/chromedp/chromedp"

	"github.com/yourusername/kaggen/internal/config"
)

// Manager manages browser sessions per profile using chromedp.
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*browserSession
	profiles map[string]config.BrowserProfile
	logger   *slog.Logger
}

type browserSession struct {
	allocCtx    context.Context
	allocCancel context.CancelFunc
	ctx         context.Context
	cancel      context.CancelFunc
}

// NewManager creates a new browser Manager from config.
func NewManager(cfg config.BrowserConfig, logger *slog.Logger) *Manager {
	profiles := make(map[string]config.BrowserProfile, len(cfg.Profiles))
	for _, p := range cfg.Profiles {
		profiles[p.Name] = p
	}
	return &Manager{
		sessions: make(map[string]*browserSession),
		profiles: profiles,
		logger:   logger,
	}
}

// GetSession returns a persistent chromedp context for the given profile.
// If profileName is empty, the first configured profile is used.
// Sessions are created lazily and reused across tool calls so that sequential
// actions (navigate → wait → screenshot) operate on the same browser tab.
//
// IMPORTANT: Callers must NOT derive timeout/cancel contexts from the returned
// context, as cancelling a child of a chromedp context kills the browser. Use
// RunWithTimeout for time-bounded operations instead.
func (m *Manager) GetSession(profileName string) (context.Context, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if profileName == "" {
		// Use first profile
		for name := range m.profiles {
			profileName = name
			break
		}
	}

	if profileName == "" {
		return nil, fmt.Errorf("no browser profiles configured")
	}

	if sess, ok := m.sessions[profileName]; ok {
		// Check if the browser process is still alive
		if sess.allocCtx.Err() != nil {
			// Allocator died, clean up and recreate
			sess.cancel()
			sess.allocCancel()
			delete(m.sessions, profileName)
		} else {
			return sess.ctx, nil
		}
	}

	prof, ok := m.profiles[profileName]
	if !ok {
		return nil, fmt.Errorf("browser profile %q not found", profileName)
	}

	sess, err := m.createSession(prof)
	if err != nil {
		return nil, fmt.Errorf("creating browser session for profile %q: %w", profileName, err)
	}

	m.sessions[profileName] = sess
	m.logger.Info("browser session created", "profile", profileName, "type", prof.Type)
	return sess.ctx, nil
}

func (m *Manager) createSession(prof config.BrowserProfile) (*browserSession, error) {
	sess := &browserSession{}

	switch prof.Type {
	case "managed", "":
		opts := append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.Flag("remote-debugging-address", "127.0.0.1"),
		)
		if prof.ExecPath != "" {
			opts = append(opts, chromedp.ExecPath(prof.ExecPath))
		}
		if prof.Headless {
			opts = append(opts, chromedp.Headless)
		} else {
			// chromedp defaults to headless; override to show browser
			opts = append(opts, chromedp.Flag("headless", false))
		}
		if prof.UserDataDir != "" {
			opts = append(opts, chromedp.UserDataDir(config.ExpandPath(prof.UserDataDir)))
		}
		for _, f := range prof.Flags {
			opts = append(opts, chromedp.Flag(f, true))
		}
		// Set viewport size for consistent screenshots
		opts = append(opts, chromedp.WindowSize(1280, 720))

		sess.allocCtx, sess.allocCancel = chromedp.NewExecAllocator(context.Background(), opts...)

	case "remote":
		if prof.RemoteURL == "" {
			return nil, fmt.Errorf("remote profile requires remote_url")
		}
		sess.allocCtx, sess.allocCancel = chromedp.NewRemoteAllocator(context.Background(), prof.RemoteURL)

	default:
		return nil, fmt.Errorf("unknown browser profile type: %q", prof.Type)
	}

	sess.ctx, sess.cancel = chromedp.NewContext(sess.allocCtx,
		chromedp.WithLogf(m.logger.Info),
	)

	return sess, nil
}

// CloseSession shuts down the browser session for a specific profile.
// If profileName is empty, the first configured profile is closed.
func (m *Manager) CloseSession(profileName string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if profileName == "" {
		for name := range m.profiles {
			profileName = name
			break
		}
	}

	if sess, ok := m.sessions[profileName]; ok {
		sess.cancel()
		sess.allocCancel()
		delete(m.sessions, profileName)
		m.logger.Info("browser session closed", "profile", profileName)
	}
}

// Close shuts down all browser sessions.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, sess := range m.sessions {
		sess.cancel()
		sess.allocCancel()
		m.logger.Info("browser session closed", "profile", name)
	}
	m.sessions = make(map[string]*browserSession)
}
