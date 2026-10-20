package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/worker"
)

// round.slots 3 with five roster slots registered: start reports and uses the
// first three, plus a fourth that still holds work, marked draining.
func TestRoundStartReportsSlotSet(t *testing.T) {
	dir := orchProject(t, false)
	os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(`{"git":{"baseBranch":"feat/x"},"round":{"slots":3,"roster":["ben","dana","nia","kit","sam"]}}`), 0o644)
	os.WriteFile(filepath.Join(dir, ".rota", "BACKLOG.md"), []byte("# TODO\n\n## Bugs\n\n## Features\n\n## Tasks\n\n## Completed\n"), 0o644)
	start := func(args ...string) string {
		t.Helper()
		code, out, errOut := rotaStdin(t, dir, "", append([]string{"round", "start", "--holder-pid", strconv.Itoa(os.Getpid()), "--base", "feat/x"}, args...)...)
		if code != 0 {
			t.Fatalf("round start: %d %s %s", code, out, errOut)
		}
		return out
	}
	slotLines := func(out string) (names []string, draining []string) {
		for _, l := range strings.Split(out, "\n") {
			f := strings.Split(l, "\t")
			if len(f) >= 4 && strings.HasPrefix(f[2], "park/") {
				names = append(names, f[0])
				if f[len(f)-1] == "draining" {
					draining = append(draining, f[0])
				}
			}
		}
		return
	}
	start("--slots", "5")
	if names, _ := slotLines(start()); strings.Join(names, ",") != "ben,dana,nia" {
		t.Fatalf("idle extras: slots %v, want ben,dana,nia", names)
	}
	if _, err := worker.UpdateSlot(dir, "kit", func(s *worker.Slot) { s.SetTask("41") }); err != nil {
		t.Fatal(err)
	}
	names, draining := slotLines(start())
	if strings.Join(names, ",") != "ben,dana,nia,kit" || strings.Join(draining, ",") != "kit" {
		t.Fatalf("slots %v draining %v, want kit draining after the first three", names, draining)
	}
	if reg := worker.LoadRegistryTolerant(dir); reg.Slot("sam") == nil {
		t.Error("sam must stay registered")
	}
	before, _ := os.ReadFile(filepath.Join(dir, ".rota", "workers.json"))
	_, out, _ := rotaStdin(t, dir, "", "round", "status")
	var kit string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "kit\t") {
			kit = l
		}
	}
	if !strings.HasSuffix(kit, "\tdraining") {
		t.Errorf("round status kit row = %q, want it marked draining:\n%s", kit, out)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, ".rota", "workers.json")); string(after) != string(before) {
		t.Error("round status wrote the registry")
	}
}

// An empty test.fast with test.full set warns once at round start (#1169).
func TestRoundStartWarnsEmptyFast(t *testing.T) {
	dir := orchProject(t, false)
	os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(`{"git":{"baseBranch":"feat/x"},"test":{"fast":[],"full":["true"]}}`), 0o644)
	os.WriteFile(filepath.Join(dir, ".rota", "BACKLOG.md"), []byte("# TODO\n\n## Bugs\n\n## Features\n\n## Tasks\n\n## Completed\n"), 0o644)
	code, out, errOut := rotaStdin(t, dir, "", "round", "start", "--holder-pid", strconv.Itoa(os.Getpid()), "--base", "feat/x")
	if code != 0 {
		t.Fatalf("round start: %d %s %s", code, out, errOut)
	}
	if n := strings.Count(errOut, "test.fast is empty"); n != 1 {
		t.Errorf("warning count = %d, want 1:\n%s", n, errOut)
	}
}
