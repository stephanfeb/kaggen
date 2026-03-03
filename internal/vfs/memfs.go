package vfs

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemFS is an in-memory filesystem implementation for tests and ephemeral sandboxes.
type MemFS struct {
	mu   sync.RWMutex
	root *memNode
}

// Compile-time interface check.
var _ FS = (*MemFS)(nil)

// NewMemFS creates a new empty in-memory filesystem.
func NewMemFS() *MemFS {
	return &MemFS{
		root: &memNode{
			name:     "",
			isDir:    true,
			children: make(map[string]*memNode),
			modTime:  time.Now(),
			mode:     0755 | fs.ModeDir,
		},
	}
}

type memNode struct {
	name     string
	isDir    bool
	data     []byte
	children map[string]*memNode
	modTime  time.Time
	mode     fs.FileMode
}

// memFileInfo implements fs.FileInfo.
type memFileInfo struct {
	name    string
	size    int64
	mode    fs.FileMode
	modTime time.Time
	isDir   bool
}

func (fi *memFileInfo) Name() string      { return fi.name }
func (fi *memFileInfo) Size() int64       { return fi.size }
func (fi *memFileInfo) Mode() fs.FileMode { return fi.mode }
func (fi *memFileInfo) ModTime() time.Time { return fi.modTime }
func (fi *memFileInfo) IsDir() bool       { return fi.isDir }
func (fi *memFileInfo) Sys() any          { return nil }

// memDirEntry implements fs.DirEntry.
type memDirEntry struct {
	info *memFileInfo
}

func (de *memDirEntry) Name() string               { return de.info.name }
func (de *memDirEntry) IsDir() bool                { return de.info.isDir }
func (de *memDirEntry) Type() fs.FileMode          { return de.info.mode.Type() }
func (de *memDirEntry) Info() (fs.FileInfo, error)  { return de.info, nil }

func (n *memNode) fileInfo() *memFileInfo {
	size := int64(len(n.data))
	return &memFileInfo{
		name:    n.name,
		size:    size,
		mode:    n.mode,
		modTime: n.modTime,
		isDir:   n.isDir,
	}
}

// clean normalises a path for internal use.
func clean(name string) string {
	p := path.Clean("/" + name)
	if p == "/" {
		return ""
	}
	return p[1:] // strip leading /
}

// split returns the parent path and base name.
func split(name string) (string, string) {
	dir, base := path.Split(name)
	return strings.TrimSuffix(dir, "/"), base
}

// lookup traverses the tree to find a node.
func (m *MemFS) lookup(name string) (*memNode, error) {
	name = clean(name)
	if name == "" {
		return m.root, nil
	}
	parts := strings.Split(name, "/")
	cur := m.root
	for _, part := range parts {
		if !cur.isDir {
			return nil, &fs.PathError{Op: "lookup", Path: name, Err: fs.ErrNotExist}
		}
		child, ok := cur.children[part]
		if !ok {
			return nil, &fs.PathError{Op: "lookup", Path: name, Err: fs.ErrNotExist}
		}
		cur = child
	}
	return cur, nil
}

// lookupParent finds the parent node and returns the base name.
func (m *MemFS) lookupParent(name string) (*memNode, string, error) {
	name = clean(name)
	if name == "" {
		return nil, "", &fs.PathError{Op: "lookup", Path: "/", Err: fs.ErrInvalid}
	}
	dir, base := split(name)
	parent, err := m.lookup(dir)
	if err != nil {
		return nil, "", err
	}
	if !parent.isDir {
		return nil, "", &fs.PathError{Op: "lookup", Path: dir, Err: fmt.Errorf("not a directory")}
	}
	return parent, base, nil
}

// memFile is a readable file handle backed by a snapshot of the data.
type memFile struct {
	info   *memFileInfo
	reader *bytes.Reader
	node   *memNode // for directory listing
	mu     *sync.RWMutex
}

func (f *memFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *memFile) Close() error               { return nil }

func (f *memFile) Read(b []byte) (int, error) {
	if f.reader == nil {
		return 0, io.EOF
	}
	return f.reader.Read(b)
}

func (f *memFile) ReadDir(n int) ([]fs.DirEntry, error) {
	if f.node == nil || !f.node.isDir {
		return nil, fmt.Errorf("not a directory")
	}
	f.mu.RLock()
	defer f.mu.RUnlock()

	entries := make([]fs.DirEntry, 0, len(f.node.children))
	for _, child := range f.node.children {
		entries = append(entries, &memDirEntry{info: child.fileInfo()})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	if n > 0 && n < len(entries) {
		entries = entries[:n]
	}
	return entries, nil
}

// memWritableFile supports both reading and writing for OpenFile.
type memWritableFile struct {
	name   string
	node   *memNode
	buf    *bytes.Buffer
	mu     *sync.RWMutex
	flag   int
	closed bool
}

func (f *memWritableFile) Stat() (fs.FileInfo, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.node.fileInfo(), nil
}

func (f *memWritableFile) Read(b []byte) (int, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return bytes.NewReader(f.node.data).Read(b)
}

func (f *memWritableFile) Write(p []byte) (int, error) {
	if f.closed {
		return 0, fs.ErrClosed
	}
	return f.buf.Write(p)
}

func (f *memWritableFile) Sync() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.flag&os.O_APPEND != 0 {
		f.node.data = append(f.node.data, f.buf.Bytes()...)
	} else {
		f.node.data = make([]byte, f.buf.Len())
		copy(f.node.data, f.buf.Bytes())
	}
	f.node.modTime = time.Now()
	return nil
}

