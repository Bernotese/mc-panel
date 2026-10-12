package files

import (
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// sums returns the lowercase hex sha256 and sha512 of content.
func sums(content string) (string, string) {
	a := sha256.Sum256([]byte(content))
	b := sha512.Sum512([]byte(content))
	return hex.EncodeToString(a[:]), hex.EncodeToString(b[:])
}

func uploadChecked(s *Service, path string, overwrite bool, content string, sum filesapi.Checksum) (filesapi.UploadResponse, error) {
	return s.UploadChecked(ctx, 1, path, overwrite, int64(len(content)), sum, strings.NewReader(content))
}

func TestUploadCheckedAcceptsMatchingDigest(t *testing.T) {
	s, data := newService(t)
	put(t, data, "plugins/keep.txt", "")
	const content = "jar bytes"
	h256, h512 := sums(content)
	for _, sum := range []filesapi.Checksum{
		{Algo: filesapi.AlgoSHA256, Hex: h256},
		{Algo: filesapi.AlgoSHA512, Hex: h512},
	} {
		target := "plugins/" + sum.Algo + ".jar"
		res, err := uploadChecked(s, target, false, content, sum)
		if err != nil {
			t.Fatalf("%s: %v", sum.Algo, err)
		}
		if res.Size != int64(len(content)) || res.SHA256 != h256 || res.SHA512 != h512 {
			t.Fatalf("%s: answer %+v, want both digests", sum.Algo, res)
		}
		if v, _ := get(t, data, target); v != content {
			t.Fatalf("%s: stored %q", sum.Algo, v)
		}
	}
	// Overwriting with a matching digest replaces the file.
	h256b, _ := sums("v2")
	if _, err := uploadChecked(s, "plugins/sha256.jar", true, "v2", filesapi.Checksum{Algo: filesapi.AlgoSHA256, Hex: h256b}); err != nil {
		t.Fatalf("checked overwrite: %v", err)
	}
	if v, _ := get(t, data, "plugins/sha256.jar"); v != "v2" {
		t.Fatalf("checked overwrite stored %q", v)
	}
	// Without a checksum the answer stays {size}.
	res, err := upload(s, "plugins/plain.jar", false, content)
	if err != nil || res.SHA256 != "" || res.SHA512 != "" || res.Size != int64(len(content)) {
		t.Fatalf("plain upload: %+v %v", res, err)
	}
	noTemp(t, data)
}

func TestUploadCheckedMismatchLeavesNothing(t *testing.T) {
	s, data := newService(t)
	put(t, data, "plugins/Old.jar", "old content")
	other256, other512 := sums("something else")
	for name, c := range map[string]struct {
		path      string
		overwrite bool
		sum       filesapi.Checksum
	}{
		"new file, sha512":  {"plugins/New.jar", false, filesapi.Checksum{Algo: filesapi.AlgoSHA512, Hex: other512}},
		"overwrite, sha256": {"plugins/Old.jar", true, filesapi.Checksum{Algo: filesapi.AlgoSHA256, Hex: other256}},
	} {
		_, err := uploadChecked(s, c.path, c.overwrite, "new content", c.sum)
		if !errors.Is(err, filesapi.ErrHashMismatch) || errors.Is(err, filesapi.ErrInvalid) {
			t.Errorf("%s: %v, want ErrHashMismatch", name, err)
		}
	}
	if _, ok := get(t, data, "plugins/New.jar"); ok {
		t.Fatal("a mismatching upload became visible")
	}
	if v, _ := get(t, data, "plugins/Old.jar"); v != "old content" {
		t.Fatalf("a mismatching overwrite changed the target: %q", v)
	}
	noTemp(t, data)
	// The reservation is given back.
	if s.pending != 0 {
		t.Fatalf("pending after mismatch: %d", s.pending)
	}
}

func TestUploadCheckedRefusesInvalidChecksums(t *testing.T) {
	s, data := newService(t)
	h256, h512 := sums("x")
	for name, sum := range map[string]filesapi.Checksum{
		"unknown algo":   {Algo: "md5", Hex: h256},
		"algo only":      {Algo: filesapi.AlgoSHA256},
		"hex only":       {Hex: h256},
		"uppercase":      {Algo: filesapi.AlgoSHA256, Hex: strings.ToUpper(h256)},
		"sha512 of 64":   {Algo: filesapi.AlgoSHA512, Hex: h256},
		"sha256 of 128":  {Algo: filesapi.AlgoSHA256, Hex: h512},
		"not hex":        {Algo: filesapi.AlgoSHA256, Hex: strings.Repeat("g", 64)},
		"trailing space": {Algo: filesapi.AlgoSHA256, Hex: h256[:63] + " "},
	} {
		if _, err := uploadChecked(s, "a.jar", false, "x", sum); !errors.Is(err, filesapi.ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
		}
	}
	if _, ok := get(t, data, "a.jar"); ok {
		t.Fatal("an upload with an invalid checksum was stored")
	}
	noTemp(t, data)
}

// Computing both digests keeps the upload a stream.
func TestUploadCheckedStreamsWithoutBuffering(t *testing.T) {
	const size = 64 << 20
	s, data := newService(t, func(c *Config) { c.Limits.MaxUploadBytes = size })
	h := sha512.New()
	if _, err := io.Copy(h, &zeroReader{n: size}); err != nil {
		t.Fatal(err)
	}
	sum := filesapi.Checksum{Algo: filesapi.AlgoSHA512, Hex: hex.EncodeToString(h.Sum(nil))}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	res, err := s.UploadChecked(ctx, 1, "world.zip", false, size, sum, &zeroReader{n: size})
	runtime.ReadMemStats(&after)
	if err != nil || res.SHA512 != sum.Hex {
		t.Fatalf("checked 64 MiB upload: %+v %v", res, err)
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 16<<20 {
		t.Fatalf("a %d MiB checked upload allocated %d MiB", size>>20, grew>>20)
	}
	if fi, err := os.Stat(filepath.Join(data, "world.zip")); err != nil || fi.Size() != size {
		t.Fatalf("stored size: %v", err)
	}
}

// putUpload sends content as PUT /v1/upload with query q.
func putUpload(t *testing.T, ts *httptest.Server, q url.Values, content string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, ts.URL+filesapi.PathUpload+"?"+q.Encode(), strings.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, body
}

func TestServerUploadChecksumParameters(t *testing.T) {
	s, data := newService(t)
	ts := startHTTP(t, NewServer(s, "test", discardLog()))
	const content = "plugin"
	h256, h512 := sums(content)
	query := func(path string, kv ...string) url.Values {
		v := url.Values{"id": {"1"}, "path": {path}}
		for i := 0; i+1 < len(kv); i += 2 {
			v.Add(kv[i], kv[i+1])
		}
		return v
	}

	status, body := putUpload(t, ts, query("a.jar", filesapi.QuerySHA512, h512), content)
	if status != http.StatusCreated {
		t.Fatalf("checked upload: %d %s", status, body)
	}
	var res filesapi.UploadResponse
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatal(err)
	}
	if res.Size != int64(len(content)) || res.SHA256 != h256 || res.SHA512 != h512 {
		t.Fatalf("checked upload answer: %s", body)
	}
	status, body = putUpload(t, ts, query("plain.jar"), content)
	if status != http.StatusCreated || strings.Contains(string(body), "sha") {
		t.Fatalf("plain upload: %d %s, want {size} only", status, body)
	}

	for name, q := range map[string]url.Values{
		"both":      query("b.jar", filesapi.QuerySHA256, h256, filesapi.QuerySHA512, h512),
		"twice":     query("b.jar", filesapi.QuerySHA256, h256, filesapi.QuerySHA256, h256),
		"empty":     query("b.jar", filesapi.QuerySHA256, ""),
		"uppercase": query("b.jar", filesapi.QuerySHA256, strings.ToUpper(h256)),
		"too short": query("b.jar", filesapi.QuerySHA512, h256),
		"not hex":   query("b.jar", filesapi.QuerySHA256, strings.Repeat("z", 64)),
	} {
		status, body := putUpload(t, ts, q, content)
		if status != http.StatusBadRequest || errCode(t, body) != filesapi.CodeInvalid {
			t.Errorf("%s: %d %s, want 400 invalid", name, status, body)
		}
	}

	status, body = putUpload(t, ts, query("c.jar", filesapi.QuerySHA512, strings.Repeat("0", 128)), content)
	if status != http.StatusBadRequest || errCode(t, body) != filesapi.CodeHashMismatch {
		t.Fatalf("mismatch: %d %s, want 400 hash_mismatch", status, body)
	}
	for _, n := range []string{"b.jar", "c.jar"} {
		if _, ok := get(t, data, n); ok {
			t.Errorf("%s was stored", n)
		}
	}
	noTemp(t, data)
}
