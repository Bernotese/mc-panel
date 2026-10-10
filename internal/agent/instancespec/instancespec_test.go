package instancespec_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

func ptr[T any](v T) *T { return &v }

func validSpec() instancespec.Spec {
	return instancespec.Spec{Type: instancespec.TypePaper, Version: "1.21.4", Java: instancespec.Java21, MemoryMiB: 4096}
}

func TestValidSpecs(t *testing.T) {
	cases := map[string]func(*instancespec.Spec){
		"paper":         func(*instancespec.Spec) {},
		"latest":        func(s *instancespec.Spec) { s.Version = "LATEST" },
		"fabric loader": func(s *instancespec.Spec) { s.Type = instancespec.TypeFabric; s.LoaderVersion = ptr("0.19.5") },
		"neoforge":      func(s *instancespec.Spec) { s.Type = instancespec.TypeNeoForge; s.LoaderVersion = ptr("21.4.140") },
		"modrinth": func(s *instancespec.Spec) {
			s.Type = instancespec.TypeModrinth
			s.Version = ""
			s.Modpack = ptr("cobblemon-fabric")
		},
		"curseforge url": func(s *instancespec.Spec) {
			s.Type = instancespec.TypeCurseForge
			s.Version = ""
			s.Modpack = ptr("https://www.curseforge.com/minecraft/modpacks/all-the-mods-10")
			s.CurseForgeAPIKey = ptr("$2a$10$abcdefghijklmnopqrstuv")
		},
		"min memory": func(s *instancespec.Spec) { s.MemoryMiB = 1024 },
		"max memory": func(s *instancespec.Spec) { s.MemoryMiB = 32768 },
	}
	for name, mod := range cases {
		s := validSpec()
		mod(&s)
		if err := s.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestInvalidSpecs(t *testing.T) {
	cases := map[string]func(*instancespec.Spec){
		"unknown type":       func(s *instancespec.Spec) { s.Type = "bukkit" },
		"empty version":      func(s *instancespec.Spec) { s.Version = "" },
		"version with space": func(s *instancespec.Spec) { s.Version = "1.21 4" },
		"version newline":    func(s *instancespec.Spec) { s.Version = "1.21\nEULA=FALSE" },
		"version too long":   func(s *instancespec.Spec) { s.Version = strings.Repeat("1", 65) },
		"loader on paper":    func(s *instancespec.Spec) { s.LoaderVersion = ptr("1") },
		"bad loader":         func(s *instancespec.Spec) { s.Type = instancespec.TypeFabric; s.LoaderVersion = ptr("0.19 5") },
		"modpack on paper":   func(s *instancespec.Spec) { s.Modpack = ptr("x") },
		"modrinth no pack":   func(s *instancespec.Spec) { s.Type = instancespec.TypeModrinth; s.Version = "" },
		"modrinth version":   func(s *instancespec.Spec) { s.Type = instancespec.TypeModrinth; s.Modpack = ptr("x") },
		"modrinth bad slug": func(s *instancespec.Spec) {
			s.Type = instancespec.TypeModrinth
			s.Version = ""
			s.Modpack = ptr("Bad Slug")
		},
		"curseforge other url": func(s *instancespec.Spec) {
			s.Type = instancespec.TypeCurseForge
			s.Version = ""
			s.Modpack = ptr("https://evil.example/minecraft/modpacks/x")
			s.CurseForgeAPIKey = ptr("k")
		},
		"curseforge no key": func(s *instancespec.Spec) {
			s.Type = instancespec.TypeCurseForge
			s.Version = ""
			s.Modpack = ptr("atm10")
		},
		"curseforge java8": func(s *instancespec.Spec) {
			s.Type = instancespec.TypeCurseForge
			s.Version = ""
			s.Modpack = ptr("atm10")
			s.CurseForgeAPIKey = ptr("k")
			s.Java = instancespec.Java8
		},
		"key on paper": func(s *instancespec.Spec) { s.CurseForgeAPIKey = ptr("k") },
		"key with newline": func(s *instancespec.Spec) {
			s.Type = instancespec.TypeCurseForge
			s.Version = ""
			s.Modpack = ptr("atm10")
			s.CurseForgeAPIKey = ptr("k\nX=1")
		},
		"unknown java":     func(s *instancespec.Spec) { s.Java = "java11" },
		"memory too small": func(s *instancespec.Spec) { s.MemoryMiB = 768 },
		"memory too large": func(s *instancespec.Spec) { s.MemoryMiB = 33024 },
		"memory step":      func(s *instancespec.Spec) { s.MemoryMiB = 4000 },
		"bad property":     func(s *instancespec.Spec) { s.Properties.MaxPlayers = ptr(0) },
	}
	for name, mod := range cases {
		s := validSpec()
		mod(&s)
		err := s.Validate()
		if !errors.Is(err, instancespec.ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
}

func TestNamesAndDomains(t *testing.T) {
	for _, n := range []string{"survival", "a1", "creative-2", strings.Repeat("a", 32)} {
		if !instancespec.ValidName(n) {
			t.Errorf("ValidName(%q) = false", n)
		}
	}
	for _, n := range []string{"", "a", "-x", "Upper", "a_b", "a.b", strings.Repeat("a", 33)} {
		if instancespec.ValidName(n) {
			t.Errorf("ValidName(%q) = true", n)
		}
	}
	for _, d := range []string{"mc.example.com", "example.de", "mc.localhost", "a-b.c-d.example"} {
		if err := instancespec.ValidBaseDomain(d); err != nil {
			t.Errorf("ValidBaseDomain(%q): %v", d, err)
		}
	}
	for _, d := range []string{"", "localhost", "MC.example.com", "-a.example", "a..b", "a.b.", ".a.b", "a_b.example", "a.b:25565", strings.Repeat("a.", 101) + "de"} {
		if err := instancespec.ValidBaseDomain(d); !errors.Is(err, instancespec.ErrInvalid) {
			t.Errorf("ValidBaseDomain(%q) = %v", d, err)
		}
	}
	fqdn, err := instancespec.FQDN("survival", "mc.example.com")
	if err != nil || fqdn != "survival.mc.example.com" {
		t.Fatalf("FQDN = %q, %v", fqdn, err)
	}
	if err := instancespec.CheckFQDN("survival", "survival.mc.example.com"); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"creative.mc.example.com", "survival", "survival.", "survivalx.example.com", "survival.mc.example.com\n"} {
		if err := instancespec.CheckFQDN("survival", f); !errors.Is(err, instancespec.ErrInvalid) {
			t.Errorf("CheckFQDN(survival, %q) = %v", f, err)
		}
	}
}
