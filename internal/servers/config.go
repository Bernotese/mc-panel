package servers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/store"
)

// Desired states of an instance (instances.desired_state).
const (
	desiredRunning = "running"
	desiredStopped = "stopped"
)

// config is the stored configuration of one instance as the admin gave it. Properties stay
// sparse (unset = default), so the resolved values are computed on every read.
type config struct {
	Type          instancespec.Type
	Version       string
	LoaderVersion *string
	Modpack       *string
	Java          instancespec.Java
	MemoryMiB     int
	Properties    instancespec.Properties
}

func instanceFromListRow(r store.ServersListInstancesRow) store.Instance {
	return store.Instance{
		ServerID: r.ServerID, Type: r.Type, Version: r.Version, LoaderVersion: r.LoaderVersion,
		Modpack: r.Modpack, Java: r.Java, MemoryMib: r.MemoryMib, Properties: r.Properties,
		DesiredState: r.DesiredState, Revision: r.Revision, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		WebPort: r.WebPort,
	}
}

func configFromInstance(in store.Instance) (config, error) {
	var props instancespec.Properties
	if err := json.Unmarshal([]byte(in.Properties), &props); err != nil {
		return config{}, fmt.Errorf("servers: properties of server %d: %w", in.ServerID, err)
	}
	return config{
		Type: instancespec.Type(in.Type), Version: in.Version, LoaderVersion: nullString(in.LoaderVersion),
		Modpack: nullString(in.Modpack), Java: instancespec.Java(in.Java), MemoryMiB: int(in.MemoryMib),
		Properties: props,
	}, nil
}

// spec is the agent's view of the config; the CurseForge key is only attached for curseforge.
func (c config) spec(curseForgeKey *string) instancespec.Spec {
	s := instancespec.Spec{
		Type: c.Type, Version: c.Version, LoaderVersion: c.LoaderVersion, Modpack: c.Modpack,
		Java: c.Java, MemoryMiB: c.MemoryMiB, Properties: c.Properties,
	}
	if c.Type == instancespec.TypeCurseForge {
		s.CurseForgeAPIKey = curseForgeKey
	}
	return s
}

// validate checks the config with the agent's rules (instancespec), including the
// CurseForge key from the settings.
func (c config) validate(curseForgeKey *string) error {
	if c.Type == instancespec.TypeCurseForge && curseForgeKey == nil {
		return fmt.Errorf("%w: curseforge_api_key: set a CurseForge API key in the server settings first", instancespec.ErrInvalid)
	}
	return c.spec(curseForgeKey).Validate()
}

func (c config) propertiesJSON() (string, error) {
	b, err := json.Marshal(c.Properties)
	return string(b), err
}

// auditDetails describes the config for server.create. It contains no secrets: the
// CurseForge key lives in the settings, not in the config.
func (c config) auditDetails(name string) map[string]any {
	return map[string]any{
		"name": name, "type": string(c.Type), "version": c.Version,
		"loader_version": strOrNil(c.LoaderVersion), "modpack": strOrNil(c.Modpack),
		"java": string(c.Java), "memory_mib": c.MemoryMiB, "properties": c.Properties,
	}
}

func eqPtr[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// diff returns the changed fields as {"old": …, "new": …}; properties are compared per key
// ("properties.motd").
func diff(old, cur config) (map[string]any, error) {
	d := map[string]any{}
	change := func(field string, o, n any) { d[field] = map[string]any{"old": o, "new": n} }
	if old.Version != cur.Version {
		change("version", old.Version, cur.Version)
	}
	if !eqPtr(old.LoaderVersion, cur.LoaderVersion) {
		change("loader_version", strOrNil(old.LoaderVersion), strOrNil(cur.LoaderVersion))
	}
	if !eqPtr(old.Modpack, cur.Modpack) {
		change("modpack", strOrNil(old.Modpack), strOrNil(cur.Modpack))
	}
	if old.Java != cur.Java {
		change("java", string(old.Java), string(cur.Java))
	}
	if old.MemoryMiB != cur.MemoryMiB {
		change("memory_mib", old.MemoryMiB, cur.MemoryMiB)
	}
	op, err := propertyMap(old.Properties)
	if err != nil {
		return nil, err
	}
	np, err := propertyMap(cur.Properties)
	if err != nil {
		return nil, err
	}
	keys := slices.Sorted(maps.Keys(op))
	for k := range np {
		if _, ok := op[k]; !ok {
			keys = append(keys, k)
		}
	}
	for _, k := range keys {
		if !bytes.Equal(op[k], np[k]) {
			change("properties."+k, rawOrNil(op[k]), rawOrNil(np[k]))
		}
	}
	return d, nil
}

func propertyMap(p instancespec.Properties) (map[string]json.RawMessage, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	return m, json.Unmarshal(b, &m)
}

// rawOrNil keeps a JSON value as is (audit details embed it) and maps "unset" to null.
func rawOrNil(v json.RawMessage) any {
	if v == nil {
		return nil
	}
	return v
}

// decodeProperties strictly decodes a ServerProperties object; unknown keys and wrong types
// are validation errors. Absent or null means "all defaults".
func decodeProperties(raw json.RawMessage) (instancespec.Properties, error) {
	var p instancespec.Properties
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return p, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return instancespec.Properties{}, fmt.Errorf("%w: properties: %w", instancespec.ErrInvalid, err)
	}
	return p, nil
}
