package files

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/Bernotese/mc-panel/internal/config"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// lowerWalkBudget sets the walk budget for one test (without the margin over the configured
// entry limits and the backup name bytes).
func lowerWalkBudget(t *testing.T, names, nameBytes int64) {
	t.Helper()
	oldN, oldB, oldM, oldS := walkMaxNames, walkMaxNameBytes, walkMargin, maxBackupNameBytes
	walkMaxNames, walkMaxNameBytes, walkMargin, maxBackupNameBytes = names, nameBytes, 0, 1
	t.Cleanup(func() { walkMaxNames, walkMaxNameBytes, walkMargin, maxBackupNameBytes = oldN, oldB, oldM, oldS })
}

// smallEntries are limits whose entry counts do not raise the walk budget.
func smallEntries(c *Config) { c.Limits.MaxBackupEntries, c.Limits.MaxExtractEntries = 1, 1 }

// SEC-23: a walk that reads more names (or name bytes) than the budget ends with ErrTooLarge
// instead of collecting them all; raised entry limits raise the budget.
func TestWalkTreeBudget(t *testing.T) {
	s, data := newService(t, smallEntries)
	for i := range 30 {
		put(t, data, fmt.Sprintf("d%d/f%d", i%3, i), "x")
	}
	root, err := s.open(1)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	count := func(lim config.FilesLimits) (int, error) {
		n := 0
		err := walkTree(root, "", newWalkBudget(lim), func(string, fs.DirEntry, error) error { n++; return nil })
		return n, err
	}
	if n, err := count(s.cfg.Limits); err != nil || n != 33 {
		t.Fatalf("within the budget: %d entries, %v", n, err)
	}
	lowerWalkBudget(t, 20, 1<<20)
	if _, err := count(s.cfg.Limits); !errors.Is(err, filesapi.ErrTooLarge) {
		t.Fatalf("names over the budget: %v", err)
	}
	raised := s.cfg.Limits
	raised.MaxBackupEntries = 20 // twice that is 40
	if n, err := count(raised); err != nil || n != 33 {
		t.Fatalf("budget raised by max_backup_entries: %d entries, %v", n, err)
	}
	raised = s.cfg.Limits
	raised.MaxExtractEntries = 20
	if n, err := count(raised); err != nil || n != 33 {
		t.Fatalf("budget raised by max_extract_entries: %d entries, %v", n, err)
	}
	lowerWalkBudget(t, 1000, 40)
	if _, err := count(s.cfg.Limits); !errors.Is(err, filesapi.ErrTooLarge) {
		t.Fatalf("name bytes over the budget: %v", err)
	}
}

// More leftovers than one batch are all removed (per batch, not collected first); an instance
// over the budget is skipped with a warning, and the next one is still cleaned.
func TestCleanupTempBatchesAndBudget(t *testing.T) {
	s, data := newService(t, smallEntries)
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

// The temp folder of a restore with a raised max_backup_entries is never left behind: the
// data directory walk fits the raised budget, and unlockTree counts only folders.
func TestCleanupRemovesLargeRestoreTemp(t *testing.T) {
	s, data := newService(t, func(c *Config) { c.Limits.MaxBackupEntries, c.Limits.MaxExtractEntries = 40, 1 })
	for i := range 60 { // old/ and new/ of a restore, more files than walkMaxNames
		put(t, data, fmt.Sprintf("%s/old/world/f%d", deadTmp, i), "x")
		put(t, data, fmt.Sprintf("%s/new/world/f%d", deadTmp, i), "x")
	}
	for i := range 50 { // the data directory itself, more names than walkMaxNames
		put(t, data, fmt.Sprintf("world/f%d", i), "x")
	}
	makeReadOnly(t, data, deadTmp+"/old/world", deadTmp+"/new/world")
	lowerWalkBudget(t, 20, 1<<20)
	if n := s.CleanupTemp(context.Background()); n != 1 {
		t.Fatalf("removed %d leftovers, want the restore temp folder", n)
	}
	noTemp(t, data)
}

// unlockTree counts only folders; visibleNames counts every name.
func TestUnlockAndVisibleNamesBudget(t *testing.T) {
	s, data := newService(t, smallEntries)
	for i := range 25 {
		put(t, data, fmt.Sprintf("t/sub%d/f", i), "x")
	}
	for i := range 40 {
		put(t, data, fmt.Sprintf("files/f%d", i), "x")
	}
	root, err := s.open(1)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := unlockTree(root, "t", s.cfg.Limits); err != nil {
		t.Fatalf("within the budget: %v", err)
	}
	if names, err := visibleNames(root, "t", s.cfg.Limits); err != nil || len(names) != 25 {
		t.Fatalf("within the budget: %d names, %v", len(names), err)
	}
	lowerWalkBudget(t, 20, 1<<20)
	if err := unlockTree(root, "t", s.cfg.Limits); !errors.Is(err, filesapi.ErrTooLarge) {
		t.Fatalf("unlockTree with more folders than the budget: %v", err)
	}
	if err := unlockTree(root, "files", s.cfg.Limits); err != nil {
		t.Fatalf("unlockTree with many files but no folders: %v", err)
	}
	if _, err := visibleNames(root, "t", s.cfg.Limits); !errors.Is(err, filesapi.ErrTooLarge) {
		t.Fatalf("visibleNames over the budget: %v", err)
	}
}
