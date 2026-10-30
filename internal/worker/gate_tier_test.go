package worker

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/ledger"
)

// tierWorld is newWorld with w1 rebuilt to change only files (path: body).
func tierWorld(t *testing.T, files map[string]string) *world {
	t.Helper()
	w := newWorld(t, ghURL)
	gitq(t, w.worker, "reset", "-q", "--hard", "origin/main")
	for f, body := range files {
		p := filepath.Join(w.worker, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitq(t, w.worker, "add", f)
	}
	gitq(t, w.worker, "commit", "-q", "-m", "change")
	gitq(t, w.worker, "push", "-q", "-f", "origin", "w1")
	w.forge("sha", gitq(t, w.worker, "rev-parse", "HEAD"))
	return w
}

// AC-4, AC-5, AC-7, AC-8: the gate verifies a change with the commands of its
// tier and records the tier and what it skipped.
func TestGateTier(t *testing.T) {
	readme := map[string]string{"README.md": "moved image"}
	code := map[string]string{"x.go": "package x"}
	cases := []struct {
		name    string
		files   map[string]string
		cfg     string
		body    string
		verdict string
		tier    string
		skipped []string
		note    string // a substring of the GATE-TIER note, "" for none
	}{
		{"docs runs test.docs only", readme, `{"test":{"full":["false"],"e2e":["false"],"docs":["true"]}}`, "", GatePass, "docs", []string{"test.full", "test.e2e"}, "GATE-TIER w1 — docs (docs-only change: README.md); ran test.docs; skipped test.full, test.e2e"},
		{"docs still fails on test.docs", readme, `{"test":{"full":["true"],"docs":["false"]}}`, "", GateVerifyFailed, "docs", []string{"test.full"}, "ran test.docs"},
		{"empty test.docs runs test.full", readme, `{"test":{"full":["false"]}}`, "", GateVerifyFailed, "docs", nil, ""},
		{"one-way door runs test.full", readme, `{"test":{"full":["false"],"docs":["true"]}}`, "Door: one-way. Blast radius: x.", GateVerifyFailed, "full", nil, ""},
		{"risky path runs test.full", readme, `{"test":{"full":["false"],"docs":["true"]},"change":{"riskyPaths":["README.md"]}}`, "", GateVerifyFailed, "full", nil, ""},
		{"code runs test.full", code, `{"test":{"full":["false"],"docs":["true"],"small":["true"]}}`, "", GateVerifyFailed, "full", nil, ""},
		{"small runs test.small with files", code, `{"test":{"full":["false"],"e2e":["false"],"small":["test {files} = x.go"]},"change":{"smallBelow":100}}`, "", GatePass, "small", []string{"test.full", "test.e2e"}, "ran test.small"},
		{"bad change config runs test.full", readme, `{"test":{"full":["false"],"docs":["true"]},"change":{"smallBelow":-1}}`, "", GateVerifyFailed, "full", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := tierWorld(t, c.files)
			w.setConfig(c.cfg)
			w.setPRBody(c.body)
			res, err := w.gate(false, GateOpts{})
			if err != nil || res.Verdict != c.verdict {
				t.Fatalf("verdict = %q (%v), want %q\n%s\n%s", res.Verdict, err, c.verdict, res.Err, strings.Join(res.Notes, "\n"))
			}
			if res.Tier != c.tier || !slices.Equal(res.Skipped, c.skipped) {
				t.Errorf("tier %q skipped %v, want %q %v", res.Tier, res.Skipped, c.tier, c.skipped)
			}
			notes := strings.Join(res.Notes, "\n")
			if c.note != "" && !strings.Contains(notes, c.note) {
				t.Errorf("notes lack %q:\n%s", c.note, notes)
			}
			if c.note == "" && strings.Contains(notes, "GATE-TIER") {
				t.Errorf("a tier that changed nothing printed a GATE-TIER note:\n%s", notes)
			}
			es, _ := ledger.Load(w.dir)
			var tier string
			for _, e := range es {
				if e.Kind == ledger.KindGate {
					tier = e.DetailStr("tier")
				}
			}
			if tier != c.tier {
				t.Errorf("gate ledger tier = %q, want %q", tier, c.tier)
			}
		})
	}
}
