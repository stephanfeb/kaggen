// Package agent provides the SFTP tool for secure file transfer.
package agent

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"github.com/yourusername/kaggen/internal/config"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// SFTP limits.
const (
	sftpMaxFileSize     = 100 * 1024 * 1024 // 100MB max file transfer
	sftpMaxContentSize  = 1 * 1024 * 1024   // 1MB max for content-based operations
	sftpMaxListEntries  = 1000              // Max entries in list
	sftpDefaultFileMode = 0644
	sftpDefaultDirMode  = 0755
)

// SFTPToolArgs defines the input arguments for the sftp tool.
type SFTPToolArgs struct {
	Action       string `json:"action" jsonschema:"required,description=Action: upload download list mkdir rm stat,enum=upload,enum=download,enum=list,enum=mkdir,enum=rm,enum=stat"`
	ConnectionID string `json:"connection_id" jsonschema:"required,description=SSH connection ID to use (from ssh connect action)."`
	RemotePath   string `json:"remote_path,omitempty" jsonschema:"description=Remote file or directory path. Required for most actions."`
	LocalPath    string `json:"local_path,omitempty" jsonschema:"description=Local file path. Required for upload/download with files."`
	Content      string `json:"content,omitempty" jsonschema:"description=File content for upload (alternative to local_path). Max 1MB."`
	Recursive    bool   `json:"recursive,omitempty" jsonschema:"description=Recursive operation (for list and rm)."`
	Permissions  int    `json:"permissions,omitempty" jsonschema:"description=File permissions as octal number (e.g. 0644). Default: 0644 for files 0755 for dirs."`
}

// SFTPToolResult is the result of an SFTP operation.
type SFTPToolResult struct {
	Success        bool           `json:"success"`
	Message        string         `json:"message"`
	Entries        []SFTPEntry    `json:"entries,omitempty"`
	FileInfo       *SFTPFileInfo  `json:"file_info,omitempty"`
	Content        string         `json:"content,omitempty"`
	LocalPath      string         `json:"local_path,omitempty"`
	BytesTransferred int64        `json:"bytes_transferred,omitempty"`
}

// SFTPEntry represents a file or directory in a listing.
type SFTPEntry struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	IsDir       bool   `json:"is_dir"`
	Size        int64  `json:"size"`
	Permissions string `json:"permissions"`
	ModTime     string `json:"mod_time"`
}

// SFTPFileInfo contains detailed file information.
type SFTPFileInfo struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Size        int64  `json:"size"`
	IsDir       bool   `json:"is_dir"`
	Permissions string `json:"permissions"`
	ModTime     string `json:"mod_time"`
}

// NewSFTPTool creates a new SFTP tool.
func NewSFTPTool(manager *SSHConnectionManager) tool.CallableTool {
	return function.NewFunctionTool(
		func(ctx context.Context, args SFTPToolArgs) (*SFTPToolResult, error) {
			return executeSFTPTool(ctx, args, manager)
		},
		function.WithName("sftp"),
		function.WithDescription(`Transfer files securely via SFTP.

Requires an active SSH connection (from ssh connect action).

Actions:
- upload: Upload a file (from local_path or content string)
- download: Download a file (to local_path or return content)
- list: List directory contents (supports recursive)
- mkdir: Create a directory
- rm: Remove a file or directory (supports recursive)
- stat: Get file or directory information

Limits:
- Max file size: 100MB
- Max content upload: 1MB (use local_path for larger files)
- Max list entries: 1000`),
	)
}

