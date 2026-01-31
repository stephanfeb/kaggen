// Package session implements trpc-agent-go's session.Service backed by the local filesystem.
package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"trpc.group/trpc-go/trpc-agent-go/event"
)

const (
	// maxScannerBuffer is the maximum size of a single JSONL line (10 MB).
	maxScannerBuffer = 10 * 1024 * 1024
	// sessionFileSizeWarn is the file size threshold for warning (50 MB).
	sessionFileSizeWarn = 50 * 1024 * 1024
	// sessionEventCountWarn is the event count threshold for warning.
	sessionEventCountWarn = 5000
)

// SessionWarning holds information about session health issues detected during loading.
type SessionWarning struct {
	FileSize   int64 // file size in bytes (0 if no warning)
	EventCount int   // number of events (0 if no warning)
	Skipped    int   // number of lines skipped due to parse/size errors
}

// ReadEventJSONL reads events from a JSONL file.
// It skips corrupt or oversized lines rather than failing, and returns
// a SessionWarning if the file is unusually large.
func ReadEventJSONL(path string) ([]event.Event, *SessionWarning, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("open events file: %w", err)
	}
	defer file.Close()

	// Check file size for warning.
	var warn SessionWarning
	if info, err := file.Stat(); err == nil {
		if info.Size() > sessionFileSizeWarn {
			warn.FileSize = info.Size()
		}
	}

	var events []event.Event
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, maxScannerBuffer), maxScannerBuffer)

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var evt event.Event
		if err := json.Unmarshal(line, &evt); err != nil {
			slog.Warn("skipping corrupt session line",
				"path", path,
				"line", lineNum,
				"error", err,
				"line_size", len(line),
			)
			warn.Skipped++
			continue
		}
		events = append(events, evt)
	}

	if err := scanner.Err(); err != nil {
		slog.Warn("scanner error reading session file, returning partial results",
			"path", path,
			"error", err,
			"events_read", len(events),
		)
		warn.Skipped++
	}

	if len(events) > sessionEventCountWarn {
		warn.EventCount = len(events)
	}

	var warnPtr *SessionWarning
	if warn.FileSize > 0 || warn.EventCount > 0 || warn.Skipped > 0 {
		warnPtr = &warn
	}

	return events, warnPtr, nil
}

// AppendEventJSONL appends an event to a JSONL file.
func AppendEventJSONL(path string, evt *event.Event) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	data, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open events file: %w", err)
	}
	defer file.Close()

	if _, err := file.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write event: %w", err)
	}

	return nil
}

// readJSON reads a JSON file into the given value.
func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// writeJSON writes a value as JSON to a file atomically.
func writeJSON(path string, v any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.Marshal(v)
	if err != nil {
		return err
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}

	return os.Rename(tmpPath, path)
}
