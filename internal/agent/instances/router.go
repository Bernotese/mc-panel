package instances

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/Bernotese/mc-panel/internal/agent/docker"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

// managedID returns the instance ID of a container that mc-panel manages: it carries
// a positive mcpanel.instance label AND is called mcpanel-mc-<that id>. Anything
// else (no label, other name, label pointing elsewhere) is foreign and never touched.
func managedID(labels map[string]string, names ...string) (int64, bool) {
	id, err := strconv.ParseInt(labels[LabelInstance], 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	want := ContainerName(id)
	for _, n := range names {
		if strings.TrimPrefix(n, "/") == want {
			return id, true
		}
	}
	return 0, false
}

// Routes maps the FQDN of every managed container (running or not) to its
// backend "mcpanel-mc-<id>:25565". Containers with labels that do not form a
// valid name/FQDN pair are skipped; for a duplicate FQDN the lowest ID wins.
func Routes(containers []docker.ContainerSummary) map[string]string {
	sorted := slices.Clone(containers)
	ids := make(map[string]int64, len(sorted))
	for _, c := range sorted {
		id, _ := managedID(c.Labels, c.Names...)
		ids[c.ID] = id
	}
	slices.SortFunc(sorted, func(a, b docker.ContainerSummary) int { return cmp.Compare(ids[a.ID], ids[b.ID]) })
	routes := map[string]string{}
	for _, c := range sorted {
		id, ok := managedID(c.Labels, c.Names...)
		if !ok {
			continue
		}
		fqdn := c.Labels[LabelFQDN]
		if instancespec.CheckFQDN(c.Labels[LabelName], fqdn) != nil {
			continue
		}
		if _, dup := routes[fqdn]; dup {
			continue
		}
		routes[fqdn] = ContainerName(id) + ":" + strconv.Itoa(MinecraftPort)
	}
	return routes
}

// RenderRoutes returns the mc-router routes file. encoding/json sorts map keys,
// so equal mappings always give equal bytes.
func RenderRoutes(mappings map[string]string) ([]byte, error) {
	if mappings == nil {
		mappings = map[string]string{}
	}
	b, err := json.MarshalIndent(struct {
		Mappings map[string]string `json:"mappings"`
	}{mappings}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// WriteRoutes atomically replaces <dir>/routes.json with data (temp file in the
// same directory, fsync, rename, fsync of the directory). It reports changed=false
// and writes nothing when the file already has exactly this content.
func WriteRoutes(dir string, data []byte) (changed bool, err error) {
	// The router runs as uid 65532 and must read the directory and the file.
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // G301: mc-router (uid 65532) reads this directory; it holds no secrets
		return false, err
	}
	if err := os.Chmod(dir, 0o755); err != nil { //nolint:gosec // G302: see above, MkdirAll is subject to the umask
		return false, err
	}
	path := filepath.Join(dir, routesFileName)
	old, err := os.ReadFile(path) //nolint:gosec // G304: fixed file name below the configured instances_dir, never request input
	switch {
	case err == nil && bytes.Equal(old, data):
		return false, nil
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return false, err
	}
	tmp, err := os.CreateTemp(dir, ".routes-*.json")
	if err != nil {
		return false, err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return false, err
	}
	if err = tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return false, err
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return false, err
	}
	if err = tmp.Close(); err != nil {
		return false, err
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return false, err
	}
	if err = syncDir(dir); err != nil {
		return true, fmt.Errorf("routes written, but fsync of %s failed: %w", dir, err)
	}
	return true, nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir) //nolint:gosec // G304: directory below the configured instances_dir
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}
