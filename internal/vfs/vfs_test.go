package vfs_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/yourusername/kaggen/internal/vfs"
)

// fsFactory creates a fresh FS for each test.
type fsFactory func(t *testing.T) vfs.FS

// runConformance runs the shared conformance suite against any FS implementation.
func runConformance(t *testing.T, name string, factory fsFactory) {
	t.Run(name, func(t *testing.T) {
		t.Run("WriteAndRead", func(t *testing.T) {
			fsys := factory(t)
			if err := fsys.WriteFile("hello.txt", []byte("world"), 0644); err != nil {
				t.Fatal(err)
			}
			data, err := fsys.ReadFile("hello.txt")
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != "world" {
				t.Fatalf("got %q, want %q", data, "world")
			}
		})

		t.Run("Stat", func(t *testing.T) {
			fsys := factory(t)
			fsys.WriteFile("test.txt", []byte("data"), 0644)
			info, err := fsys.Stat("test.txt")
			if err != nil {
				t.Fatal(err)
			}
			if info.IsDir() {
				t.Fatal("expected file, got dir")
			}
			if info.Size() != 4 {
				t.Fatalf("size = %d, want 4", info.Size())
			}
		})

		t.Run("MkdirAllAndReadDir", func(t *testing.T) {
			fsys := factory(t)
			if err := fsys.MkdirAll("a/b/c", 0755); err != nil {
				t.Fatal(err)
			}
			fsys.WriteFile("a/b/file.txt", []byte("nested"), 0644)
			entries, err := fsys.ReadDir("a/b")
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 2 {
				t.Fatalf("got %d entries, want 2", len(entries))
			}
			names := make([]string, len(entries))
			for i, e := range entries {
				names[i] = e.Name()
			}
			// Entries should be sorted
			if names[0] != "c" || names[1] != "file.txt" {
				t.Fatalf("entries = %v, want [c file.txt]", names)
			}
		})

		t.Run("Overwrite", func(t *testing.T) {
			fsys := factory(t)
			fsys.WriteFile("f.txt", []byte("v1"), 0644)
			fsys.WriteFile("f.txt", []byte("v2"), 0644)
			data, _ := fsys.ReadFile("f.txt")
			if string(data) != "v2" {
				t.Fatalf("got %q, want %q", data, "v2")
			}
		})

		t.Run("Remove", func(t *testing.T) {
			fsys := factory(t)
			fsys.WriteFile("del.txt", []byte("gone"), 0644)
			if err := fsys.Remove("del.txt"); err != nil {
				t.Fatal(err)
			}
			_, err := fsys.ReadFile("del.txt")
			if err == nil {
				t.Fatal("expected error reading removed file")
			}
		})

		t.Run("RemoveNonEmpty", func(t *testing.T) {
			fsys := factory(t)
			fsys.MkdirAll("dir", 0755)
			fsys.WriteFile("dir/file.txt", []byte("x"), 0644)
			err := fsys.Remove("dir")
			if err == nil {
				t.Fatal("expected error removing non-empty directory")
			}
		})

		t.Run("RemoveAll", func(t *testing.T) {
			fsys := factory(t)
			fsys.MkdirAll("tree/deep/nested", 0755)
			fsys.WriteFile("tree/deep/nested/f.txt", []byte("x"), 0644)
			if err := fsys.RemoveAll("tree"); err != nil {
				t.Fatal(err)
			}
			_, err := fsys.Stat("tree")
			if err == nil {
				t.Fatal("expected error after RemoveAll")
			}
		})

		t.Run("Rename", func(t *testing.T) {
			fsys := factory(t)
			fsys.WriteFile("old.txt", []byte("data"), 0644)
			if err := fsys.Rename("old.txt", "new.txt"); err != nil {
				t.Fatal(err)
			}
			data, err := fsys.ReadFile("new.txt")
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != "data" {
				t.Fatalf("got %q", data)
			}
			_, err = fsys.ReadFile("old.txt")
			if err == nil {
				t.Fatal("expected old file to be gone")
			}
		})

		t.Run("OpenFileAppend", func(t *testing.T) {
			fsys := factory(t)
			fsys.WriteFile("append.txt", []byte("hello"), 0644)
			f, err := fsys.OpenFile("append.txt", os.O_APPEND|os.O_WRONLY, 0644)
			if err != nil {
				t.Fatal(err)
			}
			f.Write([]byte(" world"))
			f.Close()

			data, _ := fsys.ReadFile("append.txt")
			if string(data) != "hello world" {
				t.Fatalf("got %q, want %q", data, "hello world")
			}
		})

		t.Run("OpenFileCreate", func(t *testing.T) {
			fsys := factory(t)
			f, err := fsys.OpenFile("new.txt", os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				t.Fatal(err)
			}
			f.Write([]byte("created"))
			f.Close()

			data, _ := fsys.ReadFile("new.txt")
			if string(data) != "created" {
				t.Fatalf("got %q", data)
			}
		})

		t.Run("ReadNonExistent", func(t *testing.T) {
			fsys := factory(t)
			_, err := fsys.ReadFile("nope.txt")
			if err == nil {
				t.Fatal("expected error")
			}
		})

		t.Run("RemoveRoot", func(t *testing.T) {
			fsys := factory(t)
			if err := fsys.Remove("/"); err == nil {
				t.Fatal("expected error removing root")
			}
			if err := fsys.RemoveAll("/"); err == nil {
				t.Fatal("expected error removing root")
			}
		})

		t.Run("Open", func(t *testing.T) {
			fsys := factory(t)
			fsys.WriteFile("readable.txt", []byte("content"), 0644)
			f, err := fsys.Open("readable.txt")
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			buf := make([]byte, 7)
			n, err := f.Read(buf)
			if err != nil {
				t.Fatal(err)
			}
			if string(buf[:n]) != "content" {
				t.Fatalf("got %q", buf[:n])
			}
		})

		t.Run("StatDir", func(t *testing.T) {
			fsys := factory(t)
			fsys.MkdirAll("mydir", 0755)
			info, err := fsys.Stat("mydir")
			if err != nil {
				t.Fatal(err)
			}
			if !info.IsDir() {
				t.Fatal("expected directory")
			}
		})
	})
}

