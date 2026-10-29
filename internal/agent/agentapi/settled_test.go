package agentapi_test

import (
	"encoding/json"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
)

func TestInstanceStatusSettled(t *testing.T) {
	cases := []struct {
		name string
		st   agentapi.InstanceStatus
		want bool
	}{
		{"idle", agentapi.InstanceStatus{}, true},
		{"empty queue", agentapi.InstanceStatus{Queued: []agentapi.PowerAction{}}, true},
		{"operation running", agentapi.InstanceStatus{Operation: agentapi.OpStopping}, false},
		{"start queued", agentapi.InstanceStatus{Queued: []agentapi.PowerAction{agentapi.PowerStart}}, false},
		{"not inspected", agentapi.InstanceStatus{Uncertain: true}, false},
	}
	for _, tc := range cases {
		if got := tc.st.Settled(); got != tc.want {
			t.Errorf("%s: Settled() = %v, want %v", tc.name, got, tc.want)
		}
	}
	var st agentapi.InstanceStatus
	if err := json.Unmarshal([]byte(`{"operation":"","queued":["start"]}`), &st); err != nil || len(st.Queued) != 1 || st.Settled() {
		t.Fatalf("queued round trip: %+v, %v", st, err)
	}
}
