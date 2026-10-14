package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

func TestRunAdminList(t *testing.T) {
	db := storetest.Open(t)
	storetest.CreateUser(t, db, "bob", "admin")
	storetest.CreateUser(t, db, "alice", "admin")
	storetest.CreateUser(t, db, "carol", "user")
	var out bytes.Buffer
	if err := runAdminList(context.Background(), db, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "alice\nbob\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestRunAdminListEmpty(t *testing.T) {
	db := storetest.Open(t)
	storetest.CreateUser(t, db, "carol", "user")
	var out bytes.Buffer
	if err := runAdminList(context.Background(), db, &out); err != nil || out.Len() != 0 {
		t.Fatalf("no admins: %q %v", out.String(), err)
	}
}

func TestAdminListCommand(t *testing.T) {
	t.Chdir(t.TempDir())
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var out, errOut bytes.Buffer
	if code := run([]string{"admin", "list", "-dev"}, f, &out, &errOut); code != 0 || out.Len() != 0 {
		t.Fatalf("admin list on an empty dev DB: code %d, out %q, err %q", code, out.String(), errOut.String())
	}
	if code := run([]string{"admin", "list", "-dev", "extra"}, f, &out, &errOut); code != 1 ||
		!strings.Contains(errOut.String(), "unexpected arguments") {
		t.Fatalf("extra argument: code %d, err %q", code, errOut.String())
	}
}

func TestUsageMentionsAdminList(t *testing.T) {
	if !strings.Contains(usageText, "panel admin list") {
		t.Fatal("usage does not mention admin list")
	}
}
