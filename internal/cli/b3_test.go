package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/verdict"
)

// ---- B3: verdict refusals, the Iron Law, loop-only flags

// b3Record stores one verdict for a branch key under root.
func b3Record(t *testing.T, root, key, kind, v, sha string) {
	t.Helper()
	if _, err := verdict.AddBranch(root, key, verdict.NewRecord(kind, v, sha, verdict.Body{})); err != nil {
		t.Fatal(err)
	}
}

// b3ShipPRWith ships feat/x as a PR with the given deps.
func b3ShipPRWith(t *testing.T, deps *Deps, work string) (trOut, map[string]any) {
	t.Helper()
	o := trRunWith(t, deps, work, "body\n", "ship", "pr", "feat/x", "--title", "T", "--body-file", "-", "--json")
	return o, envelope(t, o.stdout)
}

func TestShipPRRefusedByVerdict(t *testing.T) {
	shipDeterministic(t)
	work := shipFixture(t, "")
	shipBranchOf(t, work, "feat/x", [3]string{"x.txt", "work", ""})
	f := shipPRForge("https://github.com/fake/repo/pull/1")
	deps := useForge(t, f)
	tip := gitT(t, work, "rev-parse", "--short", "feat/x")

	b3Record(t, work, "feat/x", verdict.ReviewQuality, verdict.Fail, "old1234")
	o, env := b3ShipPRWith(t, deps, work)
	d, _ := env["data"].(map[string]any)
	if o.code != 4 || d["blockedBy"] != "verdict" || d["kind"] != "review-quality" || d["verdict"] != "FAIL" ||
		d["sha"] != "old1234" || d["stale"] != true || d["changed"] != false {
		t.Fatalf("exit %d data %v", o.code, d)
	}
	if !strings.Contains(o.stdout, "rota verdict show feat/x") {
		t.Errorf("hint missing: %s", o.stdout)
	}
	if len(f.calls) != 0 || gitT(t, work, "ls-remote", "origin", "feat/x") != "" {
		t.Errorf("a refused ship pushed or called the forge: %q", f.calls)
	}

	// A fresh FAIL at the tip is not stale; a newer PASS clears it.
	b3Record(t, work, "feat/x", verdict.ReviewQuality, verdict.Fail, tip)
	if _, env = b3ShipPRWith(t, deps, work); env["data"].(map[string]any)["stale"] != false {
		t.Errorf("tip FAIL reported stale: %v", env["data"])
	}
	b3Record(t, work, "feat/x", verdict.ReviewQuality, verdict.Pass, tip)
	if o, _ = b3ShipPRWith(t, deps, work); o.code != 0 {
		t.Fatalf("a newer PASS must clear: exit %d\n%s%s", o.code, o.stdout, o.stderr)
	}
}

func TestShipPRSecondOpinionAdvisoryUnderCodex(t *testing.T) {
	shipDeterministic(t)
	cfg := `{"backlog":{"backend":"file"},"issues":{"provider":"github","retryWaitSeconds":0},"ship":{"secondOpinionRunner":"codex"}}`
	work := shipFixture(t, cfg)
	shipBranchOf(t, work, "feat/x", [3]string{"x.txt", "work", ""})
	deps := useForge(t, shipPRForge("https://github.com/fake/repo/pull/1"))
	b3Record(t, work, "feat/x", verdict.SecondOpinion, verdict.Fail, "abc1234")
	if o, _ := b3ShipPRWith(t, deps, work); o.code != 0 {
		t.Fatalf("codex advisory must not refuse: exit %d\n%s%s", o.code, o.stdout, o.stderr)
	}
	// A review FAIL still blocks under codex.
	b3Record(t, work, "feat/x", verdict.ReviewSpec, verdict.Fail, "abc1234")
	if o, _ := b3ShipPRWith(t, deps, work); o.code != 4 {
		t.Fatalf("review FAIL under codex: exit %d", o.code)
	}
}

func TestShipMergeRefusedByVerdict(t *testing.T) {
	shipDeterministic(t)
	work := shipFixture(t, "")
	shipBranchOf(t, work, "feat/x", [3]string{"x.txt", "work", ""})
	b3Record(t, work, "feat/x", verdict.SecondOpinion, verdict.Fail, "abc1234")
	head := gitT(t, work, "rev-parse", "HEAD")
	o := trRun(t, work, "merge: x\n", "ship", "merge", "feat/x", "--body-file", "-", "--json")
	d, _ := envelope(t, o.stdout)["data"].(map[string]any)
	if o.code != 4 || d["blockedBy"] != "verdict" || d["kind"] != "second-opinion" || d["changed"] != false {
		t.Fatalf("exit %d data %v", o.code, d)
	}
	if gitT(t, work, "rev-parse", "HEAD") != head || gitT(t, work, "branch", "--list", "feat/x") == "" {
		t.Error("a refused merge changed the repo")
	}
	// CONCERNS never blocks.
	b3Record(t, work, "feat/x", verdict.SecondOpinion, verdict.Concerns, "abc1234")
	if o := trRun(t, work, "merge: x\n", "ship", "merge", "feat/x", "--body-file", "-"); o.code != 0 {
		t.Fatalf("CONCERNS must not refuse: exit %d\n%s%s", o.code, o.stdout, o.stderr)
	}
}

