package receipt

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s := New(t.TempDir())
	s.Now = func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
	return s
}

func call(id string) Call { return Call{ID: id, Verb: "round assign", Fingerprint: "fp"} }

func ran(e Entry) func(Entry) (Entry, bool) { return func(Entry) (Entry, bool) { return e, true } }

func TestDoRecordsOnceAndReplays(t *testing.T) {
	s := testStore(t)
	runs := 0
	run := func(Entry) (Entry, bool) { runs++; return Entry{Status: Done, Text: "ok"}, true }
	first, err := s.Do(call("r1"), run)
	if err != nil || first.Replayed || first.RecordErr != nil || runs != 1 {
		t.Fatalf("first: %+v err=%v runs=%d", first, err, runs)
	}
	if first.Entry.At != "2026-01-02T03:04:05Z" || first.Entry.ID != "r1" || first.Entry.Attempts != 1 {
		t.Errorf("the clock and identity are stamped: %+v", first.Entry)
	}
	again, err := s.Do(call("r1"), run)
	if err != nil || !again.Replayed || again.Entry.Text != "ok" || runs != 1 {
		t.Errorf("retry: %+v err=%v runs=%d", again, err, runs)
	}
}

func TestDoRetriesAFailedCallWithItsPriorAndCountsAttempts(t *testing.T) {
	s := testStore(t)
	s.Do(call("r1"), ran(Entry{Status: Failed, FailedStage: "claim", Residual: []string{"branch"}}))
	var prior Entry
	out, err := s.Do(call("r1"), func(p Entry) (Entry, bool) { prior = p; return Entry{Status: Done, Text: "ok"}, true })
	if err != nil || out.Replayed {
		t.Fatalf("a failed receipt must run again: %+v err=%v", out, err)
	}
	if prior.FailedStage != "claim" {
		t.Errorf("run sees the failed attempt: %+v", prior)
	}
	if e, found, _ := s.Lookup(call("r1")); !found || e.Status != Done || e.Attempts != 2 || e.FailedStage != "" {
		t.Errorf("got %+v found=%v", e, found)
	}
}

func TestDoRefusesAnotherCallUnderTheSameID(t *testing.T) {
	s := testStore(t)
	s.Do(call("r1"), ran(Entry{Status: Done}))
	var mm *Mismatch
	for name, c := range map[string]Call{
		"other arguments": {ID: "r1", Verb: "round assign", Fingerprint: "other"},
		"other verb":      {ID: "r1", Verb: "round pick", Fingerprint: "fp"},
	} {
		runs := 0
		_, err := s.Do(c, func(Entry) (Entry, bool) { runs++; return Entry{}, true })
		if !errors.As(err, &mm) || runs != 0 {
			t.Errorf("%s: err=%v runs=%d", name, err, runs)
		}
	}
}

func TestDoLeavesNoReceiptForACallThatWroteNothing(t *testing.T) {
	s := testStore(t)
	out, err := s.Do(call("r1"), func(Entry) (Entry, bool) { return Entry{}, false })
	if err != nil || out.Replayed {
		t.Fatalf("%+v err=%v", out, err)
	}
	if _, found, _ := s.Lookup(call("r1")); found {
		t.Error("nothing was written, so nothing is recorded")
	}
}

func TestDoRefusesAnUnreadableFileWithoutRunning(t *testing.T) {
	s := testStore(t)
	os.MkdirAll(filepath.Join(s.Root, ".rota"), 0o755)
	os.WriteFile(filepath.Join(s.Root, File), []byte("{nope"), 0o644)
	runs := 0
	if _, err := s.Do(call("r1"), func(Entry) (Entry, bool) { runs++; return Entry{}, true }); err == nil || runs != 0 {
		t.Errorf("a corrupt receipts file must not read as empty: err=%v runs=%d", err, runs)
	}
}

func TestConcurrentCallsUnderOneIDRunTheMutationOnce(t *testing.T) {
	s := testStore(t)
	var runs, replays atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := s.Do(call("same"), func(Entry) (Entry, bool) {
				runs.Add(1)
				time.Sleep(50 * time.Millisecond) // long enough for the others to arrive
				return Entry{Status: Done, Text: "ok"}, true
			})
			if err != nil {
				t.Error(err)
			}
			if out.Replayed {
				replays.Add(1)
			}
		}()
	}
	wg.Wait()
	if runs.Load() != 1 || replays.Load() != 7 {
		t.Errorf("runs=%d replays=%d, want 1 and 7", runs.Load(), replays.Load())
	}
}

func TestDifferentIDsDoNotWaitOnEachOther(t *testing.T) {
	s := testStore(t)
	release := make(chan struct{})
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		s.Do(call("slow"), func(Entry) (Entry, bool) { close(started); <-release; return Entry{Status: Done}, true })
		close(done)
	}()
	<-started
	if _, err := s.Do(call("fast"), ran(Entry{Status: Done})); err != nil {
		t.Fatal(err)
	}
	close(release)
	<-done
}

func TestPruneDropsTheFileAndTheClaims(t *testing.T) {
	s := testStore(t)
	s.Do(call("r1"), ran(Entry{Status: Done}))
	if err := Prune(s.Root); err != nil {
		t.Fatal(err)
	}
	if left, _ := filepath.Glob(filepath.Join(s.Root, File+"*")); len(left) != 1 || filepath.Base(left[0]) != "receipts.json.lock" {
		t.Errorf("only the file's own lock may survive the prune: %v", left)
	}
	if err := Prune(s.Root); err != nil {
		t.Errorf("pruning nothing: %v", err)
	}
}

func TestRecordKeepsTheNewestPerVerb(t *testing.T) {
	s := testStore(t)
	gate := func(i int) Call { return Call{ID: fmt.Sprintf("g%d", i), Verb: "worker gate", Fingerprint: "fp"} }
	for i := 0; i < KeepPerVerb+5; i++ {
		s.Do(gate(i), ran(Entry{Status: Done}))
	}
	s.Do(Call{ID: "a0", Verb: "round assign", Fingerprint: "fp"}, ran(Entry{Status: Done}))
	if _, found, _ := s.Lookup(gate(0)); found {
		t.Error("the oldest gate receipt must be dropped")
	}
	if _, found, _ := s.Lookup(gate(5)); !found {
		t.Error("the newest KeepPerVerb gate receipts stay")
	}
	if _, found, _ := s.Lookup(call("a0")); !found {
		t.Error("gate ticks must not push out another verb's receipt")
	}
}

func TestShowFindsAnEntryByIDAlone(t *testing.T) {
	s := testStore(t)
	s.Do(call("r1"), ran(Entry{Status: Done, Text: "ok"}))
	s.Do(Call{ID: "r2", Verb: "worker gate", Fingerprint: "other"}, ran(Entry{Status: Failed, FailedStage: "merge"}))
	for id, want := range map[string]string{"r1": Done, "r2": Failed} {
		e, found, err := s.Show(id)
		if err != nil || !found || e.ID != id || e.Status != want {
			t.Errorf("%s: %+v found=%v err=%v", id, e, found, err)
		}
	}
	if e, found, err := s.Show("nope"); err != nil || found {
		t.Errorf("an unused id is absent: %+v found=%v err=%v", e, found, err)
	}
}

func TestShowRefusesACorruptFile(t *testing.T) {
	s := testStore(t)
	os.MkdirAll(filepath.Dir(path(s.Root)), 0o777)
	os.WriteFile(path(s.Root), []byte("{not json"), 0o666)
	if _, _, err := s.Show("r1"); err == nil {
		t.Error("a corrupt receipts file must be an error, not absent")
	}
}
