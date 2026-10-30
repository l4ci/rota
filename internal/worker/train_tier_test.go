package worker

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
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
