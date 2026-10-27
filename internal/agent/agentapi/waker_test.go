package agentapi_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

func TestWakerRequestValidate(t *testing.T) {
	many := make([]agentapi.WakerTarget, agentapi.MaxWakerTargets+1)
	for i := range many {
		many[i] = agentapi.WakerTarget{ID: int64(i + 1), FQDN: "dev-a.example.com"}
	}
	ok := agentapi.WakerTarget{ID: 1, FQDN: "dev-survival.example.com"}
	cases := []struct {
		name  string
		req   agentapi.WakerRequest
		valid bool
	}{
		{"nil list", agentapi.WakerRequest{}, false},
		{"empty list", agentapi.WakerRequest{Sleeping: []agentapi.WakerTarget{}}, true},
		{"one dev", agentapi.WakerRequest{Sleeping: []agentapi.WakerTarget{ok}}, true},
		{"too many", agentapi.WakerRequest{Sleeping: many}, false},
		{"duplicate id", agentapi.WakerRequest{Sleeping: []agentapi.WakerTarget{ok, {ID: 1, FQDN: "dev-b.example.com"}}}, false},
		{"duplicate fqdn", agentapi.WakerRequest{Sleeping: []agentapi.WakerTarget{ok, {ID: 2, FQDN: ok.FQDN}}}, false},
		{"id 0", agentapi.WakerRequest{Sleeping: []agentapi.WakerTarget{{ID: 0, FQDN: ok.FQDN}}}, false},
		{"not a dev", agentapi.WakerRequest{Sleeping: []agentapi.WakerTarget{{ID: 1, FQDN: "survival.example.com"}}}, false},
		{"uppercase", agentapi.WakerRequest{Sleeping: []agentapi.WakerTarget{{ID: 1, FQDN: "dev-Survival.example.com"}}}, false},
		{"too long", agentapi.WakerRequest{Sleeping: []agentapi.WakerTarget{{ID: 1, FQDN: "dev-a." + strings.Repeat("b", 250) + ".com"}}}, false},
		{"no domain", agentapi.WakerRequest{Sleeping: []agentapi.WakerTarget{{ID: 1, FQDN: "dev-a"}}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.req.Validate()
			if tc.valid && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
			if !tc.valid && !errors.Is(err, instancespec.ErrInvalid) {
				t.Fatalf("Validate() = %v, want an ErrInvalid", err)
			}
		})
	}
}
