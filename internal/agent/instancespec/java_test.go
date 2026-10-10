package instancespec_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

func TestJavaForSharedCases(t *testing.T) {
	raw, err := os.ReadFile("testdata/java_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Type    instancespec.Type `json:"type"`
		Version string            `json:"version"`
		Java    instancespec.Java `json:"java"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 10 {
		t.Fatalf("only %d cases", len(cases))
	}
	for _, c := range cases {
		if got := instancespec.JavaFor(c.Type, c.Version); got != c.Java {
			t.Errorf("JavaFor(%s, %q) = %s, want %s", c.Type, c.Version, got, c.Java)
		}
	}
}
