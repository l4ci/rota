package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const startHandoffBody = "<!-- rota-handoff: orchestrator -->\n# Round 11 handoff\n\nReview queue: #626 first.\n"

type startHandoff struct {
	Data struct {
		Handoff *struct {
			Path     string `json:"path"`
			Heading  string `json:"heading"`
			Consumed bool   `json:"consumed"`
		} `json:"handoff"`
	} `json:"data"`
}

func startJSON(t *testing.T, dir string, args ...string) startHandoff {
	t.Helper()
	code, out, errOut := rotaStdin(t, dir, "", append([]string{"--json", "round", "start", "--holder-pid", strconv.Itoa(os.Getpid()), "--base", "feat/x", "--slots", "1"}, args...)...)
	if code != 0 {
		t.Fatalf("round start: %d %s %s", code, out, errOut)
	}
	var m startHandoff
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return m
}

func TestRoundStartReportsAndConsumesHandoff(t *testing.T) {
	dir := orchProject(t, false)
	os.WriteFile(filepath.Join(dir, ".rota", "BACKLOG.md"), []byte("# TODO\n\n## Bugs\n\n## Features\n\n## Tasks\n\n## Completed\n"), 0o644)
	if m := startJSON(t, dir); m.Data.Handoff != nil {
		t.Fatalf("no note, got handoff %+v", m.Data.Handoff)
	}
	h := filepath.Join(dir, ".rota", "handoff", "feat", "x.md")
	os.MkdirAll(filepath.Dir(h), 0o755)
	os.WriteFile(h, []byte(startHandoffBody), 0o644)

	m := startJSON(t, dir)
	if m.Data.Handoff == nil || m.Data.Handoff.Path != h || m.Data.Handoff.Heading != "Round 11 handoff" {
		t.Fatalf("handoff = %+v", m.Data.Handoff)
	}
	if _, err := os.Stat(h); err != nil {
		t.Fatalf("a plain start must leave the note: %v", err)
	}

	startJSON(t, dir, "--consume-handoff")
	if _, err := os.Stat(h); !os.IsNotExist(err) {
		t.Error("--consume-handoff should move the note away")
	}
	if _, err := os.Stat(h + ".consumed"); err != nil {
		t.Errorf("no .consumed archive: %v", err)
	}
	if m := startJSON(t, dir); m.Data.Handoff != nil {
		t.Errorf("consumed note still reported: %+v", m.Data.Handoff)
	}
}

func handoffProject(t *testing.T, body string) (dir, note string) {
	t.Helper()
	dir = orchProject(t, false)
	os.WriteFile(filepath.Join(dir, ".rota", "BACKLOG.md"), []byte("# TODO\n\n## Bugs\n\n## Features\n\n## Tasks\n\n## Completed\n"), 0o644)
	note = filepath.Join(dir, ".rota", "handoff", "feat", "x.md")
	os.MkdirAll(filepath.Dir(note), 0o755)
	if body != "" {
		os.WriteFile(note, []byte(body), 0o644)
	}
	return dir, note
}

func startText(t *testing.T, dir string, args ...string) (out, errOut string) {
	t.Helper()
	code, out, errOut := rotaStdin(t, dir, "", append([]string{"round", "start", "--holder-pid", strconv.Itoa(os.Getpid()), "--base", "feat/x", "--slots", "1"}, args...)...)
	if code != 0 {
		t.Fatalf("round start: %d %s %s", code, out, errOut)
	}
	return out, errOut
}

func TestRoundStartHandoffTextLine(t *testing.T) {
	dir, h := handoffProject(t, startHandoffBody)
	out, _ := startText(t, dir)
	want := "handoff\t" + h + "\tRound 11 handoff\tread it before choosing the slate, then rerun with --consume-handoff"
	if !strings.Contains(out, want) {
		t.Errorf("missing %q in:\n%s", want, out)
	}
	out, _ = startText(t, dir, "--consume-handoff")
	want = "handoff\t" + h + "\tRound 11 handoff\tconsumed: read " + h + ".consumed"
	if !strings.Contains(out, want) {
		t.Errorf("missing %q in:\n%s", want, out)
	}
}

func TestRoundStartHandoffConsumedField(t *testing.T) {
	dir, _ := handoffProject(t, startHandoffBody)
	if m := startJSON(t, dir); m.Data.Handoff == nil || m.Data.Handoff.Consumed {
		t.Errorf("plain start: %+v", m.Data.Handoff)
	}
	if m := startJSON(t, dir, "--consume-handoff"); m.Data.Handoff == nil || !m.Data.Handoff.Consumed {
		t.Errorf("--consume-handoff: %+v", m.Data.Handoff)
	}
}

func TestRoundStartIgnoresMarkerlessNote(t *testing.T) {
	dir, h := handoffProject(t, "# Worker branch note\n\nnot for the orchestrator\n")
	if m := startJSON(t, dir, "--consume-handoff"); m.Data.Handoff != nil {
		t.Errorf("marker-less note reported: %+v", m.Data.Handoff)
	}
	if _, err := os.Stat(h); err != nil {
		t.Errorf("marker-less note must stay put: %v", err)
	}
}

func TestRoundStartWarnsWhenConsumeFails(t *testing.T) {
	dir, h := handoffProject(t, startHandoffBody)
	// A non-empty directory at the archive path makes the rename fail.
	os.MkdirAll(filepath.Join(h+".consumed", "x"), 0o755)
	out, errOut := startText(t, dir, "--consume-handoff")
	if !strings.Contains(errOut, "could not archive handoff note") {
		t.Errorf("no consume warning: %q", errOut)
	}
	if !strings.Contains(out, "consume failed") || strings.Contains(out, "rerun with --consume-handoff") {
		t.Errorf("hint should report the failed consume, got:\n%s", out)
	}
	if _, err := os.Stat(h); err != nil {
		t.Errorf("note must stay after a failed consume: %v", err)
	}
	if m := startJSON(t, dir, "--consume-handoff"); m.Data.Handoff == nil || m.Data.Handoff.Consumed {
		t.Errorf("failed consume reported as consumed: %+v", m.Data.Handoff)
	}
}

func TestRoundStartWarnsOnUnreadableNote(t *testing.T) {
	dir, h := handoffProject(t, "")
	os.MkdirAll(h, 0o755) // a directory where the note should be: ReadFile fails, not-exist it is not
	_, errOut := startText(t, dir)
	if !strings.Contains(errOut, "unreadable") {
		t.Errorf("no unreadable warning: %q", errOut)
	}
}
