package files

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

var ctx = context.Background()

// layout creates a typical data directory plus a secret outside of it.
func layout(t *testing.T, data string) (outside string) {
	t.Helper()
	put(t, data, "server.properties", "motd=Hi\nrcon.password=s3cret\n")
	put(t, data, ".rcon-cli.env", "password=s3cret\n")
	put(t, data, ".rcon-cli.yaml", "password: s3cret\n")
	put(t, data, ".mcpanel-tmp-dead", "partial")
	put(t, data, "plugins/EssentialsX.jar", "PK\x00\x01binary")
	put(t, data, "plugins/.rcon-cli.env", "nested, visible")
	put(t, data, "plugins/Essentials/config.yml", "spawn: true\n")
	put(t, data, "world/level.dat", "\x00\x01")
	if err := os.Symlink("world", filepath.Join(data, "current")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(data, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside = filepath.Join(filepath.Dir(filepath.Dir(data)), "secret.txt")
	if err := os.WriteFile(outside, []byte("host secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(data, "abs-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../secret.txt", filepath.Join(data, "rel-link")); err != nil {
		t.Fatal(err)
	}
	return outside
}

func TestListShowsTypesAndHidesReserved(t *testing.T) {
	s, data := newService(t)
	layout(t, data)
	res, err := s.List(ctx, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range res.Entries {
		got = append(got, e.Name+":"+string(e.Type))
	}
	want := "plugins:dir world:dir abs-link:symlink current:symlink pipe:other rel-link:symlink server.properties:file"
	if strings.Join(got, " ") != want {
		t.Fatalf("root listing:\n got %s\nwant %s", strings.Join(got, " "), want)
	}
	if res.Truncated {
		t.Fatal("small folder reported as truncated")
	}
	for _, e := range res.Entries {
		if e.Name == "server.properties" && (e.Size != int64(len("motd=Hi\nrcon.password=s3cret\n")) || e.MTime <= 0) {
			t.Fatalf("file entry: %+v", e)
		}
		if e.Type == filesapi.TypeDir && e.Size != 0 {
			t.Fatalf("folder size must be 0: %+v", e)
		}
	}

	res, err = s.List(ctx, 1, "plugins")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 3 || res.Entries[0].Name != "Essentials" || res.Entries[1].Name != ".rcon-cli.env" {
		t.Fatalf("plugins listing: %+v", res.Entries)
	}

	for p, want := range map[string]error{
		"current":           filesapi.ErrNotRegular, // symlinked folder is never listed
		"missing":           filesapi.ErrNotFound,
		".rcon-cli.env":     filesapi.ErrNotFound,
		"server.properties": filesapi.ErrNotRegular,
		"../x":              filesapi.ErrInvalid,
	} {
		if _, err := s.List(ctx, 1, p); !errors.Is(err, want) {
			t.Errorf("list %q: %v, want %v", p, err, want)
		}
	}
}

func TestListTruncates(t *testing.T) {
	s, data := newService(t)
	for i := range 60 {
		put(t, data, fmt.Sprintf("many/f%02d", i), "x")
	}
	res, err := s.List(ctx, 1, "many")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 50 || !res.Truncated {
		t.Fatalf("got %d entries truncated=%v, want 50 true", len(res.Entries), res.Truncated)
	}
}

func TestReadText(t *testing.T) {
	s, data := newService(t)
	layout(t, data)
	res, err := s.Read(ctx, 1, "plugins/Essentials/config.yml")
	if err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(filepath.Join(data, "plugins", "Essentials", "config.yml"))
	if res.Content != "spawn: true\n" || res.Size != 12 || res.MTime != fi.ModTime().UnixMilli() {
		t.Fatalf("read: %+v", res)
	}
	put(t, data, "big.txt", strings.Repeat("a", 64<<10+1))
	put(t, data, "latin1.txt", "caf\xe9")
	for p, want := range map[string]error{
		"plugins/EssentialsX.jar": filesapi.ErrNotText, // NUL byte
		"latin1.txt":              filesapi.ErrNotText,
		"big.txt":                 filesapi.ErrNotText,
		"":                        filesapi.ErrNotRegular,
	} {
		if _, err := s.Read(ctx, 1, p); !errors.Is(err, want) {
			t.Errorf("read %q: %v, want %v", p, err, want)
		}
	}
}

func TestReadRefusesUntrustedFiles(t *testing.T) {
	s, data := newService(t)
	layout(t, data)
	for p, want := range map[string]error{
		"abs-link":          filesapi.ErrNotRegular,
		"rel-link":          filesapi.ErrNotRegular,
		"current":           filesapi.ErrNotRegular,
		"current/level.dat": filesapi.ErrNotRegular,
		"pipe":              filesapi.ErrNotRegular,
		"world":             filesapi.ErrNotRegular,
		".rcon-cli.env":     filesapi.ErrNotFound,
		".rcon-cli.yaml":    filesapi.ErrNotFound,
		".mcpanel-tmp-dead": filesapi.ErrNotFound,
	} {
		done := make(chan error, 1)
		go func() {
			_, err := s.Read(ctx, 1, p)
			done <- err
		}()
		select {
		case err := <-done:
			if !errors.Is(err, want) {
				t.Errorf("read %q: %v, want %v", p, err, want)
			}
			if err != nil && strings.Contains(err.Error(), "host secret") {
				t.Errorf("read %q leaked content: %v", p, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("read %q hangs", p)
		}
	}
}

func TestReadRedactsRconPassword(t *testing.T) {
	s, data := newService(t)
	layout(t, data)
	put(t, data, "plugins/server.properties", "rcon.password=nested\n")
	res, err := s.Read(ctx, 1, "server.properties")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Content, "s3cret") || !strings.Contains(res.Content, "rcon.password=********") {
		t.Fatalf("not redacted: %q", res.Content)
	}
	res, err = s.Read(ctx, 1, "plugins/server.properties")
	if err != nil || res.Content != "rcon.password=nested\n" {
		t.Fatalf("only the top-level server.properties is redacted: %q %v", res.Content, err)
	}
}

func TestWriteCreatesAndDetectsConflicts(t *testing.T) {
	s, data := newService(t)
	res, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "notes.txt", Content: "one"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if v, _ := get(t, data, "notes.txt"); v != "one" || res.Size != 3 {
		t.Fatalf("created %q %+v", v, res)
	}
	if fi, _ := os.Stat(filepath.Join(data, "notes.txt")); fi.Mode().Perm()&0o022 != 0 {
		t.Fatalf("new file mode %v is group/world writable", fi.Mode())
	}
	if _, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "notes.txt", Content: "dup"}); !errors.Is(err, filesapi.ErrExists) {
		t.Fatalf("create over existing: %v, want ErrExists", err)
	}
	m := res.MTime
	res2, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "notes.txt", Content: "two", ExpectedMTime: &m})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if v, _ := get(t, data, "notes.txt"); v != "two" {
		t.Fatalf("updated %q", v)
	}
	stale := res2.MTime - 1000
	if _, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "notes.txt", Content: "three", ExpectedMTime: &stale}); !errors.Is(err, filesapi.ErrModified) {
		t.Fatalf("stale mtime: %v, want ErrModified", err)
	}
	if _, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "gone.txt", Content: "x", ExpectedMTime: &m}); !errors.Is(err, filesapi.ErrModified) {
		t.Fatalf("file vanished: %v, want ErrModified", err)
	}
	if _, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "big.txt", Content: strings.Repeat("a", 64<<10+1)}); !errors.Is(err, filesapi.ErrTooLarge) {
		t.Fatalf("too large: %v", err)
	}
	if _, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "missing/x.txt", Content: "x"}); !errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("missing parent: %v", err)
	}
	for _, n := range names(t, data) {
		if strings.HasPrefix(n, tempPrefix) {
			t.Fatalf("temp file left behind: %s", n)
		}
	}
}

