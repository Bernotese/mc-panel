package console_test

import (
	"context"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/auth"
	"github.com/Bernotese/mc-panel/internal/config"
	"github.com/Bernotese/mc-panel/internal/console"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

// SEC-10: the stream recheck goes through the effective session stage of auth, so a user who got
// server.console but has no second factor loses an open stream at the next 5 s recheck. The
// session middleware already refuses to open such a stream; the test injects the principal to
// stand for a stream opened in the window before the grant took effect. With a method the
// stream stays open.
func TestStreamRecheckEndsWhenGrantMakes2FAMandatory(t *testing.T) {
	for _, withTOTP := range []bool{false, true} {
		name := "without a second factor"
		if withTOTP {
			name = "with TOTP"
		}
		t.Run(name, func(t *testing.T) {
			var sessions *auth.Sessions
			e := newEnvWith(t, func(d *console.Deps) {
				sessions = auth.NewSessions(store.New(d.DB), d.Clock, config.DevPanel(t.TempDir()).Session)
				d.Access = console.NewAccess(sessions, rbac.NewDBChecker(d.DB))
			})
			id := createInstance(t, e.db, "survival")
			alice := storetest.CreateUser(t, e.db, "alice", "user")
			if withTOTP {
				mustExec(t, e.db, `UPDATE users SET totp_enabled = 1 WHERE id = ?`, alice)
			}
			_, sess, err := sessions.Create(context.Background(), auth.NewSession{UserID: alice, Stage: principal.StageFull})
			if err != nil {
				t.Fatal(err)
			}
			grant(t, e.db, alice, id, "server.view", "server.console")
			p := userP(alice, "alice")
			p.SessionID = sess.ID
			srv := e.streamServer(nil)
			s := e.agent.prepare()
			c := e.openStream(srv, p, id, "", "")
			expectState(t, c.next(), true)
			e.timers.fire(t, 5*time.Second)
			if withTOTP {
				e.timers.fire(t, 15*time.Second)
				if ev := c.next(); ev.Comment != "ping" {
					t.Fatalf("event = %+v, want ping (stream stays open)", ev)
				}
				return
			}
			c.expectClosed()
			s.waitClosed(t)
		})
	}
}
