package worker

import (
	"fmt"
	"github.com/l4ci/rota/internal/gittest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/git"
)

// tierTrainWorld is trainWorld with each branch adding the file its map value
// names, so a member can be docs-only (b1.md) or code (b2.txt).
func tierTrainWorld(t *testing.T, cfg string, files map[string]string) *world {
	t.Helper()
	w := newWorld(t, "")
	var slots []string
	branches := make([]string, 0, len(files))
	for b := range files {
		branches = append(branches, b)
	}
	slices.Sort(branches)
	for _, b := range branches {
		gittest.Run(t, w.dir, "checkout", "-q", "-b", b, "main")
		trainWrite(t, w, files[b], b)
		gittest.Run(t, w.dir, "checkout", "-q", "main")
		slots = append(slots, fmt.Sprintf(`{"name":"%s","branch":"%s"}`, b, b))
	}
	os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"), []byte(`{"slots":[`+strings.Join(slots, ",")+`]}`), 0o644)
	w.setConfig(cfg)
	return w
}

// AC-4, AC-5, AC-8: the train verifies with the highest tier of its members.
func TestTrainTier(t *testing.T) {
	const cfg = `{"test":{"full":["false"],"e2e":["false"],"docs":["true"]}}`
	w := tierTrainWorld(t, cfg, map[string]string{"b1": "b1.md", "b2": "b2.md"})
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2"}})
	if err != nil || res.Verdict != GatePass || res.Tier != "docs" || !slices.Equal(res.Skipped, []string{"test.full", "test.e2e"}) {
		t.Fatalf("docs train: %s tier %q skipped %v err %v\n%s\n%s", res.Verdict, res.Tier, res.Skipped, err, res.Err, strings.Join(res.Notes, "\n"))
	}
	if !w.onMain("b1.md") || !w.onMain("b2.md") || !strings.Contains(strings.Join(res.Notes, "\n"), "GATE-TIER train — docs") {
		t.Errorf("docs train did not land both or print the tier: %+v", res)
	}

	// one code member makes the whole train full
	w = tierTrainWorld(t, cfg, map[string]string{"b1": "b1.md", "b2": "b2.txt"})
	res, err = w.train(TrainOpts{Targets: []string{"b1", "b2"}})
	if err != nil || res.Verdict != GateVerifyFailed || res.Tier != "full" || len(res.Skipped) != 0 {
		t.Fatalf("mixed train ran test.docs: %s tier %q skipped %v err %v", res.Verdict, res.Tier, res.Skipped, err)
	}

	// an empty test.docs trains exactly as before
	w = tierTrainWorld(t, `{"test":{"full":["false"]}}`, map[string]string{"b1": "b1.md"})
	if res, err = w.train(TrainOpts{Targets: []string{"b1"}}); err != nil || res.Verdict != GateVerifyFailed || len(res.Skipped) != 0 {
		t.Fatalf("empty test.docs: %s skipped %v err %v", res.Verdict, res.Skipped, err)
	}
}

// The train classifies the heads it merges, not only what each member's
// check saw: a member re-pushed with code after a docs-only check verifies
// full, and so does a train that syncs another base in.
func TestTrainTierPinsTheMergedHeads(t *testing.T) {
	dir := tierRepo(t, "x.go") // feat changes code
	rev := func(ref string) string { return gittest.Run(t, dir, "rev-parse", ref) }
	cfg := ParseGateConfig(decodeCfg(t, `{"test":{"full":["false"],"docs":["true"]}}`))
	run := func(o TrainOpts) trainPlanning {
		tr := &trainRun{e: Env{Git: git.Exec}, ctx: bg, root: dir, o: o}
		p := trainPlanning{trainPlan: trainPlan{in: gateInput{cfg: cfg}, baseSHA: rev("main"), heads: []string{rev("feat")}}, res: TrainResult{Members: []TrainMember{{Target: "feat", Branch: "feat", Tier: "docs"}}}}
		return tr.pickTier(p)
	}
	if tr := run(TrainOpts{}); tr.res.Tier != "full" || tr.tierCfg != nil {
		t.Fatalf("a docs-checked member whose head holds code verified as %q", tr.res.Tier)
	}
	docs := tierRepo(t, "README.md")
	dir = docs
	if tr := run(TrainOpts{}); tr.res.Tier != "docs" || tr.tierCfg == nil {
		t.Fatalf("a docs-only head: tier %q", tr.res.Tier)
	}
	if tr := run(TrainOpts{SyncBase: "release"}); tr.res.Tier != "full" || tr.tierCfg != nil {
		t.Fatalf("a synced train verified as %q", tr.res.Tier)
	}
}

// Members that are each small are classified together: their lines add up, so
// three 90-line members under smallBelow 100 are a full train.
func TestTrainTierSumsMemberLines(t *testing.T) {
	dir := t.TempDir()
	gittest.Init(t, dir, "main")
	gittest.Run(t, dir, "commit", "-q", "--allow-empty", "-m", "base")
	base := gittest.Run(t, dir, "rev-parse", "main")
	var heads []string
	for _, n := range []string{"a", "b", "c"} {
		gittest.Run(t, dir, "checkout", "-q", "-b", n, base)
		if err := os.WriteFile(filepath.Join(dir, n+".go"), []byte(strings.Repeat("x\n", 90)), 0o644); err != nil {
			t.Fatal(err)
		}
		gittest.Run(t, dir, "add", n+".go")
		gittest.Run(t, dir, "commit", "-q", "-m", n)
		heads = append(heads, gittest.Run(t, dir, "rev-parse", n))
	}
	cfg := ParseGateConfig(decodeCfg(t, `{"test":{"full":["false"],"small":["true"]},"change":{"smallBelow":100,"smallPaths":["*.go"]}}`))
	pick := func(n int) trainPlanning {
		tr := &trainRun{e: Env{Git: git.Exec}, ctx: bg, root: dir}
		p := trainPlanning{trainPlan: trainPlan{in: gateInput{cfg: cfg}, baseSHA: base, heads: heads[:n]}}
		for i := 0; i < n; i++ {
			p.res.Members = append(p.res.Members, TrainMember{Target: "m", Branch: "m", Tier: "small"})
		}
		return tr.pickTier(p)
	}
	if tr := pick(1); tr.res.Tier != "small" {
		t.Fatalf("one 90-line member: tier %q, want small", tr.res.Tier)
	}
	if tr := pick(3); tr.res.Tier != "full" || tr.tierCfg != nil {
		t.Fatalf("three 90-line members: tier %q, want full", tr.res.Tier)
	}
}
