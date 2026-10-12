package store_test

import (
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

// pluginInstall is a valid row for serverID/filename (LuckPerms from Modrinth).
func pluginInstall(serverID int64, filename string) store.PluginsUpsertInstallParams {
	return store.PluginsUpsertInstallParams{
		ServerID: serverID, Filename: filename, Sha512: strings.Repeat("a", 128), Source: "modrinth",
		ProjectID: "Vebnzrzj", ProjectSlug: "luckperms", Title: "LuckPerms", VersionID: "lp5571",
		VersionName: "5.5.71", Channel: "release", InstalledAt: 10,
	}
}

func upsertInstall(t *testing.T, q *store.Queries, p store.PluginsUpsertInstallParams) {
	t.Helper()
	if err := q.PluginsUpsertInstall(t.Context(), p); err != nil {
		t.Fatalf("upsert %s: %v", p.Filename, err)
	}
}

func listInstalls(t *testing.T, q *store.Queries, serverID int64) []store.PluginInstall {
	t.Helper()
	list, err := q.PluginsListInstalls(t.Context(), serverID)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func TestPluginInstallsRoundTrip(t *testing.T) {
	db := storetest.Open(t)
	q := store.New(db)
	a := storetest.CreateServer(t, db, "survival")
	b := storetest.CreateServer(t, db, "creative")

	if list := listInstalls(t, q, a); list == nil || len(list) != 0 {
		t.Fatalf("empty server: %#v, want an empty, non-nil list", list)
	}
	upsertInstall(t, q, pluginInstall(a, "b.jar"))
	upsertInstall(t, q, pluginInstall(a, "A.jar"))
	upsertInstall(t, q, pluginInstall(b, "c.jar"))
	list := listInstalls(t, q, a)
	if len(list) != 2 || list[0].Filename != "A.jar" || list[1].Filename != "b.jar" {
		t.Fatalf("list = %+v, want A.jar, b.jar (byte order)", list)
	}

	// An upsert on the same (server, filename) replaces every other column.
	upd := pluginInstall(a, "b.jar")
	upd.Sha512, upd.Source, upd.ProjectID, upd.ProjectSlug = strings.Repeat("b", 128), "hangar", "2", "ViaVersion"
	upd.Title, upd.VersionID, upd.VersionName, upd.Channel, upd.InstalledAt = "ViaVersion", "5.5.1", "5.5.1", "beta", 20
	upsertInstall(t, q, upd)
	list = listInstalls(t, q, a)
	want := store.PluginInstall{
		ServerID: a, Filename: "b.jar", Sha512: upd.Sha512, Source: "hangar", ProjectID: "2", ProjectSlug: "ViaVersion",
		Title: "ViaVersion", VersionID: "5.5.1", VersionName: "5.5.1", Channel: "beta", InstalledAt: 20,
	}
	if len(list) != 2 || list[1] != want {
		t.Fatalf("after upsert: %+v, want second row %+v", list, want)
	}

	if err := q.PluginsDeleteInstall(t.Context(), store.PluginsDeleteInstallParams{ServerID: a, Filename: "b.jar"}); err != nil {
		t.Fatal(err)
	}
	if err := q.PluginsDeleteInstall(t.Context(), store.PluginsDeleteInstallParams{ServerID: a, Filename: "b.jar"}); err != nil {
		t.Fatalf("deleting a missing row: %v", err)
	}
	if list := listInstalls(t, q, a); len(list) != 1 || list[0].Filename != "A.jar" {
		t.Fatalf("after delete: %+v", list)
	}
	if list := listInstalls(t, q, b); len(list) != 1 || list[0].Filename != "c.jar" {
		t.Fatalf("other server touched: %+v", list)
	}
}

func TestPluginInstallsChecks(t *testing.T) {
	db := storetest.Open(t)
	q := store.New(db)
	ctx := t.Context()
	sid := storetest.CreateServer(t, db, "survival")
	for name, mutate := range map[string]func(*store.PluginsUpsertInstallParams){
		"source curseforge": func(p *store.PluginsUpsertInstallParams) { p.Source = "curseforge" },
		"source manual":     func(p *store.PluginsUpsertInstallParams) { p.Source = "manual" },
		"channel snapshot":  func(p *store.PluginsUpsertInstallParams) { p.Channel = "snapshot" },
		"unknown server":    func(p *store.PluginsUpsertInstallParams) { p.ServerID = sid + 1000 },
	} {
		p := pluginInstall(sid, "x.jar")
		mutate(&p)
		if err := q.PluginsUpsertInstall(ctx, p); err == nil {
			t.Errorf("%s: row accepted", name)
		}
	}
	upsertInstall(t, q, pluginInstall(sid, "x.jar"))
	if _, err := db.ExecContext(ctx, "DELETE FROM servers WHERE id = ?", sid); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM plugin_installs").Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows left after deleting the server: n=%d err=%v", n, err)
	}
}
