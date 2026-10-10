package principal_test

import (
	"context"
	"testing"

	"github.com/Bernotese/mc-panel/internal/principal"
)

func TestContextRoundTrip(t *testing.T) {
	if _, ok := principal.From(context.Background()); ok {
		t.Fatal("empty context must have no principal")
	}
	p := principal.Principal{UserID: 7, Username: "jannes", Role: principal.RoleAdmin, Stage: principal.StageFull}
	got, ok := principal.From(principal.With(context.Background(), p))
	if !ok || got.UserID != 7 || !got.IsAdmin() {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
}
