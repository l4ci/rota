package ledger

import (
	"bytes"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
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

func TestAppendConcurrent(t *testing.T) {
	root := t.TempDir()
	var wg sync.WaitGroup
	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 5; i++ {
				if err := Append(root, Entry{Kind: KindGate, Issue: fmt.Sprint(g), Detail: Detail("pad", string(bytes.Repeat([]byte("x"), 3000)))}); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	data, _ := os.ReadFile(Path(root))
	if n := bytes.Count(data, []byte("\n")); n != 50 {
		t.Fatalf("%d lines, want 50", n)
	}
	got, err := Load(root)
	if err != nil || len(got) != 50 {
		t.Fatalf("Load = %d entries, %v: a line was torn", len(got), err)
	}
}
