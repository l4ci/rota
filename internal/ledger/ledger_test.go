package ledger

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/fsio"
)

func TestAppendLoadRoundTrip(t *testing.T) {
	root := t.TempDir()
	ts := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	hr := 62.5
	in := Entry{TS: ts, Kind: KindAssign, Round: 3, Issue: "574", Slot: "ben", Account: "work", Harness: "claude", Detail: Detail("headroom", hr, "unset", nil)}
	if err := Append(root, in); err != nil {
		t.Fatal(err)
	}
	got, err := Load(root)
	if err != nil || len(got) != 1 {
		t.Fatalf("Load = %v, %v", got, err)
	}
	e := got[0]
	if !e.TS.Equal(ts) || e.Kind != KindAssign || e.Round != 3 || e.Issue != "574" || e.Slot != "ben" || e.Account != "work" || e.Harness != "claude" || e.PR != "" {
		t.Errorf("entry = %+v", e)
	}
	if f, ok := e.DetailFloat("headroom"); !ok || f != hr {
		t.Errorf("headroom = %v, %v", f, ok)
	}
	if _, ok := e.DetailFloat("unset"); ok {
		t.Error("a nil detail value must be absent, not 0")
	}
}

func TestLoadMissing(t *testing.T) {
	got, err := Load(t.TempDir())
	if err != nil || got != nil {
		t.Fatalf("Load = %v, %v", got, err)
	}
}

func TestLoadSkipsTornLine(t *testing.T) {
	root := t.TempDir()
	Append(root, Entry{Kind: KindDone, Issue: "1"})
	f, _ := os.OpenFile(Path(root), os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("{\"kind\":\n")
	f.Close()
	Append(root, Entry{Kind: KindDone, Issue: "2"})
	got, _ := Load(root)
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
}

// Append writes under the file's sidecar lock: while another holder has it,
// nothing is written, and the line lands once it is released.
func TestAppendWaitsForTheLock(t *testing.T) {
	root := t.TempDir()
	done := make(chan error, 1)
	err := fsio.Locked(Path(root), fsio.LockTimeout, func() error {
		go func() { done <- Append(root, Entry{Kind: KindGate, Issue: "1"}) }()
		select {
		case err := <-done:
			t.Errorf("Append returned while the lock was held: %v", err)
		case <-time.After(300 * time.Millisecond):
		}
		if data, _ := os.ReadFile(Path(root)); len(data) != 0 {
			t.Errorf("a line was written under another holder's lock: %q", data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Append did not finish after the lock was released")
	}
	if got, err := Load(root); err != nil || len(got) != 1 {
		t.Fatalf("Load = %v, %v", got, err)
	}
}

// A few concurrent appends all land whole.
func TestAppendConcurrentKeepsLinesWhole(t *testing.T) {
	root := t.TempDir()
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := Append(root, Entry{Kind: KindGate, Issue: fmt.Sprint(g), Detail: Detail("pad", string(bytes.Repeat([]byte("x"), 3000)))}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got, err := Load(root); err != nil || len(got) != 4 {
		t.Fatalf("Load = %d entries, %v", len(got), err)
	}
}

// A line longer than any scanner buffer does not lose the rest of the log.
func TestLoadReadsOverlongLine(t *testing.T) {
	root := t.TempDir()
	Append(root, Entry{Kind: KindDone, Issue: "1"})
	Append(root, Entry{Kind: KindGate, Issue: "2", Detail: Detail("pad", string(bytes.Repeat([]byte("x"), 2<<20)))})
	Append(root, Entry{Kind: KindDone, Issue: "3"})
	got, err := Load(root)
	if err != nil || len(got) != 3 {
		t.Fatalf("Load = %d entries, %v", len(got), err)
	}
}

func TestTrimDropsRoundsOlderThanKeep(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".rota"), 0o755)
	for _, r := range []int{0, 1, 2, 3, 4, 5} {
		if err := Append(root, Entry{Kind: KindAssign, Round: r, Issue: "12"}); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := Trim(root, 5, 0); err != nil || n != 0 { // 0 keeps everything
		t.Fatalf("keep 0: dropped %d, %v", n, err)
	}
	n, err := Trim(root, 5, 2) // rounds 4 and 5 stay, and the unrounded 0
	if err != nil || n != 3 {
		t.Fatalf("dropped %d, %v, want 3", n, err)
	}
	es, _ := Load(root)
	var rounds []int
	for _, e := range es {
		rounds = append(rounds, e.Round)
	}
	if fmt.Sprint(rounds) != "[0 4 5]" {
		t.Errorf("rounds left %v, want [0 4 5]", rounds)
	}
	if n, err := Trim(root, 5, 2); err != nil || n != 0 {
		t.Errorf("second trim dropped %d, %v", n, err)
	}
}