// executeSFTPTool handles SFTP tool actions.
func executeSFTPTool(ctx context.Context, args SFTPToolArgs, manager *SSHConnectionManager) (*SFTPToolResult, error) {
	if args.ConnectionID == "" {
		return &SFTPToolResult{
			Success: false,
			Message: "connection_id is required",
		}, nil
	}

	// Get SFTP client
	sftpClient, err := manager.GetSFTPClient(args.ConnectionID)
	if err != nil {
		return &SFTPToolResult{
			Success: false,
			Message: fmt.Sprintf("failed to get SFTP client: %s", err),
		}, nil
	}

	// Update activity
	manager.UpdateActivity(args.ConnectionID)

	switch args.Action {
	case "upload":
		return sftpUpload(args, sftpClient)
	case "download":
		return sftpDownload(args, sftpClient)
	case "list":
		return sftpList(args, sftpClient)
	case "mkdir":
		return sftpMkdir(args, sftpClient)
	case "rm":
		return sftpRemove(args, sftpClient)
	case "stat":
		return sftpStat(args, sftpClient)
	default:
		return &SFTPToolResult{
			Success: false,
			Message: fmt.Sprintf("unknown action: %s", args.Action),
		}, nil
	}
}

// sftpUpload uploads a file to the remote server.
func sftpUpload(args SFTPToolArgs, client *sftp.Client) (*SFTPToolResult, error) {
	if args.RemotePath == "" {
		return &SFTPToolResult{
			Success: false,
			Message: "remote_path is required for upload",
		}, nil
	}

	var reader io.Reader

	if args.Content != "" {
		// Upload from content string
		if len(args.Content) > sftpMaxContentSize {
			return &SFTPToolResult{
				Success: false,
				Message: fmt.Sprintf("content too large (%d bytes, max %d)", len(args.Content), sftpMaxContentSize),
			}, nil
		}
		reader = strings.NewReader(args.Content)
	} else if args.LocalPath != "" {
		// Upload from local file
		localPath := config.ExpandPath(args.LocalPath)
		f, err := os.Open(localPath)
		if err != nil {
			return &SFTPToolResult{
				Success: false,
				Message: fmt.Sprintf("failed to open local file: %s", err),
			}, nil
		}
		defer f.Close()

		info, err := f.Stat()
		if err != nil {
			return &SFTPToolResult{
				Success: false,
				Message: fmt.Sprintf("failed to stat local file: %s", err),
			}, nil
		}
		if info.Size() > sftpMaxFileSize {
			return &SFTPToolResult{
				Success: false,
				Message: fmt.Sprintf("file too large (%d bytes, max %d)", info.Size(), sftpMaxFileSize),
			}, nil
		}
		reader = f
	} else {
		return &SFTPToolResult{
			Success: false,
			Message: "either content or local_path is required for upload",
		}, nil
	}

	// Create remote file
	remoteFile, err := client.Create(args.RemotePath)
	if err != nil {
		return &SFTPToolResult{
			Success: false,
			Message: fmt.Sprintf("failed to create remote file: %s", err),
		}, nil
	}
	defer remoteFile.Close()

	// Copy data
	written, err := io.Copy(remoteFile, reader)
	if err != nil {
		return &SFTPToolResult{
			Success: false,
			Message: fmt.Sprintf("failed to write to remote file: %s", err),
		}, nil
	}

	// Set permissions if specified
	if args.Permissions != 0 {
		if err := client.Chmod(args.RemotePath, os.FileMode(args.Permissions)); err != nil {
			return &SFTPToolResult{
				Success:          true,
				Message:          fmt.Sprintf("uploaded %d bytes but failed to set permissions: %s", written, err),
				BytesTransferred: written,
			}, nil
		}
	}

	return &SFTPToolResult{
		Success:          true,
		Message:          fmt.Sprintf("uploaded %d bytes to %s", written, args.RemotePath),
		BytesTransferred: written,
	}, nil
}

