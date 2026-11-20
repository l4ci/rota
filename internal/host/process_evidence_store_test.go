package host

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

type lockProbeFS struct {
	OSFileSystem
	wait time.Duration
	path string
}

func (f *lockProbeFS) Locked(p string, wait time.Duration, fn func() error) error {
	f.path, f.wait = p, wait
	return f.OSFileSystem.Locked(p, wait, fn)
}

func newEvidenceStore(t *testing.T) (evidenceStore, *lockProbeFS) {
	t.Helper()
	fs := &lockProbeFS{}
	return evidenceStore{fs: fs, path: t.TempDir() + "/slot.evidence"}, fs
}

func TestEvidenceStoreLockWaitIsShared(t *testing.T) {
	s, fs := newEvidenceStore(t)
	ran := false
	if err := s.Locked(func() error { ran = true; return nil }); err != nil || !ran {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	if fs.wait != evidenceLockWait || fs.path != s.path {
		t.Fatalf("lock %q for %v", fs.path, fs.wait)
	}
}

func TestEvidenceStoreMissingFile(t *testing.T) {
	s, _ := newEvidenceStore(t)
	got, err := s.Load()
	if err != nil || got.Found {
		t.Fatalf("missing file: %+v, %v", got, err)
	}
}

func TestEvidenceStoreRoundTripsEnvelope(t *testing.T) {
	s, _ := newEvidenceStore(t)
	for _, procs := range []processEvidence{{{PID: 4, Start: 9}, {PID: 5}}, {}} {
		if err := s.Save(procs); err != nil {
			t.Fatal(err)
		}
		got, err := s.Load()
		if err != nil || !got.Found || got.Bare || len(got.Legacy) != 0 || !reflect.DeepEqual(got.Procs, procs) {
			t.Fatalf("saved %v, loaded %+v, %v", procs, got, err)
		}
	}
	raw, _ := os.ReadFile(s.path)
	if !strings.HasPrefix(string(raw), `{"version":1,"processes":[`) {
		t.Fatalf("not the versioned envelope: %s", raw)
	}
}

func TestEvidenceStoreLoadsBareArrays(t *testing.T) {
	s, _ := newEvidenceStore(t)
	if err := os.WriteFile(s.path, []byte(`[3, {"pid":4,"start":9}, {"pid":5}]`), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil || !got.Found || !got.Bare ||
		!reflect.DeepEqual(got.Procs, processEvidence{{4, 9}, {PID: 5}}) || !reflect.DeepEqual(got.Legacy, []int{3}) {
		t.Fatalf("%+v, %v", got, err)
	}
	// An empty bare list loads, and says it is bare: it proves nothing.
	os.WriteFile(s.path, []byte(`[]`), 0600)
	if got, err = s.Load(); err != nil || !got.Bare || len(got.Procs)+len(got.Legacy) != 0 {
		t.Fatalf("%+v, %v", got, err)
	}
}

func TestEvidenceStoreRejectsInvalid(t *testing.T) {
	for _, raw := range []string{"", "null", "[0]", "[-1]", "[{}]", `["x"]`, `{"pid":1}`, `{"version":1}`,
		`{"version":1,"processes":null}`, `{"version":2,"processes":[]}`, `{"version":1,"processes":[{"pid":0}]}`} {
		s, _ := newEvidenceStore(t)
		os.WriteFile(s.path, []byte(raw), 0600)
		if _, err := s.Load(); !errors.Is(err, errEvidenceCorrupt) {
			t.Errorf("%q accepted: %v", raw, err)
		}
	}
}
