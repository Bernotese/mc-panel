package serverfiles

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// Review findings M1 and round 2: after EOF and after the upload is answered, reads never
// touch the connection's deadlines.
func TestDeadlineReaderAfterFinish(t *testing.T) {
	var set int
	d := &deadlineReader{r: strings.NewReader("ab"), dl: func() error { set++; return nil }}
	if _, err := io.ReadAll(d); err != nil {
		t.Fatal(err)
	}
	if set == 0 {
		t.Fatal("no read deadline was set")
	}
	// the Transport's extra read after the announced length must not arm a deadline again
	before := set
	if n, err := d.Read(make([]byte, 1)); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("read after EOF = %d, %v", n, err)
	}
	if set != before {
		t.Fatal("a read after EOF set a read deadline")
	}
	d.finish()
	if n, err := d.Read(make([]byte, 1)); n != 0 || !errors.Is(err, errUploadDone) {
		t.Fatalf("read after finish = %d, %v", n, err)
	}
	if set != before {
		t.Fatal("a read after finish set a read deadline")
	}
}
