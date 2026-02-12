package trust

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AttachmentStore handles filesystem storage for email attachments.
// Attachments are stored in a sandboxed directory structure:
// basePath/{message_id}/{filename}
type AttachmentStore struct {
	basePath string
}

// NewAttachmentStore creates a new attachment store at the given base path.
// The base path will be created if it doesn't exist.
func NewAttachmentStore(basePath string) (*AttachmentStore, error) {
	if err := os.MkdirAll(basePath, 0700); err != nil {
		return nil, fmt.Errorf("failed to create attachments directory: %w", err)
	}
	return &AttachmentStore{basePath: basePath}, nil
}

// Save stores an attachment and returns the relative path.
// The file is stored at {message_id}/{filename}.
func (s *AttachmentStore) Save(messageID, filename string, data []byte) (string, error) {
	// Sanitize inputs to prevent path traversal
	messageID = sanitizePath(messageID)
	filename = sanitizePath(filename)

	if messageID == "" || filename == "" {
		return "", fmt.Errorf("invalid message_id or filename")
	}

	// Create message directory
	msgDir := filepath.Join(s.basePath, messageID)
	if err := os.MkdirAll(msgDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create message directory: %w", err)
	}

	// Write file
	fullPath := filepath.Join(msgDir, filename)
	if err := os.WriteFile(fullPath, data, 0600); err != nil {
		return "", fmt.Errorf("failed to write attachment: %w", err)
	}

	// Return relative path
	return filepath.Join(messageID, filename), nil
}

// Read retrieves an attachment by its relative path.
func (s *AttachmentStore) Read(relativePath string) ([]byte, error) {
	// Validate the path doesn't escape the base directory
	fullPath := filepath.Join(s.basePath, relativePath)
	if !strings.HasPrefix(fullPath, s.basePath) {
		return nil, fmt.Errorf("invalid path")
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("attachment not found")
		}
		return nil, fmt.Errorf("failed to read attachment: %w", err)
	}
	return data, nil
}

// Delete removes an attachment by its relative path.
func (s *AttachmentStore) Delete(relativePath string) error {
	fullPath := filepath.Join(s.basePath, relativePath)
	if !strings.HasPrefix(fullPath, s.basePath) {
		return fmt.Errorf("invalid path")
	}

	if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete attachment: %w", err)
	}

	// Try to remove the parent directory if empty
	dir := filepath.Dir(fullPath)
	_ = os.Remove(dir) // Ignore errors (directory might not be empty)

	return nil
}

// DeleteMessage removes all attachments for a message.
func (s *AttachmentStore) DeleteMessage(messageID string) error {
	messageID = sanitizePath(messageID)
	if messageID == "" {
		return fmt.Errorf("invalid message_id")
	}

	msgDir := filepath.Join(s.basePath, messageID)
	if !strings.HasPrefix(msgDir, s.basePath) {
		return fmt.Errorf("invalid path")
	}

	return os.RemoveAll(msgDir)
}

// BasePath returns the base path for the attachment store.
func (s *AttachmentStore) BasePath() string {
	return s.basePath
}

// sanitizePath removes any path traversal attempts from a path component.
func sanitizePath(p string) string {
	// Remove any directory separators and parent references
	p = filepath.Base(p)
	if p == "." || p == ".." {
		return ""
	}
	return p
}
