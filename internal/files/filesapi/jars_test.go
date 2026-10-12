package filesapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

var (
	hex64  = strings.Repeat("ab", 32)
	hex128 = strings.Repeat("cd", 64)
)

func TestChecksumValidate(t *testing.T) {
	for _, ok := range []filesapi.Checksum{
		{Algo: filesapi.AlgoSHA256, Hex: hex64},
		{Algo: filesapi.AlgoSHA512, Hex: hex128},
		{Algo: "sha512", Hex: strings.Repeat("0123456789abcdef", 8)},
	} {
		if err := ok.Validate(); err != nil {
			t.Errorf("Validate(%+v) = %v", ok, err)
		}
	}
	for _, bad := range []filesapi.Checksum{
		{},
		{Algo: "md5", Hex: strings.Repeat("a", 32)},
		{Algo: "SHA512", Hex: hex128},                            // algorithm names are lower case
		{Algo: filesapi.AlgoSHA256, Hex: hex128},                 // wrong length for the algorithm
		{Algo: filesapi.AlgoSHA512, Hex: hex64},                  // wrong length for the algorithm
		{Algo: filesapi.AlgoSHA256, Hex: strings.ToUpper(hex64)}, // lowercase hex only
		{Algo: filesapi.AlgoSHA256, Hex: strings.Repeat("g", 64)},
		{Algo: filesapi.AlgoSHA256, Hex: hex64[:63] + " "},
		{Algo: filesapi.AlgoSHA512, Hex: ""},
	} {
		if err := bad.Validate(); !errors.Is(err, filesapi.ErrInvalid) {
			t.Errorf("Validate(%+v) = %v, want ErrInvalid", bad, err)
		}
	}
}

func TestChecksumMatches(t *testing.T) {
	s256 := filesapi.Checksum{Algo: filesapi.AlgoSHA256, Hex: hex64}
	s512 := filesapi.Checksum{Algo: filesapi.AlgoSHA512, Hex: hex128}
	if !s256.Matches(hex64, "other") || s256.Matches(strings.Repeat("00", 32), hex128) {
		t.Fatal("sha256 must compare the sha256 digest only")
	}
	if !s512.Matches("other", hex128) || s512.Matches(hex64, strings.Repeat("00", 64)) {
		t.Fatal("sha512 must compare the sha512 digest only")
	}
	if (filesapi.Checksum{}).Matches("", "") {
		t.Fatal("an empty checksum matches nothing")
	}
}

func TestChecksumFromQuery(t *testing.T) {
	sum, ok, err := filesapi.ChecksumFromQuery(url.Values{"id": {"1"}, "path": {"plugins/x.jar"}})
	if err != nil || ok || sum != (filesapi.Checksum{}) {
		t.Fatalf("no parameter: %+v %v %v", sum, ok, err)
	}
	sum, ok, err = filesapi.ChecksumFromQuery(url.Values{"sha512": {hex128}})
	if err != nil || !ok || sum != (filesapi.Checksum{Algo: filesapi.AlgoSHA512, Hex: hex128}) {
		t.Fatalf("sha512: %+v %v %v", sum, ok, err)
	}
	sum, ok, err = filesapi.ChecksumFromQuery(url.Values{"sha256": {hex64}})
	if err != nil || !ok || sum.Algo != filesapi.AlgoSHA256 {
		t.Fatalf("sha256: %+v %v %v", sum, ok, err)
	}
	for name, q := range map[string]url.Values{
		"both":      {"sha256": {hex64}, "sha512": {hex128}},
		"twice":     {"sha512": {hex128, hex128}},
		"empty":     {"sha256": {""}},
		"bad hex":   {"sha512": {strings.ToUpper(hex128)}},
		"short":     {"sha256": {hex64[:10]}},
		"wrong len": {"sha256": {hex128}},
	} {
		if _, ok, err := filesapi.ChecksumFromQuery(q); !errors.Is(err, filesapi.ErrInvalid) || ok {
			t.Errorf("%s: ok=%v err=%v, want ErrInvalid", name, ok, err)
		}
	}
}

func TestValidJarDir(t *testing.T) {
	for dir, want := range map[string]bool{
		"plugins": true, "mods": true, "": false, "Plugins": false, "MODS": false,
		"plugins/": false, "plugins/x": false, "../plugins": false, "world": false, "config": false,
	} {
		if got := filesapi.ValidJarDir(dir); got != want {
			t.Errorf("ValidJarDir(%q) = %v, want %v", dir, got, want)
		}
	}
}

func TestIsJarName(t *testing.T) {
	type res struct{ enabled, ok bool }
	for name, want := range map[string]res{
		"LuckPerms-Bukkit-5.5.71.jar": {true, true},
		"foo.JAR":                     {true, true},
		"x.jar.disabled":              {false, true},
		"x.JAR.Disabled":              {false, true},
		"a.jar":                       {true, true},
		".jar":                        {false, false}, // nothing before the suffix
		".jar.disabled":               {false, false},
		"x.jar.disabled.jar":          {true, true},
		"x.disabled":                  {false, false},
		"x.jar.old":                   {false, false},
		"x.zip":                       {false, false},
		"jar":                         {false, false},
		"":                            {false, false},
	} {
		enabled, ok := filesapi.IsJarName(name)
		if enabled != want.enabled || ok != want.ok {
			t.Errorf("IsJarName(%q) = %v, %v; want %v, %v", name, enabled, ok, want.enabled, want.ok)
		}
	}
}

