package ledger

import (
	"os"
	"strings"
	"testing"
	"time"
)

var recNow = time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)

func fakeRecorder(round int) (*Recorder, *[]string) {
	var warns []string
	return &Recorder{
		Round: func(string) int { return round },
		Resolve: func(_ string, e Entry) (Stamp, bool) {
			if e.Issue == "12" {
				return Stamp{Slot: "ben", Harness: "claude", Account: "work"}, true
			}
			return Stamp{}, false
		},
		Now:  func() time.Time { return recNow },
		Warn: func(m string) { warns = append(warns, m) },
	}, &warns
}

func TestRecorderStampsRoundClockAndSlot(t *testing.T) {
	dir := t.TempDir()
	r, _ := fakeRecorder(7)
	r.Note(dir, Entry{Kind: KindBounce, Issue: "12"})
	r.Note(dir, Entry{Kind: KindGate, Issue: "12", Slot: "kit", Round: 3, TS: recNow.Add(time.Hour)})
	r.Note(dir, Entry{Kind: KindBounce, Issue: "99"})
	es, err := Load(dir)
	if err != nil || len(es) != 3 {
		t.Fatalf("%v %+v", err, es)
	}
	if e := es[0]; e.Round != 7 || !e.TS.Equal(recNow) || e.Slot != "ben" || e.Harness != "claude" || e.Account != "work" {
		t.Errorf("filled entry %+v", e)
	}
	if e := es[1]; e.Round != 3 || !e.TS.Equal(recNow.Add(time.Hour)) || e.Slot != "kit" || e.Account != "work" {
		t.Errorf("named entry %+v", e)
	}
	if e := es[2]; e.Slot != "" || e.Account != "" || e.Round != 7 {
		t.Errorf("unresolved entry %+v", e)
	}
}

func TestRecorderNoteWarnsAndAppendErrReturns(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(Path(dir), 0o755); err != nil { // a directory where the file belongs
		t.Fatal(err)
	}
	r, warns := fakeRecorder(1)
	r.Note(dir, Entry{Kind: KindBounce, Issue: "12"})
	if len(*warns) != 1 || !strings.Contains((*warns)[0], Path(dir)) {
		t.Errorf("warns = %q", *warns)
	}
	if err := r.AppendErr(dir, Entry{Kind: KindBounce, Issue: "12"}); err == nil {
		t.Error("AppendErr hid the failure")
	}
	if len(*warns) != 1 {
		t.Errorf("AppendErr warned: %q", *warns)
	}
}

func TestRecorderWithoutDependenciesStillWritesWhenStamped(t *testing.T) {
	dir := t.TempDir()
	(&Recorder{}).Note(dir, Entry{TS: recNow, Kind: KindPark})
	if es, _ := Load(dir); len(es) != 1 {
		t.Fatalf("entries %+v", es)
	}
}
