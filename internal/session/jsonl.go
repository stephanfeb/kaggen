// Package session implements trpc-agent-go's session.Service backed by the local filesystem.
package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"trpc.group/trpc-go/trpc-agent-go/event"
)

// ReadEventJSONL reads events from a JSONL file.
func ReadEventJSONL(path string) ([]event.Event, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open events file: %w", err)
	}
	defer file.Close()

	var events []event.Event
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var evt event.Event
		if err := json.Unmarshal(line, &evt); err != nil {
			return nil, fmt.Errorf("parse line %d: %w", lineNum, err)
		}
		events = append(events, evt)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan events file: %w", err)
	}

	return events, nil
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
