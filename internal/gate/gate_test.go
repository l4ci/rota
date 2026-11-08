package gate

import (
	"encoding/json"
	"errors"
	"github.com/l4ci/rota/internal/fsio"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
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
		if seen[g.Name] || g.Name == "" || g.Creates == "" || (len(g.Skills) == 0 && !g.Enforced()) {
			t.Errorf("bad row %+v", g)
		}
		seen[g.Name] = true
		if g.Enforced() {
			enforced = append(enforced, g.Name)
		}
	}
	// The maintainer's B1 ruling, plus B3's reset: exactly these gates are
	// enforced in code.
	if want := []string{TagPush, ReleasePublish, PublicFiling, MergeApproval, DebugReset, BugreportFile}; !reflect.DeepEqual(enforced, want) {
		t.Errorf("enforced %v, want %v", enforced, want)
	}
	for _, name := range []string{"issue-close", "pr-open", "decision-write"} {
		if !seen[name] {
			t.Errorf("skill-only gate %s missing from the registry", name)
		}
	}
}

func TestClearRejectsMalformedConfirmation(t *testing.T) {
	for _, tc := range []struct {
		name string
		conf Confirm
	}{
		{"empty note", Confirm{Given: true}},
		{"whitespace note", Confirm{Given: true, Note: " \t\n\u2003"}},
		{"note without confirmation", Confirm{Note: "yes"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := project(t, "")
			err := Clear(root, "off", fixedNow, TagPush, "release push", "v1", tc.conf, nil)
			if !errors.Is(err, ErrBadConfirm) {
				t.Errorf("Clear returned %v, want ErrBadConfirm", err)
			}
			if lines := auditLines(t, root); len(lines) != 0 {
				t.Errorf("malformed confirmation audited %v", lines)
			}
		})
	}
}

// fixedNow is the audit clock of these tests, in a non-UTC zone so the UTC
// conversion is checked.
var fixedNow = time.Date(2026, 10, 3, 12, 0, 0, 0, time.FixedZone("x", 7200))

