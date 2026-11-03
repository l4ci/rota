package cli

import (
	"context"
	"fmt"
	"github.com/l4ci/rota/internal/gittest"
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

type excludedTrainForge struct {
	worker.Forge
	prs map[int]tracker.PRInfo
}

func (f excludedTrainForge) PRView(_ context.Context, n int) (tracker.PRInfo, error) {
	return f.prs[n], nil
}

func TestWorkerTrainAllExcludedOutput(t *testing.T) {
	dir := workerProject(t, `{"forge":{"provider":"github"},"ship":{"review":"light"},"test":{"full":["false"]}}`)
	origin := filepath.Join(t.TempDir(), "origin.git")
	gittest.Run(t, dir, "init", "-q", "--bare", "-b", "main", origin)
	gittest.Run(t, dir, "remote", "add", "origin", origin)
	gittest.Run(t, dir, "push", "-q", "origin", "main")
	// Refusals also publish gate reports. Keep that transport off the real forge.
	deps := useForge(t, &forge{answer: func(string, []string) (string, string, int) {
		return "", "report transport unavailable in fixture", 1
	}})
	if code, out, errs := rotaInWith(t, deps, dir, "worker", "pool", "init", "--slots", "2", "--base", "main"); code != 0 {
		t.Fatalf("pool: %d %s %s", code, out, errs)
	}
	for i := 1; i <= 2; i++ {
		gittest.Run(t, dir, "push", "-q", "origin", fmt.Sprintf("rota-worker/w%d", i))
		if _, err := worker.UpdateSlot(dir, fmt.Sprintf("w%d", i), func(s *worker.Slot) { s.SetPR(fmt.Sprintf("https://github.com/o/r/pull/%d", i+6)) }); err != nil {
			t.Fatal(err)
		}
	}
	base := gittest.Run(t, dir, "rev-parse", "main")
	for _, tc := range []struct {
		name, body, reason string
	}{
		{"reviews", decisionSectionsFixture, blockedby.ReviewMissing},
		{"sections and review", "", blockedby.AssumptionsMissing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prs := map[int]tracker.PRInfo{}
			for i, body := range []string{tc.body, decisionSectionsFixture} {
				prs[i+7] = tracker.PRInfo{Head: fmt.Sprintf("rota-worker/w%d", i+1), HeadSHA: base, Base: "main", State: "OPEN", Body: body}
			}
			deps.WorkerEnv = func() worker.Env {
				return worker.Env{Forge: func(string, string, tracker.Settings) (worker.Forge, error) {
					return excludedTrainForge{prs: prs}, nil
				}}
			}
			for _, jsonMode := range []bool{false, true} {
				t.Run(fmt.Sprintf("json=%t", jsonMode), func(t *testing.T) {
					args := []string{"worker", "train", "w1", "w2", "--base", "main", "--no-assign-check"}
					if jsonMode {
						args = append([]string{"--json"}, args...)
					}
					code, out, errs := rotaInWith(t, deps, dir, args...)
					if code != 4 || gittest.Run(t, dir, "rev-parse", "main") != base {
						t.Fatalf("must refuse without verifying (test.full is false) or landing: %d %s %s", code, out, errs)
					}
					if jsonMode {
						d := data(t, out)
						cs, ok := d["culprits"].([]any)
						if !ok || len(cs) != 2 || d["blockedBy"] != tc.reason || d["changed"] != false || len(d["verified"].([]any)) != 0 || len(d["landed"].([]any)) != 0 {
							t.Fatalf("incomplete JSON refusal: %s", out)
						}
						for i, reason := range []string{tc.reason, blockedby.ReviewMissing} {
							c := cs[i].(map[string]any)
							if c["target"] != fmt.Sprintf("w%d", i+1) || c["blockedBy"] != reason {
								t.Errorf("wrong culprit: %v", c)
							}
						}
						return
					}
					t.Logf("AC-2 plain text: exit=%d exclusion notes=%d", code, strings.Count(errs, "TRAIN-SKIP "))
					for i, reason := range []string{tc.reason, blockedby.ReviewMissing} {
						want := fmt.Sprintf("TRAIN-SKIP w%d (https://github.com/o/r/pull/%d) — blockedBy: %s;", i+1, i+7, reason)
						if !strings.Contains(errs, want) {
							t.Errorf("plain-text refusal must name every excluded member and reason; missing %q: %s %s", want, out, errs)
						}
						if reason == blockedby.ReviewMissing && !strings.Contains(errs, "needs a recorded review-quality verdict") {
							t.Errorf("missing review remedy: %s", errs)
						}
					}
				})
			}
		})
	}
}

func (f sectionPRForge) PRView(context.Context, int) (tracker.PRInfo, error) {
	return tracker.PRInfo{Head: "rota-worker/w1", HeadSHA: f.sha, Base: "main", State: "OPEN", Body: f.body}, nil
}

func TestWorkerSectionsRefusalData(t *testing.T) {
	dir := workerProject(t, `{"forge":{"provider":"github"},"ship":{"review":"light"},"test":{"full":["false"]}}`)
	origin := filepath.Join(t.TempDir(), "origin.git")
	gittest.Run(t, dir, "init", "-q", "--bare", "-b", "main", origin)
	gittest.Run(t, dir, "remote", "add", "origin", origin)
	gittest.Run(t, dir, "push", "-q", "origin", "main")
	deps := testDeps()
	if code, out, errs := rotaInWith(t, deps, dir, "worker", "pool", "init", "--slots", "1", "--base", "main"); code != 0 {
		t.Fatalf("pool: %d %s %s", code, out, errs)
	}
	gittest.Run(t, dir, "push", "-q", "origin", "rota-worker/w1")
	if _, err := worker.UpdateSlot(dir, "w1", func(s *worker.Slot) { s.SetPR("https://github.com/o/r/pull/7") }); err != nil {
		t.Fatal(err)
	}
	sha := strings.TrimSpace(gittest.Run(t, dir, "rev-parse", "origin/rota-worker/w1"))
	for _, tc := range []struct{ body, reason string }{
		{"## Rulings\nNone\n", blockedby.AssumptionsMissing},
		{"## Assumptions\nNone\n", blockedby.RulingsMissing},
		{decisionSectionsFixture, blockedby.ReviewMissing},
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
				if tc.reason == blockedby.ReviewMissing {
					t.Logf("AC-2 CLI: exit=%d excluded=%d", code, len(cs))
				}
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
