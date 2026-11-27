package worker

import (
	"fmt"
	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/landing"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pushStamped adds a commit to the worker's branch, stamped with the attempt
// trailer ("" for none), pushes it and points the fake forge at it. It returns
// the new tip.
func (w *world) pushStamped(file, attempt string) string {
	w.t.Helper()
	os.WriteFile(filepath.Join(w.worker, file), []byte(file+"\n"), 0o644)
	gittest.Run(w.t, w.worker, "add", file)
	msg := "work on " + file
	if attempt != "" {
		msg += "\n\n" + AttemptTrailer + ": " + attempt
	}
	gittest.Run(w.t, w.worker, "commit", "-q", "-m", msg)
	gittest.Run(w.t, w.worker, "push", "-q", "origin", "w1")
	sha := gittest.Run(w.t, w.worker, "rev-parse", "HEAD")
	w.forge("sha", sha)
	return sha
}

// setAttemptSlot writes slot w1 holding attempt (and the branch tip it
// inherited, "" for none) with the PR recorded.
func (w *world) setAttemptSlot(attempt, base string) {
	extra := ""
	if attempt != "" {
		extra += fmt.Sprintf(`,"attempt":%q`, attempt)
	}
	if base != "" {
		extra += fmt.Sprintf(`,"attemptBase":%q`, base)
	}
	os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"),
		[]byte(fmt.Sprintf(`{"slots":[{"name":"w1","branch":"w1","task":"#5","pr":%q%s}]}`, ghURL, extra)), 0o644)
	w.setPRBody("Closes #5\n")
}

// A PR carrying a commit stamped with an attempt the slot no longer holds is
// refused; an unstamped commit, the slot's own attempt, a slot that records no
// attempt (assigned before #958) and the commits a transfer inherited are not.
func TestGateRefusesACommitFromASupersededAttempt(t *testing.T) {
	const old, cur = "aaaaaaaa", "bbbbbbbb"
	cases := []struct {
		name  string
		setup func(w *world)
		check bool
		want  string
	}{
		{"stale stamp", func(w *world) { w.pushStamped("a.txt", old); w.setAttemptSlot(cur, "") }, false, landing.GateStaleAttempt},
		{"stale stamp under check-only", func(w *world) { w.pushStamped("a.txt", old); w.setAttemptSlot(cur, "") }, true, landing.GateStaleAttempt},
		{"own stamp", func(w *world) { w.pushStamped("a.txt", cur); w.setAttemptSlot(cur, "") }, false, landing.GatePass},
		{"no stamp", func(w *world) { w.pushStamped("a.txt", ""); w.setAttemptSlot(cur, "") }, false, landing.GatePass},
		{"slot has no attempt", func(w *world) { w.pushStamped("a.txt", old); w.setAttemptSlot("", "") }, false, landing.GatePass},
		{"inherited commits are the new holder's to keep", func(w *world) {
			base := w.pushStamped("a.txt", old)
			w.pushStamped("b.txt", cur)
			w.setAttemptSlot(cur, base)
		}, false, landing.GatePass},
		{"inherited tip, nothing pushed yet", func(w *world) {
			base := w.pushStamped("a.txt", old)
			w.setAttemptSlot(cur, base)
		}, false, landing.GatePass},
		{"stale push after the inherited tip", func(w *world) {
			base := w.pushStamped("a.txt", old)
			w.setAttemptSlot(cur, base)
			w.pushStamped("b.txt", old)
		}, false, landing.GateStaleAttempt},
		{"commits merged in from the base are not judged", func(w *world) {
			base := w.pushStamped("a.txt", old)
			w.setAttemptSlot(cur, base)
			os.WriteFile(filepath.Join(w.dir, "other.txt"), []byte("other\n"), 0o644)
			gittest.Run(w.t, w.dir, "add", "other.txt")
			gittest.Run(w.t, w.dir, "commit", "-q", "-m", "other work\n\n"+AttemptTrailer+": cccccccc")
			gittest.Run(w.t, w.dir, "push", "-q", "origin", "main")
			gittest.Run(w.t, w.worker, "fetch", "-q", "origin")
			gittest.Run(w.t, w.worker, "merge", "-q", "--no-edit", "origin/main")
			w.pushStamped("b.txt", cur)
		}, true, landing.GateFresh},
		{"inherited tip no longer an ancestor is not judged", func(w *world) {
			w.pushStamped("a.txt", old)
			w.setAttemptSlot(cur, strings.Repeat("0", 40))
		}, false, landing.GatePass},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, ghURL)
			c.setup(w)
			res, err := w.gate(false, GateOpts{CheckOnly: c.check})
			if c.check && c.want == landing.GatePass {
				c.want = landing.GateFresh
			}
			if err != nil || res.Verdict != c.want {
				t.Fatalf("verdict = %q (%v), want %q: %+v", res.Verdict, err, c.want, res)
			}
			if c.want != landing.GateStaleAttempt {
				return
			}
			if res.Changed || w.onMain("a.txt") {
				t.Errorf("nothing may land: %+v", res)
			}
			if strings.Contains(res.Err, cur) || !strings.Contains(res.Err, old) {
				t.Errorf("message names the stamped attempt, not the current one: %q", res.Err)
			}
		})
	}
}

// A PR onto a milestone branch that merges the default branch brings commits
// other attempts already landed there: they are not this slot's pushes. A stale
// stamp that only the PR head reaches is still refused (#4263).
func TestGateAttemptCheckSkipsTheDefaultBranchOnAMilestoneBase(t *testing.T) {
	const old, cur, landed = "aaaaaaaa", "bbbbbbbb", "cccccccc"
	for _, c := range []struct {
		name, config string
		stale        bool
		def, want    string
	}{
		{name: "merged default branch passes", def: "main", want: landing.GateFresh},
		{name: "stale stamp after the merge", def: "main", stale: true, want: landing.GateStaleAttempt},
		{name: "configured base branch", def: "develop", config: `,"git":{"baseBranch":"develop"}`, want: landing.GateFresh},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, ghURL)
			w.setConfig(`{"test":{"full":["true"]}` + c.config + `}`)
			gittest.Run(t, w.dir, "push", "-q", "origin", "main:refs/heads/milestone/M01")
			gittest.Run(t, w.dir, "branch", "milestone/M01", "main")
			if c.def != "main" {
				gittest.Run(t, w.dir, "checkout", "-q", "-b", c.def)
			}
			os.WriteFile(filepath.Join(w.dir, "other.txt"), []byte("other\n"), 0o644)
			gittest.Run(t, w.dir, "add", "other.txt")
			gittest.Run(t, w.dir, "commit", "-q", "-m", "other work\n\n"+AttemptTrailer+": "+landed)
			gittest.Run(t, w.dir, "push", "-q", "origin", c.def)
			gittest.Run(t, w.worker, "fetch", "-q", "origin")
			gittest.Run(t, w.worker, "merge", "-q", "--no-edit", "origin/"+c.def)
			tip := w.pushStamped("b.txt", cur)
			if c.stale {
				tip = w.pushStamped("c.txt", old)
			}
			w.setAttemptSlot(cur, "")
			w.forge("base", "milestone/M01")
			w.forge("sha", tip)

			res, err := w.gate(false, GateOpts{CheckOnly: true, Base: "milestone/M01"})
			if err != nil || res.Verdict != c.want {
				t.Fatalf("verdict = %q (%v), want %q: %+v", res.Verdict, err, c.want, res)
			}
			if c.stale && !strings.Contains(res.Err, old) {
				t.Errorf("the refusal names the stale stamp, not the landed one: %q", res.Err)
			}
		})
	}
}