func TestMemFS(t *testing.T) {
	runConformance(t, "MemFS", func(t *testing.T) vfs.FS {
		return vfs.NewMemFS()
	})

	t.Run("ConcurrentAccess", func(t *testing.T) {
		fsys := vfs.NewMemFS()
		var wg sync.WaitGroup
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				name := "file_" + strings.Repeat("x", i%10) + ".txt"
				fsys.WriteFile(name, []byte("data"), 0644)
				fsys.ReadFile(name)
			}(i)
		}
		wg.Wait()
	})
}

func TestScopedFS(t *testing.T) {
	runConformance(t, "ScopedFS", func(t *testing.T) vfs.FS {
		dir := t.TempDir()
		fsys, err := vfs.NewScopedFS(dir)
		if err != nil {
			t.Fatal(err)
		}
		return fsys
	})

	t.Run("PathTraversal", func(t *testing.T) {
		dir := t.TempDir()
		fsys, _ := vfs.NewScopedFS(dir)

		// Write a file outside the sandbox
		outside := filepath.Join(filepath.Dir(dir), "outside.txt")
		os.WriteFile(outside, []byte("secret"), 0644)
		t.Cleanup(func() { os.Remove(outside) })

		// Attempt to read via traversal
		_, err := fsys.ReadFile("../outside.txt")
		if err == nil {
			t.Fatal("expected path traversal to be blocked")
		}
	})

	t.Run("AbsolutePathBlocked", func(t *testing.T) {
		dir := t.TempDir()
		fsys, _ := vfs.NewScopedFS(dir)

		// Absolute paths outside root should be blocked (they get cleaned to relative)
		// Writing /etc/passwd should resolve to <root>/etc/passwd, not the real /etc/passwd
		err := fsys.MkdirAll("etc", 0755)
		if err != nil {
			t.Fatal(err)
		}
		err = fsys.WriteFile("/etc/test", []byte("safe"), 0644)
		if err != nil {
			t.Fatal(err)
		}
		// Verify it's inside the sandbox
		data, err := os.ReadFile(filepath.Join(dir, "etc", "test"))
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "safe" {
			t.Fatal("file not in expected location")
		}
	})

	t.Run("SymlinkEscape", func(t *testing.T) {
		dir := t.TempDir()
		fsys, _ := vfs.NewScopedFS(dir)

		// Create a file outside
		outside := filepath.Join(filepath.Dir(dir), "symlink_target.txt")
		os.WriteFile(outside, []byte("secret"), 0644)
		t.Cleanup(func() { os.Remove(outside) })

		// Create a symlink inside the sandbox pointing outside
		link := filepath.Join(dir, "escape")
		if err := os.Symlink(filepath.Dir(dir), link); err != nil {
			t.Skip("symlinks not supported")
		}

		_, err := fsys.ReadFile("escape/symlink_target.txt")
		if err == nil {
			t.Fatal("expected symlink escape to be blocked")
		}
	})

	t.Run("RenameStaysInScope", func(t *testing.T) {
		dir := t.TempDir()
		fsys, _ := vfs.NewScopedFS(dir)

		fsys.WriteFile("inside.txt", []byte("data"), 0644)
		// ../outside.txt gets cleaned to /outside.txt which maps to <root>/outside.txt
		// This should succeed (stays inside sandbox).
		err := fsys.Rename("inside.txt", "../outside.txt")
		if err != nil {
			t.Fatalf("rename within scope should succeed: %v", err)
		}
		// The file should be at the root of the VFS as "outside.txt"
		data, err := fsys.ReadFile("outside.txt")
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "data" {
			t.Fatalf("got %q", data)
		}
	})

	t.Run("RenameViaSymlinkEscape", func(t *testing.T) {
		dir := t.TempDir()
		fsys, _ := vfs.NewScopedFS(dir)

		// Create a symlink to parent dir
		link := filepath.Join(dir, "escape")
		if err := os.Symlink(filepath.Dir(dir), link); err != nil {
			t.Skip("symlinks not supported")
		}

		fsys.WriteFile("inside.txt", []byte("data"), 0644)
		err := fsys.Rename("inside.txt", "escape/stolen.txt")
		if err == nil {
			t.Fatal("expected symlink-based rename escape to be blocked")
		}
	})

	t.Run("RemoveRootBlocked", func(t *testing.T) {
		dir := t.TempDir()
		fsys, _ := vfs.NewScopedFS(dir)

		err := fsys.Remove(".")
		if err == nil {
			t.Fatal("expected removing root to be blocked")
		}
		err = fsys.RemoveAll("")
		if err == nil {
			t.Fatal("expected removing root to be blocked")
		}
	})

	t.Run("ErrorsUseVirtualPaths", func(t *testing.T) {
		dir := t.TempDir()
		fsys, _ := vfs.NewScopedFS(dir)

		_, err := fsys.ReadFile("nonexistent.txt")
		if err == nil {
			t.Fatal("expected error")
		}
		// The error should NOT contain the real directory path
		if strings.Contains(err.Error(), dir) {
			t.Fatalf("error leaks real path: %v", err)
		}
	})
}
