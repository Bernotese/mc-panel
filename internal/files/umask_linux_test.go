//go:build linux

package files

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// The systemd unit runs the service with UMask=0027; new files and folders must then not be
// readable by others (spec §3.1), whatever mode the code asks for.
func TestCreatedModesHonourUmask(t *testing.T) {
	old := syscall.Umask(0o027)
	defer syscall.Umask(old)
	s, data := newService(t)
	if _, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "a.txt", Content: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Mkdir(ctx, 1, "dir"); err != nil {
		t.Fatal(err)
	}
	if _, err := upload(s, "dir/b.bin", false, "y"); err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]os.FileMode{"a.txt": 0o640, "dir": 0o750, "dir/b.bin": 0o640} {
		fi, err := os.Stat(filepath.Join(data, rel))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != want {
			t.Errorf("%s: mode %v, want %v", rel, fi.Mode().Perm(), want)
		}
	}
}

// A FIFO swapped in after the Lstat check must not block: files and folders are opened with
// O_NONBLOCK (and O_DIRECTORY), and the opened object must be the one Lstat saw. openChecked
// is the open step on its own, called with the Lstat result of a regular file or folder while
// the path is a FIFO - exactly the state after a swap.
func TestOpenDoesNotBlockOnSwappedFIFO(t *testing.T) {
	s, data := newService(t)
	put(t, data, "file", "x")
	if err := os.Mkdir(filepath.Join(data, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(data, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := s.open(1)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	fileInfo, err := root.Lstat("file")
	if err != nil {
		t.Fatal(err)
	}
	dirInfo, err := root.Lstat("dir")
	if err != nil {
		t.Fatal(err)
	}
	for name, open := range map[string]func() error{
		"openRegular":  func() error { _, _, err := openRegular(root, "pipe", os.O_RDONLY); return err },
		"openDir":      func() error { _, _, err := openDir(root, "pipe"); return err },
		"swapped file": func() error { _, _, err := openChecked(root, "pipe", fileInfo, os.O_RDONLY); return err },
		"swapped folder": func() error {
			_, _, err := openChecked(root, "pipe", dirInfo, os.O_RDONLY|syscall.O_DIRECTORY)
			return err
		},
		"swapped folder, no dir": func() error { _, _, err := openChecked(root, "pipe", dirInfo, os.O_RDONLY); return err },
		"list":                   func() error { _, err := s.List(ctx, 1, "pipe"); return err },
	} {
		done := make(chan error, 1)
		go func() { done <- open() }()
		select {
		case err := <-done:
			if !errors.Is(err, filesapi.ErrNotRegular) {
				t.Errorf("%s: %v, want ErrNotRegular", name, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s blocks on a FIFO", name)
		}
	}
}
