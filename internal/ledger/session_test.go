package ledger

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/jsonx"
)

const usageLine = `{"type":"assistant","message":{"id":"m1","usage":{"input_tokens":1,"output_tokens":2,"cache_read_input_tokens":3,"cache_creation_input_tokens":4}}}` + "\n"

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

var sessNow = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

func TestSessionRecordRoundTripAndRefusesUnsafeSlot(t *testing.T) {
	cd := t.TempDir()
	s := Session{Slot: "dana", ID: "sid-1", Transcript: "/x/sid-1.jsonl", UpdatedAt: sessNow}
	if err := RecordSession(cd, s); err != nil {
		t.Fatal(err)
	}
	got, ok := LoadSession(cd, "dana")
	if !ok || got.ID != "sid-1" || got.Transcript != "/x/sid-1.jsonl" || !got.UpdatedAt.Equal(sessNow) {
		t.Errorf("%+v %v", got, ok)
	}
	if err := RecordSession(cd, Session{Slot: "../evil", ID: "x"}); err == nil {
		t.Error("a slot name that leaves the directory must be refused")
	}
}

func TestSessionDetailReportsIdPathTokensAndSubagents(t *testing.T) {
	cd, dir := t.TempDir(), t.TempDir()
	tr := filepath.Join(dir, "sid-1.jsonl")
	writeFile(t, tr, usageLine)
	sub := filepath.Join(dir, "sid-1", "subagents", "agent-a.jsonl")
	writeFile(t, sub, strings.Replace(usageLine, `"m1"`, `"s1"`, 1))
	RecordSession(cd, Session{Slot: "dana", ID: "sid-1", Transcript: tr, UpdatedAt: sessNow})
	d := SessionDetail(cd, "dana", "claude", sessNow.Add(-time.Hour), "", nil)
	e := Entry{Detail: d}
	if e.DetailStr("session") != "sid-1" || e.DetailStr("transcript") != tr {
		t.Errorf("detail %v", d)
	}
	if tok, ok := e.DetailTokens(); !ok || tok != (Tokens{Input: 2, Output: 4, CacheRead: 6, CacheWrite: 8}) {
		t.Errorf("tokens %+v %v", tok, ok)
	}
	if subs, _ := d.Get("subagents"); !reflect.DeepEqual(subs, []any{sub}) {
		t.Errorf("subagents %v, want [%s]", subs, sub)
	}
}

func TestSessionDetailUnavailableNeverZero(t *testing.T) {
	cd := t.TempDir()
	// No record at all, a record from before the assignment, and a record whose file is gone.
	RecordSession(cd, Session{Slot: "old", ID: "s", Transcript: "/gone.jsonl", UpdatedAt: sessNow.Add(-48 * time.Hour)})
	RecordSession(cd, Session{Slot: "gone", ID: "s2", Transcript: "/gone.jsonl", UpdatedAt: sessNow})
	for slot, wantSession := range map[string]string{"none": "unavailable", "old": "unavailable", "gone": "s2"} {
		d := SessionDetail(cd, slot, "claude", sessNow.Add(-time.Hour), "", nil)
		e := Entry{Detail: d}
		if e.DetailStr("session") != wantSession {
			t.Errorf("%s: session %q, want %q", slot, e.DetailStr("session"), wantSession)
		}
		if e.DetailStr("tokens") != "unavailable" {
			t.Errorf("%s: tokens %v, want unavailable", slot, d)
		}
	}
}

func TestSessionDetailUnavailableForAHarnessWithoutUsage(t *testing.T) {
	cd, dir := t.TempDir(), t.TempDir()
	tr := filepath.Join(dir, "s.jsonl")
	writeFile(t, tr, "{}\n")
	RecordSession(cd, Session{Slot: "dana", ID: "s", Transcript: tr, UpdatedAt: sessNow})
	d := SessionDetail(cd, "dana", "opencode", sessNow.Add(-time.Hour), "", nil)
	if (Entry{Detail: d}).DetailStr("tokens") != "unavailable" {
		t.Errorf("%v", d)
	}
}

func TestSessionDetailCopiesOnlyWhenAsked(t *testing.T) {
	cd, dir := t.TempDir(), t.TempDir()
	out := filepath.Join(t.TempDir(), "transcripts")
	tr := filepath.Join(dir, "sid-1.jsonl")
	writeFile(t, tr, usageLine)
	RecordSession(cd, Session{Slot: "dana", ID: "sid-1", Transcript: tr, UpdatedAt: sessNow})
	d := SessionDetail(cd, "dana", "claude", sessNow.Add(-time.Hour), "", nil)
	if (Entry{Detail: d}).DetailStr("transcriptCopy") != "" {
		t.Error("copy must be off by default")
	}
	var copies []string
	filepath.WalkDir(cd, func(p string, de os.DirEntry, _ error) error {
		if !de.IsDir() && strings.HasSuffix(p, ".jsonl") {
			copies = append(copies, p)
		}
		return nil
	})
	if len(copies) != 0 {
		t.Errorf("copying off, yet transcripts written under the state dir: %v", copies)
	}
	d = SessionDetail(cd, "dana", "claude", sessNow.Add(-time.Hour), out, nil)
	cp := (Entry{Detail: d}).DetailStr("transcriptCopy")
	if cp != filepath.Join(out, "sid-1.jsonl") {
		t.Fatalf("copy recorded as %q: %v", cp, d)
	}
	if b, err := os.ReadFile(cp); err != nil || string(b) != usageLine {
		t.Errorf("copy %q: %v", cp, err)
	}
	// Transcripts hold the whole conversation: owner-only.
	if fi, err := os.Stat(cp); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("copy mode %v %v, want 0600", fi.Mode(), err)
	}
	if fi, err := os.Stat(out); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("copy dir mode %v %v, want 0700", fi.Mode(), err)
	}
}

func TestSessionDetailSaysSoWhenTheCopyFails(t *testing.T) {
	cd, out := t.TempDir(), t.TempDir()
	RecordSession(cd, Session{Slot: "dana", ID: "sid-1", Transcript: filepath.Join(t.TempDir(), "gone.jsonl"), UpdatedAt: sessNow})
	var warned []string
	d := SessionDetail(cd, "dana", "claude", sessNow.Add(-time.Hour), out, func(m string) { warned = append(warned, m) })
	if got := (Entry{Detail: d}).DetailStr("transcriptCopy"); got != "failed" {
		t.Errorf("transcriptCopy %q, want failed", got)
	}
	if len(warned) != 1 || !strings.Contains(warned[0], "sid-1") {
		t.Errorf("warnings %v", warned)
	}
}

func TestSetUnavailableMarksEverySessionKey(t *testing.T) {
	d := jsonx.NewObject()
	SetUnavailable(d)
	e := Entry{Detail: d}
	for _, k := range []string{"session", "transcript", "tokens"} {
		if e.DetailStr(k) != Unavailable {
			t.Errorf("%s = %q", k, e.DetailStr(k))
		}
	}
}
