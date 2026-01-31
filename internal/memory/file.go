// Package memory handles bootstrap files and memory management.
package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
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
type FileMemory struct {
	workspace string
}

// NewFileMemory creates a new file-based memory handler.
func NewFileMemory(workspace string) *FileMemory {
	return &FileMemory{workspace: workspace}
}

// Workspace returns the workspace directory path.
func (m *FileMemory) Workspace() string {
	return m.workspace
}

// LoadBootstrap loads all bootstrap files and returns the combined content.
func (m *FileMemory) LoadBootstrap() (string, error) {
	var parts []string

	// Load bootstrap files in order
	for _, filename := range BootstrapFiles {
		content, err := m.readFile(filename)
		if err != nil {
			if os.IsNotExist(err) {
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
	memoryDir := filepath.Join(m.workspace, "memory")

	today := time.Now().Format("2006-01-02")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")

	var parts []string

	// Try to load yesterday's log
	yesterdayContent, err := m.readFileFromDir(memoryDir, yesterday+".md")
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if yesterdayContent != "" {
		parts = append(parts, fmt.Sprintf("# Yesterday's Log (%s)\n\n%s", yesterday, yesterdayContent))
	}

	// Try to load today's log
	todayContent, err := m.readFileFromDir(memoryDir, today+".md")
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if todayContent != "" {
		parts = append(parts, fmt.Sprintf("# Today's Log (%s)\n\n%s", today, todayContent))
	}

	return strings.Join(parts, "\n\n"), nil
}

// readFile reads a file from the workspace.
func (m *FileMemory) readFile(filename string) (string, error) {
	path := filepath.Join(m.workspace, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// readFileFromDir reads a file from a specific directory.
func (m *FileMemory) readFileFromDir(dir, filename string) (string, error) {
	path := filepath.Join(dir, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// AppendToDaily appends content to today's daily log.
func (m *FileMemory) AppendToDaily(content string) error {
	memoryDir := filepath.Join(m.workspace, "memory")
	if err := os.MkdirAll(memoryDir, 0755); err != nil {
		return fmt.Errorf("create memory directory: %w", err)
	}

	today := time.Now().Format("2006-01-02")
	path := filepath.Join(memoryDir, today+".md")

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
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
	path := filepath.Join(m.workspace, "MEMORY.md")

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open MEMORY.md: %w", err)
	}
	defer f.Close()

	if _, err := fmt.Fprintf(f, "\n%s\n", content); err != nil {
		return fmt.Errorf("write to MEMORY.md: %w", err)
	}

	return nil
}
