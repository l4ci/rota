package cli

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/reap"
)

// linkedReapProject is a main checkout with a registered slot ben, an aged
// slots dir under its temp root, and a linked worktree with its own .rota/
// but no registry (workers.json is gitignored).
func linkedReapProject(t *testing.T) (main, linked string, d *Deps) {
	t.Helper()
	main, d = reapProject(t, nil, true)
	linked = filepath.Join(main, ".worktrees", "old")
	for _, dir := range []string{filepath.Join(main, ".rota", "tmp", "slots", "ben"), filepath.Join(linked, ".rota")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for path, body := range map[string]string{
		filepath.Join(main, ".rota", "workers.json"):      `{"slots":[{"name":"ben","worktree":".worktrees/ben"}]}`,
		filepath.Join(main, ".rota", "tmp", ".gitignore"): "*\n",
		// The worktree stays clean for git, as a checkout with tracked .rota/ is.
		filepath.Join(linked, ".rota", ".gitignore"): "*\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-3 * time.Hour)
	ageTree(t, filepath.Join(main, ".rota", "tmp", "slots", "ben"), old)
	ageTree(t, filepath.Join(main, ".rota", "tmp", "slots"), old)
	d.ProcessUses = func() ([]reap.ProcUse, error) { return nil, nil }
	return main, linked, d
}

func TestReapFromLinkedWorktreeHoldsRegisteredSlotTmp(t *testing.T) {
	main, linked, d := linkedReapProject(t)
	for name, dir := range map[string]string{"main checkout": main, "linked worktree": linked} {
		code, out, errOut := rotaInWith(t, d, dir, "--json", "reap", "--kind", "tmp", "--apply")
		if code != 0 {
			t.Fatalf("%s: %d %s %s", name, code, out, errOut)
		}
		if got := candIDs(data(t, out)); !slices.Equal(got, []string{"tmp:slots held"}) {
			t.Errorf("%s: candidates = %v, want the slots dir held", name, got)
		}
		if _, err := os.Stat(filepath.Join(main, ".rota", "tmp", "slots", "ben")); err != nil {
			t.Fatalf("%s: a registered slot's temp dir was deleted", name)
		}
	}
}

func TestDoctorLeftoversFromLinkedWorktreeHoldRegisteredSlotTmp(t *testing.T) {
	main, linked, d := linkedReapProject(t)
	for name, dir := range map[string]string{"main checkout": main, "linked worktree": linked} {
		in, warn := d.doctorReapInput(context.Background(), dir)
		if in == nil || len(warn) != 0 {
			t.Fatalf("%s: input %v, warnings %v", name, in, warn)
		}
		hint := strings.Join(doctorLeftovers(context.Background(), d.Git, dir, "", in, d.ProcessUses), "\n")
		if strings.Contains(hint, "--kind tmp") {
			t.Errorf("%s: doctor counts a registered slot's temp dir: %s", name, hint)
		}
	}
}
