package plugins

import (
	"slices"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

func TestKindOf(t *testing.T) {
	for typ, want := range map[instancespec.Type]string{
		instancespec.TypePaper:    "plugin plugins plugin paper,spigot,bukkit modrinth,hangar",
		instancespec.TypePurpur:   "plugin plugins plugin purpur,paper,spigot,bukkit modrinth,hangar",
		instancespec.TypeFabric:   "mod mods mod fabric modrinth",
		instancespec.TypeNeoForge: "mod mods mod neoforge modrinth",
	} {
		k, ok := kindOf(typ)
		var srcs []string
		for _, s := range k.Sources {
			srcs = append(srcs, string(s))
		}
		got := k.Kind + " " + k.Dir + " " + k.ProjectType + " " + strings.Join(k.Loaders, ",") + " " + strings.Join(srcs, ",")
		if !ok || got != want {
			t.Errorf("%s: %q (%v), want %q", typ, got, ok, want)
		}
	}
	for _, typ := range []instancespec.Type{instancespec.TypeVanilla, instancespec.TypeModrinth, instancespec.TypeCurseForge, "bukkit"} {
		if _, ok := kindOf(typ); ok {
			t.Errorf("%s has plugins", typ)
		}
	}
	k, _ := kindOf(instancespec.TypeFabric)
	if k.hasSource(upstream.Hangar) || !k.hasSource(upstream.Modrinth) || !k.isMod() {
		t.Errorf("fabric kind = %+v", k)
	}
}

func TestPageURL(t *testing.T) {
	for _, tc := range []struct {
		src                     upstream.Source
		kind, owner, slug, want string
	}{
		{upstream.Modrinth, "plugin", "", "luckperms", "https://modrinth.com/plugin/luckperms"},
		{upstream.Modrinth, "mod", "", "fabric-api", "https://modrinth.com/mod/fabric-api"},
		{upstream.Hangar, "plugin", "ViaVersion", "ViaBackwards", "https://hangar.papermc.io/ViaVersion/ViaBackwards"},
		{upstream.Hangar, "plugin", "", "ViaBackwards", "https://hangar.papermc.io/"},
		{upstream.Modrinth, "plugin", "", "a b/c", "https://modrinth.com/plugin/a%20b%2Fc"},
	} {
		if got := pageURL(tc.src, tc.kind, tc.owner, tc.slug); got != tc.want {
			t.Errorf("pageURL(%s, %s, %q, %q) = %q, want %q", tc.src, tc.kind, tc.owner, tc.slug, got, tc.want)
		}
	}
}

func TestCompatible(t *testing.T) {
	paper, _ := kindOf(instancespec.TypePaper)
	fabric, _ := kindOf(instancespec.TypeFabric)
	m := upstream.Version{Source: upstream.Modrinth, GameVersions: []string{"1.21.10", "1.21.11"}, Loaders: []string{"bukkit", "folia"}}
	if !compatible(m, paper, "1.21.11") || compatible(m, paper, "26.3") || compatible(m, fabric, "1.21.11") {
		t.Error("modrinth: loader or game version check wrong")
	}
	h := upstream.Version{Source: upstream.Hangar, GameVersions: []string{"1.21.11"}}
	if !compatible(h, paper, "1.21.11") || compatible(h, paper, "1.21.10") {
		t.Error("hangar: game version check wrong")
	}
	if slices.Contains(paper.Loaders, "fabric") {
		t.Error("paper loaders contain fabric")
	}
}
