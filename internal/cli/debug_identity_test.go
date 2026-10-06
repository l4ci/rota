package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/debugctr"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/verdict"
)

func debugIssueRun(t *testing.T, deps *Deps, root string, want int, args ...string) *jsonx.Object {
	t.Helper()
	code, env, stderr := rotaRunWith(t, deps, append([]string{"--json", "-C", root, "debug"}, args...)...)
	if code != want {
		t.Fatalf("debug %v: exit %d, want %d: %v %s", args, code, want, env, stderr)
	}
	if code == ExitUsage || code == ExitResolution {
		return nil
	}
	return issueData(t, env)
}

func TestDebugIssueAliases(t *testing.T) {
	root := verdictRepo(t)
	if err := os.WriteFile(filepath.Join(root, ".rota", "config.json"), []byte(issuesConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	deps := withTracker(t, issueFixture())
	debugIssueRun(t, deps, root, 0, "counter", "init", "#9")
	ctr, err := debugctr.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	st, err := ctr.State()
	if err != nil {
		t.Fatal(err)
	}
	if issueGet(st, "bug_id") != "9" {
		t.Fatalf("session identity: %v", st)
	}
	for i, ref := range []string{"9", "#9", "B9"} {
		d := debugIssueRun(t, deps, root, 0, "verdict", ref, "--verdict", "FAIL")
		if n, _ := jsonx.Int(issueGet(d, "failedFixes")); n != i+1 || issueGet(d, "bugId") != "9" {
			t.Fatalf("%s: %v", ref, d)
		}
	}
	for _, ref := range []string{"F9", "99", "#3"} {
		debugIssueRun(t, deps, root, 3, "verdict", ref, "--verdict", "FAIL")
		debugIssueRun(t, deps, root, 3, "counter", "init", ref)
		debugIssueRun(t, deps, root, 3, "reset", ref, "--reason", "invalid item")
	}
	debugIssueRun(t, deps, root, 4, "counter", "record-attempt", "--hypothesis", "h", "--commit", "abc")
	debugIssueRun(t, deps, root, 0, "counter", "clear")
	gitIn(t, root, "checkout", "-b", "another-session")
	for _, ref := range []string{"9", "#9", "B9", "b09"} {
		debugIssueRun(t, deps, root, 4, "counter", "init", ref)
	}
	d := debugIssueRun(t, deps, root, 0, "reset", "b9", "--reason", "new evidence", "--confirm", "--confirm-note", "yes")
	if issueGet(d, "cleared") != json.Number("3") {
		t.Fatalf("reset: %v", d)
	}
	debugIssueRun(t, deps, root, 0, "counter", "init", "B9")
	if s := verdict.Load(root); len(s.Items) != 1 || verdict.FailedFixes(s.Items["9"]) != 0 {
		t.Fatalf("store: %+v", s)
	}
}

func TestDebugLegacyAliases(t *testing.T) {
	root := verdictRepo(t)
	if err := os.WriteFile(filepath.Join(root, ".rota", "config.json"), []byte(issuesConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	deps := withTracker(t, issueFixture())
	ctr, err := debugctr.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ctr.Init("#9"); err != nil {
		t.Fatal(err)
	}
	// A reset of one old spelling cannot erase the other spellings' failures.
	b3Fails(t, root, "9", 1)
	b3Fails(t, root, "#9", 1)
	b3Fails(t, root, "B09", 1)
	if _, err := verdict.ResetItem(root, "B09", verdict.NewRecord(verdict.DebugReset, verdict.Reset, "s", verdict.Body{})); err != nil {
		t.Fatal(err)
	}
	b3Fails(t, root, "B09", 1)
	debugIssueRun(t, deps, root, 4, "counter", "record-attempt", "--hypothesis", "h", "--commit", "abc")
	debugIssueRun(t, deps, root, 4, "counter", "init", "b9")
	d := debugIssueRun(t, deps, root, 0, "reset", "9", "--reason", "new evidence", "--confirm", "--confirm-note", "yes")
	if issueGet(d, "cleared") != json.Number("3") {
		t.Fatalf("reset: %v", d)
	}
	debugIssueRun(t, deps, root, 0, "counter", "record-attempt", "--hypothesis", "h", "--commit", "abc")
	if list := verdict.Load(root).Items["9"]; len(list) != 6 {
		t.Fatalf("lost history: %+v", list)
	}
}

func TestDebugUmbrellaIdentities(t *testing.T) {
	for _, mode := range []string{"fresh", "legacy", "qualified-legacy"} {
		t.Run(mode, func(t *testing.T) {
			root, deps, _, _ := umbrellaProject(t)
			gitIn(t, root, "init")
			gitIn(t, root, "commit", "--allow-empty", "-m", "initial")
			if mode == "legacy" {
				b3Fails(t, root, "#1", 3)
			}
			if mode == "qualified-legacy" {
				b3Fails(t, root, "web:F1", 2)
				b3Fails(t, root, "web#1", 1)
				b3Fails(t, root, "api:#1", 1)
			}
			if mode == "fresh" {
				for _, ref := range []string{"web:F1", "web#1", "web:#1"} {
					debugIssueRun(t, deps, root, 0, "verdict", ref, "--verdict", "FAIL")
				}
			}
			debugIssueRun(t, deps, root, 4, "counter", "init", "1", "--repo", "web")
			debugIssueRun(t, deps, filepath.Join(root, "web"), 4, "counter", "init", "F1")
			debugIssueRun(t, deps, root, 2, "counter", "init", "1")
			apiWant := 0
			if mode == "legacy" {
				apiWant = 4
			}
			debugIssueRun(t, deps, root, apiWant, "counter", "init", "api:1")
			debugIssueRun(t, deps, root, 0, "reset", "web#1", "--reason", "new evidence", "--confirm", "--confirm-note", "yes")
			debugIssueRun(t, deps, root, 0, "counter", "init", "web:1")
			debugIssueRun(t, deps, root, apiWant, "counter", "init", "api:F1")
			apiCount := map[string]int{"fresh": 0, "legacy": 3, "qualified-legacy": 1}[mode]
			if verdict.FailedFixes(verdict.Load(root).Items["api:1"]) != apiCount {
				t.Fatal("web reset changed api")
			}
		})
	}
}

func TestDebugLegacyHistoryBeyondCap(t *testing.T) {
	root := verdictRepo(t)
	if err := os.WriteFile(filepath.Join(root, ".rota", "config.json"), []byte(issuesConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	deps := withTracker(t, issueFixture())
	for _, ref := range []string{"9", "#9", "B9"} {
		b3Fails(t, root, ref, 10)
	}
	// Consolidation and the next append must retain all 30 existing failures.
	d := debugIssueRun(t, deps, root, 0, "verdict", "b9", "--verdict", "PASS")
	if issueGet(d, "failedFixes") != json.Number("30") {
		t.Fatalf("lost failures during migration: %v", d)
	}
	debugIssueRun(t, deps, root, 4, "counter", "init", "#9")
	d = debugIssueRun(t, deps, root, 0, "reset", "B9", "--reason", "new evidence", "--confirm", "--confirm-note", "yes")
	if issueGet(d, "cleared") != json.Number("30") {
		t.Fatalf("reset lost failures: %v", d)
	}
	debugIssueRun(t, deps, root, 0, "counter", "init", "9")
}