// sftpDownload downloads a file from the remote server.
func sftpDownload(args SFTPToolArgs, client *sftp.Client) (*SFTPToolResult, error) {
	if args.RemotePath == "" {
		return &SFTPToolResult{
			Success: false,
			Message: "remote_path is required for download",
		}, nil
	}

	// Check remote file size
	info, err := client.Stat(args.RemotePath)
	if err != nil {
		return &SFTPToolResult{
			Success: false,
			Message: fmt.Sprintf("failed to stat remote file: %s", err),
		}, nil
	}
	if info.IsDir() {
		return &SFTPToolResult{
			Success: false,
			Message: "cannot download a directory",
		}, nil
	}
	if info.Size() > sftpMaxFileSize {
		return &SFTPToolResult{
			Success: false,
			Message: fmt.Sprintf("file too large (%d bytes, max %d)", info.Size(), sftpMaxFileSize),
		}, nil
	}

	// Open remote file
	remoteFile, err := client.Open(args.RemotePath)
	if err != nil {
		return &SFTPToolResult{
			Success: false,
			Message: fmt.Sprintf("failed to open remote file: %s", err),
		}, nil
	}
	defer remoteFile.Close()

	if args.LocalPath != "" {
		// Download to local file
		localPath := config.ExpandPath(args.LocalPath)

		// Ensure parent directory exists
		if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
			return &SFTPToolResult{
				Success: false,
				Message: fmt.Sprintf("failed to create local directory: %s", err),
			}, nil
		}

		localFile, err := os.Create(localPath)
		if err != nil {
			return &SFTPToolResult{
				Success: false,
				Message: fmt.Sprintf("failed to create local file: %s", err),
			}, nil
		}
		defer localFile.Close()

		written, err := io.Copy(localFile, remoteFile)
		if err != nil {
			return &SFTPToolResult{
				Success: false,
				Message: fmt.Sprintf("failed to write local file: %s", err),
			}, nil
		}

		return &SFTPToolResult{
			Success:          true,
			Message:          fmt.Sprintf("downloaded %d bytes to %s", written, localPath),
			LocalPath:        localPath,
			BytesTransferred: written,
		}, nil
	}

	// Download to content (for small text files)
	if info.Size() > sftpMaxContentSize {
		return &SFTPToolResult{
			Success: false,
			Message: fmt.Sprintf("file too large for content return (%d bytes, max %d). Use local_path instead.", info.Size(), sftpMaxContentSize),
		}, nil
	}

	data, err := io.ReadAll(remoteFile)
	if err != nil {
		return &SFTPToolResult{
			Success: false,
			Message: fmt.Sprintf("failed to read remote file: %s", err),
		}, nil
	}

	return &SFTPToolResult{
		Success:          true,
		Message:          fmt.Sprintf("downloaded %d bytes", len(data)),
		Content:          string(data),
		BytesTransferred: int64(len(data)),
	}, nil
}

// sftpList lists directory contents.
func sftpList(args SFTPToolArgs, client *sftp.Client) (*SFTPToolResult, error) {
	if args.RemotePath == "" {
		return &SFTPToolResult{
			Success: false,
			Message: "remote_path is required for list",
		}, nil
	}

	var entries []SFTPEntry

	if args.Recursive {
		// Recursive listing
		err := walkDir(client, args.RemotePath, func(path string, info os.FileInfo) error {
			if len(entries) >= sftpMaxListEntries {
				return fmt.Errorf("too many entries (max %d)", sftpMaxListEntries)
			}
			entries = append(entries, fileInfoToEntry(path, info))
			return nil
		})
		if err != nil {
			return &SFTPToolResult{
				Success: false,
				Message: fmt.Sprintf("failed to list directory: %s", err),
			}, nil
		}
	} else {
		// Non-recursive listing
		files, err := client.ReadDir(args.RemotePath)
		if err != nil {
			return &SFTPToolResult{
				Success: false,
				Message: fmt.Sprintf("failed to list directory: %s", err),
			}, nil
		}

		for _, info := range files {
			if len(entries) >= sftpMaxListEntries {
				break
			}
			path := filepath.Join(args.RemotePath, info.Name())
			entries = append(entries, fileInfoToEntry(path, info))
		}
	}

	// Sort entries: directories first, then by name
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return entries[i].Name < entries[j].Name
	})

	return &SFTPToolResult{
		Success: true,
		Message: fmt.Sprintf("listed %d entries", len(entries)),
		Entries: entries,
	}, nil
}

// walkDir recursively walks a remote directory.
func walkDir(client *sftp.Client, path string, fn func(string, os.FileInfo) error) error {
	info, err := client.Stat(path)
	if err != nil {
		return err
	}

	if err := fn(path, info); err != nil {
		return err
	}

	if !info.IsDir() {
		return nil
	}

	files, err := client.ReadDir(path)
	if err != nil {
		return err
	}

	for _, f := range files {
		childPath := filepath.Join(path, f.Name())
		if err := walkDir(client, childPath, fn); err != nil {
			return err
		}
	}

	return nil
}

