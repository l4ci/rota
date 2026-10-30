package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// finishDeps is testDeps with ROTA_SLOT unset (or slot), whatever the
// developer's shell holds.
func finishDeps(slot string) *Deps { return finishWith(testDeps(), slot) }

func finishWith(d *Deps, slot string) *Deps {
	real := d.Getenv
	d.Getenv = func(k string) string {
		if k == "ROTA_SLOT" {
			return slot
		}
		return real(k)
	}
	return d
}

// finishFixture is shipFixture with two open features and a branch rota/fin
// that touches `files` files and names F71 (and F72 when both).
func finishFixture(t *testing.T, files int, both bool) string {
	t.Helper()
	shipDeterministic(t)
	work := shipFixture(t, "")
	write(t, filepath.Join(work, ".git", "info", "exclude"), ".rota/status.json*\n.rota/proof/\n")
	b, _ := os.ReadFile(filepath.Join(work, ".rota", "BACKLOG.md"))
	open := "## Features\n\n- **[F71] [Minor] First feature.** One.\n- **[F72] [Minor] Second feature.** Two.\n"
	write(t, filepath.Join(work, ".rota", "BACKLOG.md"), strings.Replace(string(b), "## Features\n", open, 1))
	gitT(t, work, "add", "-A")
	gitT(t, work, "commit", "-q", "-m", "open items")
	var cs [][3]string
	for i := 0; i < files; i++ {
		body := ""
		if i == 0 {
			body = "Refs [F71]"
			if both {
				body += " and [F72]"
			}
		}
		cs = append(cs, [3]string{"f" + string(rune('a'+i)) + ".txt", "feat: part", body})
	}
	shipBranchOf(t, work, "rota/fin", cs...)
	return work
}

func finishRun(t *testing.T, d *Deps, dir string, wantCode int, args ...string) map[string]any {
	t.Helper()
	o := trRunWith(t, d, dir, "", append([]string{"ship", "finish", "--json"}, args...)...)
	if o.code != wantCode {
		t.Fatalf("exit %d, want %d\n%s%s", o.code, wantCode, o.stdout, o.stderr)
	}
	env := envelope(t, o.stdout)
	d2, _ := env["data"].(map[string]any)
	return d2
}

func finishProve(t *testing.T, dir string, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if o := trRun(t, dir, "", "proof", "add", id, "--check", "unit", "--result", "PASS", "--evidence", "ok", "--sha", "abc"); o.code != 0 {
			t.Fatalf("proof add %s: %d %s", id, o.code, o.stderr)
		}
	}
}

func sub(m map[string]any, k string) map[string]any { v, _ := m[k].(map[string]any); return v }

func TestShipFinishPRPath(t *testing.T) {
	work := finishFixture(t, 5, false)
	finishProve(t, work, "F71")
	if o := trRun(t, work, "", "status", "add", "rota/fin", "--items", "F71"); o.code != 0 {
		t.Fatalf("status add: %+v", o)
	}
	d := finishDeps("")
	data := finishRun(t, d, work, 0, "rota/fin")
	if data["branch"] != "rota/fin" || data["base"] != "main" || data["changed"] != true || data["autonomy"] != "off" {
		t.Errorf("data %v", data)
	}
	if data["commit"] != gitT(t, work, "rev-parse", "--short", "rota/fin") {
		t.Errorf("commit %v", data["commit"])
	}
	if st := sub(data, "status"); st["removed"] != float64(1) {
		t.Errorf("status %v", st)
	}
	if it := sub(data, "items"); !reflect.DeepEqual(it["completed"], []any{"F71"}) || !reflect.DeepEqual(it["referenced"], []any{"F71"}) || it["skipped"] != nil {
		t.Errorf("items %v", it)
	}
	if sg := sub(data, "signals"); sg["files"] != float64(5) || sg["items"] != float64(1) || sg["debugCycles"] != float64(0) {
		t.Errorf("signals %v", sg)
	}
	l := sub(data, "learn")
	if l["action"] != "fire" || !strings.Contains(l["reason"].(string), "5 files") || !strings.Contains(l["nudge"].(string), "/rota-learn") {
		t.Errorf("learn %v", l)
	}
	if sub(data, "docs")["action"] != "skip" || sub(data, "docs")["reason"] != "docs.afterWork off" {
		t.Errorf("docs %v", sub(data, "docs"))
	}
	if b, _ := os.ReadFile(filepath.Join(work, ".rota", "BACKLOG.md")); !strings.Contains(string(b), "~~**[F71]") {
		t.Errorf("F71 not completed:\n%s", b)
	}
	if o := trRun(t, work, "", "status", "show", "rota/fin", "--json"); !strings.Contains(o.stdout, `"active": false`) {
		t.Errorf("status entry left: %s", o.stdout)
	}

	// Second run: nothing left to do.
	data = finishRun(t, d, work, 0, "rota/fin")
	if data["changed"] != false {
		t.Errorf("second run changed: %v", data)
	}
	if it := sub(data, "items"); !reflect.DeepEqual(it["alreadyDone"], []any{"F71"}) || !reflect.DeepEqual(it["completed"], []any{}) {
		t.Errorf("items %v", it)
	}
	for _, g := range []string{"learn", "docs", "release"} {
		if x := sub(data, g); x["action"] != "skip" || x["reason"] != "already finished" {
			t.Errorf("%s %v", g, x)
		}
	}
}

