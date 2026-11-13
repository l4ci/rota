package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

func explicitPRSlot(t *testing.T) (string, *Deps, *cliHost, string) {
	t.Helper()
	dir := workerProject(t, `{}`)
	var calls []string
	deps := workerForge(t, &calls)
	h := &cliHost{inSession: true}
	useHost(deps, h)
	gittest.Run(t, dir, "remote", "add", "origin", "https://github.com/o/r.git")
	if code, out, errs := rotaInWith(t, deps, dir, "worker", "pool", "init", "--slots", "1", "--base", "main"); code != 0 {
		t.Fatalf("pool: %d %s %s", code, out, errs)
	}
	if err := worker.Update(dir, func(d *worker.Doc) {
		s := d.Slot("w1")
		s.SetTask("8")
		s.SetHandle("rota:w1")
		d.QueuePR(worker.QueuedPR{From: "w1", Issue: "7", PR: "https://github.com/o/r/pull/7", Branch: "dana/7-x", Base: "main"})
	}); err != nil {
		t.Fatal(err)
	}
	brief := filepath.Join(t.TempDir(), "relay")
	if err := os.WriteFile(brief, []byte("use the cache"), 0600); err != nil {
		t.Fatal(err)
	}
	return dir, deps, h, brief
}

func TestWorkerDispatchExplicitPR(t *testing.T) {
	dir, deps, _, brief := explicitPRSlot(t)
	code, out, errs := rotaInWith(t, deps, dir, "worker", "dispatch", "w1", "--relay", "--pr", "7", "--body-file", brief)
	if code != 0 {
		t.Fatalf("explicit PR dispatch: %d %s %s", code, out, errs)
	}
	reg := worker.LoadRegistryTolerant(dir)
	if msg := worker.CheckApprovals("## Approvals\n- orchestrator relay round 1\n", reg.QueuedPR("7").Relays); msg != "" {
		t.Fatal(msg)
	}
	if len(reg.Slot("w1").Relays()) != 0 {
		t.Fatal("explicit PR relay attached to the current issue")
	}
	for _, flags := range [][]string{{"--pr", "7"}, {"--relay", "--pr", "0"}, {"--relay", "--pr", "-1"}, {"--relay", "--pr", "x"}, {"--confirm-not-submitted", "--pr", "7"}} {
		args := append([]string{"worker", "dispatch", "w1", "--body-file", brief}, flags...)
		if code, _, _ := rotaInWith(t, deps, dir, args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", flags, code)
		}
	}
}

func TestWorkerDispatchExplicitPRRefusesBeforeSending(t *testing.T) {
	for _, state := range []string{"CLOSED", "MERGED", "unlisted"} {
		t.Run(state, func(t *testing.T) {
			dir, deps, h, brief := explicitPRSlot(t)
			deps.TrackerOptions = []tracker.Option{tracker.WithExec(func(context.Context, string, string, []string, []byte) ([]byte, []byte, int, error) {
				return []byte(`{"state":"` + state + `"}`), nil, 0, nil
			}, func(n string) (string, error) { return "/fake/" + n, nil })}
			pr := "7"
			if state == "unlisted" {
				pr = "9"
			}
			path := filepath.Join(dir, ".rota", "workers.json")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			code, out, errs := rotaInWith(t, deps, dir, "worker", "dispatch", "w1", "--relay", "--pr", pr, "--body-file", brief, "--round", "55")
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s: exit=%d, host calls=%d, registry unchanged=%v", state, code, len(h.calls), bytes.Equal(before, after))
			if code != 4 || len(h.calls) != 0 || !bytes.Equal(before, after) {
				t.Fatalf("refused target: exit=%d %s %s", code, out, errs)
			}
		})
	}
}
