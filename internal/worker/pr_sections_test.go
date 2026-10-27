package worker

import (
	"context"
	"fmt"
	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/tracker"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestGateRequiresPRSections(t *testing.T) {
	w := newWorld(t, ghURL)
	w.forge("body", "## Rulings\nNone\n")
	res, err := w.gate(false, GateOpts{CheckOnly: true})
	if err != nil || res.Verdict != "assumptions-missing" || !res.Refused(err) || ClassifyVerdict(res.Verdict).BlockedBy != blockedby.AssumptionsMissing || !strings.Contains(res.Err, "## Assumptions") || res.Changed {
		t.Fatalf("missing Assumptions must refuse without landing: %+v %v", res, err)
	}
}

const completePRSections = "\n## Assumptions\nNone\n\n## Rulings\nNone\n"

func TestGatePRSectionPresence(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"rulings missing", "## Assumptions\nNone\n", "rulings-missing"},
		{"both missing", "An ordinary description", "assumptions-missing"},
		{"None", completePRSections, GateFresh},
		{"empty content", "## Assumptions\n## Rulings\n", GateFresh},
		{"wrong heading depth", "### Assumptions\n### Rulings\n", "assumptions-missing"},
		{"mentions only", "We discussed ## Assumptions and ## Rulings.", "assumptions-missing"},
		{"case and whitespace", "  ## assumptions  \r\n## RULINGS\r\n", GateFresh},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t, ghURL)
			w.forge("body", tc.body)
			res, err := w.gate(false, GateOpts{CheckOnly: true})
			if err != nil || res.Verdict != tc.want || res.Changed {
				t.Fatalf("%+v %v", res, err)
			}
		})
	}
}

// Each PR has independent forge state, while all three merge into one origin.
type sectionTrainForge struct {
	Forge
	prs map[int]*fakeForge
}

func (f sectionTrainForge) PRView(ctx context.Context, n int) (tracker.PRInfo, error) {
	return f.prs[n].PRView(ctx, n)
}
func (f sectionTrainForge) PRRequestMerge(ctx context.Context, n int, o tracker.MergeOpts) error {
	return f.prs[n].PRRequestMerge(ctx, n, o)
}

func sectionTrainWorld(t *testing.T, bodies []string) (*world, Env) {
	t.Helper()
	w := newWorld(t, ghURL)
	prs := map[int]*fakeForge{}
	var slots []string
	for i, body := range bodies {
		branch := fmt.Sprintf("w%d", i+1)
		if i > 0 {
			gitq(t, w.worker, "checkout", "-q", "-b", branch, "main")
			file := fmt.Sprintf("work%d.txt", i+1)
			if err := os.WriteFile(filepath.Join(w.worker, file), []byte(branch), 0644); err != nil {
				t.Fatal(err)
			}
			gitq(t, w.worker, "add", file)
			gitq(t, w.worker, "commit", "-qm", branch)
			gitq(t, w.worker, "push", "-q", "origin", branch)
		}
		pw := *w
		pw.forgeDB = filepath.Join(filepath.Dir(w.forgeDB), fmt.Sprintf("pr%d.json", i))
		pw.forge("origin", w.origin)
		pw.forge("head", branch)
		pw.forge("sha", gitq(t, w.worker, "rev-parse", branch))
		pw.forge("base", "main")
		pw.forge("state", "OPEN")
		pw.forge("body", body)
		prs[i+7] = &fakeForge{w: &pw, provider: "github"}
		slots = append(slots, fmt.Sprintf(`{"name":%q,"branch":%q,"pr":"https://github.com/o/r/pull/%d"}`, branch, branch, i+7))
	}
	if err := os.WriteFile(RegistryPath(w.dir), []byte(`{"slots":[`+strings.Join(slots, ",")+`]}`), 0644); err != nil {
		t.Fatal(err)
	}
	e := w.env(false)
	e.Forge = func(provider, dir string, cfg tracker.Settings) (Forge, error) {
		return sectionTrainForge{Forge: prs[7], prs: prs}, nil
	}
	return w, e
}

func TestTrainPRSectionsFiltersBeforeVerify(t *testing.T) {
	w, e := sectionTrainWorld(t, []string{completePRSections, "## Rulings\nNone\n", completePRSections})
	w.setConfig(`{"test":{"full":["test -f work.txt && test ! -f work2.txt && test -f work3.txt"]}}`)
	res, err := e.Train(bg, w.dir, TrainOpts{Base: "main", Targets: []string{"w1", "w2", "w3"}})
	if err != nil || res.Verdict != GatePass || !slices.Equal(res.Landed, []string{"w1", "w3"}) || len(res.Culprits) != 1 || res.Culprits[0].Target != "w2" || res.Culprits[0].BlockedBy != blockedby.AssumptionsMissing || len(res.Verified) != 1 || w.onMain("work2.txt") {
		t.Fatalf("a missing section must exclude only its PR before verification: %+v %v", res, err)
	}
}

func TestTrainPRSectionsAllMissing(t *testing.T) {
	w, e := sectionTrainWorld(t, []string{"## Rulings\nNone\n", "## Assumptions\nNone\n", ""})
	w.setConfig(`{"test":{"full":["false"]}}`)
	res, err := e.Train(bg, w.dir, TrainOpts{Base: "main", Targets: []string{"w1", "w2", "w3"}})
	if err != nil || !res.Refused(err) || res.Verdict != GateAssumptionsMissing || res.Changed || len(res.Landed) != 0 || len(res.Verified) != 0 || len(res.Culprits) != 3 {
		t.Fatalf("all section culprits must land and verify nothing: %+v %v", res, err)
	}
	for i, want := range []string{blockedby.AssumptionsMissing, blockedby.RulingsMissing, blockedby.AssumptionsMissing} {
		c := res.Culprits[i]
		if c.Target != fmt.Sprintf("w%d", i+1) || c.PR != fmt.Sprintf("https://github.com/o/r/pull/%d", i+7) || c.BlockedBy != want {
			t.Errorf("culprit %d: %+v", i, c)
		}
	}
}

func TestTrainPRSectionsExplicitOrder(t *testing.T) {
	w, e := sectionTrainWorld(t, []string{completePRSections, "## Rulings\nNone\n", completePRSections})
	res, err := e.Train(bg, w.dir, TrainOpts{Base: "main", Targets: []string{"w1", "w2", "w3"}, Order: []string{"9", "8", "7"}})
	if err != nil || res.Verdict != GatePass || !slices.Equal(res.Landed, []string{"w3", "w1"}) || len(res.Culprits) != 1 {
		t.Fatalf("filter must preserve the explicit order of the remaining PRs: %+v %v", res, err)
	}
}