// Every enforced gate refuses without --confirm at every autonomy level, and
// a confirmed pass is audited with the level in force.
func TestClearAtEveryAutonomyLevel(t *testing.T) {
	for _, level := range []string{"off", "roundtrip", "auto"} {
		for _, g := range Registry {
			if !g.Enforced() {
				continue
			}
			root := project(t, "")
			err := Clear(root, level, fixedNow, g.Name, g.Verbs[0], "v1.2.3", Confirm{}, nil)
			var r *Refused
			if !errors.As(err, &r) || r.Gate != g.Name || r.Paths == nil {
				t.Fatalf("%s/%s: no confirm gave %v", level, g.Name, err)
			}
			if lines := auditLines(t, root); lines != nil {
				t.Fatalf("%s/%s: a refusal audited %v", level, g.Name, lines)
			}
			if err := Clear(root, level, fixedNow, g.Name, g.Verbs[0], "v1.2.3", Confirm{Given: true, Note: `"yes" – go`}, nil); err != nil {
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
		if err := Clear(root, "off", fixedNow, TagPush, "release push", "v1", Confirm{Given: true, Note: "ok"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	lines := auditLines(t, root)
	if len(lines) != 3 || lines[2]["autonomy"] != "off" {
		t.Fatalf("%v", lines)
	}
}

func TestMergePolicy(t *testing.T) {
	p, err := NewMergePolicy("none", nil)
	if err != nil || p.Mode != MergeNone || p.NeedsFiles() {
		t.Fatalf("none: %+v %v", p, err)
	}
	if ok, _ := p.Covers([]string{"a"}); ok {
		t.Error("none covers nothing")
	}
	p, err = NewMergePolicy("all", nil)
	if ok, hit := p.Covers(nil); err != nil || !ok || hit == nil || len(hit) != 0 {
		t.Errorf("all: %v %v %v", ok, hit, err)
	}
	p, err = NewMergePolicy("paths", []string{"rota-release", " ", "*.md"})
	if err != nil || !p.NeedsFiles() || !reflect.DeepEqual(p.Paths, []string{"rota-release", "*.md"}) {
		t.Fatalf("paths: %+v %v", p, err)
	}
	if ok, hit := p.Covers([]string{"internal/x.go", "README.md", "rota-release/SKILL.md"}); !ok || !reflect.DeepEqual(hit, []string{"README.md", "rota-release/SKILL.md"}) {
		t.Errorf("paths hit: %v %v", ok, hit)
	}
	if ok, _ := p.Covers([]string{"internal/x.go"}); ok {
		t.Error("paths: an unlisted file needs no approval")
	}
	for _, bad := range []string{"sometimes", "true", ""} {
		if _, err := NewMergePolicy(bad, nil); !errors.Is(err, ErrBadMergeMode) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestClearRecordsEscalation(t *testing.T) {
	root := project(t, "")
	if err := Clear(root, "off", fixedNow, MergeApproval, "ship pr-merge", "PR 7", Confirm{Given: true, Note: "Approve.", Escalation: "e3"}, []string{}); err != nil {
		t.Fatal(err)
	}
	if err := Clear(root, "off", fixedNow, MergeApproval, "ship pr-merge", "PR 7", Confirm{Given: true, Note: "ok"}, []string{}); err != nil {
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

func TestClearEscalationWithoutConfirmationRefuses(t *testing.T) {
	root := project(t, "")
	err := Clear(root, "off", fixedNow, MergeApproval, "ship pr-merge", "PR 7", Confirm{Escalation: "e1"}, []string{"protected.go"})
	var refused *Refused
	if !errors.As(err, &refused) {
		t.Fatalf("Clear returned %v, want Refused", err)
	}
	if refused.Gate != MergeApproval || !reflect.DeepEqual(refused.Paths, []string{"protected.go"}) {
		t.Errorf("refusal lost gate or paths: %+v", refused)
	}
	if refused.Error() != "manual gate 'merge-approval' is not cleared" || refused.Hint() != `ask the user, then re-run with --confirm --confirm-note "<their answer>"` {
		t.Errorf("refusal wording changed: %s; %s", refused.Error(), refused.Hint())
	}
	if lines := auditLines(t, root); len(lines) != 0 {
		t.Errorf("an escalation without confirmation audited %v", lines)
	}
}

func TestAutopilotAuditLine(t *testing.T) {
	root := t.TempDir()
	if err := Autopilot(root, fixedNow, "round tick merge", "ben", "into main"); err != nil {
		t.Fatal(err)
	}
	ls := auditLines(t, root)
	if len(ls) != 1 || ls[0]["gate"] != "autopilot" || ls[0]["verb"] != "round tick merge" || ls[0]["target"] != "ben" || ls[0]["note"] != "into main" {
		t.Fatalf("%v", ls)
	}
}

func TestReadAudit(t *testing.T) {
	root := t.TempDir()
	if got, err := ReadAudit(root); err != nil || got != nil {
		t.Fatalf("missing log: %v, %v", got, err)
	}
	Autopilot(root, fixedNow, "round tick merge", "ben", "into main")
	Autopilot(root, fixedNow, "round tick assign", "dana", "item 7")
	got, err := ReadAudit(root)
	if err != nil || len(got) != 2 {
		t.Fatalf("%v, %v", got, err)
	}
	if got[0].Target != "ben" || got[1].Verb != "round tick assign" || !got[0].TS.Equal(fixedNow) {
		t.Errorf("order or fields: %v", got)
	}
}

func TestAuditRecordRoundTrip(t *testing.T) {
	root := t.TempDir()
	want := Record{TS: fixedNow.UTC(), Gate: MergeApproval, Verb: "ship merge", Target: "PR 7", Note: "ok", Autonomy: "auto", Escalation: "e3"}
	if err := AppendAudit(root, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadAudit(root)
	if err != nil || len(got) != 1 || got[0] != want {
		t.Fatalf("%+v, %v", got, err)
	}
}

// A damaged line is skipped by ReadAudit and stops ReadAuditStrict; a missing
// log is no records for one and os.ErrNotExist for the other.
func TestReadAuditPolicies(t *testing.T) {
	root := t.TempDir()
	if _, err := ReadAuditStrict(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing log: %v", err)
	}
	Autopilot(root, fixedNow, "round tick merge", "ben", "into main")
	p := filepath.Join(root, AuditFile)
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("{\"gate\":null,\"ts\":\"t\"}\n[1]\n{\"verb\":\"x\"}\n")
	f.Close()
	got, err := ReadAudit(root)
	if err != nil || len(got) != 3 { // everything but the non-object line
		t.Fatalf("lenient: %+v, %v", got, err)
	}
	var le *fsio.LineError
	if _, err := ReadAuditStrict(root); !errors.As(err, &le) || le.Line != 2 {
		t.Fatalf("strict: %v", err)
	}
}

// Every gate name handed to Clear is a Registry row, and the callers name it
// through a gate constant rather than a string literal.
func TestClearCallersNameRegistryGates(t *testing.T) {
	known := map[string]bool{}
	for _, g := range Registry {
		known[g.Name] = true
	}
	consts := map[string]string{
		"TagPush": TagPush, "ReleasePublish": ReleasePublish, "PublicFiling": PublicFiling,
		"MergeApproval": MergeApproval, "DebugReset": DebugReset, "BugreportFile": BugreportFile,
		"FreezeClear": FreezeClear,
	}
	root := filepath.Join("..", "..", "internal")
	fset := token.NewFileSet()
	calls := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		f, perr := parser.ParseFile(fset, p, nil, 0)
		if perr != nil {
			return perr
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			// gate.Clear(root, cfg, now, name, ...) / clearGate(c, name, ...)
			idx := -1
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				if fn.Name == "clearGate" {
					idx = 1
				}
			case *ast.SelectorExpr:
				if x, ok := fn.X.(*ast.Ident); ok && fn.Sel.Name == "Clear" && strings.Contains(strings.ToLower(x.Name), "gate") {
					idx = 3
				}
			}
			if idx < 0 {
				return true
			}
			if idx >= len(call.Args) {
				return true
			}
			calls++
			pos := fset.Position(call.Pos())
			switch a := call.Args[idx].(type) {
			case *ast.BasicLit:
				t.Errorf("%s: gate name is a literal %s; use a gate constant", pos, a.Value)
			case *ast.SelectorExpr:
				if v, ok := consts[a.Sel.Name]; !ok || !known[v] {
					t.Errorf("%s: gate %s is not in the Registry", pos, a.Sel.Name)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls < 8 {
		t.Fatalf("found only %d Clear calls; the scan lost its callers", calls)
	}
}

func TestFreezeClearListed(t *testing.T) {
	for _, g := range Registry {
		if g.Name == FreezeClear {
			if g.Enforced() || len(g.Skills) == 0 {
				t.Errorf("freeze-clear is inventory only, got %+v", g)
			}
			return
		}
	}
	t.Error("freeze-clear missing from the Registry")
}
