package worker

import (
	"fmt"
	"os"
	"os/exec"
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
		gitq(t, w.dir, "checkout", "-q", "-b", b, "main")
		trainWrite(t, w, files[b], b)
		gitq(t, w.dir, "checkout", "-q", "main")
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
	rev := func(ref string) string {
		out, err := exec.Command("git", "-C", dir, "rev-parse", ref).Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	cfg := ParseGateConfig(decodeCfg(t, `{"test":{"full":["false"],"docs":["true"]}}`))
	run := func(o TrainOpts) *trainRun {
		tr := &trainRun{e: Env{Git: git.Exec}, ctx: bg, root: dir, o: o, in: gateInput{cfg: cfg},
			baseSHA: rev("main"), heads: []string{rev("feat")}}
		tr.res.Members = []TrainMember{{Target: "feat", Branch: "feat", Tier: "docs"}}
		tr.pickTier()
		return tr
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
