package ledger

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func claudeLine(id string, in, out, cr, cw int) string {
	return `{"type":"assistant","message":{"id":"` + id + `","usage":{"input_tokens":` + strconv.Itoa(in) + `,"output_tokens":` + strconv.Itoa(out) +
		`,"cache_read_input_tokens":` + strconv.Itoa(cr) + `,"cache_creation_input_tokens":` + strconv.Itoa(cw) + `}}}`
}

func TestClaudeUsageSumsMainAndSubagentsOnceAndListsSubagents(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "abc.jsonl")
	writeLines(t, main,
		`{"type":"user","message":{"role":"user"}}`,
		claudeLine("m1", 10, 5, 100, 20),
		claudeLine("m1", 10, 5, 100, 20), // streamed twice: counted once
		claudeLine("m2", 1, 2, 3, 4))
	sub := filepath.Join(dir, "abc", "subagents", "agent-1.jsonl")
	writeLines(t, sub, claudeLine("s1", 7, 8, 9, 10))
	got, subs, err := ClaudeUsage(main)
	if err != nil {
		t.Fatal(err)
	}
	if want := (Tokens{Input: 18, Output: 15, CacheRead: 112, CacheWrite: 34}); got != want {
		t.Errorf("tokens %+v, want %+v", got, want)
	}
	if len(subs) != 1 || subs[0] != sub {
		t.Errorf("subagents %v", subs)
	}
}

func TestClaudeUsageMissingFileIsAnError(t *testing.T) {
	if _, _, err := ClaudeUsage(filepath.Join(t.TempDir(), "nope.jsonl")); err == nil {
		t.Fatal("a missing transcript must not read as zero tokens")
	}
}

func TestCodexUsageTakesTheLastTotal(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rollout.jsonl")
	writeLines(t, p,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"cached_input_tokens":4,"output_tokens":2}}}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":null}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":50,"cached_input_tokens":40,"output_tokens":9,"cache_write_input_tokens":3}}}}`)
	got, err := CodexUsage(p)
	if err != nil {
		t.Fatal(err)
	}
	if want := (Tokens{Input: 50, Output: 9, CacheRead: 40, CacheWrite: 3}); got != want {
		t.Errorf("tokens %+v, want %+v", got, want)
	}
}

func TestCodexUsageWithoutTokenCountIsAnError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rollout.jsonl")
	writeLines(t, p, `{"type":"session_meta"}`)
	if _, err := CodexUsage(p); err == nil {
		t.Fatal("no token_count event must read as unavailable, not 0")
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
	writeLines(t, tr, claudeLine("m1", 1, 2, 3, 4))
	writeLines(t, filepath.Join(dir, "sid-1", "subagents", "agent-a.jsonl"), claudeLine("s1", 1, 1, 1, 1))
	RecordSession(cd, Session{Slot: "dana", ID: "sid-1", Transcript: tr, UpdatedAt: sessNow})
	d := SessionDetail(cd, "dana", "claude", sessNow.Add(-time.Hour), "")
	e := Entry{Detail: d}
	if e.DetailStr("session") != "sid-1" || e.DetailStr("transcript") != tr {
		t.Errorf("detail %v", d)
	}
	if tok, ok := e.DetailTokens(); !ok || tok != (Tokens{Input: 2, Output: 3, CacheRead: 4, CacheWrite: 5}) {
		t.Errorf("tokens %+v %v", tok, ok)
	}
	if subs, _ := d.Get("subagents"); subs == nil {
		t.Error("subagents not listed")
	}
}

func TestSessionDetailUnavailableNeverZero(t *testing.T) {
	cd := t.TempDir()
	// No record at all, a record from before the assignment, and a record whose file is gone.
	RecordSession(cd, Session{Slot: "old", ID: "s", Transcript: "/gone.jsonl", UpdatedAt: sessNow.Add(-48 * time.Hour)})
	RecordSession(cd, Session{Slot: "gone", ID: "s2", Transcript: "/gone.jsonl", UpdatedAt: sessNow})
	for slot, wantSession := range map[string]string{"none": "unavailable", "old": "unavailable", "gone": "s2"} {
		d := SessionDetail(cd, slot, "claude", sessNow.Add(-time.Hour), "")
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
	writeLines(t, tr, `{}`)
	RecordSession(cd, Session{Slot: "dana", ID: "s", Transcript: tr, UpdatedAt: sessNow})
	d := SessionDetail(cd, "dana", "opencode", sessNow.Add(-time.Hour), "")
	if (Entry{Detail: d}).DetailStr("tokens") != "unavailable" {
		t.Errorf("%v", d)
	}
}

func TestSessionDetailCopiesOnlyWhenAsked(t *testing.T) {
	cd, dir, out := t.TempDir(), t.TempDir(), t.TempDir()
	tr := filepath.Join(dir, "sid-1.jsonl")
	writeLines(t, tr, claudeLine("m1", 1, 2, 3, 4))
	RecordSession(cd, Session{Slot: "dana", ID: "sid-1", Transcript: tr, UpdatedAt: sessNow})
	d := SessionDetail(cd, "dana", "claude", sessNow.Add(-time.Hour), "")
	if (Entry{Detail: d}).DetailStr("transcriptCopy") != "" {
		t.Error("copy must be off by default")
	}
	if ents, _ := os.ReadDir(out); len(ents) != 0 {
		t.Error("nothing may be written when copying is off")
	}
	d = SessionDetail(cd, "dana", "claude", sessNow.Add(-time.Hour), out)
	cp := (Entry{Detail: d}).DetailStr("transcriptCopy")
	if cp == "" {
		t.Fatalf("no copy recorded: %v", d)
	}
	if b, err := os.ReadFile(cp); err != nil || !strings.Contains(string(b), "m1") {
		t.Errorf("copy %q: %v", cp, err)
	}
}
