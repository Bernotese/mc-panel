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
