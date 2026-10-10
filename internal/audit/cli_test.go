package audit_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

func TestRunAuditVerify(t *testing.T) {
	db := storetest.Open(t)
	seed(t, db, 2)
	var out bytes.Buffer
	if err := audit.RunAuditVerify(context.Background(), db, &out); err != nil {
		t.Fatalf("RunAuditVerify on valid chain: %v", err)
	}
	if !strings.Contains(out.String(), "audit chain OK (2 entries)") {
		t.Fatalf("output = %q", out.String())
	}

	mustExec(t, db, "DROP TRIGGER audit_log_no_update")
	mustExec(t, db, "UPDATE audit_log SET ip = '198.51.100.1' WHERE id = 1")
	out.Reset()
	err := audit.RunAuditVerify(context.Background(), db, &out)
	if !errors.Is(err, audit.ErrChainBroken) {
		t.Fatalf("err = %v, want ErrChainBroken", err)
	}
	if !strings.Contains(out.String(), "audit chain BROKEN at entry id 1") {
		t.Fatalf("output = %q", out.String())
	}
}
