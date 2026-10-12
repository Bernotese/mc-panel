package files

import (
	"archive/zip"
	"bytes"
	"strconv"
	"strings"
	"testing"
)

// bigDirectoryJar is a valid jar with plugin.yml whose central directory holds about
// n * 60 KB of entry comments (comments live only in the central directory).
func bigDirectoryJar(t *testing.T, n int) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("plugin.yml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("name: Wide\n")); err != nil {
		t.Fatal(err)
	}
	comment := strings.Repeat("c", 60_000)
	for i := range n {
		if _, err := zw.CreateHeader(&zip.FileHeader{Name: "c/" + strconv.Itoa(i), Method: zip.Store, Comment: comment}); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Jar metadata reads at most maxJarCentralDirBytes (16 MiB) of central directory; extraction
// keeps its own, larger maxCentralDirBytes.
func TestJarMetaCentralDirectoryBudget(t *testing.T) {
	if maxJarCentralDirBytes != 16<<20 || maxCentralDirBytes != 64<<20 {
		t.Fatalf("budgets %d / %d, want 16 MiB for jars and 64 MiB for extraction", maxJarCentralDirBytes, maxCentralDirBytes)
	}
	small := bigDirectoryJar(t, 200) // ~12 MB of directory
	if m := readJarMeta(bytes.NewReader(small), int64(len(small)), maxJarEntries); m == nil || m.Name != "Wide" {
		t.Fatalf("12 MB directory: %+v, want the metadata", m)
	}
	big := bigDirectoryJar(t, 300) // ~18 MB of directory
	if m := readJarMeta(bytes.NewReader(big), int64(len(big)), maxJarEntries); m != nil {
		t.Fatalf("18 MB directory: %+v, want nil", *m)
	}
	if err := checkCentralDirectory(bytes.NewReader(big), int64(len(big)), maxJarEntries, maxCentralDirBytes); err != nil {
		t.Fatalf("the extraction budget refuses an 18 MB directory: %v", err)
	}
}
