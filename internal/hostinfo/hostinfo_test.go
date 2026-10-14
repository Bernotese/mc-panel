package hostinfo

import "testing"

func TestParseMeminfo(t *testing.T) {
	total, avail, ok := ParseMeminfo([]byte("MemTotal:        8000000 kB\nMemFree: 1 kB\nMemAvailable:    6000000 kB\n"))
	if !ok || total != 8000000*1024 || avail != 6000000*1024 {
		t.Fatalf("got %d/%d/%v", total, avail, ok)
	}
	for _, in := range []string{"", "MemTotal: lots\nMemAvailable: 1 kB\n", "MemTotal: 5 kB\n"} {
		if _, _, ok := ParseMeminfo([]byte(in)); ok {
			t.Fatalf("%q must not parse", in)
		}
	}
}

func TestMemTotal(t *testing.T) {
	got, err := memTotal([]byte("MemTotal:       16384000 kB\nMemFree: 1 kB\n"))
	if err != nil || got != 16384000*1024 {
		t.Fatalf("got %d, %v", got, err)
	}
	for _, in := range []string{"", "MemTotal: lots\n", "MemTotal: 0 kB\n", "MemFree: 1 kB\n"} {
		if _, err := memTotal([]byte(in)); err == nil {
			t.Fatalf("%q must fail", in)
		}
	}
}