func TestShipFinishAfterMerge(t *testing.T) {
	work := finishFixture(t, 5, false)
	finishProve(t, work, "F71")
	if o := trRun(t, work, "merge: fin\n", "ship", "merge", "rota/fin", "--body-file", "-"); o.code != 0 {
		t.Fatalf("merge: %+v", o)
	}
	if gitT(t, work, "branch", "--list", "rota/fin") != "" {
		t.Fatal("branch still there")
	}
	data := finishRun(t, finishDeps(""), work, 0, "rota/fin")
	if data["commit"] != gitT(t, work, "rev-parse", "--short", "HEAD") {
		t.Errorf("commit %v", data["commit"])
	}
	if sg := sub(data, "signals"); sg["files"] != float64(5) || sg["items"] != float64(1) {
		t.Errorf("signals %v", sg)
	}
	if it := sub(data, "items"); !reflect.DeepEqual(it["completed"], []any{"F71"}) {
		t.Errorf("items %v", it)
	}
	if sub(data, "learn")["action"] != "fire" {
		t.Errorf("learn %v", sub(data, "learn"))
	}
	// A commit that is not a merge is exit 3; the base branch needs a name.
	gitT(t, work, "checkout", "-q", "HEAD^1")
	if o := trRunWith(t, finishDeps(""), work, "", "ship", "finish", "rota/gone", "--json"); o.code != 3 {
		t.Errorf("not a merge commit: %+v", o)
	}
	gitT(t, work, "checkout", "-q", "main")
	if o := trRunWith(t, finishDeps(""), work, "", "ship", "finish", "--json"); o.code != 2 {
		t.Errorf("no branch name on base: %+v", o)
	}
}

func TestShipFinishRoundWorker(t *testing.T) {
	work := finishFixture(t, 2, false)
	before := shipTree(t, work)
	o := trRunWith(t, finishDeps("ben"), work, "", "ship", "finish", "rota/fin", "--json")
	if o.code != 4 || !strings.Contains(o.stdout, `"blockedBy": "round worker"`) {
		t.Fatalf("%+v", o)
	}
	if !reflect.DeepEqual(before, shipTree(t, work)) {
		t.Error("a refusal changed .rota/")
	}
}

func TestShipFinishUmbrella(t *testing.T) {
	u := umbrella(t)
	shipBranchOf(t, filepath.Join(u, "svc"), "feat/x", [3]string{"x.txt", "feat: x", ""})
	if o := trRunWith(t, finishDeps(""), u, "", "ship", "finish", "feat/x", "--json"); o.code != 2 {
		t.Errorf("umbrella root: %+v", o)
	}
	if o := trRunWith(t, finishDeps(""), u, "", "ship", "finish", "feat/x", "--repo", "svc", "--json"); o.code != 4 || !strings.Contains(o.stdout, `"blockedBy": "umbrella"`) {
		t.Errorf("--repo: %+v", o)
	}
}

func TestShipFinishUnproven(t *testing.T) {
	work := finishFixture(t, 2, true)
	finishProve(t, work, "F71")
	data := finishRun(t, finishDeps(""), work, 4, "rota/fin")
	it := sub(data, "items")
	if !reflect.DeepEqual(it["completed"], []any{"F71"}) || !reflect.DeepEqual(it["unproven"], []any{"F72"}) || data["blockedBy"] != "proof missing" {
		t.Errorf("data %v", data)
	}
	if data["changed"] != true {
		t.Errorf("changed %v", data["changed"])
	}
}

func TestShipFinishIssueBackend(t *testing.T) {
	shipDeterministic(t)
	cfg := `{"forge": {"provider": "github"}, "issues": {"source": "github"}}`
	work := shipFixture(t, cfg)
	shipBranchOf(t, work, "feat/t", [3]string{"a.txt", "feat: export [F07]", ""})
	deps := finishWith(withTracker(t, issueFixture()), "")
	data := finishRun(t, deps, work, 0, "feat/t")
	if it := sub(data, "items"); it["skipped"] != "issue backend" || !reflect.DeepEqual(it["completed"], []any{}) {
		t.Errorf("items %v", it)
	}
}
