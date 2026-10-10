package agentapi_test

import (
	"errors"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

func goodEnsure() agentapi.EnsureRequest {
	return agentapi.EnsureRequest{
		ID: 1, Name: "survival", FQDN: "survival.mc.example.com", Revision: 1,
		Spec: instancespec.Spec{Type: instancespec.TypePaper, Version: "1.21.4", Java: instancespec.Java21, MemoryMiB: 4096},
	}
}

func TestEnsureRequestValidate(t *testing.T) {
	if err := goodEnsure().Validate(); err != nil {
		t.Fatal(err)
	}
	bad := map[string]func(*agentapi.EnsureRequest){
		"id":       func(r *agentapi.EnsureRequest) { r.ID = 0 },
		"name":     func(r *agentapi.EnsureRequest) { r.Name = "Bad" },
		"fqdn":     func(r *agentapi.EnsureRequest) { r.FQDN = "other.mc.example.com" },
		"revision": func(r *agentapi.EnsureRequest) { r.Revision = 0 },
		"spec":     func(r *agentapi.EnsureRequest) { r.Spec.MemoryMiB = 1 },
	}
	for name, mod := range bad {
		r := goodEnsure()
		mod(&r)
		if err := r.Validate(); !errors.Is(err, instancespec.ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
}

func TestPowerActions(t *testing.T) {
	for _, a := range []agentapi.PowerAction{agentapi.PowerStart, agentapi.PowerStop, agentapi.PowerRestart} {
		if !agentapi.ValidPowerAction(a) {
			t.Errorf("%s must be valid", a)
		}
	}
	if agentapi.ValidPowerAction("kill") {
		t.Error("kill must be invalid")
	}
}
