// Package instancespec holds the rules for Minecraft instances that panel and agent share:
// the spec types, their validation, the Java image derivation and the mapping to
// itzg/minecraft-server environment variables. The panel validates with it before storing;
// the agent validates every request again with the same code because it never trusts the panel.
package instancespec

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

type Type string

const (
	TypeVanilla    Type = "vanilla"
	TypePaper      Type = "paper"
	TypePurpur     Type = "purpur"
	TypeFabric     Type = "fabric"
	TypeNeoForge   Type = "neoforge"
	TypeModrinth   Type = "modrinth"
	TypeCurseForge Type = "curseforge"
)

var Types = []Type{TypeVanilla, TypePaper, TypePurpur, TypeFabric, TypeNeoForge, TypeModrinth, TypeCurseForge}

// IsModpack reports whether the pack, not the version field, decides the Minecraft version.
func (t Type) IsModpack() bool { return t == TypeModrinth || t == TypeCurseForge }

// HasLoader reports whether the type takes an optional mod loader version.
func (t Type) HasLoader() bool { return t == TypeFabric || t == TypeNeoForge }

type Java string

const (
	Java8  Java = "java8"
	Java17 Java = "java17"
	Java21 Java = "java21"
	Java25 Java = "java25"
)

var Javas = []Java{Java8, Java17, Java21, Java25}

const (
	MinMemoryMiB  = 1024
	MaxMemoryMiB  = 32768
	MemoryStepMiB = 256
)

// ErrInvalid wraps every validation error; the message names the field.
var ErrInvalid = errors.New("invalid instance spec")

var (
	// A DNS label: 2–32 characters, no leading or trailing hyphen (the name is the subdomain).
	nameRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}[a-z0-9]$`)
	labelRe   = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	versionRe = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.\-+_]{0,63}$`)
	slugRe    = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)
	cfURLRe   = regexp.MustCompile(`^https://www\.curseforge\.com/minecraft/modpacks/([a-z0-9-]{1,64})$`)
	cfKeyRe   = regexp.MustCompile(`^[A-Za-z0-9$./_-]{1,128}$`)
)

const (
	maxBaseDomainLen = 200
	maxDNSNameLen    = 253
)

func invalid(field, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", ErrInvalid, field, fmt.Sprintf(format, args...))
}

type Spec struct {
	Type             Type       `json:"type"`
	Version          string     `json:"version"`
	LoaderVersion    *string    `json:"loader_version"`
	Modpack          *string    `json:"modpack"`
	Java             Java       `json:"java"`
	MemoryMiB        int        `json:"memory_mib"`
	Properties       Properties `json:"properties"`
	CurseForgeAPIKey *string    `json:"curseforge_api_key"`
}

func (s Spec) Validate() error {
	if !slices.Contains(Types, s.Type) {
		return invalid("type", "unknown type %q", s.Type)
	}
	if s.Type.IsModpack() {
		if s.Version != "" {
			return invalid("version", "must be empty for modpacks (the pack decides)")
		}
	} else if s.Version != "LATEST" && !versionRe.MatchString(s.Version) {
		return invalid("version", "must be LATEST or match %s", versionRe)
	}
	if s.LoaderVersion != nil {
		if !s.Type.HasLoader() {
			return invalid("loader_version", "only allowed for fabric and neoforge")
		}
		if !versionRe.MatchString(*s.LoaderVersion) {
			return invalid("loader_version", "must match %s", versionRe)
		}
	}
	if err := s.validateModpack(); err != nil {
		return err
	}
	if !slices.Contains(Javas, s.Java) {
		return invalid("java", "unknown java %q", s.Java)
	}
	if s.MemoryMiB < MinMemoryMiB || s.MemoryMiB > MaxMemoryMiB || s.MemoryMiB%MemoryStepMiB != 0 {
		return invalid("memory_mib", "must be %d..%d in steps of %d", MinMemoryMiB, MaxMemoryMiB, MemoryStepMiB)
	}
	if err := s.Properties.Validate(); err != nil {
		return err
	}
	if s.Type == TypeCurseForge {
		if s.CurseForgeAPIKey == nil || !cfKeyRe.MatchString(*s.CurseForgeAPIKey) {
			return invalid("curseforge_api_key", "required for curseforge and must match %s", cfKeyRe)
		}
		if s.Java == Java8 {
			return invalid("java", "curseforge needs java17 or newer")
		}
	} else if s.CurseForgeAPIKey != nil {
		return invalid("curseforge_api_key", "only allowed for curseforge")
	}
	return nil
}

func (s Spec) validateModpack() error {
	switch {
	case !s.Type.IsModpack():
		if s.Modpack != nil {
			return invalid("modpack", "only allowed for modrinth and curseforge")
		}
	case s.Modpack == nil:
		return invalid("modpack", "required for %s", s.Type)
	case s.Type == TypeModrinth && !slugRe.MatchString(*s.Modpack):
		return invalid("modpack", "must be a Modrinth project slug matching %s", slugRe)
	case s.Type == TypeCurseForge && !slugRe.MatchString(*s.Modpack) && !cfURLRe.MatchString(*s.Modpack):
		return invalid("modpack", "must be a CurseForge slug or https://www.curseforge.com/minecraft/modpacks/<slug>")
	}
	return nil
}

func ValidName(name string) bool { return nameRe.MatchString(name) }

// ValidBaseDomain accepts lowercase DNS names with at least two labels.
func ValidBaseDomain(d string) error {
	if d == "" || len(d) > maxBaseDomainLen {
		return invalid("base_domain", "must be 1..%d characters", maxBaseDomainLen)
	}
	labels := strings.Split(d, ".")
	if len(labels) < 2 {
		return invalid("base_domain", "needs at least two labels, e.g. mc.example.com")
	}
	for _, l := range labels {
		if !labelRe.MatchString(l) {
			return invalid("base_domain", "label %q must match %s", l, labelRe)
		}
	}
	return nil
}

// CheckDNSName accepts a lowercase DNS name of at most 253 characters without a trailing
// dot, every label matching the base domain label rule (the number of labels is the
// caller's rule).
func CheckDNSName(fqdn string) error {
	if fqdn == "" || len(fqdn) > maxDNSNameLen {
		return invalid("fqdn", "must be 1..%d characters", maxDNSNameLen)
	}
	for l := range strings.SplitSeq(fqdn, ".") {
		if !labelRe.MatchString(l) {
			return invalid("fqdn", "label %q must match %s", l, labelRe)
		}
	}
	return nil
}

func FQDN(name, baseDomain string) (string, error) {
	if !ValidName(name) {
		return "", invalid("name", "must match %s", nameRe)
	}
	if err := ValidBaseDomain(baseDomain); err != nil {
		return "", err
	}
	return name + "." + baseDomain, nil
}

// CheckFQDN verifies that fqdn is exactly "<name>.<valid base domain>".
func CheckFQDN(name, fqdn string) error {
	if !ValidName(name) {
		return invalid("name", "must match %s", nameRe)
	}
	base, ok := strings.CutPrefix(fqdn, name+".")
	if !ok {
		return invalid("fqdn", "must start with %q", name+".")
	}
	if err := ValidBaseDomain(base); err != nil {
		return invalid("fqdn", "%v", err)
	}
	return nil
}