// sftpMkdir creates a directory.
func sftpMkdir(args SFTPToolArgs, client *sftp.Client) (*SFTPToolResult, error) {
	if args.RemotePath == "" {
		return &SFTPToolResult{
			Success: false,
			Message: "remote_path is required for mkdir",
		}, nil
	}

	if err := client.MkdirAll(args.RemotePath); err != nil {
		return &SFTPToolResult{
			Success: false,
			Message: fmt.Sprintf("failed to create directory: %s", err),
		}, nil
	}

	// Set permissions if specified
	mode := os.FileMode(sftpDefaultDirMode)
	if args.Permissions != 0 {
		mode = os.FileMode(args.Permissions)
	}
	if err := client.Chmod(args.RemotePath, mode); err != nil {
		return &SFTPToolResult{
			Success: true,
			Message: fmt.Sprintf("created directory but failed to set permissions: %s", err),
		}, nil
	}

	return &SFTPToolResult{
		Success: true,
		Message: fmt.Sprintf("created directory %s", args.RemotePath),
	}, nil
}

// sftpRemove removes a file or directory.
func sftpRemove(args SFTPToolArgs, client *sftp.Client) (*SFTPToolResult, error) {
	if args.RemotePath == "" {
		return &SFTPToolResult{
			Success: false,
			Message: "remote_path is required for rm",
		}, nil
	}

	// Stat to check if it exists and if it's a directory
	info, err := client.Stat(args.RemotePath)
	if err != nil {
		return &SFTPToolResult{
			Success: false,
			Message: fmt.Sprintf("path not found: %s", err),
		}, nil
	}

	if info.IsDir() {
		if args.Recursive {
			if err := client.RemoveAll(args.RemotePath); err != nil {
				return &SFTPToolResult{
					Success: false,
					Message: fmt.Sprintf("failed to remove directory: %s", err),
				}, nil
			}
			return &SFTPToolResult{
				Success: true,
				Message: fmt.Sprintf("removed directory %s recursively", args.RemotePath),
			}, nil
		}
		return &SFTPToolResult{
			Success: false,
			Message: "cannot remove directory without recursive: true",
		}, nil
	}

	if err := client.Remove(args.RemotePath); err != nil {
		return &SFTPToolResult{
			Success: false,
			Message: fmt.Sprintf("failed to remove file: %s", err),
		}, nil
	}

	return &SFTPToolResult{
		Success: true,
		Message: fmt.Sprintf("removed %s", args.RemotePath),
	}, nil
}

// sftpStat gets file or directory information.
func sftpStat(args SFTPToolArgs, client *sftp.Client) (*SFTPToolResult, error) {
	if args.RemotePath == "" {
		return &SFTPToolResult{
			Success: false,
			Message: "remote_path is required for stat",
		}, nil
	}

	info, err := client.Stat(args.RemotePath)
	if err != nil {
		return &SFTPToolResult{
			Success: false,
			Message: fmt.Sprintf("failed to stat path: %s", err),
		}, nil
	}

	return &SFTPToolResult{
		Success: true,
		Message: "stat successful",
		FileInfo: &SFTPFileInfo{
			Name:        info.Name(),
			Path:        args.RemotePath,
			Size:        info.Size(),
			IsDir:       info.IsDir(),
			Permissions: info.Mode().String(),
			ModTime:     info.ModTime().Format(time.RFC3339),
		},
	}, nil
}

// fileInfoToEntry converts os.FileInfo to SFTPEntry.
func fileInfoToEntry(path string, info fs.FileInfo) SFTPEntry {
	return SFTPEntry{
		Name:        info.Name(),
		Path:        path,
		IsDir:       info.IsDir(),
		Size:        info.Size(),
		Permissions: info.Mode().String(),
		ModTime:     info.ModTime().Format(time.RFC3339),
	}
}
