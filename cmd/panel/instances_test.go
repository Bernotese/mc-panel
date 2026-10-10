package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

// waitServer polls GET /servers/{id} until ok accepts the status (the reconcile loop runs in
// the background of the app).
func waitServer(t *testing.T, c *client, id int64, what string, ok func(status map[string]any) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		out := c.must(200, "GET", fmt.Sprintf("/servers/%d", id), nil)
		st, _ := out["status"].(map[string]any)
		if ok(st) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("server %d never became %s: %v", id, what, st)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func stateIs(want string) func(map[string]any) bool {
	return func(st map[string]any) bool { return st["state"] == want }
}

// Create → reconcile (ensure, start) → running → change → restart_required → restart
// (ensure with recreate) → running in the new revision; then acceptance criterion 4.
func TestInstanceLifecycleEndToEnd(t *testing.T) {
	e := newEnv(t)
	admin, secret := loginAdminWithTOTP(e)
	sudo(e, admin, secret)
	admin.must(200, "PUT", "/servers/settings", map[string]any{"base_domain": "mc.example.com"})
	a := admin.must(201, "POST", "/servers", paperServer("survival"))
	b := admin.must(201, "POST", "/servers", paperServer("creative"))
	srvA, srvB := int64(a["id"].(float64)), int64(b["id"].(float64))
	if a["address"] != "survival.mc.example.com" {
		t.Fatalf("address = %v", a["address"])
	}

	waitServer(t, admin, srvA, "running", stateIs("running"))
	waitServer(t, admin, srvB, "running", stateIs("running"))
	if req := e.instances.firstEnsure(); req.FQDN != "survival.mc.example.com" || req.Revision != 1 || req.Recreate {
		t.Fatalf("first ensure = %+v", req)
	}

	out := admin.must(200, "PATCH", fmt.Sprintf("/servers/%d", srvA), map[string]any{"memory_mib": 6144})
	if st, _ := out["status"].(map[string]any); !isTrue(st["restart_required"]) {
		t.Fatalf("after PATCH: %v", st)
	}
	admin.must(202, "POST", fmt.Sprintf("/servers/%d/power", srvA), map[string]string{"action": "restart"})
	if req := e.instances.lastEnsure(); req.ID != srvA || !req.Recreate || req.Revision != 2 || req.Spec.MemoryMiB != 6144 {
		t.Fatalf("restart ensure = %+v", req)
	}
	waitServer(t, admin, srvA, "running in revision 2", func(st map[string]any) bool {
		return st["state"] == "running" && !isTrue(st["restart_required"])
	})

	// acceptance criterion 4: server.power on A allows start/stop of A, nothing else
	u := admin.must(201, "POST", "/users", map[string]string{"username": "friend", "password": "another long secret 42", "role": "user"})
	admin.must(200, "PUT", fmt.Sprintf("/users/%d/permissions", int64(u["id"].(float64))), map[string]any{
		"grants": []map[string]any{{"server_id": srvA, "permissions": []string{"server.power"}}},
	})
	friend := e.client("")
	friend.must(200, "POST", "/auth/login", map[string]string{"username": "friend", "password": "another long secret 42"})
	friend.must(202, "POST", fmt.Sprintf("/servers/%d/power", srvA), map[string]string{"action": "stop"})
	waitServer(t, friend, srvA, "stopped", stateIs("stopped"))
	friend.must(202, "POST", fmt.Sprintf("/servers/%d/power", srvA), map[string]string{"action": "start"})
	waitServer(t, friend, srvA, "running", stateIs("running"))
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{"PATCH", fmt.Sprintf("/servers/%d", srvA), map[string]any{"memory_mib": 2048}},
		{"DELETE", fmt.Sprintf("/servers/%d", srvA), nil},
		{"GET", fmt.Sprintf("/servers/%d", srvB), nil},
		{"POST", fmt.Sprintf("/servers/%d/power", srvB), map[string]string{"action": "stop"}},
		{"GET", "/servers/settings", nil},
	} {
		if c, out := friend.do(tc.method, tc.path, tc.body); c != 403 {
			t.Errorf("%s %s as friend = %d %v, want 403", tc.method, tc.path, c, out)
		}
	}
}

// The fake mirrors the agent's downgrade guard: an ensure with an older revision than the
// container's is a no-op, even with recreate (a stale or replayed request).
func TestFakeInstancesNeverDowngrades(t *testing.T) {
	f := newFakeInstances()
	ctx := context.Background()
	ensure := func(rev int64, recreate bool) {
		t.Helper()
		req := agentapi.EnsureRequest{
			ID: 1, Name: "survival", FQDN: "survival.mc.example.com", Revision: rev, Recreate: recreate,
			Spec: instancespec.Spec{Type: instancespec.TypePaper, Version: "1.21.11", Java: instancespec.Java21, MemoryMiB: 4096},
		}
		if err := f.Ensure(ctx, req); err != nil {
			t.Fatal(err)
		}
	}
	revision := func() int64 {
		resp, _ := f.Instances(ctx)
		return resp.Instances[0].Revision
	}
	ensure(2, false)
	ensure(1, true)
	if got := revision(); got != 2 {
		t.Fatalf("revision after an older recreate = %d, want 2", got)
	}
	ensure(3, false)
	if got := revision(); got != 2 {
		t.Fatalf("revision after a newer ensure without recreate = %d, want 2", got)
	}
	ensure(3, true)
	if got := revision(); got != 3 {
		t.Fatalf("revision after a newer recreate = %d, want 3", got)
	}
}
