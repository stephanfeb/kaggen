package p2p

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/libp2p/go-libp2p/core/network"

	"github.com/yourusername/kaggen/internal/config"
)

// FilesProtocol handles the /kaggen/files/1.0.0 protocol.
type FilesProtocol struct {
	*APIHandler
	publicDir string
}

// NewFilesProtocol creates a new files protocol handler.
func NewFilesProtocol(logger *slog.Logger) *FilesProtocol {
	h := &FilesProtocol{
		APIHandler: NewAPIHandler(FilesProtocolID, logger),
		publicDir:  config.ExpandPath("~/.kaggen/public"),
	}

	h.RegisterMethod("get", h.getFile)
	h.RegisterMethod("list", h.listFiles)

	return h
}

// StreamHandler returns the stream handler for this protocol.
func (p *FilesProtocol) StreamHandler() network.StreamHandler {
	return p.HandleStream
}

type getFileParams struct {
	Path string `json:"path"`
}

type fileResponse struct {
	Filename string `json:"filename"`
	MimeType string `json:"mime_type"`
	Size     int64  `json:"size"`
	Data     []byte `json:"data"`
}

func (p *FilesProtocol) getFile(params json.RawMessage) (any, error) {
	var args getFileParams
	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if args.Path == "" {
		return nil, fmt.Errorf("path is required")
	}

	// Security: only allow filenames, no path traversal
	name := filepath.Base(args.Path)
	if name == "" || strings.Contains(name, "..") || strings.Contains(name, "/") {
		return nil, fmt.Errorf("invalid filename")
	}

	filePath := filepath.Join(p.publicDir, name)

	// Verify path is still inside public directory
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, fmt.Errorf("file not found")
	}
	absPubDir, _ := filepath.Abs(p.publicDir)
	if !strings.HasPrefix(absPath, absPubDir) {
		return nil, fmt.Errorf("file not found")
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("file not found")
	}

	info, _ := os.Stat(absPath)

	// Detect MIME type from extension
	mimeType := mime.TypeByExtension(filepath.Ext(name))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	return &fileResponse{
		Filename: name,
		MimeType: mimeType,
		Size:     info.Size(),
		Data:     data,
	}, nil
}

type listFilesParams struct {
	Path string `json:"path,omitempty"`
}

type fileInfo struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
	IsDir    bool   `json:"is_dir"`
}

func (p *FilesProtocol) listFiles(params json.RawMessage) (any, error) {
	var args listFilesParams
	if len(params) > 0 {
		json.Unmarshal(params, &args)
	}

	// Only list files in the public directory root
	entries, err := os.ReadDir(p.publicDir)
	if err != nil {
		// Directory might not exist yet
		return map[string]any{"files": []fileInfo{}}, nil
	}

	files := make([]fileInfo, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, fileInfo{
			Name:     entry.Name(),
			Size:     info.Size(),
			Modified: info.ModTime().Format("2006-01-02T15:04:05Z07:00"),
			IsDir:    entry.IsDir(),
		})
	}

	return map[string]any{"files": files}, nil
}
