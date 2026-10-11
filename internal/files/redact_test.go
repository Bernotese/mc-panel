package files

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRedactProperties(t *testing.T) {
	in := "motd=Hi\nrcon.password=s3cret\r\n  rcon.password : other\nrcon.password\tspaced\nrcon.passwordless=keep\nrcon.port=25575\nrcon.password=\n"
	want := "motd=Hi\nrcon.password=********\r\n  rcon.password : ********\nrcon.password\t********\nrcon.passwordless=keep\nrcon.port=25575\nrcon.password=\n"
	if got := redactProperties(in); got != want {
		t.Fatalf("redact:\n got %q\nwant %q", got, want)
	}
}

func TestRestorePassword(t *testing.T) {
	current := "rcon.password=s3cret\nmotd=old\n"
	next := "rcon.password=********\nmotd=new\n"
	if got := restorePassword(next, current); got != "rcon.password=s3cret\nmotd=new\n" {
		t.Fatalf("restore: %q", got)
	}
	// A deliberately changed value is kept.
	if got := restorePassword("rcon.password=mine\n", current); got != "rcon.password=mine\n" {
		t.Fatalf("changed value: %q", got)
	}
	// No password in the file: the placeholder line is dropped instead of setting "********".
	if got := restorePassword("rcon.password=********\nmotd=x\n", "motd=x\n"); got != "motd=x\n" {
		t.Fatalf("no current password: %q", got)
	}
	// The last definition wins, as in java.util.Properties.
	if got := restorePassword("rcon.password : ********", "rcon.password=a\nrcon.password=b\n"); got != "rcon.password : b" {
		t.Fatalf("last definition: %q", got)
	}
}

// java.util.Properties continues a logical line when a natural line ends in an odd number of
// backslashes; lines end with \n, \r\n or \r; comment lines never continue.
func TestRestorePasswordRespectsLineContinuation(t *testing.T) {
	current := "rcon.password=s3cret\n"
	for _, c := range []struct{ name, next, want string }{
		// The placeholder is part of the motd value: it must never become the password.
		{"odd backslashes", "motd=hi \\\nrcon.password=********\n", "motd=hi \\\nrcon.password=********\n"},
		{"three backslashes", "motd=hi \\\\\\\nrcon.password=********\n", "motd=hi \\\\\\\nrcon.password=********\n"},
		{"continued twice", "motd=a\\\n b\\\nrcon.password=********\n", "motd=a\\\n b\\\nrcon.password=********\n"},
		{"cr terminator", "motd=hi \\\rrcon.password=********\r", "motd=hi \\\rrcon.password=********\r"},
		// An escaped backslash does not continue the line: the placeholder is a real key.
		{"even backslashes", "motd=hi \\\\\nrcon.password=********\n", "motd=hi \\\\\nrcon.password=s3cret\n"},
		// A comment line never continues, whatever it ends with.
		{"comment", "# note \\\nrcon.password=********\n", "# note \\\nrcon.password=s3cret\n"},
		{"bang comment", "  ! note \\\nrcon.password=********\n", "  ! note \\\nrcon.password=s3cret\n"},
	} {
		if got := restorePassword(c.next, current); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

func TestRedactMultiLineAndCRPasswords(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		// The whole logical value is hidden, also its continuation lines.
		{"continued value", "rcon.password=ab\\\n   cd\r\nmotd=x\n", "rcon.password=********\r\nmotd=x\n"},
		{"cr terminators", "motd=x\rrcon.password=s3cret\rlevel=w\r", "motd=x\rrcon.password=********\rlevel=w\r"},
		// A password-like continuation line is hidden too (it keeps its own continuation).
		{"inside another value", "motd=a \\\nrcon.password=s3cret\n", "motd=a \\\nrcon.password=********\n"},
		{"inside, continued", "motd=a \\\nrcon.password=s3\\\n cret\n", "motd=a \\\nrcon.password=********\\\n cret\n"},
	} {
		if got := redactProperties(c.in); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
	// Saving the redacted view of a continued password restores the original text.
	orig := "rcon.password=ab\\\n   cd\r\nmotd=x\n"
	if got := restorePassword(redactProperties(orig), orig); got != orig {
		t.Fatalf("round trip of a continued password:\n got %q\nwant %q", got, orig)
	}
}

// A value continued over millions of lines must not cost quadratic time, and many short lines
// must not cost memory per line: the editor limit allows 5 MiB of such text.
func TestRedactIsLinearOnHugeFiles(t *testing.T) {
	cont := "rcon.password=a\\\n" + strings.Repeat("\\\n", (5<<20)/2)
	blank := strings.Repeat("\n", 5<<20)
	for name, in := range map[string]string{"continuation lines": cont, "empty lines": blank} {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		start := time.Now()
		red := redactProperties(in)
		_ = restorePassword(red, in)
		_, _ = currentPassword(in)
		took := time.Since(start)
		runtime.ReadMemStats(&after)
		if took > 30*time.Second { // linear: ~1 s (5 s with -race); quadratic: far beyond 90 s
			t.Errorf("%s: %s for 5 MiB", name, took)
		}
		if grew := after.TotalAlloc - before.TotalAlloc; grew > 64<<20 {
			t.Errorf("%s: allocated %d MiB for 5 MiB", name, grew>>20)
		}
	}
	if got := redactProperties(cont); got != "rcon.password=********\n" {
		t.Fatalf("continued value not redacted as one: %q...", got[:min(len(got), 40)])
	}
	if got := restorePassword("rcon.password=********\n", cont); got != cont {
		t.Fatal("continued value not restored exactly")
	}
}
