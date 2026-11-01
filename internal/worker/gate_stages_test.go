package worker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/tracker"
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
			out, result, done, err := run.stepRemote(original, ghURL)
			if err != nil || done == c.remote || out != c.remote {
				t.Fatalf("remote=%v done=%v err=%v result=%+v", out, done, err, result)
			}
			if c.remote {
				if result.Verdict != "" {
					t.Fatalf("remote output: %+v result=%+v", out, result)
				}
			} else if result.Verdict != GateCheckBroke || !strings.Contains(result.Err, c.message) {
				t.Fatalf("verdict=%q message=%q; want check-broke containing %q", result.Verdict, result.Err, c.message)
			}
			if original.Verdict != "" {
				t.Fatalf("stage changed its input: %+v / %+v", out, original)
			}
		})
	}
}

// A supplied answer can be reused for another check. Following a PR must not
// overwrite notes owned by that caller, even when its slice has spare capacity.
func TestGateSlotHeadStageOwnsNotes(t *testing.T) {
	backing := []string{"existing", "caller-owned"}
	answer := GateResult{Notes: backing[:1]}
	run := gate{e: Env{}.WithDefaults(), ctx: bg, root: t.TempDir(), o: GateOpts{Slot: "kit", CheckOnly: true}}
	target := GateTarget{Name: "kit", Task: "#5", Branch: "kit/5-first", PR: ghURL}
	forged := gateForgeResult{forge: stagePRForge{info: tracker.PRInfo{State: "OPEN", Head: "kit/5-second"}}}
	out, result, done, err := run.stepSlotHead(answer, target, forged, true)
	if err != nil || done || out.target.Branch != "kit/5-second" || len(result.Notes) != 2 {
		t.Fatalf("follow result=%+v branch=%q done=%v err=%v", result, out.target.Branch, done, err)
	}
	if backing[1] != "caller-owned" || len(answer.Notes) != 1 || target.Branch != "kit/5-first" {
		t.Fatalf("stage changed caller's input: notes=%q answer=%+v branch=%q", backing, answer, target.Branch)
	}
}

type stagePRForge struct {
	Forge
	info tracker.PRInfo
}

func (f stagePRForge) PRView(context.Context, int) (tracker.PRInfo, error) {
	return f.info, nil
}

// The PR identity check consumes a supplied PR read and refs. It needs neither
// forge setup nor a preceding slot-head check, and does not re-read that PR.
func TestGatePRMatchesSuppliedHead(t *testing.T) {
	dir := gittest.NewRepo(t, "main")
	gittest.Commit(t, dir, "head", "work.txt", "work")
	head := gittest.Run(t, dir, "rev-parse", "HEAD")
	run := gate{e: Env{Git: git.Exec}.WithDefaults(), ctx: bg, root: dir, o: GateOpts{Slot: "kit", Base: "main"}}
	for _, c := range []struct{ name, branch, base, verdict string }{
		{"matching", "kit/5-second", "main", ""},
		{"another branch", "kit/6-other", "main", GatePRMismatch},
		{"stacked", "kit/5-second", "kit/5-first", GatePRMismatch},
	} {
		t.Run(c.name, func(t *testing.T) {
			target := GateTarget{Branch: "kit/5-second", PR: ghURL}
			info := tracker.PRInfo{State: "OPEN", Head: c.branch, Base: c.base, HeadSHA: head}
			// A nil forge makes an unintended re-read fail instead of hiding it.
			verified, result, done, err := run.stepPRMatches(GateResult{}, target,
				gateForgeResult{provider: "github"}, true, gateRefsResult{head: "HEAD"}, &info)
			if err != nil || verified != head || result.Verdict != c.verdict || done != (c.verdict != "") {
				t.Fatalf("verified=%q result=%+v done=%v err=%v", verified, result, done, err)
			}
			if info.Head != c.branch || info.Base != c.base || info.HeadSHA != head {
				t.Fatalf("changed supplied PR: %+v", info)
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
	out, result, done, err := run.stepRefs(answer, "worker", false)
	if err != nil || done || result.Verdict != "" {
		t.Fatalf("refs result=%+v done=%v err=%v", result, done, err)
	}
	if out.head != "worker" || out.base != "main" || out.localHead != head {
		t.Fatalf("refs head=%q base=%q verified=%q, want worker/main/%s", out.head, out.base, out.localHead, head)
	}
}