func TestWriteRefusesSymlinks(t *testing.T) {
	s, data := newService(t)
	outside := layout(t, data)
	fi, _ := os.Lstat(filepath.Join(data, "abs-link"))
	m := fi.ModTime().UnixMilli()
	for _, p := range []string{"abs-link", "rel-link", "current/new.txt", "pipe"} {
		_, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: p, Content: "pwned", ExpectedMTime: &m})
		if !errors.Is(err, filesapi.ErrNotRegular) {
			t.Errorf("write %q: %v, want ErrNotRegular", p, err)
		}
		if _, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: p, Content: "pwned"}); err == nil {
			t.Errorf("create %q succeeded", p)
		}
	}
	if b, _ := os.ReadFile(outside); string(b) != "host secret" {
		t.Fatalf("file outside the data dir changed: %q", b)
	}
	if _, err := os.Stat(filepath.Join(data, "world", "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("write through a symlinked folder created a file: %v", err)
	}
}

func TestWriteKeepsRconPassword(t *testing.T) {
	s, data := newService(t)
	put(t, data, "server.properties", "rcon.password=s3cret\nmotd=old\n")
	r, err := s.Read(ctx, 1, "server.properties")
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(r.Content, "motd=old", "motd=new", 1)
	if _, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "server.properties", Content: edited, ExpectedMTime: &r.MTime}); err != nil {
		t.Fatal(err)
	}
	if v, _ := get(t, data, "server.properties"); v != "rcon.password=s3cret\nmotd=new\n" {
		t.Fatalf("password lost: %q", v)
	}
}