func TestClientUploadCheckedSendsTheChecksum(t *testing.T) {
	for _, sum := range []filesapi.Checksum{
		{Algo: filesapi.AlgoSHA512, Hex: hex128},
		{Algo: filesapi.AlgoSHA256, Hex: hex64},
	} {
		other := filesapi.QuerySHA256
		if sum.Algo == filesapi.AlgoSHA256 {
			other = filesapi.QuerySHA512
		}
		c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			if r.Method != http.MethodPut || r.URL.Path != filesapi.PathUpload || q.Get("id") != "4" ||
				q.Get("path") != "mods/x.jar" || q.Get("overwrite") != "0" || q.Get(sum.Algo) != sum.Hex ||
				q.Has(other) || r.ContentLength != 5 {
				http.Error(w, "bad request "+r.URL.String(), 400)
				return
			}
			n, _ := io.Copy(io.Discard, r.Body)
			writeJSON(w, 201, filesapi.UploadResponse{Size: n, SHA256: hex64, SHA512: hex128})
		}))
		got, err := c.UploadChecked(context.Background(), 4, "mods/x.jar", false, 5, sum, strings.NewReader("hello"))
		if err != nil || got != (filesapi.UploadResponse{Size: 5, SHA256: hex64, SHA512: hex128}) {
			t.Fatalf("%s: UploadChecked = %+v, %v", sum.Algo, got, err)
		}
	}
}

func TestClientUploadWithoutChecksumSendsNoDigestParameter(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Has(filesapi.QuerySHA256) || q.Has(filesapi.QuerySHA512) {
			http.Error(w, "unexpected digest parameter", 400)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		// The service leaves the digests out without a checksum (omitempty).
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"size":3}`))
	}))
	got, err := c.Upload(context.Background(), 1, "plugins/x.jar", true, 3, strings.NewReader("abc"))
	if err != nil || got != (filesapi.UploadResponse{Size: 3}) {
		t.Fatalf("Upload = %+v, %v", got, err)
	}
	raw, _ := json.Marshal(filesapi.UploadResponse{Size: 3})
	if string(raw) != `{"size":3}` {
		t.Fatalf("UploadResponse without digests = %s, want {\"size\":3}", raw)
	}
}

func TestClientUploadCheckedRejectsBadInputWithoutRequest(t *testing.T) {
	called := false
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	ctx := context.Background()
	for _, sum := range []filesapi.Checksum{
		{},
		{Algo: "md5", Hex: strings.Repeat("a", 32)},
		{Algo: filesapi.AlgoSHA512, Hex: hex64},
		{Algo: filesapi.AlgoSHA256, Hex: strings.ToUpper(hex64)},
	} {
		if _, err := c.UploadChecked(ctx, 1, "plugins/x.jar", false, 1, sum, strings.NewReader("x")); !errors.Is(err, filesapi.ErrInvalid) {
			t.Errorf("%+v: err = %v, want ErrInvalid", sum, err)
		}
	}
	good := filesapi.Checksum{Algo: filesapi.AlgoSHA512, Hex: hex128}
	if _, err := c.UploadChecked(ctx, 1, "../x.jar", false, 1, good, strings.NewReader("x")); !errors.Is(err, filesapi.ErrInvalid) {
		t.Errorf("bad path: err = %v, want ErrInvalid", err)
	}
	if _, err := c.UploadChecked(ctx, 0, "plugins/x.jar", false, 1, good, strings.NewReader("x")); !errors.Is(err, filesapi.ErrInvalid) {
		t.Errorf("id 0: err = %v, want ErrInvalid", err)
	}
	if _, err := c.UploadChecked(ctx, 1, "plugins/x.jar", false, -1, good, strings.NewReader("x")); !errors.Is(err, filesapi.ErrInvalid) {
		t.Errorf("negative size: err = %v, want ErrInvalid", err)
	}
	if called {
		t.Fatal("an invalid upload reached the service")
	}
}

func TestClientUploadCheckedHashMismatch(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		writeJSON(w, http.StatusBadRequest, filesapi.ErrorResponse{Error: "checksum does not match", Code: filesapi.CodeHashMismatch})
	}))
	sum := filesapi.Checksum{Algo: filesapi.AlgoSHA512, Hex: hex128}
	_, err := c.UploadChecked(context.Background(), 1, "plugins/x.jar", false, 3, sum, strings.NewReader("abc"))
	if !errors.Is(err, filesapi.ErrHashMismatch) || errors.Is(err, filesapi.ErrInvalid) || errors.Is(err, filesapi.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrHashMismatch only", err)
	}
}

func TestClientJars(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req filesapi.JarsRequest
		if r.Method != http.MethodPost || r.URL.Path != filesapi.PathJars || r.Header.Get("Content-Type") != "application/json" ||
			json.NewDecoder(r.Body).Decode(&req) != nil || req.ID != 7 || req.Dir != filesapi.DirPlugins {
			http.Error(w, "bad request", 400)
			return
		}
		writeJSON(w, 200, filesapi.JarsResponse{Truncated: true, Jars: []filesapi.Jar{
			{Name: "A.jar", Enabled: true, Size: 3, MTime: 5, SHA256: hex64, SHA512: hex128,
				Meta: &filesapi.JarMeta{Kind: filesapi.JarKindPlugin, Name: "A", Version: "1.0"}},
			{Name: "big.jar.disabled", Enabled: false, Size: filesapi.MaxJarHashBytes + 1, MTime: 6},
		}})
	}))
	got, err := c.Jars(context.Background(), 7, filesapi.DirPlugins)
	if err != nil || !got.Truncated || len(got.Jars) != 2 {
		t.Fatalf("Jars = %+v, %v", got, err)
	}
	a, big := got.Jars[0], got.Jars[1]
	if a.Meta == nil || a.Meta.Kind != filesapi.JarKindPlugin || a.Meta.Name != "A" || a.SHA512 != hex128 || !a.Enabled {
		t.Fatalf("first jar = %+v (meta %+v)", a, a.Meta)
	}
	if big.Meta != nil || big.SHA256 != "" || big.SHA512 != "" || big.Enabled {
		t.Fatalf("second jar = %+v", big)
	}
	raw, _ := json.Marshal(filesapi.Jar{Name: "x.jar"})
	if !strings.Contains(string(raw), `"meta":null`) || !strings.Contains(string(raw), `"sha512":""`) {
		t.Fatalf("Jar wire form = %s", raw)
	}
}

func TestClientJarsNeverReturnsNil(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jars":null,"truncated":false}`))
	}))
	got, err := c.Jars(context.Background(), 1, filesapi.DirMods)
	if err != nil || got.Jars == nil || len(got.Jars) != 0 {
		t.Fatalf("Jars = %#v, %v; want an empty, non-nil list", got, err)
	}
}