func (f *memWritableFile) Close() error {
	if f.closed {
		return nil
	}
	f.closed = true
	// Flush pending writes.
	return f.Sync()
}

// --- FS interface implementation ---

func (m *MemFS) Open(name string) (fs.File, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	node, err := m.lookup(name)
	if err != nil {
		return nil, err
	}
	return &memFile{
		info:   node.fileInfo(),
		reader: bytes.NewReader(node.data),
		node:   node,
		mu:     &m.mu,
	}, nil
}

func (m *MemFS) Stat(name string) (fs.FileInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	node, err := m.lookup(name)
	if err != nil {
		return nil, err
	}
	return node.fileInfo(), nil
}

func (m *MemFS) ReadDir(name string) ([]fs.DirEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	node, err := m.lookup(name)
	if err != nil {
		return nil, err
	}
	if !node.isDir {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fmt.Errorf("not a directory")}
	}
	entries := make([]fs.DirEntry, 0, len(node.children))
	for _, child := range node.children {
		entries = append(entries, &memDirEntry{info: child.fileInfo()})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	return entries, nil
}

func (m *MemFS) ReadFile(name string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	node, err := m.lookup(name)
	if err != nil {
		return nil, err
	}
	if node.isDir {
		return nil, &fs.PathError{Op: "read", Path: name, Err: fmt.Errorf("is a directory")}
	}
	cp := make([]byte, len(node.data))
	copy(cp, node.data)
	return cp, nil
}

func (m *MemFS) WriteFile(name string, data []byte, perm fs.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	parent, base, err := m.lookupParent(name)
	if err != nil {
		return err
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	if existing, ok := parent.children[base]; ok {
		if existing.isDir {
			return &fs.PathError{Op: "write", Path: name, Err: fmt.Errorf("is a directory")}
		}
		existing.data = cp
		existing.modTime = time.Now()
		existing.mode = perm
		return nil
	}
	parent.children[base] = &memNode{
		name:    base,
		data:    cp,
		modTime: time.Now(),
		mode:    perm,
	}
	return nil
}

func (m *MemFS) MkdirAll(name string, perm fs.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	name = clean(name)
	if name == "" {
		return nil // root always exists
	}
	parts := strings.Split(name, "/")
	cur := m.root
	for _, part := range parts {
		if child, ok := cur.children[part]; ok {
			if !child.isDir {
				return &fs.PathError{Op: "mkdir", Path: name, Err: fmt.Errorf("not a directory")}
			}
			cur = child
		} else {
			node := &memNode{
				name:     part,
				isDir:    true,
				children: make(map[string]*memNode),
				modTime:  time.Now(),
				mode:     perm | fs.ModeDir,
			}
			cur.children[part] = node
			cur = node
		}
	}
	return nil
}

func (m *MemFS) Remove(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	name = clean(name)
	if name == "" {
		return &fs.PathError{Op: "remove", Path: "/", Err: fs.ErrPermission}
	}
	parent, base, err := m.lookupParent(name)
	if err != nil {
		return err
	}
	child, ok := parent.children[base]
	if !ok {
		return &fs.PathError{Op: "remove", Path: name, Err: fs.ErrNotExist}
	}
	if child.isDir && len(child.children) > 0 {
		return &fs.PathError{Op: "remove", Path: name, Err: fmt.Errorf("directory not empty")}
	}
	delete(parent.children, base)
	return nil
}

func (m *MemFS) RemoveAll(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	name = clean(name)
	if name == "" {
		return &fs.PathError{Op: "removeall", Path: "/", Err: fs.ErrPermission}
	}
	parent, base, err := m.lookupParent(name)
	if err != nil {
		// If parent doesn't exist, nothing to remove.
		return nil
	}
	if _, ok := parent.children[base]; !ok {
		return nil // nothing to remove
	}
	delete(parent.children, base)
	return nil
}

func (m *MemFS) Rename(oldpath, newpath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	oldParent, oldBase, err := m.lookupParent(oldpath)
	if err != nil {
		return err
	}
	node, ok := oldParent.children[oldBase]
	if !ok {
		return &fs.PathError{Op: "rename", Path: oldpath, Err: fs.ErrNotExist}
	}

	newParent, newBase, err := m.lookupParent(newpath)
	if err != nil {
		return err
	}

	// Remove from old location, add to new.
	delete(oldParent.children, oldBase)
	node.name = newBase
	newParent.children[newBase] = node
	return nil
}

func (m *MemFS) OpenFile(name string, flag int, perm fs.FileMode) (WritableFile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	parent, base, err := m.lookupParent(name)
	if err != nil {
		return nil, err
	}

	node, exists := parent.children[base]
	if !exists {
		if flag&os.O_CREATE == 0 {
			return nil, &fs.PathError{Op: "openfile", Path: name, Err: fs.ErrNotExist}
		}
		node = &memNode{
			name:    base,
			modTime: time.Now(),
			mode:    perm,
		}
		parent.children[base] = node
	}
	if node.isDir {
		return nil, &fs.PathError{Op: "openfile", Path: name, Err: fmt.Errorf("is a directory")}
	}

	buf := &bytes.Buffer{}
	if flag&os.O_TRUNC != 0 {
		node.data = nil
	}
	// For non-append writes, start with existing data so Write replaces.
	if flag&os.O_APPEND == 0 && flag&os.O_TRUNC == 0 {
		buf.Write(node.data)
	}

	return &memWritableFile{
		name: name,
		node: node,
		buf:  buf,
		mu:   &m.mu,
		flag: flag,
	}, nil
}
