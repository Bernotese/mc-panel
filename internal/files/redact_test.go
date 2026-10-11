package files

import "testing"

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