func TestClientJarsRejectsBadInputWithoutRequest(t *testing.T) {
	called := false
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	for _, dir := range []string{"", "world", "Plugins", "plugins/", "plugins/x", "../plugins", "/plugins"} {
		if _, err := c.Jars(context.Background(), 1, dir); !errors.Is(err, filesapi.ErrInvalid) {
			t.Errorf("dir %q: err = %v, want ErrInvalid", dir, err)
		}
	}
	if _, err := c.Jars(context.Background(), 0, filesapi.DirPlugins); !errors.Is(err, filesapi.ErrInvalid) {
		t.Errorf("id 0: err = %v, want ErrInvalid", err)
	}
	if called {
		t.Fatal("an invalid jars request reached the service")
	}
}

// Hashing up to 500 jars on a cache miss takes longer than the 10 s of the small calls; the
// listing has its own timeout (2 min, V1).
func TestClientJarsHasItsOwnTimeout(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		writeJSON(w, 200, filesapi.JarsResponse{Jars: []filesapi.Jar{}})
	}))
	filesapi.SetCallTimeouts(c, 50*time.Millisecond, 50*time.Millisecond)
	filesapi.SetJarsTimeout(c, 5*time.Second)
	if _, err := c.Jars(context.Background(), 1, filesapi.DirPlugins); err != nil {
		t.Fatalf("slow listing within the jars timeout: %v", err)
	}
	filesapi.SetJarsTimeout(c, 50*time.Millisecond)
	if _, err := c.Jars(context.Background(), 1, filesapi.DirPlugins); !errors.Is(err, filesapi.ErrUnavailable) {
		t.Fatalf("listing past the jars timeout: err = %v, want ErrUnavailable", err)
	}
	if filesapi.JarsTimeout != 2*time.Minute {
		t.Fatalf("JarsTimeout = %s, want 2m", filesapi.JarsTimeout)
	}
}

// A files service that does not know the checksum parameter (an older version) answers 201
// without digests; UploadChecked must not report that as a verified upload.
func TestClientUploadCheckedRequiresVerifiedDigests(t *testing.T) {
	for name, answer := range map[string]string{
		"no digests":    `{"size":3}`,
		"other digest":  `{"size":3,"sha256":"` + strings.Repeat("00", 32) + `","sha512":"` + strings.Repeat("00", 64) + `"}`,
		"malformed hex": `{"size":3,"sha256":"` + hex64 + `","sha512":"xyz"}`,
	} {
		c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(201)
			_, _ = w.Write([]byte(answer))
		}))
		sum := filesapi.Checksum{Algo: filesapi.AlgoSHA256, Hex: hex64}
		if _, err := c.UploadChecked(context.Background(), 1, "plugins/x.jar", false, 3, sum, strings.NewReader("abc")); !errors.Is(err, filesapi.ErrUnavailable) {
			t.Errorf("%s: err = %v, want ErrUnavailable", name, err)
		}
	}
}
