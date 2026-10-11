package serverfiles

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// Review finding M1: once the upload is answered, a late read of the body (e.g. by a client
// goroutine still sending) fails without touching the connection's deadlines.
func TestDeadlineReaderAfterFinish(t *testing.T) {
	var set, cleared int
	d := &deadlineReader{
		r:     strings.NewReader("ab"),
		dl:    func() error { set++; return nil },
		clear: func() error { cleared++; return nil },
	}
	if _, err := io.ReadAll(d); err != nil {
		t.Fatal(err)
	}
	if set == 0 || cleared != 1 {
		t.Fatalf("before finish: %d deadlines set, %d cleared (want the deadline cleared once at EOF)", set, cleared)
	}
	d.finish()
	before := set
	if n, err := d.Read(make([]byte, 1)); n != 0 || !errors.Is(err, errUploadDone) {
		t.Fatalf("read after finish = %d, %v", n, err)
	}
	if set != before || cleared != 1 {
		t.Fatal("a read after finish touched the deadlines")
	}
}
