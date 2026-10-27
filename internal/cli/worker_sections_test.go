package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

const decisionSectionsFixture = "\n## Assumptions\nNone\n## Rulings\nNone\n"

type sectionPRForge struct {
	worker.Forge
	sha, body string
}

func (f sectionPRForge) PRView(context.Context, int) (tracker.PRInfo, error) {
	return tracker.PRInfo{Head: "rota-worker/w1", HeadSHA: f.sha, Base: "main", State: "OPEN", Body: f.body}, nil
}

func TestWorkerSectionsRefusalData(t *testing.T) {
	dir := workerProject(t, `{"forge":{"provider":"github"},"ship":{"review":"none"},"test":{"full":["false"]}}`)
	origin := filepath.Join(t.TempDir(), "origin.git")
	gitT(t, dir, "init", "-q", "--bare", "-b", "main", origin)
	gitT(t, dir, "remote", "add", "origin", origin)
	gitT(t, dir, "push", "-q", "origin", "main")
	deps := testDeps()
	if code, out, errs := rotaInWith(t, deps, dir, "worker", "pool", "init", "--slots", "1", "--base", "main"); code != 0 {
		t.Fatalf("pool: %d %s %s", code, out, errs)
	}
	gitT(t, dir, "push", "-q", "origin", "rota-worker/w1")
	if _, err := worker.UpdateSlot(dir, "w1", func(s *worker.Slot) { s.SetPR("https://github.com/o/r/pull/7") }); err != nil {
		t.Fatal(err)
	}
	sha := strings.TrimSpace(gitT(t, dir, "rev-parse", "origin/rota-worker/w1"))
	for _, tc := range []struct{ body, reason string }{
		{"## Rulings\nNone\n", blockedby.AssumptionsMissing},
		{"## Assumptions\nNone\n", blockedby.RulingsMissing},
	} {
		deps.WorkerEnv = func() worker.Env {
			return worker.Env{Forge: func(string, string, tracker.Settings) (worker.Forge, error) {
				return sectionPRForge{sha: sha, body: tc.body}, nil
			}}
		}
		for _, args := range [][]string{
			{"worker", "gate", "w1", "--base", "main", "--check-only"},
			{"worker", "train", "w1", "--base", "main", "--plan"},
		} {
			code, out, errs := rotaInWith(t, deps, dir, append([]string{"--json"}, args...)...)
			d := data(t, out)
			if code != 4 || d["blockedBy"] != tc.reason || d["verdict"] != tc.reason || d["changed"] != false {
				t.Fatalf("%v: %d %s %s", args, code, out, errs)
			}
			if args[1] == "train" {
				cs, ok := d["culprits"].([]any)
				if !ok || len(cs) != 1 {
					t.Fatalf("missing culprit list: %s", out)
				}
				c := cs[0].(map[string]any)
				if c["target"] != "w1" || c["pr"] != "https://github.com/o/r/pull/7" || c["blockedBy"] != tc.reason {
					t.Fatalf("wrong culprit: %s", out)
				}
			}
		}
	}
}
