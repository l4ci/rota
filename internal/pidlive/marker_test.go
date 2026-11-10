package pidlive

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

type rec struct {
	PID   int    `json:"pid"`
	Start uint64 `json:"start,omitempty"`
	Note  string `json:"note"`
}

func (r rec) OwnerPID() int { return r.PID }

func table(self int, start uint64) fakeTable {
	return fakeTable{rows: map[int]Row{self: {Start: start, State: "S"}}}
}

func jsonMarker(t *testing.T) Marker[rec] {
	return Marker[rec]{Path: filepath.Join(t.TempDir(), "m.json"), Codec: JSON[rec]()}
}

// self is a real pid so kill -0 finds it; the table decides the rest.
var self = os.Getpid()

func TestMarkerWriteReadLive(t *testing.T) {
	m := jsonMarker(t)
	if _, ok := m.Read(); ok {
		t.Fatal("missing file read as a record")
	}
	if err := m.Write(rec{PID: self, Start: 7, Note: "x"}, nil); err != nil {
		t.Fatal(err)
	}
	live := func(r rec) bool { return Running(table(self, 7), r.PID, r.Start) }
	if r, ok := m.ReadLive(live); !ok || r.Note != "x" {
		t.Fatalf("live record = %+v, %v", r, ok)
	}
	reused := func(r rec) bool { return Running(table(self, 8), r.PID, r.Start) }
	if _, ok := m.ReadLive(reused); ok {
		t.Error("reused pid read as live")
	}
	if _, ok := m.Read(); !ok {
		t.Error("Read must not judge liveness")
	}
}

func TestMarkerReadRejectsUnparsableAndPidless(t *testing.T) {
	m := jsonMarker(t)
	for _, body := range []string{"not json", `{"pid":0}`, ""} {
		if err := os.WriteFile(m.Path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, ok := m.Read(); ok {
			t.Errorf("body %q read as a record", body)
		}
	}
}

func TestMarkerRemoveOnlyIfMine(t *testing.T) {
	m := jsonMarker(t)
	if err := m.Remove(self); err != nil {
		t.Fatalf("remove of a missing marker: %v", err)
	}
	if err := m.Write(rec{PID: self + 1}, nil); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(self); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Read(); !ok {
		t.Fatal("removed a successor's marker")
	}
	if err := m.Remove(self + 1); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Read(); ok {
		t.Error("own marker survived Remove")
	}
}

func TestMarkerClaim(t *testing.T) {
	m := jsonMarker(t)
	alive := func(rec) bool { return true }
	dead := func(rec) bool { return false }
	if _, held, err := m.Claim(rec{PID: self}, alive); held || err != nil {
		t.Fatalf("first claim: held=%v err=%v", held, err)
	}
	if _, held, _ := m.Claim(rec{PID: self}, alive); held {
		t.Error("a process held against itself")
	}
	other, held, err := m.Claim(rec{PID: self + 1, Note: "late"}, alive)
	if !held || err != nil || other.PID != self {
		t.Fatalf("live holder: other=%+v held=%v err=%v", other, held, err)
	}
	if r, _ := m.Read(); r.Note == "late" {
		t.Error("a held claim still wrote")
	}
	if _, held, _ := m.Claim(rec{PID: self + 1, Note: "late"}, dead); held {
		t.Error("a dead holder blocked the claim")
	}
	if r, _ := m.Read(); r.Note != "late" {
		t.Error("claim over a dead marker did not write")
	}
}

func TestMarkerWriteGuardErrorAborts(t *testing.T) {
	m := jsonMarker(t)
	if err := m.Write(rec{PID: self}, func() error { return errors.New("no") }); err == nil {
		t.Fatal("guard error swallowed")
	}
	if _, ok := m.Read(); ok {
		t.Error("write happened despite guard error")
	}
}

func TestTextCodec(t *testing.T) {
	c := Text()
	b, _ := c.Encode(StampOf(table(self, 42), self))
	if string(b) != strconv.Itoa(self)+" 42\n" {
		t.Errorf("encode with start = %q", b)
	}
	if b, _ := c.Encode(Stamp{PID: 5}); string(b) != "5\n" {
		t.Errorf("encode without start = %q", b)
	}
	for body, want := range map[string]Stamp{"9\n": {PID: 9}, "9 12\n": {PID: 9, Start: 12}} {
		if got, ok := c.Decode([]byte(body)); !ok || got != want {
			t.Errorf("decode %q = %+v, %v", body, got, ok)
		}
	}
	for _, bad := range []string{"", "  \n", "abc"} {
		if _, ok := c.Decode([]byte(bad)); ok {
			t.Errorf("decode %q succeeded", bad)
		}
	}
}

func TestStampLive(t *testing.T) {
	for _, c := range []struct {
		name  string
		table fakeTable
		start uint64
		want  bool
	}{
		{"same start", table(self, 7), 7, true},
		{"reused pid", table(self, 8), 7, false},
		{"no recorded start", table(self, 8), 0, true},
	} {
		if got := (Stamp{PID: self, Start: c.start}).Live(c.table); got != c.want {
			t.Errorf("%s: Live = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPrivateMarkerLeavesNoSidecar(t *testing.T) {
	dir := t.TempDir()
	m := Marker[rec]{Path: filepath.Join(dir, "run-1.json"), Codec: JSON[rec](), Private: true}
	if err := m.Write(rec{PID: self}, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Read(); !ok {
		t.Fatal("private marker did not read back")
	}
	if err := m.Remove(self + 1); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Read(); !ok {
		t.Error("Remove deleted a marker that is not the caller's")
	}
	if err := m.Remove(self); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{m.Path, m.Path + ".lock"} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s left after write+remove (stat err %v)", p, err)
		}
	}
}
