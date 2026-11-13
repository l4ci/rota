package harness

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
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

func TestTranscriptUsageRefusesAKindWithoutAReader(t *testing.T) {
	if _, _, err := TranscriptUsage("opencode", "/x"); err == nil {
		t.Fatal("want an error")
	}
}