func TestShipPRMergeRefusedByVerdict(t *testing.T) {
	f := a8Fixture()
	root, deps := a8Project(t, f)
	// PR 11 would also fail the proof check; the verdict refusal comes first
	// and records no changes-requested.
	b3Record(t, root, "feat/eleven", verdict.ReviewSpec, verdict.Fail, "abc1234")
	code, data, msg := a8RunWith(t, deps, root, "ship", "pr-merge", "11")
	if code != 4 || data["blockedBy"] != "verdict" || data["pr"] != float64(11) || data["kind"] != "review-spec" || data["changed"] != false {
		t.Fatalf("exit %d data %v %s", code, data, msg)
	}
	if len(f.merged) != 0 {
		t.Fatal("merged a blocked PR")
	}
	if is := f.Fake.Issues[1]; slices.Contains(is.Labels, "changes-requested") || !slices.Contains(is.Labels, "needs-review") {
		t.Fatalf("a refusal changed issue 2: %+v", is)
	}
	// An unblocked PR still merges.
	if code, _, msg := a8RunWith(t, deps, root, "ship", "pr-merge", "10"); code != 0 {
		t.Fatalf("PR 10: exit %d %s", code, msg)
	}
}

func TestShipPRMergeNoHeadBranchWarns(t *testing.T) {
	f := a8Fixture()
	f.prs[0].Branch = ""
	root, deps := a8Project(t, f)
	o := trRunWith(t, deps, root, "", "--json", "ship", "pr-merge", "10")
	env := envelope(t, o.stdout)
	if o.code != 0 {
		t.Fatalf("exit %d\n%s%s", o.code, o.stdout, o.stderr)
	}
	w, _ := env["warnings"].([]any)
	if len(w) != 1 || w[0] != "could not resolve the head branch of PR 10; verdicts not checked" {
		t.Errorf("warnings %v", env["warnings"])
	}
}

func b3Fails(t *testing.T, root, bug string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := verdict.AddItem(root, bug, verdict.NewRecord(verdict.DebugFix, verdict.Fail, "abc1234", verdict.Body{})); err != nil {
			t.Fatal(err)
		}
	}
}

func TestIronLawRefusesInitAndRecordAttempt(t *testing.T) {
	dir := verdictRepo(t)
	b3Fails(t, dir, "B07", 2)
	if code, _, _ := rotaIn(t, dir, "debug", "counter", "init", "B07"); code != 0 {
		t.Fatalf("2 failed fixes must not refuse init: exit %d", code)
	}
	b3Fails(t, dir, "B07", 1)
	code, out, _ := rotaIn(t, dir, "debug", "counter", "record-attempt", "--hypothesis", "h", "--commit", "c", "--json")
	d := data(t, out)
	if code != 4 || d["blockedBy"] != "iron law" || d["bugId"] != "B07" || d["failedFixes"] != float64(3) || d["changed"] != false {
		t.Fatalf("record-attempt: exit %d data %v", code, d)
	}
	if !strings.Contains(out, "rota debug reset B07") {
		t.Errorf("hint missing: %s", out)
	}
	if _, out, _ := rotaIn(t, dir, "debug", "counter", "show", "--json"); strings.Contains(out, `"attempts": [{`) {
		t.Errorf("a refused record-attempt recorded: %s", out)
	}
	// A cleared session file does not reset the count.
	rotaIn(t, dir, "debug", "counter", "clear")
	code, out, _ = rotaIn(t, dir, "debug", "counter", "init", "B07", "--json")
	if d := data(t, out); code != 4 || d["blockedBy"] != "iron law" {
		t.Fatalf("init: exit %d data %v", code, d)
	}
	if _, err := os.Stat(filepath.Join(dir, ".rota", "debug")); err == nil {
		if code, _, _ := rotaIn(t, dir, "debug", "counter", "show"); code == 0 {
			t.Error("a refused init created the session file")
		}
	}
	// Another item is unaffected, and debug verdict is never refused.
	if code, _, _ := rotaIn(t, dir, "debug", "counter", "init", "B08"); code != 0 {
		t.Errorf("B08: exit %d", code)
	}
	if code, _, _ := rotaIn(t, dir, "debug", "verdict", "B07", "--verdict", "FAIL"); code != 0 {
		t.Errorf("debug verdict refused: exit %d", code)
	}
}