func TestHiddenNamesAreNotFound(t *testing.T) {
	s, data := newService(t)
	layout(t, data)
	m := int64(1)
	for _, err := range []error{
		func() error { _, err := s.Read(ctx, 1, ".rcon-cli.env"); return err }(),
		func() error {
			_, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: ".rcon-cli.env", Content: "x", ExpectedMTime: &m})
			return err
		}(),
		s.Mkdir(ctx, 1, ".mcpanel-tmp-x"),
		s.Rename(ctx, filesapi.RenameRequest{ID: 1, From: ".rcon-cli.env", To: "stolen.env"}),
		s.Rename(ctx, filesapi.RenameRequest{ID: 1, From: "server.properties", To: ".rcon-cli.yaml"}),
		s.Delete(ctx, 1, ".rcon-cli.yaml"),
	} {
		if !errors.Is(err, filesapi.ErrNotFound) {
			t.Errorf("hidden name: %v, want ErrNotFound", err)
		}
	}
	if v, ok := get(t, data, ".rcon-cli.env"); !ok || v != "password=s3cret\n" {
		t.Fatal(".rcon-cli.env was touched")
	}
}

func TestMkdirRenameDelete(t *testing.T) {
	s, data := newService(t)
	outside := layout(t, data)

	if err := s.Mkdir(ctx, 1, "plugins/new"); err != nil {
		t.Fatal(err)
	}
	if err := s.Mkdir(ctx, 1, "plugins/new"); !errors.Is(err, filesapi.ErrExists) {
		t.Fatalf("mkdir twice: %v", err)
	}
	if err := s.Mkdir(ctx, 1, "nope/new"); !errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("mkdir without parent: %v", err)
	}
	if err := s.Mkdir(ctx, 1, ""); !errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("mkdir root: %v", err)
	}
	if err := s.Mkdir(ctx, 1, "current/new"); !errors.Is(err, filesapi.ErrNotRegular) {
		t.Fatalf("mkdir through symlink: %v", err)
	}

	if err := s.Rename(ctx, filesapi.RenameRequest{ID: 1, From: "plugins/Essentials/config.yml", To: "plugins/new/config.yml"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := get(t, data, "plugins/new/config.yml"); !ok {
		t.Fatal("rename did not move the file")
	}
	for req, want := range map[filesapi.RenameRequest]error{
		{ID: 1, From: "plugins/new/config.yml", To: "server.properties"}: filesapi.ErrExists,
		{ID: 1, From: "plugins", To: "plugins/inner"}:                    filesapi.ErrInvalid,
		{ID: 1, From: "plugins", To: "plugins"}:                          filesapi.ErrInvalid,
		{ID: 1, From: "missing", To: "x"}:                                filesapi.ErrNotFound,
		{ID: 1, From: "plugins/EssentialsX.jar", To: "nope/x"}:           filesapi.ErrNotFound,
		{ID: 1, From: "plugins/EssentialsX.jar", To: "current/x"}:        filesapi.ErrNotRegular,
		{ID: 1, From: "", To: "x"}:                                       filesapi.ErrInvalid,
	} {
		if err := s.Rename(ctx, req); !errors.Is(err, want) {
			t.Errorf("rename %+v: %v, want %v", req, err, want)
		}
	}
	// Renaming a symlink moves the link, not its target.
	if err := s.Rename(ctx, filesapi.RenameRequest{ID: 1, From: "abs-link", To: "moved-link"}); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(filepath.Join(data, "moved-link")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link not moved as a link: %v", err)
	}

	if err := s.Delete(ctx, 1, "plugins"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(data, "plugins")); !os.IsNotExist(err) {
		t.Fatal("folder not deleted recursively")
	}
	if err := s.Delete(ctx, 1, "moved-link"); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(outside); err != nil || string(b) != "host secret" {
		t.Fatalf("deleting a symlink touched its target: %v", err)
	}
	if err := s.Delete(ctx, 1, "current"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(data, "world", "level.dat")); err != nil {
		t.Fatal("deleting a symlinked folder deleted the real folder's content")
	}
	if err := s.Delete(ctx, 1, ""); !errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("delete root: %v", err)
	}
	if err := s.Delete(ctx, 1, "missing"); !errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("delete missing: %v", err)
	}
}

