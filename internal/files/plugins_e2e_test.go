package files

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// The checksum parameters and hash_mismatch reach the filesapi client (V1) over the socket.
func TestUploadCheckedEndToEnd(t *testing.T) {
	c, data := startUnix(t, uint32(os.Getuid()))
	bg := context.Background()
	put(t, data, "plugins/keep.txt", "")
	const content = "end to end jar"
	h256, h512 := sums(content)

	res, err := c.UploadChecked(bg, 1, "plugins/E2E.jar", false, int64(len(content)),
		filesapi.Checksum{Algo: filesapi.AlgoSHA512, Hex: h512}, strings.NewReader(content))
	if err != nil || res.Size != int64(len(content)) || res.SHA256 != h256 || res.SHA512 != h512 {
		t.Fatalf("checked upload: %+v %v", res, err)
	}
	if v, _ := get(t, data, "plugins/E2E.jar"); v != content {
		t.Fatalf("stored %q", v)
	}
	_, err = c.UploadChecked(bg, 1, "plugins/Bad.jar", false, int64(len(content)),
		filesapi.Checksum{Algo: filesapi.AlgoSHA256, Hex: strings.Repeat("0", 64)}, strings.NewReader(content))
	if !errors.Is(err, filesapi.ErrHashMismatch) || errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("mismatch over the client: %v, want ErrHashMismatch", err)
	}
	if _, ok := get(t, data, "plugins/Bad.jar"); ok {
		t.Fatal("a mismatching upload became visible")
	}
	noTemp(t, data)
}

// Jars over the socket: the client gets the service's list (V1 wire types).
func TestJarsEndToEnd(t *testing.T) {
	c, data := startUnix(t, uint32(os.Getuid()))
	bg := context.Background()
	put(t, data, "plugins/A.jar", "a")
	put(t, data, "plugins/B.jar.disabled", "b")
	res, err := c.Jars(bg, 1, filesapi.DirPlugins)
	if err != nil {
		t.Fatalf("jars: %v", err)
	}
	if len(res.Jars) != 2 || res.Truncated {
		t.Fatalf("jars: %+v", res)
	}
	_, a512 := sums("a")
	_, b512 := sums("b")
	if a, b := res.Jars[0], res.Jars[1]; a.Name != "A.jar" || !a.Enabled || a.SHA512 != a512 ||
		b.Name != "B.jar.disabled" || b.Enabled || b.SHA512 != b512 {
		t.Fatalf("jars: %+v", res.Jars)
	}
	empty, err := c.Jars(bg, 1, filesapi.DirMods)
	if err != nil || empty.Jars == nil || len(empty.Jars) != 0 {
		t.Fatalf("empty mods: %+v %v", empty, err)
	}
	if _, err := c.Jars(bg, 1, "world"); !errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("bad dir: %v, want ErrInvalid", err)
	}
}
