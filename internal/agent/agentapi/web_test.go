package agentapi_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

func goodWebRoutes() agentapi.WebRoutesRequest {
	return agentapi.WebRoutesRequest{Routes: []agentapi.WebRoute{
		{ID: 1, FQDN: "survival.mc.example.com", Port: 8100},
		{ID: 2, FQDN: "creative.mc.example.com", Port: 8123},
	}}
}

func TestWebRoutesRequestValidate(t *testing.T) {
	if err := goodWebRoutes().Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (agentapi.WebRoutesRequest{Routes: []agentapi.WebRoute{}}).Validate(); err != nil {
		t.Fatalf("empty list: %v", err)
	}
	many := make([]agentapi.WebRoute, agentapi.MaxWebRoutes)
	for i := range many {
		many[i] = agentapi.WebRoute{ID: int64(i + 1), FQDN: fmt.Sprintf("s%d.mc.example.com", i+1), Port: 8100}
	}
	if err := (agentapi.WebRoutesRequest{Routes: many}).Validate(); err != nil {
		t.Fatalf("%d routes: %v", agentapi.MaxWebRoutes, err)
	}
	long := strings.Repeat(strings.Repeat("a", 63)+".", 3) + strings.Repeat("a", 62) // 254 characters

	cases := map[string]func(*agentapi.WebRoutesRequest){
		"nil routes": func(r *agentapi.WebRoutesRequest) { r.Routes = nil },
		"too many routes": func(r *agentapi.WebRoutesRequest) {
			r.Routes = append(many, agentapi.WebRoute{ID: 9999, FQDN: "x.mc.example.com", Port: 8100})
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
		"port 1023":      func(r *agentapi.WebRoutesRequest) { r.Routes[0].Port = 1023 },
		"port 25565":     func(r *agentapi.WebRoutesRequest) { r.Routes[0].Port = 25565 },
		"port 25575":     func(r *agentapi.WebRoutesRequest) { r.Routes[0].Port = 25575 },
		"port 65536":     func(r *agentapi.WebRoutesRequest) { r.Routes[0].Port = 65536 },
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