func TestWriteNewServerPropertiesDropsPlaceholder(t *testing.T) {
	s, data := newService(t)
	if _, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "server.properties", Content: "rcon.password=********\nmotd=x\n"}); err != nil {
		t.Fatal(err)
	}
	if v, _ := get(t, data, "server.properties"); v != "motd=x\n" {
		t.Fatalf("the placeholder became a password: %q", v)
	}
}

// server.properties may not be renamed: under another name or in a subfolder its RCON password
// would no longer be redacted (spec §3.4).
func TestRenameKeepsServerPropertiesRedacted(t *testing.T) {
	s, data := newService(t)
	layout(t, data)
	for _, from := range []string{"server.properties", "SERVER.PROPERTIES", "Server.Properties"} {
		for _, to := range []string{"x.txt", "plugins/server.properties", "copy.properties"} {
			err := s.Rename(ctx, filesapi.RenameRequest{ID: 1, From: from, To: to})
			if !errors.Is(err, filesapi.ErrInvalid) {
				t.Errorf("rename %s -> %s: %v, want ErrInvalid", from, to, err)
			}
		}
	}
	// Renaming another file *to* server.properties stays possible.
	put(t, data, "other.properties", "motd=x\n")
	if err := s.Delete(ctx, 1, "server.properties"); err != nil {
		t.Fatal(err)
	}
	if err := s.Rename(ctx, filesapi.RenameRequest{ID: 1, From: "other.properties", To: "server.properties"}); err != nil {
		t.Fatalf("rename onto the free name: %v", err)
	}
}

// No read path (editor, download, folder zip) returns the RCON password, under any name the
// API accepts.
func TestNoReadPathReturnsTheRconPassword(t *testing.T) {
	s, data := newService(t)
	layout(t, data)
	_ = s.Rename(ctx, filesapi.RenameRequest{ID: 1, From: "server.properties", To: "leak.txt"})
	_ = s.Rename(ctx, filesapi.RenameRequest{ID: 1, From: "server.properties", To: "plugins/server.properties"})

	var paths []string
	var walk func(dir string)
	walk = func(dir string) {
		res, err := s.List(ctx, 1, dir)
		if err != nil {
			t.Fatalf("list %q: %v", dir, err)
		}
		for _, e := range res.Entries {
			p := filesapi.Join(dir, e.Name)
			paths = append(paths, p)
			if e.Type == filesapi.TypeDir {
				walk(p)
			}
		}
	}
	walk("")
	// Case variants reach the real file on a case-insensitive file system (macOS dev).
	paths = append(paths, "SERVER.PROPERTIES", "Server.properties", ".RCON-CLI.ENV", ".Rcon-Cli.Yaml")
	for _, p := range paths {
		if r, err := s.Read(ctx, 1, p); err == nil && strings.Contains(r.Content, "s3cret") {
			t.Errorf("read %s returns the password", p)
		}
		var d download
		if err := s.Download(ctx, 1, p, d.start, &d.body); err == nil && strings.Contains(d.body.String(), "s3cret") {
			t.Errorf("download %s returns the password", p)
		}
	}
	var d download
	if err := s.Download(ctx, 1, "", d.start, &d.body); err != nil {
		t.Fatal(err)
	}
	for name, content := range zipNames(t, d.body.Bytes()) {
		if strings.Contains(content, "s3cret") {
			t.Errorf("folder zip entry %s contains the password", name)
		}
	}
}
