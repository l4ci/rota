package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// noPRForge is the gate's forge for slots with no PR: it lists none.
type noPRForge struct{ worker.Forge }

func (noPRForge) OpenPRs(context.Context) ([]tracker.PR, error) { return nil, nil }
func (noPRForge) ClosedNumbers(string) []int                    { return nil }

type fixedIssues struct{}

func (fixedIssues) Get(_ context.Context, key tracker.IssueKey, _ bool) (tracker.Issue, error) {
	return tracker.Issue{Key: key, Title: "t", State: "open"}, nil
}
func (fixedIssues) RemoveLabels(context.Context, tracker.IssueKey, []string) error { return nil }
func (fixedIssues) ClosesIssue(tracker.IssueKey, tracker.ClosingSource) bool       { return true }

// harvestLandProject is an issue-backed pool of two slots: w1 holds #7, w2
// holds #9, each with one commit and no PR, so a gate or train lands locally.
// Learning comments land on the issues named in learn.
func harvestLandProject(t *testing.T, learn ...string) (string, *Deps) {
	t.Helper()
	dir := workerProject(t, `{"forge": {"provider": "github"}, "issues": {"source": "github"}, "ship":{"review":"none"},"test":{"full":["true"]}}`)
	deps := trustedDeps(t, issueFixture(), harvestTrust{})
	deps.TrackerOptions = []tracker.Option{tracker.WithExec(func(context.Context, string, string, []string, []byte) ([]byte, []byte, int, error) {
		return []byte("[]"), nil, 0, nil
	}, func(n string) (string, error) { return "/fake/" + n, nil })}
	inner := deps.WorkerEnv
	deps.WorkerEnv = func() worker.Env {
		e := inner()
		e.Forge = func(string, string, tracker.Settings) (worker.Forge, error) { return noPRForge{}, nil }
		e.Issues = func(string, tracker.Settings) (worker.Issues, error) { return fixedIssues{}, nil }
		return e
	}
	rotaInWith(t, deps, dir, "worker", "pool", "init", "--slots", "2", "--base", "main")
	doc := registryDoc(t, dir)
	for i, s := range []string{"w1", "w2"} {
		wt := filepath.Join(dir, ".worktrees", s)
		os.WriteFile(filepath.Join(wt, s+".txt"), []byte(s), 0o644)
		gittest.Run(t, wt, "add", s+".txt")
		gittest.Run(t, wt, "commit", "-q", "-m", s)
		doc["slots"].([]any)[i].(map[string]any)["task"] = []string{"7", "9"}[i]
	}
	b, _ := json.Marshal(doc)
	os.WriteFile(filepath.Join(dir, ".rota", "workers.json"), b, 0o644)
	for _, id := range learn {
		body := bodyFile(t, "tier fold needs a clean tree\n")
		if code, _, stderr := rotaRunWith(t, deps, "--json", "-C", dir, "item", "comment", "add", id, "--kind", "learning", "--body-file", body); code != 0 {
			t.Fatalf("learning on #%s: exit %d: %s", id, code, stderr)
		}
	}
	return dir, deps
}

func TestWorkerGateHarvestHint(t *testing.T) {
	gate := func(t *testing.T, dir string, deps *Deps, args ...string) (int, string) {
		t.Helper()
		code, _, errOut := rotaInWith(t, deps, dir, append([]string{"worker", "gate", "w1", "--base", "main", "--no-assign-check"}, args...)...)
		return code, errOut
	}
	t.Run("hint after a pass", func(t *testing.T) {
		dir, deps := harvestLandProject(t, "7")
		code, errOut := gate(t, dir, deps)
		if code != 0 || !strings.Contains(errOut, "HARVEST") || !strings.Contains(errOut, "#7 (1)") || !strings.Contains(errOut, "rota item comment list 7 --kind learning") {
			t.Fatalf("exit %d, stderr:\n%s", code, errOut)
		}
	})
	t.Run("no hint without learnings", func(t *testing.T) {
		dir, deps := harvestLandProject(t)
		if code, errOut := gate(t, dir, deps); code != 0 || strings.Contains(errOut, "HARVEST") {
			t.Fatalf("exit %d, stderr:\n%s", code, errOut)
		}
	})
	t.Run("no hint for another issue's learning", func(t *testing.T) {
		dir, deps := harvestLandProject(t, "9")
		if code, errOut := gate(t, dir, deps); code != 0 || strings.Contains(errOut, "HARVEST") {
			t.Fatalf("exit %d, stderr:\n%s", code, errOut)
		}
	})
	t.Run("no hint under --check-only", func(t *testing.T) {
		dir, deps := harvestLandProject(t, "7")
		if code, errOut := gate(t, dir, deps, "--check-only"); code != 0 || strings.Contains(errOut, "HARVEST") {
			t.Fatalf("exit %d, stderr:\n%s", code, errOut)
		}
	})
}

func TestWorkerTrainHarvestHint(t *testing.T) {
	train := func(t *testing.T, dir string, deps *Deps) (int, string) {
		t.Helper()
		code, _, errOut := rotaInWith(t, deps, dir, "worker", "train", "w1", "w2", "--base", "main", "--no-assign-check")
		return code, errOut
	}
	t.Run("hint names the member with a learning", func(t *testing.T) {
		dir, deps := harvestLandProject(t, "7")
		code, errOut := train(t, dir, deps)
		line := ""
		for _, l := range strings.Split(errOut, "\n") {
			if strings.Contains(l, "HARVEST") {
				line = l
			}
		}
		if code != 0 || !strings.Contains(line, "#7 (1)") || strings.Contains(line, "#9") {
			t.Fatalf("exit %d, stderr:\n%s", code, errOut)
		}
	})
	t.Run("no hint without learnings", func(t *testing.T) {
		dir, deps := harvestLandProject(t)
		if code, errOut := train(t, dir, deps); code != 0 || strings.Contains(errOut, "HARVEST") {
			t.Fatalf("exit %d, stderr:\n%s", code, errOut)
		}
	})
}
