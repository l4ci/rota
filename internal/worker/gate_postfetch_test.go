package worker

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/landing"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/tracker"
)

// mergeSpy flags the forge merge request, so a test can break git only after it.
type mergeSpy struct {
	Forge
	merged *bool
}

func (s mergeSpy) PRRequestMerge(ctx context.Context, pr int, o tracker.MergeOpts) error {
	err := s.Forge.PRRequestMerge(ctx, pr, o)
	*s.merged = err == nil
	return err
}

// postMergeFetchEnv fails the first failures fetches after the forge merge and
// counts every fetch made after it.
func postMergeFetchEnv(w *world, failures int, fetches *int) Env {
	e := w.env(false)
	merged := false
	forge, run := e.Forge, git.Exec
	e.Forge = func(provider, dir string, s tracker.Settings) (Forge, error) {
		f, err := forge(provider, dir, s)
		return mergeSpy{f, &merged}, err
	}
	e.Git = func(ctx context.Context, dir string, args ...string) (git.Result, error) {
		if merged && len(args) > 0 && args[0] == "fetch" {
			if *fetches++; *fetches <= failures {
				return git.Result{ExitCode: 128, Stderr: "fatal: unable to access the remote\n"}, nil
			}
		}
		return run(ctx, dir, args...)
	}
	return e
}

func TestGatePostMergeFetchIsRetried(t *testing.T) {
	w := newWorld(t, ghURL)
	fetches := 0
	res, err := postMergeFetchEnv(w, 1, &fetches).Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main"})
	if err != nil || res.Verdict != landing.GatePass || !res.Changed || fetches != 2 || !w.onMain("work.txt") {
		t.Fatalf("verdict=%s changed=%v fetches=%d err=%v: %s", res.Verdict, res.Changed, fetches, err, res.Err)
	}
}

func TestGatePostMergeFetchFailureIsLandedNotBroken(t *testing.T) {
	w := newWorld(t, ghURL)
	fetches := 0
	res, err := postMergeFetchEnv(w, 100, &fetches).Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != landing.GateMergedRemotely || !res.Changed || !res.VerifySkipped || res.SHA == "" || res.MergeCommit != res.SHA {
		t.Fatalf("verdict=%s changed=%v verifySkipped=%v sha=%q: %s", res.Verdict, res.Changed, res.VerifySkipped, res.SHA, res.Err)
	}
	if fetches != gateFetchTries {
		t.Errorf("fetches after the merge = %d, want %d", fetches, gateFetchTries)
	}
	for _, want := range []string{"git fetch origin", "unable to access the remote", "do not re-merge"} {
		if !strings.Contains(res.Err, want) {
			t.Errorf("err should name %q: %s", want, res.Err)
		}
	}
	es, err := ledger.Load(w.dir)
	if err != nil || !slices.ContainsFunc(es, func(e ledger.Entry) bool { return e.Kind == ledger.KindMerge }) {
		t.Errorf("no merge entry in the ledger: %+v %v", es, err)
	}
}
