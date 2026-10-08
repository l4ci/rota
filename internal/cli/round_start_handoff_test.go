package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

const startHandoffBody = "<!-- rota-handoff: orchestrator -->\n# Round 11 handoff\n\nReview queue: #626 first.\n"

type startHandoff struct {
	Data struct {
		Handoff *struct {
			Path    string `json:"path"`
			Heading string `json:"heading"`
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
