package receipt

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestRecordLookupAndAttempts(t *testing.T) {
	root := t.TempDir()
	if _, found, err := Lookup(root, "r1", "round assign", "fp"); found || err != nil {
		t.Fatalf("empty store: found=%v err=%v", found, err)
	}
	if err := Record(root, Entry{ID: "r1", Verb: "round assign", Fingerprint: "fp", Status: Failed, FailedStage: "claim", Residual: []string{"branch"}}); err != nil {
		t.Fatal(err)
	}
	if err := Record(root, Entry{ID: "r1", Verb: "round assign", Fingerprint: "fp", Status: Done, Text: "ok"}); err != nil {
		t.Fatal(err)
	}
	e, found, err := Lookup(root, "r1", "round assign", "fp")
	if err != nil || !found || e.Status != Done || e.Attempts != 2 || e.Text != "ok" || e.FailedStage != "" {
		t.Errorf("got %+v found=%v err=%v", e, found, err)
	}
}

func TestLookupRefusesAnotherCall(t *testing.T) {
	root := t.TempDir()
	Record(root, Entry{ID: "r1", Verb: "round assign", Fingerprint: "fp", Status: Done})
	var mm *Mismatch
	if _, _, err := Lookup(root, "r1", "round assign", "other"); !errors.As(err, &mm) {
		t.Errorf("other arguments: %v", err)
	}
	if _, _, err := Lookup(root, "r1", "round pick", "fp"); !errors.As(err, &mm) {
		t.Errorf("other verb: %v", err)
	}
}

func TestPruneDropsTheFile(t *testing.T) {
	root := t.TempDir()
	Record(root, Entry{ID: "r1", Verb: "v", Fingerprint: "fp", Status: Done})
	if err := Prune(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, File)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("receipts survive the prune: %v", err)
	}
	if err := Prune(root); err != nil {
		t.Errorf("pruning nothing: %v", err)
	}
}

func TestUnreadableFileIsAnError(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".rota"), 0o755)
	os.WriteFile(filepath.Join(root, File), []byte("{nope"), 0o644)
	if _, _, err := Lookup(root, "r1", "v", "fp"); err == nil {
		t.Error("a corrupt receipts file must not read as empty")
	}
}

func TestRecordKeepsTheNewestPerVerb(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < KeepPerVerb+5; i++ {
		Record(root, Entry{ID: fmt.Sprintf("g%d", i), Verb: "worker gate", Fingerprint: "fp", Status: Done})
	}
	Record(root, Entry{ID: "a0", Verb: "round assign", Fingerprint: "fp", Status: Done})
	if _, found, _ := Lookup(root, "g0", "worker gate", "fp"); found {
		t.Error("the oldest gate receipt must be dropped")
	}
	if _, found, _ := Lookup(root, "g5", "worker gate", "fp"); !found {
		t.Error("the newest KeepPerVerb gate receipts stay")
	}
	if _, found, _ := Lookup(root, "a0", "round assign", "fp"); !found {
		t.Error("gate ticks must not push out another verb's receipt")
	}
}
