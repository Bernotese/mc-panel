package main

import (
	"fmt"
	"testing"
	"time"
)

// Create a dev through the real wiring (servers, backups queue, devinst), see it in the server
// list, pause its sync, and the delete rules: prod with a dev 409 dev_exists, dev 204, then prod.
func TestDevInstanceLifecycle(t *testing.T) {
	e := newEnv(t)
	admin, secret := loginAdminWithTOTP(e)
	sudo(e, admin, secret)
	admin.must(200, "PUT", "/servers/settings", map[string]any{"base_domain": "mc.example.com"})
	prod := admin.must(201, "POST", "/servers", paperServer("survival"))
	prodID := int64(prod["id"].(float64))

	dev := admin.must(201, "POST", fmt.Sprintf("/servers/%d/dev", prodID), map[string]any{})
	devID := int64(dev["id"].(float64))
	if dev["name"] != "dev-survival" {
		t.Fatalf("dev name %v", dev["name"])
	}
	if sum, _ := dev["dev"].(map[string]any); sum == nil || sum["source_id"] != float64(prodID) || sum["source_name"] != "survival" {
		t.Fatalf("dev summary %v", dev["dev"])
	}
	if code, out := admin.do("POST", fmt.Sprintf("/servers/%d/dev", prodID), map[string]any{}); code != 409 || out["error"] != "dev_exists" {
		t.Fatalf("second dev = %d %v", code, out)
	}

	code, list := admin.doList("GET", "/servers")
	if code != 200 {
		t.Fatalf("GET /servers = %d", code)
	}
	var sawProd, sawDev bool
	for _, s := range list {
		switch int64(s["id"].(float64)) {
		case prodID:
			sawProd = s["dev_id"] == float64(devID) && s["dev"] == nil
		case devID:
			sawDev = s["dev"] != nil && s["dev_id"] == nil
		}
	}
	if !sawProd || !sawDev {
		t.Fatalf("server list without dev_id on prod or dev on dev: %v", list)
	}

	got := admin.must(200, "GET", fmt.Sprintf("/servers/%d/dev", devID), nil)
	if got["source_name"] != "survival" || got["view_distance"] != float64(4) || got["simulation_distance"] != float64(3) {
		t.Fatalf("GET dev %v", got)
	}
	if code, out := admin.do("GET", fmt.Sprintf("/servers/%d/dev", prodID), nil); code != 409 || out["error"] != "not_dev" {
		t.Fatalf("GET dev of prod = %d %v", code, out)
	}
	got = admin.must(200, "PATCH", fmt.Sprintf("/servers/%d/dev", devID), map[string]any{"sync_paused": true})
	if !isTrue(got["sync_paused"]) || got["paused_at"] == nil || got["next_sync_at"] != nil {
		t.Fatalf("after pause %v", got)
	}
	admin.must(200, "POST", fmt.Sprintf("/servers/%d/dev/ops", devID), map[string]any{"name": "Steve"})

	if code, out := admin.do("DELETE", fmt.Sprintf("/servers/%d", prodID), nil); code != 409 || out["error"] != "dev_exists" {
		t.Fatalf("DELETE prod with dev = %d %v", code, out)
	}
	// The first sync holds the dev's data gate while it is queued or running; once it has
	// ended (with the fake backups it ends at once) the dev can be deleted.
	deadline := time.Now().Add(30 * time.Second)
	deleted := false
	for !deleted {
		got = admin.must(200, "GET", fmt.Sprintf("/servers/%d/dev", devID), nil)
		if !isTrue(got["sync_pending"]) {
			if got["last_sync_status"] == nil {
				t.Fatalf("no sync result after the first sync: %v", got)
			}
			admin.must(204, "DELETE", fmt.Sprintf("/servers/%d", devID), nil)
			break
		}
		switch code, out := admin.do("DELETE", fmt.Sprintf("/servers/%d", devID), nil); code {
		case 409: // still pending
		case 204: // the sync ended between the two requests
			deleted = true
		default:
			t.Fatalf("DELETE dev while its sync is pending = %d %v", code, out)
		}
		if time.Now().After(deadline) {
			t.Fatalf("the first sync never ended: %v", got)
		}
		// The backup queue waits on wall time; the API rate limit refills on the fake clock.
		time.Sleep(50 * time.Millisecond)
		e.clk.Advance(time.Second)
	}
	if code, _ := admin.do("GET", fmt.Sprintf("/servers/%d/dev", devID), nil); code != 404 {
		t.Fatalf("GET dev after delete = %d", code)
	}
	admin.must(204, "DELETE", fmt.Sprintf("/servers/%d", prodID), nil)
}
