package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/config"
)

const testDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func validProdAgent() config.Agent {
	a := config.DefaultAgent()
	a.PanelUID = 998
	return a
}

func TestAgentInstanceDefaults(t *testing.T) {
	a := validProdAgent()
	if err := a.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if a.DockerSocket != "/var/run/docker.sock" || a.InstancesDir != "/srv/mc-panel" || a.MCUser != "mcpanel-mc" {
		t.Fatalf("defaults = %+v", a)
	}
	if a.Images != config.DefaultImages() {
		t.Fatalf("images = %+v", a.Images)
	}
	for _, java := range []string{"java8", "java17", "java21", "java25"} {
		ref, ok := a.Images.ByJava(java)
		if !ok || !strings.HasPrefix(ref, "itzg/minecraft-server:2026.9.2-"+java+"@sha256:") {
			t.Errorf("ByJava(%s) = %q, %v", java, ref, ok)
		}
	}
	if _, ok := a.Images.ByJava("java11"); ok {
		t.Error("ByJava(java11) must fail")
	}
	if !strings.HasPrefix(a.Images.Router, "itzg/mc-router:1.47.2@sha256:") {
		t.Errorf("router = %q", a.Images.Router)
	}
}

func TestDevAgentInstanceSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MCPANEL_DEV_DOCKER", "")
	a := config.DevAgent("/repo")
	if err := a.Validate(); err != nil {
		t.Fatalf("dev agent invalid: %v", err)
	}
	// Docker is off in dev mode unless MCPANEL_DEV_DOCKER=1: make dev must not create
	// containers on the developer machine on its own.
	if a.InstancesDir != "/repo/.dev/mc" || a.MCUser != "" || a.DockerSocket != "" {
		t.Fatalf("dev agent default = %+v", a)
	}

	t.Setenv("MCPANEL_DEV_DOCKER", "1")
	// Without OrbStack/Docker Desktop the default path remains (it may or may not exist).
	if got := config.DevAgent("/repo").DockerSocket; got != "/var/run/docker.sock" {
		t.Fatalf("opt-in without docker: docker_socket = %q", got)
	}

	desktop := filepath.Join(home, ".docker", "run", "docker.sock")
	orb := filepath.Join(home, ".orbstack", "run", "docker.sock")
	for _, p := range []string{desktop, orb} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(desktop, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := config.DevAgent("/repo").DockerSocket; got != desktop {
		t.Fatalf("with Docker Desktop: docker_socket = %q, want %q", got, desktop)
	}
	if err := os.WriteFile(orb, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := config.DevAgent("/repo").DockerSocket; got != orb {
		t.Fatalf("OrbStack must win: docker_socket = %q, want %q", got, orb)
	}
}

func TestAgentInstanceValidation(t *testing.T) {
	cases := map[string]func(*config.Agent){
		"relative docker socket": func(a *config.Agent) { a.DockerSocket = "docker.sock" },
		"empty docker socket":    func(a *config.Agent) { a.DockerSocket = "" },
		"unclean instances dir":  func(a *config.Agent) { a.InstancesDir = "/srv/../etc" },
		"root instances dir":     func(a *config.Agent) { a.InstancesDir = "/" },
		"colon in instances dir": func(a *config.Agent) { a.InstancesDir = "/srv/mc:panel" },
		"comma in instances dir": func(a *config.Agent) { a.InstancesDir = "/srv/mc,panel" },
		"empty mc_user in prod":  func(a *config.Agent) { a.MCUser = "" },
		"tag without digest":     func(a *config.Agent) { a.Images.Java21 = "itzg/minecraft-server:java21" },
		"short digest":           func(a *config.Agent) { a.Images.Router = "itzg/mc-router@sha256:abc" },
		"upper case digest":      func(a *config.Agent) { a.Images.Java8 = "itzg/minecraft-server@" + strings.ToUpper(testDigest) },
		"whitespace":             func(a *config.Agent) { a.Images.Java17 = "itzg/minecraft-server @" + testDigest },
		"empty image":            func(a *config.Agent) { a.Images.Java25 = "" },
	}
	for name, mod := range cases {
		a := validProdAgent()
		mod(&a)
		if err := a.Validate(); err == nil {
			t.Errorf("%s: Validate = nil, want error", name)
		}
	}
	ok := validProdAgent()
	ok.Images.Java21 = "registry.example.com:5000/mc/server:v1@" + testDigest
	ok.Images.Router = "itzg/mc-router@" + testDigest
	if err := ok.Validate(); err != nil {
		t.Fatalf("registry with port / digest without tag: %v", err)
	}
	dev := config.DevAgent("/repo")
	dev.MCUser = ""
	if err := dev.Validate(); err != nil {
		t.Fatalf("dev mode allows an empty mc_user: %v", err)
	}
}

func TestLoadAgentInstanceKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.toml")
	content := `panel_uid = 998
docker_socket = "/run/docker.sock"
instances_dir = "/var/lib/mc"
mc_user = "minecraft"
[images]
router = "itzg/mc-router@` + testDigest + `"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := config.LoadAgent(path, false, "")
	if err != nil {
		t.Fatalf("LoadAgent: %v", err)
	}
	if a.DockerSocket != "/run/docker.sock" || a.InstancesDir != "/var/lib/mc" || a.MCUser != "minecraft" {
		t.Fatalf("agent = %+v", a)
	}
	if a.Images.Router != "itzg/mc-router@"+testDigest || a.Images.Java21 != config.DefaultImages().Java21 {
		t.Fatalf("images must overlay the defaults key by key: %+v", a.Images)
	}
}

func TestLoadAgentRejectsUnknownImageKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.toml")
	content := "panel_uid = 998\n[images]\njava11 = \"x@" + testDigest + "\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadAgent(path, false, ""); err == nil || !strings.Contains(err.Error(), "java11") {
		t.Fatalf("expected unknown key error, got %v", err)
	}
}

func TestLoadAgentRejectsUnpinnedImage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.toml")
	content := "panel_uid = 998\n[images]\njava21 = \"itzg/minecraft-server:java21\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadAgent(path, false, ""); err == nil || !strings.Contains(err.Error(), "images.java21") {
		t.Fatalf("expected digest error, got %v", err)
	}
}

func TestAgentWebDefaults(t *testing.T) {
	a := validProdAgent()
	if a.WebProxyPort != 18100 || a.WebAskPort != 18101 {
		t.Fatalf("web ports = %d, %d", a.WebProxyPort, a.WebAskPort)
	}
	if !strings.HasPrefix(a.Images.Web, "caddy:2.11.7-alpine@sha256:") {
		t.Fatalf("web image = %q", a.Images.Web)
	}
	got, err := config.LoadAgent(writeAgentConfig(t, "web_proxy_port = 28100\nweb_ask_port = 28101\n[images]\nweb = \"caddy@"+testDigest+"\"\n"), false, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.WebProxyPort != 28100 || got.WebAskPort != 28101 || got.Images.Web != "caddy@"+testDigest {
		t.Fatalf("loaded = %+v", got)
	}
}

func TestAgentWebValidation(t *testing.T) {
	cases := map[string]func(*config.Agent){
		"equal ports":       func(a *config.Agent) { a.WebAskPort = a.WebProxyPort },
		"privileged proxy":  func(a *config.Agent) { a.WebProxyPort = 80 },
		"privileged ask":    func(a *config.Agent) { a.WebAskPort = 1023 },
		"minecraft port":    func(a *config.Agent) { a.WebProxyPort = 25565 },
		"rcon port":         func(a *config.Agent) { a.WebAskPort = 25575 },
		"too large":         func(a *config.Agent) { a.WebProxyPort = 65536 },
		"zero":              func(a *config.Agent) { a.WebAskPort = 0 },
		"web image no pin":  func(a *config.Agent) { a.Images.Web = "caddy:2.11.7-alpine" },
		"web image missing": func(a *config.Agent) { a.Images.Web = "" },
	}
	for name, mod := range cases {
		a := validProdAgent()
		mod(&a)
		if err := a.Validate(); err == nil {
			t.Errorf("%s: Validate = nil, want error", name)
		}
	}
	ok := validProdAgent()
	ok.WebProxyPort, ok.WebAskPort = 1024, 65535
	if err := ok.Validate(); err != nil {
		t.Fatalf("bounds: %v", err)
	}
}
