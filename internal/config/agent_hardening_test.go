package config_test

import (
	"math"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/config"
)

func writeAgentConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent.toml")
	if err := os.WriteFile(path, []byte("panel_uid = 998\n"+content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAgentHardeningDefaults(t *testing.T) {
	a, err := config.LoadAgent(writeAgentConfig(t, ""), false, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"169.254.0.0/16", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "fc00::/7", "fe80::/10"}
	if !slices.Equal(a.ContainerEgressDeny, want) || !slices.Equal(config.DefaultContainerEgressDeny(), want) {
		t.Fatalf("container_egress_deny = %v, want %v", a.ContainerEgressDeny, want)
	}
	var prefixes []string
	for _, p := range a.EgressDenyPrefixes() {
		prefixes = append(prefixes, p.String())
	}
	if !slices.Equal(prefixes, want) {
		t.Fatalf("EgressDenyPrefixes = %v", prefixes)
	}
	if a.RouterConnectionRateLimit != 50 || a.CPUPerInstance != 0 {
		t.Fatalf("router_connection_rate_limit = %d, cpu_per_instance = %v", a.RouterConnectionRateLimit, a.CPUPerInstance)
	}
	// The default must not be shared: changing one agent's list must not change the next default.
	a.ContainerEgressDeny[0] = "x"
	if config.DefaultContainerEgressDeny()[0] != "169.254.0.0/16" {
		t.Fatal("default deny list is shared")
	}
}

func TestLoadAgentHardeningKeys(t *testing.T) {
	a, err := config.LoadAgent(writeAgentConfig(t, `container_egress_deny = ["10.0.0.0/8", "2001:db8::/32"]
router_connection_rate_limit = 200
cpu_per_instance = 1.5
`), false, "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(a.EgressDenyPrefixes(), []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("2001:db8::/32")}) ||
		a.RouterConnectionRateLimit != 200 || a.CPUPerInstance != 1.5 {
		t.Fatalf("agent = %+v", a)
	}

	// An empty list disables the filter; it must not fall back to the default.
	a, err = config.LoadAgent(writeAgentConfig(t, "container_egress_deny = []\n"), false, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(a.ContainerEgressDeny) != 0 || len(a.EgressDenyPrefixes()) != 0 {
		t.Fatalf("empty list = %v", a.ContainerEgressDeny)
	}
}

func TestAgentHardeningValidation(t *testing.T) {
	cases := map[string]func(*config.Agent){
		"not a prefix":        func(a *config.Agent) { a.ContainerEgressDeny = []string{"10.0.0.1"} },
		"garbage":             func(a *config.Agent) { a.ContainerEgressDeny = []string{"private"} },
		"host bits set":       func(a *config.Agent) { a.ContainerEgressDeny = []string{"10.0.0.1/8"} },
		"4in6":                func(a *config.Agent) { a.ContainerEgressDeny = []string{"::ffff:10.0.0.0/104"} },
		"zone":                func(a *config.Agent) { a.ContainerEgressDeny = []string{"fe80::%eth0/10"} },
		"overlap v4":          func(a *config.Agent) { a.ContainerEgressDeny = []string{"10.0.0.0/8", "10.1.0.0/16"} },
		"duplicate":           func(a *config.Agent) { a.ContainerEgressDeny = []string{"fc00::/7", "fc00::/7"} },
		"too many":            func(a *config.Agent) { a.ContainerEgressDeny = manyPrefixes(65) },
		"rate limit 0":        func(a *config.Agent) { a.RouterConnectionRateLimit = 0 },
		"rate limit huge":     func(a *config.Agent) { a.RouterConnectionRateLimit = 100001 },
		"negative cpu":        func(a *config.Agent) { a.CPUPerInstance = -1 },
		"more cpus than host": func(a *config.Agent) { a.CPUPerInstance = float64(runtime.NumCPU()) + 0.5 },
		"NaN cpu":             func(a *config.Agent) { a.CPUPerInstance = math.NaN() },
		"tiny cpu":            func(a *config.Agent) { a.CPUPerInstance = 0.001 },
	}
	for name, mod := range cases {
		a := validProdAgent()
		mod(&a)
		if err := a.Validate(); err == nil {
			t.Errorf("%s: Validate = nil, want error", name)
		}
	}
	ok := validProdAgent()
	ok.ContainerEgressDeny = append(manyPrefixes(63), "fd00::/8")
	ok.RouterConnectionRateLimit = 1
	ok.CPUPerInstance = float64(runtime.NumCPU())
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid limits: %v", err)
	}
	if err := writeAndLoad(t, "cpu_per_instance = 0.5\n"); err != nil {
		t.Fatalf("fractional cpu: %v", err)
	}
}

func writeAndLoad(t *testing.T, content string) error {
	t.Helper()
	_, err := config.LoadAgent(writeAgentConfig(t, content), false, "")
	return err
}

func manyPrefixes(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = netip.AddrFrom4([4]byte{10, byte(i), 0, 0}).String() + "/16"
	}
	return out
}

func TestEgressDenyErrorNamesTheKey(t *testing.T) {
	a := validProdAgent()
	a.ContainerEgressDeny = []string{"10.0.0.0/8", "10.1.0.0/16"}
	if err := a.Validate(); err == nil || !strings.Contains(err.Error(), "container_egress_deny") || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("Validate = %v", err)
	}
}
