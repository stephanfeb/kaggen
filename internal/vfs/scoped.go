package vfs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ScopedFS restricts all filesystem operations to a root directory.
// It prevents path traversal via symlink resolution and canonical path checking.
// Errors never leak the real filesystem path.
type ScopedFS struct {
	root string // absolute, symlink-resolved path
}

// Compile-time interface check.
var _ FS = (*ScopedFS)(nil)

// NewScopedFS creates a filesystem scoped to the given root directory.
// The root must be an absolute path to an existing directory.
func NewScopedFS(root string) (*ScopedFS, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("vfs: resolve root: %w", err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("vfs: eval symlinks on root: %w", err)
	}
	info, err := os.Stat(real)
	if err != nil {
		return nil, fmt.Errorf("vfs: stat root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("vfs: root is not a directory: %s", real)
	}
	return &ScopedFS{root: real}, nil
}

// Root returns the absolute root path of this scoped filesystem.
func (s *ScopedFS) Root() string {
	return s.root
}

// resolve converts a virtual path to a real path under root.
// This is the security-critical function.
func (s *ScopedFS) resolve(name string) (string, error) {
	// Clean the path: collapse .., remove double slashes, strip leading /
	cleaned := filepath.Clean("/" + name)
	full := filepath.Join(s.root, cleaned)

	// Try to resolve the full path. If it doesn't exist yet (e.g., WriteFile
	// creating a new file), resolve the longest existing ancestor instead.
	real, err := filepath.EvalSymlinks(full)
	if err != nil {
		// Walk up to find the first existing ancestor and verify it's in scope.
		dir := full
		for {
			parent := filepath.Dir(dir)
			if parent == dir {
				// Reached filesystem root without finding an existing ancestor
				// inside our scope — this shouldn't happen since s.root exists.
				break
			}
			dir = parent
			realParent, err2 := filepath.EvalSymlinks(dir)
			if err2 == nil {
				if !s.isUnderRoot(realParent) {
					return "", &fs.PathError{Op: "resolve", Path: name, Err: fs.ErrPermission}
				}
				return full, nil
			}
		}
		// Parent resolution failed entirely — treat as not found.
		return full, nil
	}

	if !s.isUnderRoot(real) {
		return "", &fs.PathError{Op: "resolve", Path: name, Err: fs.ErrPermission}
	}
	return real, nil
}

// isUnderRoot checks whether resolved is at or under the root.
func (s *ScopedFS) isUnderRoot(resolved string) bool {
	return resolved == s.root || strings.HasPrefix(resolved, s.root+string(filepath.Separator))
}

// wrapErr rewrites an os-level error to use the virtual path, preventing
// real filesystem paths from leaking to agents.
func wrapErr(op, virtualPath string, err error) error {
	if err == nil {
		return nil
	}
	// Unwrap os.PathError to avoid leaking the real filesystem path.
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return &fs.PathError{Op: op, Path: virtualPath, Err: pathErr.Err}
	}
	return &fs.PathError{Op: op, Path: virtualPath, Err: err}
}

func (s *ScopedFS) Open(name string) (fs.File, error) {
	real, err := s.resolve(name)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(real)
	if err != nil {
		return nil, wrapErr("open", name, err)
	}
	return f, nil
}

func (s *ScopedFS) Stat(name string) (fs.FileInfo, error) {
	real, err := s.resolve(name)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(real)
	if err != nil {
		return nil, wrapErr("stat", name, err)
	}
	return info, nil
}

func (s *ScopedFS) ReadDir(name string) ([]fs.DirEntry, error) {
	real, err := s.resolve(name)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(real)
	if err != nil {
		return nil, wrapErr("readdir", name, err)
	}
	return entries, nil
}

func (s *ScopedFS) ReadFile(name string) ([]byte, error) {
	real, err := s.resolve(name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(real)
	if err != nil {
		return nil, wrapErr("read", name, err)
	}
	return data, nil
}

func (s *ScopedFS) WriteFile(name string, data []byte, perm fs.FileMode) error {
	real, err := s.resolve(name)
	if err != nil {
		return err
	}
	if err := os.WriteFile(real, data, perm); err != nil {
		return wrapErr("write", name, err)
	}
	return nil
}

func (s *ScopedFS) MkdirAll(name string, perm fs.FileMode) error {
	real, err := s.resolve(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(real, perm); err != nil {
		return wrapErr("mkdir", name, err)
	}
	return nil
}

func (s *ScopedFS) Remove(name string) error {
	real, err := s.resolve(name)
	if err != nil {
		return err
	}
	// Prevent removing the root itself.
	if real == s.root {
		return &fs.PathError{Op: "remove", Path: name, Err: fs.ErrPermission}
	}
	if err := os.Remove(real); err != nil {
		return wrapErr("remove", name, err)
	}
	return nil
}

func (s *ScopedFS) RemoveAll(name string) error {
	real, err := s.resolve(name)
	if err != nil {
		return err
	}
	if real == s.root {
		return &fs.PathError{Op: "removeall", Path: name, Err: fs.ErrPermission}
	}
	if err := os.RemoveAll(real); err != nil {
		return wrapErr("removeall", name, err)
	}
	return nil
}

func (s *ScopedFS) Rename(oldpath, newpath string) error {
	realOld, err := s.resolve(oldpath)
	if err != nil {
		return err
	}
	realNew, err := s.resolve(newpath)
	if err != nil {
		return err
	}
	if err := os.Rename(realOld, realNew); err != nil {
		return wrapErr("rename", oldpath, err)
	}
	return nil
}

func (s *ScopedFS) OpenFile(name string, flag int, perm fs.FileMode) (WritableFile, error) {
	real, err := s.resolve(name)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(real, flag, perm)
	if err != nil {
		return nil, wrapErr("openfile", name, err)
	}
	return f, nil
}
