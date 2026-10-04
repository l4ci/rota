package gate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func project(t *testing.T, cfg string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	if cfg != "" {
		if err := os.WriteFile(filepath.Join(root, ".rota", "config.json"), []byte(cfg), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func auditLines(t *testing.T, root string) []map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, AuditFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("audit line %q: %v", l, err)
		}
		out = append(out, m)
	}
	return out
}

func TestRegistry(t *testing.T) {
	seen := map[string]bool{}
	var enforced []string
	for _, g := range Registry {
		if seen[g.Name] || g.Name == "" || g.Creates == "" || len(g.Skills) == 0 {
			t.Errorf("bad row %+v", g)
		}
		seen[g.Name] = true
		if g.Enforced() {
			enforced = append(enforced, g.Name)
		}
	}
	// The maintainer's B1 ruling, plus B3's reset: exactly these gates are
	// enforced in code.
	if want := []string{TagPush, ReleasePublish, PublicFiling, MergeApproval, DebugReset}; !reflect.DeepEqual(enforced, want) {
		t.Errorf("enforced %v, want %v", enforced, want)
	}
	for _, name := range []string{"issue-close", "issue-label"} {
		if !seen[name] {
			t.Errorf("skill-only gate %s missing from the registry", name)
		}
	}
}

func TestConfirmValidate(t *testing.T) {
	for _, c := range []struct {
		conf Confirm
		ok   bool
	}{
		{Confirm{}, true},
		{Confirm{Given: true, Note: "yes, push it"}, true},
		{Confirm{Given: true}, false},
		{Confirm{Given: true, Note: "  "}, false},
		{Confirm{Note: "yes"}, false},
	} {
		if err := c.conf.Validate(); (err == nil) != c.ok {
			t.Errorf("%+v: %v", c.conf, err)
		}
	}
}

// Every enforced gate refuses without --confirm at every autonomy level, and
// a confirmed pass is audited with the level in force.
func TestClearAtEveryAutonomyLevel(t *testing.T) {
	now = func() time.Time { return time.Date(2026, 10, 3, 12, 0, 0, 0, time.FixedZone("x", 7200)) }
	t.Cleanup(func() { now = time.Now })
	for _, level := range []string{"off", "auto", "loop"} {
		for _, g := range Registry {
			if !g.Enforced() {
				continue
			}
			root := project(t, `{"autonomy": {"level": "`+level+`"}}`)
			err := Clear(root, g.Name, g.Verbs[0], "v1.2.3", Confirm{}, nil)
			var r *Refused
			if !errors.As(err, &r) || r.Gate != g.Name || r.Paths == nil {
				t.Fatalf("%s/%s: no confirm gave %v", level, g.Name, err)
			}
			if lines := auditLines(t, root); lines != nil {
				t.Fatalf("%s/%s: a refusal audited %v", level, g.Name, lines)
			}
			if err := Clear(root, g.Name, g.Verbs[0], "v1.2.3", Confirm{Given: true, Note: `"yes" – go`}, nil); err != nil {
				t.Fatalf("%s/%s: confirmed: %v", level, g.Name, err)
			}
			want := map[string]any{"ts": "2026-10-03T10:00:00Z", "gate": g.Name, "verb": g.Verbs[0], "target": "v1.2.3",
				"note": `"yes" – go`, "autonomy": level}
			if lines := auditLines(t, root); len(lines) != 1 || !reflect.DeepEqual(lines[0], want) {
				t.Fatalf("%s/%s: audit %v", level, g.Name, lines)
			}
		}
	}
}

