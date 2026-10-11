//go:build linux

package files

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

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

// A FIFO swapped in after the Lstat check must not block a reader: openRegular opens with
// O_NONBLOCK and then rejects the non-regular file.
func TestOpenRegularDoesNotBlockOnFIFO(t *testing.T) {
	s, data := newService(t)
	if err := syscall.Mkfifo(filepath.Join(data, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := s.open(1)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	f, err := root.OpenFile("pipe", os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatalf("O_NONBLOCK open of a FIFO must return at once: %v", err)
	}
	_ = f.Close()
	if _, _, err := openRegular(root, "pipe", os.O_RDONLY); err == nil {
		t.Fatal("openRegular accepted a FIFO")
	}
}
