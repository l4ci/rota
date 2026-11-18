package worker

import (
	"context"
	"fmt"
	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/landing"
	"github.com/l4ci/rota/internal/tracker"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestGateRequiresPRSections(t *testing.T) {
	w := newWorld(t, ghURL)
	w.forge("body", "## Rulings\nNone\n")
	res, err := w.gate(false, GateOpts{CheckOnly: true})
	if err != nil || res.Verdict != "assumptions-missing" || !res.Refused(err) || landing.ClassifyVerdict(res.Verdict).BlockedBy != blockedby.AssumptionsMissing || !strings.Contains(res.Err, "## Assumptions") || res.Changed {
		t.Fatalf("missing Assumptions must refuse without landing: %+v %v", res, err)
	}
}

func TestGateReportsAllMissingPRSections(t *testing.T) {
	w := newWorld(t, ghURL)
	w.forge("body", "An ordinary description")
	res, err := w.gate(false, GateOpts{CheckOnly: true})
	named := 0
	for _, heading := range []string{"## Assumptions", "## Rulings"} {
		if strings.Contains(res.Err, heading) {
			named++
		}
		if !strings.Contains(res.Err, heading) || !strings.Contains(res.Hint, heading) {
			t.Errorf("refusal and remedy must name %s: %+v", heading, res)
		}
	}
	t.Logf("AC-3 gate: missing headings named=%d", named)
	if err != nil || res.Verdict != landing.GateAssumptionsMissing || !res.Refused(err) || res.Changed {
		t.Fatalf("all missing sections must refuse without landing: %+v %v", res, err)
	}
}

const completePRSections = "\n## Assumptions\nNone\n\n## Rulings\nNone\n"

func TestGatePRSectionPresence(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"rulings missing", "## Assumptions\nNone\n", "rulings-missing"},
		{"both missing", "An ordinary description", "assumptions-missing"},
		{"None", completePRSections, landing.GateFresh},
		{"empty content", "## Assumptions\n## Rulings\n", landing.GateFresh},
		{"wrong heading depth", "### Assumptions\n### Rulings\n", "assumptions-missing"},
		{"mentions only", "We discussed ## Assumptions and ## Rulings.", "assumptions-missing"},
		{"case and whitespace", "  ## assumptions  \r\n## RULINGS\r\n", landing.GateFresh},
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
			gittest.Run(t, w.worker, "checkout", "-q", "-b", branch, "main")
			file := fmt.Sprintf("work%d.txt", i+1)
			if err := os.WriteFile(filepath.Join(w.worker, file), []byte(branch), 0644); err != nil {
				t.Fatal(err)
			}
			gittest.Run(t, w.worker, "add", file)
			gittest.Run(t, w.worker, "commit", "-qm", branch)
			gittest.Run(t, w.worker, "push", "-q", "origin", branch)
		}
		pw := *w
		pw.forgeDB = filepath.Join(filepath.Dir(w.forgeDB), fmt.Sprintf("pr%d.json", i))
		pw.forge("origin", w.origin)
		pw.forge("head", branch)
		pw.forge("sha", gittest.Run(t, w.worker, "rev-parse", branch))
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
	if err != nil || res.Verdict != landing.GatePass || !slices.Equal(res.Landed, []string{"w1", "w3"}) || len(res.Culprits) != 1 || res.Culprits[0].Target != "w2" || res.Culprits[0].BlockedBy != blockedby.AssumptionsMissing || len(res.Verified) != 1 || w.onMain("work2.txt") {
		t.Fatalf("a missing section must exclude only its PR before verification: %+v %v", res, err)
	}
}

