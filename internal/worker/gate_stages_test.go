package worker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/gittest"
)

// The remote seam needs a resolved identity, not forge initialization or the
// rest of a gate fixture. Errors retain their real cause in the public answer.
func TestGateRemoteStage(t *testing.T) {
	for _, c := range []struct {
		name    string
		exit    int
		err     error
		remote  bool
		message string
	}{
		{name: "remote exists", remote: true},
		{name: "absent remote", exit: 2, message: "no 'origin' remote"},
		{name: "failed probe", exit: 1, message: "exit 1"},
		{name: "failed process", err: errors.New("chdir: no such file or directory"), message: "could not run git"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := Env{Git: func(context.Context, string, ...string) (git.Result, error) {
				return git.Result{ExitCode: c.exit}, c.err
			}}.WithDefaults()
			run := gate{e: e, ctx: bg, root: t.TempDir(), o: GateOpts{Slot: "w1", Base: "main"}}
			original := GateResult{Slot: "w1", Base: "main", PR: ghURL}
			input := gateIdentity{gate: run, res: &original, pr: ghURL, branch: "w1"}
			out, result, done, err := run.stepRemote(input)
			if err != nil || done == c.remote || out.remote != c.remote {
				t.Fatalf("remote=%v done=%v err=%v result=%+v", out.remote, done, err, result)
			}
			if c.remote {
				if out.prNum != "7" || result.Verdict != "" {
					t.Fatalf("remote output: %+v result=%+v", out, result)
				}
			} else if result.Verdict != GateCheckBroke || !strings.Contains(result.Err, c.message) {
				t.Fatalf("verdict=%q message=%q; want check-broke containing %q", result.Verdict, result.Err, c.message)
			}
			if input.prNum != "" || original.Verdict != "" {
				t.Fatalf("stage changed its input: %+v / %+v", input, original)
			}
		})
	}
}

// References can be resolved from a supplied identity, with no forge or
// registry. The returned tip is the local commit a later stage must merge.
func TestGateReferenceStage(t *testing.T) {
	dir := gittest.NewRepo(t, "main")
	gittest.Commit(t, dir, "base", "base.txt", "base")
	gittest.Run(t, dir, "checkout", "-q", "-b", "worker")
	gittest.Commit(t, dir, "worker", "worker.txt", "worker")
	head := gittest.Run(t, dir, "rev-parse", "HEAD")
	run := gate{e: Env{Git: git.Exec}.WithDefaults(), ctx: bg, root: dir, o: GateOpts{Slot: "worker", Base: "main"}}
	answer := GateResult{Slot: "worker", Base: "main"}
	in := gateReferences{gateIdentity: gateIdentity{gate: run, res: &answer, branch: "worker"}}
	out, result, done, err := run.stepRefs(in)
	if err != nil || done || result.Verdict != "" {
		t.Fatalf("refs result=%+v done=%v err=%v", result, done, err)
	}
	if out.headRef != "worker" || out.baseRef != "main" || out.verified != head {
		t.Fatalf("refs head=%q base=%q verified=%q, want worker/main/%s", out.headRef, out.baseRef, out.verified, head)
	}
	if in.headRef != "" || in.verified != "" {
		t.Fatalf("stage changed its input: %+v", in)
	}
}
