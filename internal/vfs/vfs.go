// Package vfs provides a virtual filesystem abstraction for sandboxing agent I/O.
//
// Agents interact with the filesystem exclusively through the FS interface.
// Two implementations are provided:
//   - ScopedFS: jails operations to a root directory on the real filesystem
//   - MemFS: in-memory filesystem for tests and ephemeral sandboxes
package vfs

import (
	"io"
	"io/fs"
)

// FS is a read-write virtual filesystem interface.
// It extends the read-only io/fs.FS with write operations needed by agent tools.
type FS interface {
	// Open opens a file for reading.
	Open(name string) (fs.File, error)

	// Stat returns file info without opening the file.
	Stat(name string) (fs.FileInfo, error)

	// ReadDir reads a directory and returns its entries.
	ReadDir(name string) ([]fs.DirEntry, error)

	// ReadFile reads an entire file and returns its contents.
	ReadFile(name string) ([]byte, error)

	// WriteFile writes data to a file, creating it if necessary.
	WriteFile(name string, data []byte, perm fs.FileMode) error

	// MkdirAll creates a directory and all parents as needed.
	MkdirAll(name string, perm fs.FileMode) error

	// Remove removes a file or empty directory.
	Remove(name string) error

	// RemoveAll removes a path and all children.
	RemoveAll(name string) error

	// Rename moves/renames a file or directory.
	Rename(oldpath, newpath string) error

	// OpenFile opens a file with the given flags and permissions.
	// Flags use the standard os package constants (os.O_APPEND, etc.).
	OpenFile(name string, flag int, perm fs.FileMode) (WritableFile, error)
}

// WritableFile extends fs.File with write capabilities.
type WritableFile interface {
	fs.File
	io.Writer
	Sync() error
}
