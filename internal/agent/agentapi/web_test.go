package agentapi_test

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

func goodWebRoutes() agentapi.WebRoutesRequest {
	return agentapi.WebRoutesRequest{Routes: []agentapi.WebRoute{
		{ID: 1, FQDN: "survival.mc.example.com", Targets: []agentapi.WebTarget{{Path: "", Port: 8100}, {Path: "afk-smoke", Port: 8200}}},
		{ID: 2, FQDN: "creative.mc.example.com", Targets: root(8123)},
	}}
}

func root(port int) []agentapi.WebTarget { return []agentapi.WebTarget{{Path: "", Port: port}} }

func TestWebRoutesRequestValidate(t *testing.T) {
	if err := goodWebRoutes().Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (agentapi.WebRoutesRequest{Routes: []agentapi.WebRoute{}}).Validate(); err != nil {
		t.Fatalf("empty list: %v", err)
	}
	many := make([]agentapi.WebRoute, agentapi.MaxWebRoutes)
	for i := range many {
		many[i] = agentapi.WebRoute{ID: int64(i + 1), FQDN: fmt.Sprintf("s%d.mc.example.com", i+1), Targets: root(8100)}
	}
	if err := (agentapi.WebRoutesRequest{Routes: many}).Validate(); err != nil {
		t.Fatalf("%d routes: %v", agentapi.MaxWebRoutes, err)
	}
	long := strings.Repeat(strings.Repeat("a", 63)+".", 3) + strings.Repeat("a", 62) // 254 characters

	cases := map[string]func(*agentapi.WebRoutesRequest){
		"nil routes": func(r *agentapi.WebRoutesRequest) { r.Routes = nil },
		"too many routes": func(r *agentapi.WebRoutesRequest) {
			r.Routes = append(many, agentapi.WebRoute{ID: 9999, FQDN: "x.mc.example.com", Targets: root(8100)})
		},
		"id 0":           func(r *agentapi.WebRoutesRequest) { r.Routes[0].ID = 0 },
		"negative id":    func(r *agentapi.WebRoutesRequest) { r.Routes[0].ID = -1 },
		"duplicate id":   func(r *agentapi.WebRoutesRequest) { r.Routes[1].ID = 1 },
		"duplicate fqdn": func(r *agentapi.WebRoutesRequest) { r.Routes[1].FQDN = r.Routes[0].FQDN },
		"upper case":     func(r *agentapi.WebRoutesRequest) { r.Routes[0].FQDN = "Survival.mc.example.com" },
		"trailing dot":   func(r *agentapi.WebRoutesRequest) { r.Routes[0].FQDN = "survival.mc.example.com." },
		"two labels":     func(r *agentapi.WebRoutesRequest) { r.Routes[0].FQDN = "survival.example" },
		"underscore":     func(r *agentapi.WebRoutesRequest) { r.Routes[0].FQDN = "sur_vival.mc.example.com" },
		"empty fqdn":     func(r *agentapi.WebRoutesRequest) { r.Routes[0].FQDN = "" },
		"254 characters": func(r *agentapi.WebRoutesRequest) { r.Routes[0].FQDN = long },
		"injection":      func(r *agentapi.WebRoutesRequest) { r.Routes[0].FQDN = `a.b.c"}]` },
		"port 1023":      func(r *agentapi.WebRoutesRequest) { r.Routes[0].Targets[0].Port = 1023 },
		"port 25565":     func(r *agentapi.WebRoutesRequest) { r.Routes[0].Targets[0].Port = 25565 },
		"port 25575":     func(r *agentapi.WebRoutesRequest) { r.Routes[0].Targets[0].Port = 25575 },
		"port 65536":     func(r *agentapi.WebRoutesRequest) { r.Routes[0].Targets[0].Port = 65536 },
		"nil targets":    func(r *agentapi.WebRoutesRequest) { r.Routes[0].Targets = nil },
		"no targets":     func(r *agentapi.WebRoutesRequest) { r.Routes[0].Targets = []agentapi.WebTarget{} },
		"nine targets": func(r *agentapi.WebRoutesRequest) {
			r.Routes[0].Targets = root(8100)
			for i := range agentapi.MaxWebTargets {
				r.Routes[0].Targets = append(r.Routes[0].Targets, agentapi.WebTarget{Path: fmt.Sprintf("p%d", i), Port: 8200 + i})
			}
		},
		"upper case path": func(r *agentapi.WebRoutesRequest) { r.Routes[0].Targets[1].Path = "Afk" },
		"leading dash":    func(r *agentapi.WebRoutesRequest) { r.Routes[0].Targets[1].Path = "-afk" },
		"trailing dash":   func(r *agentapi.WebRoutesRequest) { r.Routes[0].Targets[1].Path = "afk-" },
		"nested path":     func(r *agentapi.WebRoutesRequest) { r.Routes[0].Targets[1].Path = "afk/smoke" },
		"dot path":        func(r *agentapi.WebRoutesRequest) { r.Routes[0].Targets[1].Path = "afk.smoke" },
		"33 characters":   func(r *agentapi.WebRoutesRequest) { r.Routes[0].Targets[1].Path = strings.Repeat("a", 33) },
		"duplicate path":  func(r *agentapi.WebRoutesRequest) { r.Routes[0].Targets[0].Path = "afk-smoke" },
		"two roots":       func(r *agentapi.WebRoutesRequest) { r.Routes[0].Targets[1].Path = "" },
		"unsorted": func(r *agentapi.WebRoutesRequest) {
			ts := r.Routes[0].Targets
			ts[0], ts[1] = ts[1], ts[0]
		},
		"duplicate port":  func(r *agentapi.WebRoutesRequest) { r.Routes[0].Targets[1].Port = 8100 },
		"path port 25575": func(r *agentapi.WebRoutesRequest) { r.Routes[0].Targets[1].Port = 25575 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := goodWebRoutes()
			mutate(&r)
			if err := r.Validate(); !errors.Is(err, instancespec.ErrInvalid) {
				t.Fatalf("Validate = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestCheckWebTargetsLimit(t *testing.T) {
	ts := root(8100)
	for i := range agentapi.MaxWebTargets - 1 {
		ts = append(ts, agentapi.WebTarget{Path: fmt.Sprintf("p%d", i), Port: 8200 + i})
	}
	if err := agentapi.CheckWebTargets(ts); err != nil {
		t.Fatalf("%d targets: %v", len(ts), err)
	}
	if err := agentapi.CheckWebTargets([]agentapi.WebTarget{{Path: "map", Port: 8100}}); err != nil {
		t.Fatalf("no root: %v", err)
	}
}

func TestValidWebPath(t *testing.T) {
	for p, want := range map[string]bool{
		"": true, "a": true, "0": true, "afk-smoke": true, "a-b-c": true, strings.Repeat("a", 32): true,
		strings.Repeat("a", 33): false, "A": false, "-": false, "a_b": false, "a/b": false, "..": false, " a": false, "ä": false,
	} {
		if got := agentapi.ValidWebPath(p); got != want {
			t.Errorf("ValidWebPath(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestWebRouteEqual(t *testing.T) {
	a := goodWebRoutes().Routes[0]
	if !a.Equal(goodWebRoutes().Routes[0]) {
		t.Fatal("equal routes")
	}
	for name, mutate := range map[string]func(*agentapi.WebRoute){
		"id":           func(r *agentapi.WebRoute) { r.ID = 9 },
		"fqdn":         func(r *agentapi.WebRoute) { r.FQDN = "x.mc.example.com" },
		"port":         func(r *agentapi.WebRoute) { r.Targets[1].Port = 8201 },
		"path":         func(r *agentapi.WebRoute) { r.Targets[1].Path = "map" },
		"fewer":        func(r *agentapi.WebRoute) { r.Targets = r.Targets[:1] },
		"target order": func(r *agentapi.WebRoute) { r.Targets[0], r.Targets[1] = r.Targets[1], r.Targets[0] },
	} {
		b := goodWebRoutes().Routes[0]
		mutate(&b)
		if a.Equal(b) {
			t.Errorf("%s: Equal = true", name)
		}
	}
}

func TestWebRuleEqual(t *testing.T) {
	a := agentapi.WebRule{Source: netip.MustParseAddr("172.30.1.14"), Dest: netip.MustParsePrefix("172.30.1.0/28"), Ports: []uint16{8100, 8200}}
	b := a
	b.Ports = []uint16{8100, 8200}
	if !a.Equal(b) {
		t.Fatal("equal rules")
	}
	for name, c := range map[string]agentapi.WebRule{
		"source": {Source: netip.MustParseAddr("172.30.1.13"), Dest: a.Dest, Ports: a.Ports},
		"dest":   {Source: a.Source, Dest: netip.MustParsePrefix("172.30.1.0/27"), Ports: a.Ports},
		"ports":  {Source: a.Source, Dest: a.Dest, Ports: []uint16{8100}},
		"order":  {Source: a.Source, Dest: a.Dest, Ports: []uint16{8200, 8100}},
	} {
		if a.Equal(c) {
			t.Errorf("%s: Equal = true", name)
		}
	}
}

func TestValidWebPort(t *testing.T) {
	for p, want := range map[int]bool{
		0: false, 80: false, 1023: false, 1024: true, 8100: true, 25564: true, 25565: false,
		25566: true, 25575: false, 65535: true, 65536: false, -1: false,
	} {
		if got := agentapi.ValidWebPort(p); got != want {
			t.Errorf("ValidWebPort(%d) = %v, want %v", p, got, want)
		}
	}
}