func TestTrainPRSectionsAllMissing(t *testing.T) {
	w, e := sectionTrainWorld(t, []string{"## Rulings\nNone\n", "## Assumptions\nNone\n", ""})
	w.setConfig(`{"test":{"full":["false"]}}`)
	res, err := e.Train(bg, w.dir, TrainOpts{Base: "main", Targets: []string{"w1", "w2", "w3"}})
	if err != nil || !res.Refused(err) || res.Verdict != landing.GateAssumptionsMissing || res.Changed || len(res.Landed) != 0 || len(res.Verified) != 0 || len(res.Culprits) != 3 {
		t.Fatalf("all section culprits must land and verify nothing: %+v %v", res, err)
	}
	for i, want := range []string{blockedby.AssumptionsMissing, blockedby.RulingsMissing, blockedby.AssumptionsMissing} {
		c := res.Culprits[i]
		if c.Target != fmt.Sprintf("w%d", i+1) || c.PR != fmt.Sprintf("https://github.com/o/r/pull/%d", i+7) || c.BlockedBy != want {
			t.Errorf("culprit %d: %+v", i, c)
		}
	}
	c := res.Culprits[2]
	named := 0
	for _, heading := range []string{"## Assumptions", "## Rulings"} {
		if strings.Contains(c.Gate.Err, heading) {
			named++
		}
		if !strings.Contains(c.Gate.Err, heading) || !strings.Contains(c.Gate.Hint, heading) {
			t.Errorf("pre-filter refusal and remedy must name %s: %+v", heading, c)
		}
	}
	t.Logf("AC-3 train: missing headings named=%d", named)
}

func TestTrainPRSectionsExplicitOrder(t *testing.T) {
	w, e := sectionTrainWorld(t, []string{completePRSections, "## Rulings\nNone\n", completePRSections})
	res, err := e.Train(bg, w.dir, TrainOpts{Base: "main", Targets: []string{"w1", "w2", "w3"}, Order: []string{"9", "8", "7"}})
	if err != nil || res.Verdict != landing.GatePass || !slices.Equal(res.Landed, []string{"w3", "w1"}) || len(res.Culprits) != 1 {
		t.Fatalf("filter must preserve the explicit order of the remaining PRs: %+v %v", res, err)
	}
}

func TestTrainPRSectionsPrecedesOtherChecks(t *testing.T) {
	w, e := sectionTrainWorld(t, []string{completePRSections, "## Rulings\nNone\n", completePRSections})
	// This PR also fails provenance. A static pre-filter must remove it
	// before the ordinary member checks can stop the remaining train.
	if _, err := UpdateSlot(w.dir, "w2", func(s *Slot) {
		s.AppendRelay(newRelayEntry(39, time.Unix(0, 0), "use the cache"))
	}); err != nil {
		t.Fatal(err)
	}
	res, err := e.Train(bg, w.dir, TrainOpts{Base: "main", Targets: []string{"w1", "w2", "w3"}})
	if err != nil || res.Verdict != landing.GatePass || !slices.Equal(res.Landed, []string{"w1", "w3"}) || len(res.Culprits) != 1 || res.Culprits[0].Target != "w2" {
		t.Fatalf("static section filter must precede provenance: %+v %v", res, err)
	}
}

// A member the section prefilter drops lists every record gap the full gate
// check finds, in the order `rota worker gate` lists them, not only the sections.
func TestTrainPRSectionsSkipListsEveryGap(t *testing.T) {
	w, e := sectionTrainWorld(t, []string{completePRSections, "Closes #5\n", completePRSections})
	reg, err := os.ReadFile(RegistryPath(w.dir))
	if err != nil {
		t.Fatal(err)
	}
	patched := strings.Replace(string(reg), `"name":"w2"`, `"name":"w2","outcome":{"kind":"partial","summary":["a","b","c"]}`, 1)
	if patched == string(reg) {
		t.Fatal("registry has no w2 slot to patch")
	}
	if err := os.WriteFile(RegistryPath(w.dir), []byte(patched), 0644); err != nil {
		t.Fatal(err)
	}
	w.setConfig(`{"test":{"full":["true"]}}`)
	res, err := e.Train(bg, w.dir, TrainOpts{Base: "main", Targets: []string{"w1", "w2", "w3"}})
	if err != nil || len(res.Culprits) != 1 || res.Culprits[0].Target != "w2" {
		t.Fatalf("w2 must be skipped alone: %+v %v", res, err)
	}
	c := res.Culprits[0]
	if got := gapKinds(c.Gate); strings.Join(got, ",") != landing.GateOutcomeOpen+","+landing.GateAssumptionsMissing {
		t.Fatalf("culprit gaps = %v, want outcome-open then assumptions-missing", got)
	}
	var skip string
	for _, n := range res.Notes {
		if strings.HasPrefix(n, "TRAIN-SKIP w2") {
			skip = n
		}
	}
	if !strings.Contains(skip, "partial") && !strings.Contains(skip, "outcome") || !strings.Contains(skip, "## Assumptions") {
		t.Errorf("skip note must name every gap: %q", skip)
	}
}
