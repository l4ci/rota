package cli

import (
	"context"
	"github.com/l4ci/rota/internal/limits"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/worker"
)

// roundFixture is a project with one parked and one working worktree, and a
// roundEnv with a fake host and no forge, so nothing reaches herdr or gh.
func roundFixture(t *testing.T, agents []host.Agent) string {
	t.Helper()
	root := gitRepo(t)
	for name, br := range map[string]string{"ben": "park/ben", "dana": "dana/58-x"} {
		c := exec.Command("git", "worktree", "add", "-q", "-b", br, filepath.Join(root, ".worktrees", name), "HEAD")
		c.Dir = root
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
	}
	saved := roundEnv
	t.Cleanup(func() { roundEnv = saved })
	roundEnv = func(context.Context, string) round.Env {
		e := round.Env{Git: worker.ExecGit, Base: "feat/x", ForgeErr: "fake: no forge"}
		if agents != nil {
			e.Snapshot = func(context.Context) ([]host.Agent, error) { return agents, nil }
			e.HostName = "herdr"
		}
		return e
	}
	return root
}

func TestRoundStatusAndReconcile(t *testing.T) {
	root := roundFixture(t, []host.Agent{})
	// Re-point the fake at the real worktree path now that root is known.
	roundEnv = func(context.Context, string) round.Env {
		return round.Env{Git: worker.ExecGit, Base: "feat/x", HostName: "herdr", ForgeErr: "fake: no forge",
			Snapshot: func(context.Context) ([]host.Agent, error) {
				return []host.Agent{{Tab: "w1:t1", Name: "dana", Cwd: filepath.Join(root, ".worktrees", "dana"), Status: "working"}}, nil
			}}
	}
	code, out, _ := rotaIn(t, root, "--json", "round", "status")
	if code != 0 {
		t.Fatalf("status exit %d: %s", code, out)
	}
	d := data(t, out)
	rows := d["slots"].([]any)
	if len(rows) != 2 || d["host"] != "herdr" || !reflect.DeepEqual(d["unavailable"], []any{"forge"}) {
		t.Fatalf("data = %v", d)
	}
	dana := rows[1].(map[string]any)
	if dana["name"] != "dana" || dana["issue"] != "58" || dana["hostState"] != "working" || dana["registered"] != false {
		t.Errorf("dana = %v", dana)
	}

	code, out, _ = rotaIn(t, root, "--json", "round", "reconcile")
	d = data(t, out)
	if code != 0 || d["changed"] != false || d["clean"] != false || len(d["drift"].([]any)) != 2 {
		t.Fatalf("reconcile = %d %v", code, d)
	}
	if _, err := os.Stat(worker.RegistryPath(root)); err == nil {
		t.Fatal("reconcile without --apply wrote the registry")
	}

	code, out, _ = rotaIn(t, root, "--json", "round", "reconcile", "--apply")
	d = data(t, out)
	if code != 0 || d["changed"] != true || len(d["repaired"].([]any)) != 2 || len(d["drift"].([]any)) != 0 {
		t.Fatalf("reconcile --apply = %d %v", code, d)
	}
	if s := worker.LoadRegistry(root).Slot("dana"); s == nil || worker.Str(s, "task") != "58" {
		t.Errorf("registry = %v", s)
	}

	// An open escalation shows in both verbs and on its slot's row.
	if err := worker.Update(root, jsonx.NewObject(), func(doc *jsonx.Object) {
		e := jsonx.NewObject()
		for _, kv := range [][2]any{{"id", "e1"}, {"kind", "pr"}, {"number", 3}, {"slot", "dana"}, {"title", "Which option?"},
			{"commentId", "1"}, {"sentAt", "2026-10-03T10:00:00Z"}, {"notified", false}, {"status", "pending"}} {
			e.Set(kv[0].(string), kv[1])
		}
		doc.Set("escalations", []any{e})
	}); err != nil {
		t.Fatal(err)
	}
	_, out, _ = rotaIn(t, root, "--json", "round", "status")
	d = data(t, out)
	esc := d["escalations"].([]any)
	if len(esc) != 1 || esc[0].(map[string]any)["id"] != "e1" || esc[0].(map[string]any)["status"] != "pending" {
		t.Errorf("status escalations = %v", d["escalations"])
	}
	for _, r := range d["slots"].([]any) {
		if r := r.(map[string]any); r["name"] == "dana" && !reflect.DeepEqual(r["escalations"], []any{"e1"}) {
			t.Errorf("dana row escalations = %v", r["escalations"])
		}
	}
	_, out, _ = rotaIn(t, root, "--json", "round", "reconcile")
	if d = data(t, out); len(d["escalations"].([]any)) != 1 || d["clean"] != true {
		t.Errorf("reconcile escalations = %v", d)
	}
}

func TestRoundVerbsRejectRepoAndArgs(t *testing.T) {
	root := roundFixture(t, nil)
	for _, args := range [][]string{{"round", "status", "x"}, {"round", "reconcile", "x"}, {"--repo", "a", "round", "status"}} {
		if code, _, _ := rotaIn(t, root, args...); code != 2 {
			t.Errorf("%v exit %d, want 2", args, code)
		}
	}
}

func TestRoundStatusAndReconcileListWaitingLimits(t *testing.T) {
	root := roundFixture(t, []host.Agent{})
	addLimit(t, root, waitingEntry(time.Date(2026, 10, 3, 15, 0, 0, 0, time.UTC)))
	resolved := waitingEntry(time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC))
	resolved.Status = limits.StatusResumed
	addLimit(t, root, resolved)
	for _, verb := range []string{"status", "reconcile"} {
		code, out, _ := rotaIn(t, root, "--json", "round", verb)
		d := data(t, out)
		rows, _ := d["limits"].([]any)
		if code != 0 || len(rows) != 1 || rows[0].(map[string]any)["id"] != "l1" {
			t.Fatalf("%s: %d %v", verb, code, d)
		}
		if verb == "reconcile" && len(d["drift"].([]any)) != 2 {
			t.Errorf("a waiting limit must add no drift: %v", d["drift"])
		}
		_, text, _ := rotaIn(t, root, "round", verb)
		if !strings.Contains(text, "limit\tl1\twaiting\torchestrator") {
			t.Errorf("%s text: %q", verb, text)
		}
	}
}
