package files

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// lowerWalkBudget sets the walk budget for one test.
func lowerWalkBudget(t *testing.T, names, nameBytes int64) {
	t.Helper()
	oldN, oldB := walkMaxNames, walkMaxNameBytes
	walkMaxNames, walkMaxNameBytes = names, nameBytes
	t.Cleanup(func() { walkMaxNames, walkMaxNameBytes = oldN, oldB })
}

// SEC-23: a walk that reads more names (or name bytes) than the budget ends with ErrTooLarge
// instead of collecting them all.
func TestWalkTreeBudget(t *testing.T) {
	s, data := newService(t)
	for i := range 30 {
		put(t, data, fmt.Sprintf("d%d/f%d", i%3, i), "x")
	}
	root, err := s.open(1)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	count := func() (int, error) {
		n := 0
		err := walkTree(root, "", func(string, fs.DirEntry, error) error { n++; return nil })
		return n, err
	}
	if n, err := count(); err != nil || n != 33 {
		t.Fatalf("within the budget: %d entries, %v", n, err)
	}
	lowerWalkBudget(t, 20, 1<<20)
	if _, err := count(); !errors.Is(err, filesapi.ErrTooLarge) {
		t.Fatalf("names over the budget: %v", err)
	}
	lowerWalkBudget(t, 1000, 40)
	if _, err := count(); !errors.Is(err, filesapi.ErrTooLarge) {
		t.Fatalf("name bytes over the budget: %v", err)
	}
}

// More leftovers than one batch are all removed (per batch, not collected first); an instance
// over the budget is skipped with a warning, and the next one is still cleaned.
func TestCleanupTempBatchesAndBudget(t *testing.T) {
	s, data := newService(t)
	for i := range 2*walkBatch + 7 {
		put(t, data, fmt.Sprintf("w/%s-%d", deadTmp, i), "partial")
	}
	put(t, data, "w/level.dat", "keep")
	if n := s.CleanupTemp(context.Background()); n != 2*walkBatch+7 {
		t.Fatalf("removed %d leftovers, want %d", n, 2*walkBatch+7)
	}
	noTemp(t, data)
	if v, ok := get(t, data, "w/level.dat"); !ok || v != "keep" {
		t.Fatal("cleanup touched a normal file")
	}

	// Server 1 holds more names than the budget; server 2 has one leftover.
	for i := range 40 {
		put(t, data, fmt.Sprintf("many/f%d", i), "x")
	}
	data2 := filepath.Join(filepath.Dir(data), "2")
	put(t, data2, deadTmp+"-x", "partial")
	lowerWalkBudget(t, 30, 1<<20)
	s.CleanupTemp(context.Background())
	if _, err := os.Stat(filepath.Join(data2, deadTmp+"-x")); !os.IsNotExist(err) {
		t.Fatalf("the instance after one over the budget was not cleaned: %v", err)
	}
}

// unlockTree and visibleNames read in batches against the same budget.
func TestUnlockAndVisibleNamesBudget(t *testing.T) {
	s, data := newService(t)
	for i := range 25 {
		put(t, data, fmt.Sprintf("t/sub%d/f", i), "x")
	}
	root, err := s.open(1)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := unlockTree(root, "t"); err != nil {
		t.Fatalf("within the budget: %v", err)
	}
	if names, err := visibleNames(root, "t"); err != nil || len(names) != 25 {
		t.Fatalf("within the budget: %d names, %v", len(names), err)
	}
	lowerWalkBudget(t, 20, 1<<20)
	if err := unlockTree(root, "t"); !errors.Is(err, filesapi.ErrTooLarge) {
		t.Fatalf("unlockTree over the budget: %v", err)
	}
	if _, err := visibleNames(root, "t"); !errors.Is(err, filesapi.ErrTooLarge) {
		t.Fatalf("visibleNames over the budget: %v", err)
	}
}
