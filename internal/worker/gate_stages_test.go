package worker

import (
	"context"
	"errors"
	"github.com/l4ci/rota/internal/landing"
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
			e := Env{Config: diskConfig, Git: func(context.Context, string, ...string) (git.Result, error) {
				return git.Result{ExitCode: c.exit}, c.err
			}}
			run := gateRun{e: e, ctx: bg, root: t.TempDir(), o: GateOpts{Slot: "w1", Base: "main"}}
			original := landing.GateResult{Slot: "w1", Base: "main", PR: ghURL}
			out, result, done, err := run.stepRemote(original, ghURL)
			if err != nil || done == c.remote || out != c.remote {
				t.Fatalf("remote=%v done=%v err=%v result=%+v", out, done, err, result)
			}
			if c.remote {
				if result.Verdict != "" {
					t.Fatalf("remote output: %+v result=%+v", out, result)
				}
			} else if result.Verdict != landing.GateCheckBroke || !strings.Contains(result.Err, c.message) {
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
	answer := landing.GateResult{Notes: backing[:1]}
	run := gateRun{ctx: bg, o: GateOpts{Slot: "kit", CheckOnly: true}}
	target := GateTarget{Name: "kit", Task: "#5", Branch: "kit/5-first", PR: ghURL}
	forged := gateForgeResult{forge: stagePRForge{info: tracker.PRInfo{State: "OPEN", Head: "kit/5-second"}}}
	out, result, done, err := run.stepSlotHead(answer, target, forged, true, nil)
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
	run := gateRun{e: Env{Config: diskConfig, Git: git.Exec}, ctx: bg, root: dir, o: GateOpts{Slot: "kit", Base: "main"}}
	for _, c := range []struct{ name, branch, base, verdict string }{
		{"matching", "kit/5-second", "main", ""},
		{"another branch", "kit/6-other", "main", landing.GatePRMismatch},
		{"stacked", "kit/5-second", "kit/5-first", landing.GatePRMismatch},
	} {
		t.Run(c.name, func(t *testing.T) {
			target := GateTarget{Branch: "kit/5-second", PR: ghURL}
			info := tracker.PRInfo{State: "OPEN", Head: c.branch, Base: c.base, HeadSHA: head}
			// A nil forge makes an unintended re-read fail instead of hiding it.
			verified, result, done, err := run.stepPRMatches(landing.GateResult{}, target,
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
	run := gateRun{e: Env{Config: diskConfig, Git: git.Exec}, ctx: bg, root: dir, o: GateOpts{Slot: "worker", Base: "main"}}
	answer := landing.GateResult{Slot: "worker", Base: "main"}
	out, result, done, err := run.stepRefs(answer, "worker", false)
	if err != nil || done || result.Verdict != "" {
		t.Fatalf("refs result=%+v done=%v err=%v", result, done, err)
	}
	if out.head != "worker" || out.base != "main" || out.localHead != head {
		t.Fatalf("refs head=%q base=%q verified=%q, want worker/main/%s", out.head, out.base, out.localHead, head)
	}
}

// A successful merge followed by a failed read-back has already changed base.
// It must not look like a successful gate or invite another merge attempt.
func TestGateLocalStageReadbackFailure(t *testing.T) {
	e := Env{Config: diskConfig, Git: func(_ context.Context, _ string, args ...string) (git.Result, error) {
		if args[0] == "rev-parse" {
			return git.Result{ExitCode: 128, Stderr: "cannot read landed commit"}, nil
		}
		return git.Result{}, nil
	}}
	run := gateRun{e: e, ctx: bg, root: t.TempDir(), o: GateOpts{Slot: "kit", Base: "main"}}
	_, result, done, err := run.stepMerge(landing.GateResult{}, GateTarget{Branch: "kit/5-work"}, gateForgeResult{}, false, gateRefsResult{}, "verified")
	if err != nil || !done || result.Verdict != landing.GateCheckBroke || !result.Changed || !strings.Contains(result.Err, "Do not retry the merge") {
		t.Fatalf("read-back failure lost: result=%+v done=%v err=%v", result, done, err)
	}
}

// #4248: a manual assign may give a done slot new work while its own PR is
// gated. The gate's slot writes then carry the old PR or branch, so they land
// only when the slot still holds the gated issue and branch.
func TestGateSlotHeadWriteSkipsMovedOnSlot(t *testing.T) {
	for _, c := range []struct {
		name, slotTask, slotBranch, want string
	}{
		{"still gated", "#5", "kit/5-first", "kit/5-second"},
		{"moved to another issue", "#9", "kit/9-other", "kit/9-other"},
		{"parked", "", "park/kit", "park/kit"},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			if err := Update(root, func(d *Doc) {
				s := NewSlot("kit", c.slotBranch, "/w/kit", "main", "h")
				s.SetTask(c.slotTask)
				d.AppendSlot(s)
			}); err != nil {
				t.Fatal(err)
			}
			run := gateRun{ctx: bg, root: root, o: GateOpts{Slot: "kit"}}
			target := GateTarget{Name: "kit", Task: "#5", Branch: "kit/5-first", PR: ghURL}
			forged := gateForgeResult{forge: stagePRForge{info: tracker.PRInfo{State: "OPEN", Head: "kit/5-second"}}}
			if _, _, _, err := run.stepSlotHead(landing.GateResult{}, target, forged, true, nil); err != nil {
				t.Fatal(err)
			}
			if got := LoadRegistryTolerant(root).Slot("kit").Branch(); got != c.want {
				t.Fatalf("slot branch = %q, want %q", got, c.want)
			}
		})
	}
}

type adoptForge struct {
	Forge
	prs []tracker.PR
}

func (f adoptForge) OpenPRs(context.Context) ([]tracker.PR, error) { return f.prs, nil }

func TestGateAdoptPRRequiresSameRepository(t *testing.T) {
	const branch = "kit/5-first"
	fork := tracker.PR{Branch: branch, URL: "https://github.com/o/r/pull/99"}
	owned := tracker.PR{Branch: branch, URL: ghURL, SameRepository: true}
	for _, c := range []struct {
		name string
		prs  []tracker.PR
		want string
	}{
		{name: "fork only", prs: []tracker.PR{fork}},
		{name: "fork before owned", prs: []tracker.PR{fork, owned}, want: ghURL},
		{name: "owned only", prs: []tracker.PR{owned}, want: ghURL},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := gittest.NewRepo(t, "main")
			gittest.Run(t, dir, "remote", "add", "origin", dir)
			if err := Update(dir, func(d *Doc) {
				s := NewSlot("kit", branch, "/w/kit", "main", "h")
				s.SetTask("#5")
				d.AppendSlot(s)
			}); err != nil {
				t.Fatal(err)
			}
			run := gateRun{e: Env{Config: diskConfig, Git: git.Exec}, ctx: bg, root: dir, o: GateOpts{Slot: "kit", Base: "main"}}
			target := GateTarget{Name: "kit", Task: "#5", Branch: branch}
			forged := gateForgeResult{forge: adoptForge{prs: c.prs}}
			out, result, done, err := run.stepAdoptPR(landing.GateResult{}, target, forged)
			if err != nil || done {
				t.Fatalf("adopt: done=%v err=%v result=%+v", done, err, result)
			}
			if out.PR != c.want || result.PR != c.want {
				t.Errorf("target PR = %q, result PR = %q, want %q", out.PR, result.PR, c.want)
			}
			if got := LoadRegistryTolerant(dir).Slot("kit").PR(); got != c.want {
				t.Errorf("recorded PR = %q, want %q", got, c.want)
			}
		})
	}
}

func TestGateAdoptPRWriteSkipsMovedOnSlot(t *testing.T) {
	for _, c := range []struct {
		name, slotTask, slotBranch, wantPR string
	}{
		{"still gated", "#5", "kit/5-first", ghURL},
		{"moved to another issue", "#9", "kit/9-other", ""},
		{"parked", "", "park/kit", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := gittest.NewRepo(t, "main")
			gittest.Run(t, dir, "remote", "add", "origin", dir)
			if err := Update(dir, func(d *Doc) {
				s := NewSlot("kit", c.slotBranch, "/w/kit", "main", "h")
				s.SetTask(c.slotTask)
				d.AppendSlot(s)
			}); err != nil {
				t.Fatal(err)
			}
			run := gateRun{e: Env{Config: diskConfig, Git: git.Exec}, ctx: bg, root: dir, o: GateOpts{Slot: "kit", Base: "main"}}
			target := GateTarget{Name: "kit", Task: "#5", Branch: "kit/5-first"}
			forged := gateForgeResult{forge: adoptForge{prs: []tracker.PR{{Branch: "kit/5-first", URL: ghURL, SameRepository: true}}}}
			if _, _, _, err := run.stepAdoptPR(landing.GateResult{}, target, forged); err != nil {
				t.Fatal(err)
			}
			if got := LoadRegistryTolerant(dir).Slot("kit").PR(); got != c.wantPR {
				t.Fatalf("slot PR = %q, want %q", got, c.wantPR)
			}
		})
	}
}