func TestDebugReset(t *testing.T) {
	dir := verdictRepo(t)
	for _, c := range [][]string{{"B07"}, {"B07", "--reason", "  "}, {" ", "--reason", "r"}, {"B07", "--reason", "r", "--confirm"}} {
		if code, _, _ := rotaIn(t, dir, append([]string{"debug", "reset"}, c...)...); code != 2 {
			t.Errorf("%v: exit %d, want 2", c, code)
		}
	}
	// Nothing to clear: exit 0, no record.
	code, out, _ := rotaIn(t, dir, "debug", "reset", "B07", "--reason", "r", "--json")
	if d := data(t, out); code != 0 || d["changed"] != false || d["cleared"] != float64(0) {
		t.Fatalf("empty reset: %d %v", code, d)
	}
	if len(verdict.Load(dir).Items["B07"]) != 0 {
		t.Error("an empty reset wrote a record")
	}
	b3Fails(t, dir, "B07", 3)
	if code, _, _ := rotaIn(t, dir, "debug", "counter", "init", "B07"); code != 4 {
		t.Fatalf("init before reset: exit %d", code)
	}
	// Without --confirm the debug-reset gate refuses and nothing changes.
	code, out, _ = rotaIn(t, dir, "debug", "reset", "B07", "--reason", "new angle", "--json")
	if d := data(t, out); code != 4 || d["gate"] != "debug-reset" || d["changed"] != false {
		t.Fatalf("unconfirmed reset: %d %v", code, d)
	}
	if len(verdict.Load(dir).Items["B07"]) != 3 {
		t.Fatal("a refused reset wrote a record")
	}
	code, out, _ = rotaIn(t, dir, "debug", "reset", "B07", "--reason", "new angle", "--confirm", "--confirm-note", "yes, reset it", "--json")
	d := data(t, out)
	if code != 0 || d["bugId"] != "B07" || d["cleared"] != float64(3) || d["failedFixes"] != float64(0) || d["changed"] != true {
		t.Fatalf("reset: %d %v", code, d)
	}
	if b, err := os.ReadFile(filepath.Join(dir, ".rota", "gate-audit.jsonl")); err != nil || !strings.Contains(string(b), `"gate": "debug-reset"`) || !strings.Contains(string(b), "yes, reset it") {
		t.Errorf("audit line missing: %s %v", b, err)
	}
	list := verdict.Load(dir).Items["B07"]
	if last := list[len(list)-1]; len(list) != 4 || last.Kind != verdict.DebugReset || last.Verdict != "RESET" || last.Summary != "new angle" || last.Sha == "" {
		t.Errorf("stored %+v", list)
	}
	if code, _, _ := rotaIn(t, dir, "debug", "counter", "init", "B07"); code != 0 {
		t.Errorf("init after reset: exit %d", code)
	}
	// debug verdict counts from the reset.
	_, out, _ = rotaIn(t, dir, "debug", "verdict", "B07", "--verdict", "FAIL", "--json")
	if d := data(t, out); d["failedFixes"] != float64(1) || d["next"] != "hypothesize" {
		t.Errorf("after reset: %v", d)
	}
	// verdict add still rejects the kind.
	if code, _, _ := rotaIn(t, dir, "verdict", "add", "--kind", "debug-reset", "--verdict", "RESET"); code != 2 {
		t.Errorf("verdict add debug-reset: exit %d", code)
	}
}

// --auto-loop was removed with loop autonomy: cobra rejects it as unknown.
func TestAutoLoopFlagRemoved(t *testing.T) {
	dir := gitRepo(t)
	write(t, filepath.Join(dir, ".rota", "config.json"), `{"backlog":{"backend":"file"}}`)
	for _, args := range [][]string{
		{"design", "add", "B07", "--title", "T", "--auto-loop"},
		{"plan", "add", "M01-B07", "--title", "T", "--auto-loop"},
	} {
		if code, _, errOut := rotaIn(t, dir, args...); code != 2 || !strings.Contains(errOut, `unknown flag "--auto-loop"`) {
			t.Errorf("%v: exit %d %q", args, code, errOut)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".rota", "designs", "B07.md")); err == nil {
		t.Error("a refused design add wrote the file")
	}
}