func TestClearAppends(t *testing.T) {
	root := project(t, "")
	for i := 0; i < 3; i++ {
		if err := Clear(root, TagPush, "release push", "v1", Confirm{Given: true, Note: "ok"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	lines := auditLines(t, root)
	if len(lines) != 3 || lines[2]["autonomy"] != "off" {
		t.Fatalf("%v", lines)
	}
}

func TestMatchPath(t *testing.T) {
	for _, c := range []struct {
		entry, file string
		want        bool
	}{
		{"rota-release", "rota-release/SKILL.md", true},
		{"rota-release/", "rota-release/SKILL.md", true},
		{"./docs", "docs/a/b.md", true},
		{"docs", "docsx/a.md", false},
		{"go.mod", "go.mod", true},
		{"*.md", "README.md", true},
		{"*.md", "docs/README.md", false},
		{"internal/*/gate.go", "internal/cli/gate.go", true},
		{"", "x", false},
		{"[", "[", true}, // equal wins over a malformed glob
		{"[", "a", false},
	} {
		if got := MatchPath(c.entry, c.file); got != c.want {
			t.Errorf("MatchPath(%q, %q) = %v", c.entry, c.file, got)
		}
	}
}

func TestMergePolicy(t *testing.T) {
	p, err := LoadMergePolicy(project(t, ""))
	if err != nil || p.Mode != MergeNone || p.NeedsFiles() {
		t.Fatalf("default: %+v %v", p, err)
	}
	if ok, _ := p.Covers([]string{"a"}); ok {
		t.Error("none covers nothing")
	}
	p, err = LoadMergePolicy(project(t, `{"ship": {"mergeApproval": "all"}}`))
	if ok, hit := p.Covers(nil); err != nil || !ok || hit == nil || len(hit) != 0 {
		t.Errorf("all: %v %v %v", ok, hit, err)
	}
	p, err = LoadMergePolicy(project(t, `{"ship": {"mergeApproval": "paths", "mergeApprovalPaths": ["rota-release", " ", 3, "*.md"]}}`))
	if err != nil || !p.NeedsFiles() || !reflect.DeepEqual(p.Paths, []string{"rota-release", "*.md"}) {
		t.Fatalf("paths: %+v %v", p, err)
	}
	if ok, hit := p.Covers([]string{"internal/x.go", "README.md", "rota-release/SKILL.md"}); !ok || !reflect.DeepEqual(hit, []string{"README.md", "rota-release/SKILL.md"}) {
		t.Errorf("paths hit: %v %v", ok, hit)
	}
	if ok, _ := p.Covers([]string{"internal/x.go"}); ok {
		t.Error("paths: an unlisted file needs no approval")
	}
	for _, bad := range []string{`"sometimes"`, `true`, `""`} {
		if _, err := LoadMergePolicy(project(t, `{"ship": {"mergeApproval": `+bad+`}}`)); !errors.Is(err, ErrBadMergeMode) {
			t.Errorf("%s: %v", bad, err)
		}
	}
}

func TestClearRecordsEscalation(t *testing.T) {
	root := project(t, "")
	if err := Clear(root, MergeApproval, "ship pr-merge", "PR 7", Confirm{Given: true, Note: "Approve.", Escalation: "e3"}, []string{}); err != nil {
		t.Fatal(err)
	}
	if err := Clear(root, MergeApproval, "ship pr-merge", "PR 7", Confirm{Given: true, Note: "ok"}, []string{}); err != nil {
		t.Fatal(err)
	}
	lines := auditLines(t, root)
	if len(lines) != 2 || lines[0]["escalation"] != "e3" || lines[0]["note"] != "Approve." {
		t.Fatalf("%v", lines)
	}
	if _, ok := lines[1]["escalation"]; ok {
		t.Errorf("escalation must be omitted when empty: %v", lines[1])
	}
}

func TestValidateIgnoresEscalation(t *testing.T) {
	if err := (Confirm{Escalation: "e1"}).Validate(); err != nil {
		t.Errorf("Validate must keep --confirm semantics: %v", err)
	}
}

func TestAutopilotAuditLine(t *testing.T) {
	root := t.TempDir()
	if err := Autopilot(root, "round tick merge", "ben", "into main"); err != nil {
		t.Fatal(err)
	}
	ls := auditLines(t, root)
	if len(ls) != 1 || ls[0]["gate"] != "autopilot" || ls[0]["verb"] != "round tick merge" || ls[0]["target"] != "ben" || ls[0]["note"] != "into main" {
		t.Fatalf("%v", ls)
	}
}
