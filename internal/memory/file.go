// Package memory handles bootstrap files and memory management.
package memory

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yourusername/kaggen/internal/vfs"
)

// BootstrapFiles defines the order of bootstrap files to load.
var BootstrapFiles = []string{
	"SOUL.md",
	"IDENTITY.md",
	"AGENTS.md",
	"TOOLS.md",
	"USER.md",
	"MEMORY.md",
}

// FileMemory handles reading bootstrap and memory files.
// It uses a VFS for all I/O, sandboxing memory access to the workspace.
type FileMemory struct {
	filesystem vfs.FS
	workspace  string // kept for legacy fallback paths
}

// NewFileMemory creates a new file-based memory handler.
// Deprecated: use NewFileMemoryWithFS for VFS-sandboxed access.
func NewFileMemory(workspace string) *FileMemory {
	fsys, err := vfs.NewScopedFS(workspace)
	if err != nil {
		// Fall back — workspace might not exist yet at init time.
		// Operations will fail at call time with clear errors.
		return &FileMemory{workspace: workspace}
	}
	return &FileMemory{filesystem: fsys, workspace: workspace}
}

// NewFileMemoryWithFS creates a memory handler backed by the given VFS.
func NewFileMemoryWithFS(filesystem vfs.FS, workspace string) *FileMemory {
	return &FileMemory{filesystem: filesystem, workspace: workspace}
}

// FS returns the underlying VFS. May be nil if constructed without one.
func (m *FileMemory) FS() vfs.FS {
	return m.filesystem
}

// LoadBootstrap loads all bootstrap files and returns the combined content.
func (m *FileMemory) LoadBootstrap() (string, error) {
	var parts []string

	// Load bootstrap files in order
	for _, filename := range BootstrapFiles {
		content, err := m.readFile(filename)
		if err != nil {
			if isNotExist(err) {
				continue // Skip missing files
			}
			return "", fmt.Errorf("read %s: %w", filename, err)
		}
		if content != "" {
			parts = append(parts, fmt.Sprintf("# %s\n\n%s", filename, content))
		}
	}

	// Load daily logs
	dailyLogs, err := m.loadDailyLogs()
	if err != nil {
		return "", fmt.Errorf("load daily logs: %w", err)
	}
	if dailyLogs != "" {
		parts = append(parts, dailyLogs)
	}

	return strings.Join(parts, "\n\n---\n\n"), nil
}

// loadDailyLogs loads today's and yesterday's memory logs.
func (m *FileMemory) loadDailyLogs() (string, error) {
	today := time.Now().Format("2006-01-02")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")

	var parts []string

	// Try to load yesterday's log
	yesterdayContent, err := m.readFile(filepath.Join("memory", yesterday+".md"))
	if err != nil && !isNotExist(err) {
		return "", err
	}
	if yesterdayContent != "" {
		parts = append(parts, fmt.Sprintf("# Yesterday's Log (%s)\n\n%s", yesterday, yesterdayContent))
	}

	// Try to load today's log
	todayContent, err := m.readFile(filepath.Join("memory", today+".md"))
	if err != nil && !isNotExist(err) {
		return "", err
	}
	if todayContent != "" {
		parts = append(parts, fmt.Sprintf("# Today's Log (%s)\n\n%s", today, todayContent))
	}

	return strings.Join(parts, "\n\n"), nil
}

// readFile reads a file via the VFS.
func (m *FileMemory) readFile(name string) (string, error) {
	if m.filesystem == nil {
		// Fallback for pre-VFS construction
		path := filepath.Join(m.workspace, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(data)), nil
	}
	data, err := m.filesystem.ReadFile(name)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// AppendToDaily appends content to today's daily log.
func (m *FileMemory) AppendToDaily(content string) error {
	if m.filesystem == nil {
		return m.appendToDailyLegacy(content)
	}
	if err := m.filesystem.MkdirAll("memory", 0700); err != nil {
		return fmt.Errorf("create memory directory: %w", err)
	}

	today := time.Now().Format("2006-01-02")
	f, err := m.filesystem.OpenFile(
		filepath.Join("memory", today+".md"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600,
	)
	if err != nil {
		return fmt.Errorf("open daily log: %w", err)
	}
	defer f.Close()

	timestamp := time.Now().Format("15:04")
	if _, err := fmt.Fprintf(f, "\n## %s\n\n%s\n", timestamp, content); err != nil {
		return fmt.Errorf("write to daily log: %w", err)
	}
	return nil
}

func (m *FileMemory) appendToDailyLegacy(content string) error {
	memoryDir := filepath.Join(m.workspace, "memory")
	if err := os.MkdirAll(memoryDir, 0700); err != nil {
		return fmt.Errorf("create memory directory: %w", err)
	}
	today := time.Now().Format("2006-01-02")
	path := filepath.Join(memoryDir, today+".md")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open daily log: %w", err)
	}
	defer f.Close()
	timestamp := time.Now().Format("15:04")
	if _, err := fmt.Fprintf(f, "\n## %s\n\n%s\n", timestamp, content); err != nil {
		return fmt.Errorf("write to daily log: %w", err)
	}
	return nil
}

// UpdateMemory appends content to MEMORY.md.
func (m *FileMemory) UpdateMemory(content string) error {
	if m.filesystem == nil {
		return m.updateMemoryLegacy(content)
	}
	f, err := m.filesystem.OpenFile("MEMORY.md", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open MEMORY.md: %w", err)
	}
	defer f.Close()

	if _, err := fmt.Fprintf(f, "\n%s\n", content); err != nil {
		return fmt.Errorf("write to MEMORY.md: %w", err)
	}
	return nil
}

func (m *FileMemory) updateMemoryLegacy(content string) error {
	path := filepath.Join(m.workspace, "MEMORY.md")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open MEMORY.md: %w", err)
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "\n%s\n", content); err != nil {
		return fmt.Errorf("write to MEMORY.md: %w", err)
	}
	return nil
}

// isNotExist checks whether an error indicates a missing file.
func isNotExist(err error) bool {
	if os.IsNotExist(err) {
		return true
	}
	var pathErr *fs.PathError
	if ok := errors.As(err, &pathErr); ok {
		return pathErr.Err == fs.ErrNotExist
	}
	return false
}
