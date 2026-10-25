package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
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

// The worker's gate config drives noFast, even when the round's config has a
// fast command. Cover both sides so an unconditional warning cannot pass.
func TestRoundStartNoFastFromWorkerConfig(t *testing.T) {
	for _, tc := range []struct {
		name, cfg string
		want      bool
	}{
		{"full only", `{"test":{"fast":[],"full":["true"]}}`, true},
		{"fast and full", `{"test":{"fast":["fast"],"full":["true"]}}`, false},
		{"neither", `{}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := orchProject(t, false)
			write(t, filepath.Join(dir, ".rota", "config.json"), `{"git":{"baseBranch":"feat/x"},"test":{"fast":["round-fast"],"full":["true"]}}`)
			write(t, filepath.Join(dir, ".rota", "BACKLOG.md"), "# TODO\n\n## Bugs\n\n## Features\n\n## Tasks\n\n## Completed\n")
			cfg, err := jsonx.Decode([]byte(tc.cfg))
			if err != nil {
				t.Fatal(err)
			}
			deps := depsIn(dir)
			workerEnv := deps.WorkerEnv
			deps.WorkerEnv = func() worker.Env {
				e := workerEnv()
				e.Config = func(string) any { return cfg }
				return e
			}
			code, out, errOut := rotaInWith(t, deps, dir, "--json", "round", "start", "--holder-pid", strconv.Itoa(os.Getpid()), "--base", "feat/x")
			if code != 0 {
				t.Fatalf("round start: %d %s %s", code, out, errOut)
			}
			got, present := data(t, out)["noFast"]
			if tc.want && got != true || !tc.want && present {
				t.Errorf("noFast = %v (present %v), want true iff %v", got, present, tc.want)
			}
			wantWarnings := 0
			if tc.want {
				wantWarnings = 1
			}
			if n := strings.Count(errOut, "test.fast is empty"); n != wantWarnings {
				t.Errorf("warning count = %d, want %d: %s", n, wantWarnings, errOut)
			}
		})
	}
}
