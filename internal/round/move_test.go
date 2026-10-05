package round

import (
	"context"
	"errors"
	"github.com/l4ci/rota/internal/exitcode"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// moveBoard is a board with a comment store, a claim read-back and a state
// that can be made to fail once.
type moveBoard struct {
	*boardFake
	comments  map[string][]string
	failState error
	gone      map[string]bool // issues the tracker answers 404 for
}

func (b *moveBoard) notFound(ref string) error {
	if b.gone[ref] {
		return &tracker.Error{Kind: tracker.KindNotFound, Message: "gh: Not Found (HTTP 404)"}
	}
	return nil
}
func (b *moveBoard) Release(ref, claim string) (bool, error) {
	if err := b.notFound(ref); err != nil {
		return false, err
	}
	return b.boardFake.Release(ref, claim)
}

func (b *moveBoard) AddComment(ref, kind, text string) (string, error) {
	if err := b.notFound(ref); err != nil {
		return "", err
	}
	b.comments[ref] = append(b.comments[ref], text)
	return strconv.Itoa(len(b.comments[ref])), nil
}
func (b *moveBoard) Comments(ref, kind string) ([]backlog.Comment, error) {
	var out []backlog.Comment
	for _, t := range b.comments[ref] {
		out = append(out, backlog.Comment{Kind: "feedback", Text: t})
	}
	return out, nil
}
func (b *moveBoard) Status(ref string) (*backlog.Status, error) {
	return &backlog.Status{ID: ref, Claim: b.claims[ref], State: b.states[ref]}, nil
}
func (b *moveBoard) SetState(ref, state string) (bool, error) {
	if err := b.notFound(ref); err != nil {
		return false, err
	}
	if err := b.failState; err != nil {
		b.failState = nil
		return false, err
	}
	return b.boardFake.SetState(ref, state)
}

// killHost records the panes it was asked to kill.
type killHost struct {
	*hostFake
	killed *[]string
}

func (k *killHost) Kill(_ context.Context, slot, _ string) error {
	*k.killed = append(*k.killed, slot)
	return nil
}

// labelForge answers List by label, so needs-human is observable.
type labelForge struct {
	fakeForge
	labels map[int][]string
}

func (f *labelForge) List(_ context.Context, fl tracker.ListFilter) ([]tracker.Issue, error) {
	var out []tracker.Issue
	for n, ls := range f.labels {
		for _, l := range ls {
			for _, want := range fl.Labels {
				if l == want {
					out = append(out, tracker.Issue{Number: n})
				}
			}
		}
	}
	return out, nil
}
func (f *labelForge) AddLabels(_ context.Context, n int, labels []string, _ bool) error {
	if f.labels == nil {
		f.labels = map[int][]string{}
	}
	f.labels[n] = append(f.labels[n], labels...)
	return nil
}

type moveFx struct {
	root, origin string
	env          Env
	be           *moveBoard
	host         *hostFake
	killed       []string
	forge        *labelForge
	set          roundcfg.Settings
	now          time.Time
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, errOut, code, err := worker.ExecGit(bg, dir, args...)
	if err != nil || code != 0 {
		t.Fatalf("git %v in %s: %v %s", args, dir, err, errOut)
	}
	return strings.TrimSpace(out)
}

// newMoveFx is a started round (ben and dana, pid 100 holds the lease) with a
// bare origin, issue 12 assigned to ben and a commit on ben's branch.
func newMoveFx(t *testing.T) *moveFx {
	t.Helper()
	f := &moveFx{now: time.Now(), host: &hostFake{}, forge: &labelForge{}}
	f.root = newRepo(t, nil)
	sh(t, f.root, "config", "user.email", "t@t")
	sh(t, f.root, "config", "user.name", "t")
	for p, c := range map[string]string{"references/worker-contract.md": "contract", "internal/cli/round.go": "x"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(f.root, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(f.root, p), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sh(t, f.root, "add", "internal", "references")
	sh(t, f.root, "commit", "-q", "-m", "files")
	f.origin = filepath.Join(t.TempDir(), "origin.git")
	sh(t, f.root, "init", "-q", "--bare", f.origin)
	sh(t, f.root, "remote", "add", "origin", f.origin)
	sh(t, f.root, "push", "-q", "origin", "main")
	milestoneDoc(t, f.root, "M01", "active")

	f.env = Env{Git: worker.ExecGit, Base: "main", Lease: fakeLease("h", 100), StallMinutes: 30, Forge: f.forge,
		Now: func() time.Time { return f.now }}
	f.env.Worker = worker.Env{Git: worker.ExecGit, Now: func() time.Time { return f.now }, Sleep: func(time.Duration) {},
		NewHost: func(string) host.Host { return &killHost{hostFake: f.host, killed: &f.killed} }}

	fb := &fakeBacklog{}
	fb.add("12", "Add the round assign verb now please", "M01", false, "## Acceptance\n- [ ] works\n\nedits internal/cli/round.go")
	fb.add("13", "Second issue", "M01", false, "## Acceptance\n- [ ] ok\n\nedits internal/other.go")
	fb.add("14", "Third issue", "M01", false, "## Acceptance\n- [ ] ok\n\nalso edits internal/cli/round.go")
	for _, it := range fb.items {
		n, _ := strconv.Atoi(it.ID)
		it.Number = n
	}
	f.be = &moveBoard{boardFake: &boardFake{fakeBacklog: fb}, comments: map[string][]string{}}
	f.set, _ = roundcfg.Load(os.TempDir())

	if _, err := f.env.Start(bg, f.root, startOpts(roundcfg.ScopeMilestone, 100)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.assign("12", "ben"); err != nil {
		t.Fatal(err)
	}
	f.commit(t, "ben", "ben-work.txt")
	f.killed = nil // the assignment's own dispatch closed ben's old session
	return f
}

func (f *moveFx) wt(slot string) string { return filepath.Join(f.root, ".worktrees", slot) }

func (f *moveFx) commit(t *testing.T, slot, file string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.wt(slot), file), []byte(slot), 0o644); err != nil {
		t.Fatal(err)
	}
	sh(t, f.wt(slot), "add", file)
	sh(t, f.wt(slot), "commit", "-q", "-m", "work on "+file)
}

func (f *moveFx) assign(id, agent string) (Assigned, error) {
	return f.env.Assign(bg, f.root, f.be, AssignOpts{ID: id, Agent: agent, HolderPID: 100, Settings: f.set, Getenv: func(string) string { return "" }})
}

func (f *moveFx) ret(slot, reason string, mod func(*ReturnOpts)) (Returned, error) {
	o := ReturnOpts{Slot: slot, Reason: reason, InSlot: true, Getenv: func(string) string { return "" }}
	if mod != nil {
		mod(&o)
	}
	return f.env.Return(bg, f.root, f.be, o)
}

func (f *moveFx) transfer(id, to string, mod func(*TransferOpts)) (Transferred, error) {
	o := TransferOpts{Issue: id, To: to, HolderPID: 100, Settings: f.set, Getenv: func(string) string { return "" }}
	if mod != nil {
		mod(&o)
	}
	return f.env.Transfer(bg, f.root, f.be, o)
}

func (f *moveFx) reclaim(slot string, mod func(*ReclaimOpts)) (Reclaimed, error) {
	o := ReclaimOpts{Slot: slot, HolderPID: 100, Getenv: func(string) string { return "" }}
	if mod != nil {
		mod(&o)
	}
	return f.env.Reclaim(bg, f.root, f.be, o)
}

// agents makes the host show a live agent in each named slot.
func (f *moveFx) agents(slots ...string) {
	f.env.Snapshot = func(context.Context) ([]host.Agent, error) {
		out := []host.Agent{}
		for _, s := range slots {
			out = append(out, host.Agent{Tab: "w1:" + s, Name: s, Cwd: f.wt(s), Status: "working"})
		}
		return out, nil
	}
}

func (f *moveFx) slot(name string) *worker.Slot { return worker.LoadRegistry(f.root).Slot(name) }

func (f *moveFx) remoteHas(t *testing.T, branch string) bool {
	t.Helper()
	return gitIn(t, f.root, "ls-remote", "--heads", "origin", branch) != ""
}

func hasKind(l []string, k string) bool {
	for _, v := range l {
		if v == k {
			return true
		}
	}
	return false
}

func exitOf(err error) int {
	var we *exitcode.Error
	if errors.As(err, &we) {
		return we.Exit
	}
	return -1
}

// ---- Park -------------------------------------------------------------------

func TestParkSalvagesDirtyPathsByNameAndPushes(t *testing.T) {
	f := newMoveFx(t)
	branch := f.slot("ben").Branch()
	os.WriteFile(filepath.Join(f.wt("ben"), "ben-work.txt"), []byte("edited"), 0o644)
	os.WriteFile(filepath.Join(f.wt("ben"), "new.txt"), []byte("new"), 0o644)
	os.WriteFile(filepath.Join(f.root, "elsewhere.txt"), []byte("not the slot's"), 0o644)

	p, err := f.env.Park(bg, f.root, "ben", "return")
	if err != nil {
		t.Fatal(err)
	}
	if !p.Salvaged || !p.Moved || p.Branch != branch {
		t.Fatalf("%+v", p)
	}
	if got := gitIn(t, f.root, "log", "-1", "--format=%s", branch); got != "wip: parked from ben (rota round return)" {
		t.Errorf("salvage subject %q", got)
	}
	if files := gitIn(t, f.root, "show", "--name-only", "--format=", branch); !strings.Contains(files, "new.txt") || !strings.Contains(files, "ben-work.txt") || strings.Contains(files, "elsewhere") {
		t.Errorf("salvage must stage the slot's dirty paths by name: %q", files)
	}
	if !f.remoteHas(t, branch) {
		t.Error("the work branch must be on origin")
	}
	if cur := gitIn(t, f.wt("ben"), "symbolic-ref", "--short", "HEAD"); cur != "park/ben" {
		t.Errorf("worktree must sit on park/ben, not %q", cur)
	}
	if dirty := gitIn(t, f.wt("ben"), "status", "--porcelain"); dirty != "" {
		t.Errorf("a parked worktree is clean: %q", dirty)
	}
	// Repeating after the switch only pushes again.
	if p2, err := f.env.Park(bg, f.root, "ben", "return"); err != nil || p2.Salvaged || p2.Moved || p2.Branch != branch {
		t.Fatalf("repeat: %v %+v", err, p2)
	}
}

func TestParkFailedPushLeavesTheSlotAsFound(t *testing.T) {
	f := newMoveFx(t)
	branch := f.slot("ben").Branch()
	os.WriteFile(filepath.Join(f.wt("ben"), "dirty.txt"), []byte("wip"), 0o644)
	head := gitIn(t, f.wt("ben"), "rev-parse", "HEAD")
	sh(t, f.root, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))

	_, err := f.env.Park(bg, f.root, "ben", "return")
	if exitOf(err) != exitcode.ExitUnavailable {
		t.Fatalf("a failed push is exit 5: %v", err)
	}
	if cur := gitIn(t, f.wt("ben"), "symbolic-ref", "--short", "HEAD"); cur != branch {
		t.Errorf("slot must stay on %s, not %s", branch, cur)
	}
	if got := gitIn(t, f.wt("ben"), "rev-parse", "HEAD"); got != head {
		t.Errorf("the salvage commit must be taken back: HEAD %s want %s", got, head)
	}
	if st := gitIn(t, f.wt("ben"), "status", "--porcelain"); !strings.Contains(st, "dirty.txt") {
		t.Errorf("the dirty file must still be there: %q", st)
	}
}

func TestParkRejectedSalvageCommitLeavesTheSlotAsFound(t *testing.T) {
	f := newMoveFx(t)
	branch := f.slot("ben").Branch()
	hook := filepath.Join(f.root, ".git", "hooks")
	os.MkdirAll(hook, 0o755)
	os.WriteFile(filepath.Join(hook, "pre-commit"), []byte("#!/bin/sh\necho no >&2\nexit 1\n"), 0o755)
	os.WriteFile(filepath.Join(f.wt("ben"), "dirty.txt"), []byte("wip"), 0o644)

	_, err := f.env.Park(bg, f.root, "ben", "return")
	if exitOf(err) != exitcode.ExitUnavailable {
		t.Fatalf("a rejected commit is exit 5: %v", err)
	}
	if cur := gitIn(t, f.wt("ben"), "symbolic-ref", "--short", "HEAD"); cur != branch {
		t.Errorf("slot must stay on %s, not %s", branch, cur)
	}
	if st := gitIn(t, f.wt("ben"), "status", "--porcelain"); st != "?? dirty.txt" {
		t.Errorf("the file must be back to untracked: %q", st)
	}
	if f.remoteHas(t, branch) {
		t.Error("nothing is pushed when the commit is rejected")
	}
}

// ---- Stalled ----------------------------------------------------------------

func TestStalledThreshold(t *testing.T) {
	f := newMoveFx(t)
	wt := f.wt("dana") // clean and parked: activeAt is the only signal
	t0 := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	in := StallInput{Worktree: wt, Base: "main", Holds: true, Alive: true, ActiveAt: t0.Format(time.RFC3339), Minutes: 30}
	for _, c := range []struct {
		name string
		now  time.Duration
		mod  func(*StallInput)
		want bool
	}{
		{"below", 29*time.Minute + 59*time.Second, nil, false},
		{"at", 30 * time.Minute, nil, true},
		{"above", 31 * time.Minute, nil, true},
		{"off", 3 * time.Hour, func(i *StallInput) { i.Minutes = 0 }, false},
		{"escalation pending", 3 * time.Hour, func(i *StallInput) { i.Escalated = true }, false},
		{"dead is not stalled", 3 * time.Hour, func(i *StallInput) { i.Alive = false }, false},
		{"no issue", 3 * time.Hour, func(i *StallInput) { i.Holds = false }, false},
		{"no signal at all", 3 * time.Hour, func(i *StallInput) { i.ActiveAt = "" }, false},
	} {
		x := in
		if c.mod != nil {
			c.mod(&x)
		}
		if got := f.env.Stalled(bg, x, t0.Add(c.now)); got.Stalled != c.want {
			t.Errorf("%s: stalled=%v want %v (%+v)", c.name, got.Stalled, c.want, got)
		}
	}
	if st := f.env.Stalled(bg, in, t0.Add(40*time.Minute)); st.Signal != "state change" || int(st.Idle.Minutes()) != 40 {
		t.Errorf("signal and idle time: %+v", st)
	}
}

func TestStalledReadsCommitsAndUncommittedEdits(t *testing.T) {
	f := newMoveFx(t) // ben has a commit past the base, made just now
	wt := f.wt("ben")
	tip, _ := strconv.ParseInt(gitIn(t, wt, "log", "-1", "--format=%ct"), 10, 64)
	committed := time.Unix(tip, 0)
	old := committed.Add(-3 * time.Hour).UTC().Format(time.RFC3339)
	in := StallInput{Worktree: wt, Base: "main", Holds: true, Alive: true, ActiveAt: old, Minutes: 30}

	if st := f.env.Stalled(bg, in, committed.Add(31*time.Minute)); !st.Stalled || st.Signal != "commit" {
		t.Errorf("an old activeAt is outranked by the commit: %+v", st)
	}
	if st := f.env.Stalled(bg, in, committed.Add(10*time.Minute)); st.Stalled {
		t.Errorf("a recent commit is progress: %+v", st)
	}
	// An uncommitted edit keeps a worker that does not commit from being stalled.
	edit := filepath.Join(wt, "scratch.txt")
	os.WriteFile(edit, []byte("x"), 0o644)
	mtime := committed.Add(25 * time.Minute)
	os.Chtimes(edit, mtime, mtime)
	if st := f.env.Stalled(bg, in, committed.Add(31*time.Minute)); st.Stalled || st.Signal != "uncommitted edit" {
		t.Errorf("an edit 6 min ago is progress: %+v", st)
	}
	if st := f.env.Stalled(bg, in, mtime.Add(31*time.Minute)); !st.Stalled || st.Signal != "uncommitted edit" {
		t.Errorf("then it stalls on the edit: %+v", st)
	}
}

// ---- reconcile --------------------------------------------------------------

func TestReconcileReportsStalledNeverRepairsIt(t *testing.T) {
	f := newMoveFx(t)
	f.agents("ben")
	rawSlot(f.root, "ben", func(s *jsonx.Object) {
		s.Set("handle", "w1:ben")
		s.Set("activeAt", f.now.Add(-45*time.Minute).UTC().Format(time.RFC3339))
	})
	// The commit ben made is older than the clock the test sets: move the clock.
	f.now = f.now.Add(2 * time.Hour)
	out, err := f.env.Reconcile(bg, f.root, true)
	if err != nil {
		t.Fatal(err)
	}
	got := kinds(append(out.Drift, out.Repaired...))
	if !hasKind(got["ben"], StalledSlot) || hasKind(got["ben"], DeadTab) {
		t.Fatalf("ben should be stalled: %v", got)
	}
	for _, r := range out.Repaired {
		if r.Kind == StalledSlot {
			t.Error("stalled is never repaired")
		}
	}
	if s := f.slot("ben"); s.Task() != "12" || s.ClaimID() != "ben@1" {
		t.Errorf("apply must leave the slot alone: %v", s)
	}
}

func TestReconcileDeadIsNotStalled(t *testing.T) {
	f := newMoveFx(t)
	f.agents() // the host runs nothing
	rawSlot(f.root, "ben", func(s *jsonx.Object) { s.Set("handle", "w1:ben") })
	f.now = f.now.Add(5 * time.Hour)
	rep, err := f.env.Status(bg, f.root)
	if err != nil {
		t.Fatal(err)
	}
	got := kinds(rep.Findings)["ben"]
	if !hasKind(got, DeadTab) || hasKind(got, StalledSlot) {
		t.Errorf("a slot with no live agent is dead-tab, not stalled: %v", got)
	}
}

func TestReconcileEscalationPendingIsNotStalled(t *testing.T) {
	f := newMoveFx(t)
	f.agents("ben")
	rawSlot(f.root, "ben", func(s *jsonx.Object) { s.Set("handle", "w1:ben") })
	worker.UpdateDoc(f.root, func(doc *jsonx.Object) {
		e := jsonx.NewObject()
		e.Set("id", "e1")
		e.Set("kind", "issue")
		e.Set("number", 12)
		e.Set("slot", "ben")
		e.Set("status", "pending")
		doc.Set("escalations", []any{e})
	})
	f.now = f.now.Add(5 * time.Hour)
	rep, _ := f.env.Status(bg, f.root)
	if hasKind(kinds(rep.Findings)["ben"], StalledSlot) {
		t.Errorf("a slot waiting on an escalation is never stalled: %+v", rep.Findings)
	}
	// Without the escalation it is stalled, and 0 turns the check off.
	worker.UpdateDoc(f.root, func(doc *jsonx.Object) { doc.Delete("escalations") })
	if rep, _ := f.env.Status(bg, f.root); !hasKind(kinds(rep.Findings)["ben"], StalledSlot) {
		t.Fatalf("control: stalled once the escalation is gone: %+v", rep.Findings)
	}
	f.env.StallMinutes = 0
	if rep, _ := f.env.Status(bg, f.root); hasKind(kinds(rep.Findings)["ben"], StalledSlot) {
		t.Errorf("stallMinutes 0 is off: %v", rep.Findings)
	}
}

func TestClaimMismatchOnADesyncedFixture(t *testing.T) {
	f := newMoveFx(t)
	f.env.Board = f.be
	f.forge.labels = map[int][]string{12: {"in-progress"}, 13: {"in-progress"}}
	f.forge.labelled = nil

	// In step: nothing to report.
	rep, _ := f.env.Status(bg, f.root)
	for _, fd := range rep.Findings {
		if fd.Kind == ClaimMismatch {
			t.Fatalf("a slot in step with the tracker has no mismatch: %+v", fd)
		}
	}

	// The tracker lost the claim (a human released it): the registry side is
	// repaired, the tracker is never touched.
	delete(f.be.claims, "12")
	rep, _ = f.env.Status(bg, f.root)
	var found *Finding
	for i, fd := range rep.Findings {
		if fd.Kind == ClaimMismatch {
			found = &rep.Findings[i]
		}
	}
	if found == nil || found.Slot != "ben" || found.Repair == "" {
		t.Fatalf("gone claim: %+v", rep.Findings)
	}
	out, err := f.env.Reconcile(bg, f.root, true)
	if err != nil {
		t.Fatal(err)
	}
	repaired := false
	for _, r := range out.Repaired {
		repaired = repaired || r.Kind == ClaimMismatch
	}
	if !repaired {
		t.Fatalf("apply should clear the claimId: %+v", out)
	}
	if s := f.slot("ben"); s.ClaimID() != "" || s.Task() != "12" {
		t.Errorf("claimId cleared, task kept: %v", s)
	}
	if len(f.be.claims) != 0 {
		t.Errorf("reconcile never edits the tracker: %v", f.be.claims)
	}

	// Another holder: reported, not repaired (ambiguous).
	rawSlot(f.root, "ben", func(s *jsonx.Object) { s.Set("claimId", "ben@1") })
	f.be.claims["12"] = "dana@1"
	out, _ = f.env.Reconcile(bg, f.root, true)
	for _, r := range out.Repaired {
		if r.Kind == ClaimMismatch {
			t.Errorf("an ambiguous mismatch is not repaired: %+v", r)
		}
	}
	if s := f.slot("ben"); s.ClaimID() != "ben@1" {
		t.Errorf("the registry must be left alone: %v", s)
	}
	drifted := false
	for _, d := range out.Drift {
		drifted = drifted || (d.Kind == ClaimMismatch && d.Slot == "ben")
	}
	if !drifted {
		t.Errorf("still reported: %+v", out.Drift)
	}

	// A claim naming a slot that does not hold the issue.
	delete(f.be.claims, "12")
	f.be.claims["13"] = "dana@1"
	rep, _ = f.env.Status(bg, f.root)
	orphan := false
	for _, fd := range rep.Findings {
		orphan = orphan || (fd.Kind == ClaimMismatch && fd.Slot == "dana" && fd.Issue == "13")
	}
	if !orphan {
		t.Errorf("an open claim naming a slot that does not hold the issue: %+v", rep.Findings)
	}
}

// ---- return -----------------------------------------------------------------

func TestReturnParksCommentsReleasesAndFreesTheSlot(t *testing.T) {
	f := newMoveFx(t)
	branch := f.slot("ben").Branch()
	os.WriteFile(filepath.Join(f.wt("ben"), "half.txt"), []byte("wip"), 0o644)

	res, err := f.ret("ben", "premise is wrong", func(o *ReturnOpts) { o.Note = "tried A, B is next" })
	if err != nil {
		t.Fatal(err)
	}
	if res.Issue != "12" || res.Branch != branch || !res.Salvaged || !res.Released || res.CommentID == "" || !res.Changed || res.Head == "" {
		t.Fatalf("%+v", res)
	}
	if !f.remoteHas(t, branch) {
		t.Error("the branch stays, on origin")
	}
	if f.be.claims["12"] != "" || f.be.states["12"] != "" {
		t.Errorf("claim and in-progress must be gone: %v %v", f.be.claims, f.be.states)
	}
	last := f.be.comments["12"][len(f.be.comments["12"])-1]
	for _, want := range []string{"**rota handoff** (return, from ben)", "Branch: `" + branch + "`", "Head: ", "State: committed, salvage commit", "Reason: premise is wrong", "Done and next:\ntried A, B is next", "<!-- rota:handoff ben@1 -->"} {
		if !strings.Contains(last, want) {
			t.Errorf("handoff lacks %q:\n%s", want, last)
		}
	}
	if !strings.HasSuffix(last, "<!-- rota:handoff ben@1 -->") {
		t.Errorf("the marker ends the comment:\n%s", last)
	}
	s := f.slot("ben")
	if s.Task() != "" || s.ClaimID() != "" || s.State() != "idle" || s.Branch() != "park/ben" || s.PR() != "" {
		t.Errorf("slot: %v", s)
	}
	if cur := gitIn(t, f.wt("ben"), "symbolic-ref", "--short", "HEAD"); cur != "park/ben" {
		t.Errorf("worktree on %s", cur)
	}
}

func TestReturnThenAssignPicksTheItemUpAgain(t *testing.T) {
	f := newMoveFx(t)
	branch := f.slot("ben").Branch()
	if _, err := f.ret("ben", "wrong premise", nil); err != nil {
		t.Fatal(err)
	}
	cands, err := f.env.Candidates(bg, f.root, f.be, CandidateOpts{Scope: roundcfg.ScopeMilestone})
	if err != nil {
		t.Fatal(err)
	}
	listed := false
	for _, c := range cands {
		listed = listed || c.ID == "12"
	}
	if !listed {
		t.Fatalf("a returned issue is a candidate again: %v", ids(cands))
	}
	res, err := f.assign("12", "dana")
	if err != nil || !res.Dispatched || f.be.claims["12"] != "dana@1" || f.be.states["12"] != "in-progress" {
		t.Fatalf("assign after return: %v %+v %v", err, res, f.be.claims)
	}
	if !strings.Contains(f.host.sent, "rota:handoff") || !strings.Contains(f.host.sent, branch) {
		t.Errorf("the brief must name the handoff and the pushed branch:\n%s", f.host.sent)
	}
}

func TestReturnRefusals(t *testing.T) {
	f := newMoveFx(t)
	if _, err := f.ret("ben", "  ", nil); exitOf(err) != exitcode.ExitUsage {
		t.Errorf("empty reason is usage: %v", err)
	}
	if _, err := f.ret("zed", "x", nil); exitOf(err) != exitcode.ExitResolution {
		t.Errorf("unknown slot: %v", err)
	}
	if _, err := f.ret("dana", "x", nil); exitOf(err) != exitcode.ExitResolution {
		t.Errorf("a slot holding no issue: %v", err)
	}
	_, err := f.ret("ben", "x", func(o *ReturnOpts) { o.InSlot, o.HolderPID = false, 999 })
	if blockedBy(t, err) != BlockNotYourSlot {
		t.Errorf("neither the slot's worker nor the lease holder: %v", err)
	}
	if f.be.claims["12"] != "ben@1" {
		t.Error("a refusal changes nothing")
	}
	// The orchestrator may return too.
	if _, err := f.ret("ben", "x", func(o *ReturnOpts) { o.InSlot, o.HolderPID = false, 100 }); err != nil {
		t.Errorf("the lease holder: %v", err)
	}
}

func TestReturnRepeatsAfterAFailureAndDoesNotCommentTwice(t *testing.T) {
	f := newMoveFx(t)
	f.be.failState = errors.New("tracker down")
	if _, err := f.ret("ben", "stuck", nil); exitOf(err) != exitcode.ExitUnavailable {
		t.Fatalf("a tracker failure is exit 5: %v", err)
	}
	if s := f.slot("ben"); s.Task() != "12" {
		t.Fatalf("a failed return leaves the registry as found: %v", s)
	}
	if _, err := f.ret("ben", "stuck", nil); err != nil {
		t.Fatalf("the repeat finishes the rest: %v", err)
	}
	n := 0
	for _, c := range f.be.comments["12"] {
		if strings.Contains(c, "rota:handoff") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("one handoff comment, got %d", n)
	}
	if f.slot("ben") == nil || f.slot("ben").Task() != "" {
		t.Error("slot freed")
	}
}

func TestReturnFailedPushLeavesEverythingAsFound(t *testing.T) {
	f := newMoveFx(t)
	sh(t, f.root, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))
	if _, err := f.ret("ben", "x", nil); exitOf(err) != exitcode.ExitUnavailable {
		t.Fatalf("%v", err)
	}
	if f.be.claims["12"] != "ben@1" || len(f.be.comments["12"]) != 1 { // only assign's own note
		t.Errorf("the tracker is untouched: %v %v", f.be.claims, f.be.comments)
	}
	if s := f.slot("ben"); s.Task() != "12" {
		t.Errorf("slot as found: %v", s)
	}
}

// ---- transfer ---------------------------------------------------------------

func TestTransferToASlotChecksOutTheExistingBranch(t *testing.T) {
	f := newMoveFx(t)
	branch := f.slot("ben").Branch()
	f.host.sent = ""
	res, err := f.transfer("12", "dana", func(o *TransferOpts) { o.Note = "carry on from the parser" })
	if err != nil {
		t.Fatal(err)
	}
	if res.From != "ben" || res.To != "dana" || res.Branch != branch || res.ClaimID != "dana@1" || !res.Dispatched || !res.Changed {
		t.Fatalf("%+v", res)
	}
	if cur := gitIn(t, f.wt("dana"), "symbolic-ref", "--short", "HEAD"); cur != branch {
		t.Errorf("dana must have the existing branch checked out, not %q", cur)
	}
	if _, err := os.Stat(filepath.Join(f.wt("dana"), "ben-work.txt")); err != nil {
		t.Errorf("the receiver starts from the pushed work: %v", err)
	}
	if cur := gitIn(t, f.wt("ben"), "symbolic-ref", "--short", "HEAD"); cur != "park/ben" {
		t.Errorf("the sender is parked, on %s", cur)
	}
	if f.be.claims["12"] != "dana@1" || f.be.states["12"] != "in-progress" {
		t.Errorf("claim moves, in-progress stays: %v %v", f.be.claims, f.be.states)
	}
	d, b := f.slot("dana"), f.slot("ben")
	if d.Task() != "12" || d.Branch() != branch || d.ClaimID() != "dana@1" || d.State() != "busy" {
		t.Errorf("receiver: %v", d)
	}
	if b.Task() != "" || b.ClaimID() != "" || b.State() != "idle" {
		t.Errorf("sender: %v", b)
	}
	for _, want := range []string{"You are dana", "handed to you by ben", "rota:handoff ben@1", branch} {
		if !strings.Contains(f.host.sent, want) {
			t.Errorf("brief lacks %q:\n%s", want, f.host.sent)
		}
	}
	last := f.be.comments["12"][len(f.be.comments["12"])-1]
	if !strings.Contains(last, "(transfer, from ben)") || !strings.Contains(last, "carry on from the parser") {
		t.Errorf("handoff: %s", last)
	}
}

func TestTransferRefusals(t *testing.T) {
	f := newMoveFx(t)
	if _, err := f.transfer("12", "", nil); exitOf(err) != exitcode.ExitUsage {
		t.Errorf("empty --to: %v", err)
	}
	if _, err := f.transfer("12", "zed", nil); exitOf(err) != exitcode.ExitUsage {
		t.Errorf("--to outside the roster: %v", err)
	}
	if _, err := f.transfer("99", "dana", nil); exitOf(err) != exitcode.ExitResolution {
		t.Errorf("unknown issue: %v", err)
	}
	if _, err := f.transfer("13", "dana", nil); exitOf(err) != exitcode.ExitResolution {
		t.Errorf("no slot holds 13: %v", err)
	}
	if _, err := f.transfer("12", "dana", func(o *TransferOpts) { o.HolderPID = 999 }); blockedBy(t, err) != BlockNoRound {
		t.Errorf("no lease: %v", err)
	}
	if _, err := f.transfer("12", "ben", nil); blockedBy(t, err) != BlockSameSlot {
		t.Errorf("same slot: %v", err)
	}
	if _, err := f.transfer("12", "dana", func(o *TransferOpts) { o.Settings.Brief = filepath.Join(f.root, "nope.md") }); blockedBy(t, err) != BlockBriefMissing {
		t.Errorf("brief: %v", err)
	}
	if _, err := f.assign("13", "dana"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.transfer("12", "dana", nil); blockedBy(t, err) != BlockSlotBusy {
		t.Errorf("dana holds 13: %v", err)
	}
	if f.be.claims["12"] != "ben@1" || f.slot("ben").Task() != "12" {
		t.Error("a refusal changes nothing")
	}
}

func TestTransferOverlapIsCheckedForTheReceiverAgainstOtherSlots(t *testing.T) {
	f := newMoveFx(t)
	o := startOpts(roundcfg.ScopeMilestone, 100)
	o.Slots = 3
	if _, err := f.env.Start(bg, f.root, o); err != nil {
		t.Fatal(err)
	}
	// Moving 12 to nia: ben (the sender) is no rival, and dana's 13 touches other.go.
	if _, err := f.assign("13", "dana"); err != nil {
		t.Fatal(err)
	}
	// dana's real changes count: she edits the file 12 names.
	f.commit(t, "dana", "internal/cli/round.go")
	_, err := f.transfer("12", "nia", nil)
	if by := blockedBy(t, err); by != BlockOverlap {
		t.Fatalf("12 now overlaps dana's work: %v", err)
	}
	var blk *BlockedError
	errors.As(err, &blk)
	if blk.Readiness == nil || len(blk.Readiness.Overlaps) != 1 || blk.Readiness.Overlaps[0].Slot != "dana" {
		t.Errorf("the overlap names the other slot, not the sender: %+v", blk.Readiness)
	}
	if f.be.claims["12"] != "ben@1" || f.slot("ben").Task() != "12" {
		t.Error("an overlap refusal changes nothing")
	}
	res, err := f.transfer("12", "nia", func(o *TransferOpts) { o.AcceptOverlap = true })
	if err != nil || !res.Dispatched || f.be.claims["12"] != "nia@1" {
		t.Fatalf("--accept-overlap skips only that check: %v %+v", err, res)
	}
}

func TestTransferDispatchFailureKeepsTheMoveAndResumes(t *testing.T) {
	f := newMoveFx(t)
	branch := f.slot("ben").Branch()
	f.env.Worker.NewHost = func(string) host.Host { return &failingHost{hostFake: f.host} }
	res, err := f.transfer("12", "dana", nil)
	if err == nil || res.Dispatched || !res.Changed || res.ClaimID != "dana@1" {
		t.Fatalf("the move is reported though the dispatch failed: %v %+v", err, res)
	}
	if f.be.claims["12"] != "dana@1" || f.slot("dana").Task() != "12" || f.slot("dana").Branch() != branch {
		t.Fatalf("claim and branch stay with the receiver: %v %v", f.be.claims, f.slot("dana"))
	}
	f.env.Worker.NewHost = func(string) host.Host { return &killHost{hostFake: f.host, killed: &f.killed} }
	res, err = f.transfer("12", "dana", nil)
	if err != nil || !res.Dispatched {
		t.Fatalf("the same call resumes: %v %+v", err, res)
	}
	n := 0
	for _, c := range f.be.comments["12"] {
		if strings.Contains(c, "rota:handoff") {
			n++
		}
	}
	if n != 1 || f.be.claims["12"] != "dana@1" {
		t.Errorf("a resume posts and claims nothing new: %d %v", n, f.be.claims)
	}
	if cur := gitIn(t, f.wt("dana"), "symbolic-ref", "--short", "HEAD"); cur != branch {
		t.Errorf("dana on %s", cur)
	}
}

func TestTransferToHumanLabelsAndDispatchesNothing(t *testing.T) {
	f := newMoveFx(t)
	f.host.sent = ""
	spawned := len(f.host.spawned)
	res, err := f.transfer("12", HumanTarget, func(o *TransferOpts) { o.Note = "needs a product call" })
	if err != nil {
		t.Fatal(err)
	}
	if res.To != HumanTarget || res.Dispatched || res.ClaimID != "" || !res.Changed {
		t.Fatalf("%+v", res)
	}
	if f.be.claims["12"] != "" || f.be.states["12"] != "" {
		t.Errorf("no claim and no in-progress: %v %v", f.be.claims, f.be.states)
	}
	if got := f.forge.labels[12]; len(got) != 1 || got[0] != "needs-human" {
		t.Errorf("labels %v", got)
	}
	if f.host.sent != "" || len(f.host.spawned) != spawned {
		t.Error("nothing is dispatched")
	}
	if s := f.slot("ben"); s.Task() != "" || s.State() != "idle" {
		t.Errorf("sender freed: %v", s)
	}
	if cur := gitIn(t, f.wt("ben"), "symbolic-ref", "--short", "HEAD"); cur != "park/ben" {
		t.Errorf("sender parked, on %s", cur)
	}
	cands, _ := f.env.Candidates(bg, f.root, f.be, CandidateOpts{Scope: roundcfg.ScopeMilestone})
	for _, c := range cands {
		if c.ID == "12" {
			t.Fatal("candidates must skip a needs-human issue")
		}
	}
	f.forge.labels[12] = nil // the human clears the label
	cands, _ = f.env.Candidates(bg, f.root, f.be, CandidateOpts{Scope: roundcfg.ScopeMilestone})
	back := false
	for _, c := range cands {
		back = back || c.ID == "12"
	}
	if !back {
		t.Errorf("clearing the label puts it back in the set: %v", ids(cands))
	}
}

// ---- reclaim ----------------------------------------------------------------

func TestReclaimDeadSlotParksReleasesAndClearsTheHandle(t *testing.T) {
	f := newMoveFx(t)
	branch := f.slot("ben").Branch()
	f.agents() // the host has no agent for ben's recorded tab
	rawSlot(f.root, "ben", func(s *jsonx.Object) { s.Set("handle", "w1:ben") })
	os.WriteFile(filepath.Join(f.wt("ben"), "half.txt"), []byte("wip"), 0o644)

	res, err := f.reclaim("ben", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Health != HealthDead || res.Issue != "12" || res.Branch != branch || !res.Salvaged || !res.Released || !res.Parked || !res.Changed {
		t.Fatalf("%+v", res)
	}
	if len(f.killed) != 0 {
		t.Errorf("a dead slot has nothing to kill: %v", f.killed)
	}
	if f.be.claims["12"] != "" || f.be.states["12"] != "" || !f.remoteHas(t, branch) {
		t.Errorf("released, state cleared, branch pushed: %v %v", f.be.claims, f.be.states)
	}
	s := f.slot("ben")
	if s.Task() != "" || s.Handle() != "" || s.State() != "idle" || s.ClaimID() != "" {
		t.Errorf("slot: %v", s)
	}
	last := f.be.comments["12"][len(f.be.comments["12"])-1]
	if !strings.Contains(last, "Reason: reclaimed, dead") || !strings.Contains(last, "(reclaim, from ben)") {
		t.Errorf("handoff: %s", last)
	}
	// Reclaim does not reassign: the issue is assignable, nothing was dispatched.
	if _, err := f.assign("12", "dana"); err != nil {
		t.Errorf("the issue must be assignable afterwards: %v", err)
	}
}

func TestReclaimRefusesAHealthySlotWithoutForceAndKillsAfterForce(t *testing.T) {
	f := newMoveFx(t)
	f.agents("ben")
	rawSlot(f.root, "ben", func(s *jsonx.Object) { s.Set("handle", "w1:ben") })
	if _, err := f.reclaim("ben", nil); blockedBy(t, err) != BlockHealthy {
		t.Fatalf("a healthy slot: %v", err)
	}
	if f.be.claims["12"] != "ben@1" || len(f.killed) != 0 {
		t.Error("a refusal changes nothing and kills nothing")
	}
	res, err := f.reclaim("ben", func(o *ReclaimOpts) { o.Force = true })
	if err != nil || res.Health != HealthHealthy || !res.Changed {
		t.Fatalf("--force: %v %+v", err, res)
	}
	if len(f.killed) != 1 || f.killed[0] != "ben" {
		t.Errorf("a live pane is killed first: %v", f.killed)
	}
	if last := f.be.comments["12"][len(f.be.comments["12"])-1]; !strings.Contains(last, "Reason: reclaimed, forced") {
		t.Errorf("reason: %s", last)
	}
}

func TestReclaimStalledSlotKillsThePaneFirst(t *testing.T) {
	f := newMoveFx(t)
	f.agents("ben")
	rawSlot(f.root, "ben", func(s *jsonx.Object) { s.Set("handle", "w1:ben") })
	f.now = f.now.Add(40 * time.Minute) // ben's commit and dispatch are 40 min old
	res, err := f.reclaim("ben", nil)
	if err != nil || res.Health != HealthStalled || len(f.killed) != 1 {
		t.Fatalf("%v %+v %v", err, res, f.killed)
	}
	// The clock moved 40 min past the fixture; ben's commit came a moment after it.
	if last := f.be.comments["12"][len(f.be.comments["12"])-1]; !strings.Contains(last, "Reason: reclaimed, stalled 39 min") && !strings.Contains(last, "Reason: reclaimed, stalled 40 min") {
		t.Errorf("reason: %s", last)
	}
}

func TestReclaimIdleSlotIsANoOp(t *testing.T) {
	f := newMoveFx(t)
	res, err := f.reclaim("dana", nil)
	if err != nil || res.Health != HealthIdle || res.Changed || res.Issue != "" {
		t.Fatalf("%v %+v", err, res)
	}
	if len(f.be.comments["12"]) != 1 || f.be.claims["12"] != "ben@1" {
		t.Error("nothing is touched")
	}
}

func TestReclaimRefusals(t *testing.T) {
	f := newMoveFx(t)
	if _, err := f.reclaim("zed", nil); exitOf(err) != exitcode.ExitResolution {
		t.Errorf("unknown slot: %v", err)
	}
	if _, err := f.reclaim("ben", func(o *ReclaimOpts) { o.HolderPID = 999 }); blockedBy(t, err) != BlockNoRound {
		t.Errorf("no lease: %v", err)
	}
	// No host to ask: a forced reclaim cannot prove the pane gone.
	f.env.Snapshot = nil
	rawSlot(f.root, "ben", func(s *jsonx.Object) { s.Set("handle", "w1:ben") })
	if _, err := f.reclaim("ben", func(o *ReclaimOpts) { o.Force = true }); blockedBy(t, err) != BlockLiveAgent {
		t.Errorf("live agent: %v", err)
	}
	if f.be.claims["12"] != "ben@1" || len(f.killed) != 0 {
		t.Error("a refusal changes nothing")
	}
	// A slot the registry marks dead needs no host.
	rawSlot(f.root, "ben", func(s *jsonx.Object) { s.Set("state", "dead") })
	if res, err := f.reclaim("ben", nil); err != nil || res.Health != HealthDead {
		t.Errorf("dead without a host: %v %+v", err, res)
	}
}

func TestReclaimReleasesEveryClaimOfTheSlotEvenWhenTheRegistryLostIt(t *testing.T) {
	f := newMoveFx(t)
	f.agents()
	rawSlot(f.root, "ben", func(s *jsonx.Object) { s.Set("handle", "w1:ben"); s.Delete("claimId") })
	res, err := f.reclaim("ben", nil)
	if err != nil || !res.Released {
		t.Fatalf("%v %+v", err, res)
	}
	if f.be.claims["12"] != "" {
		t.Errorf("claims by ben@… are swept: %v", f.be.claims)
	}
}

// A slot left on a file-mode ID by a mid-round `migrate issues` has no issue
// to comment on or release: reclaim warns and still frees the slot (#27).
func TestReclaimFreesASlotWhoseIssueNoLongerResolves(t *testing.T) {
	f := newMoveFx(t)
	f.agents()
	rawSlot(f.root, "ben", func(s *jsonx.Object) { s.Set("handle", "w1:ben"); s.Set("task", "B31") })
	f.be.gone = map[string]bool{"B31": true}
	res, err := f.reclaim("ben", nil)
	if err != nil {
		t.Fatalf("reclaim must not fail on an unresolvable issue: %v", err)
	}
	if !res.Changed || len(res.Warnings) != 3 || !strings.Contains(res.Warnings[0], "B31") {
		t.Fatalf("%+v", res)
	}
	s := f.slot("ben")
	if s.Task() != "" || s.State() != "idle" || s.ClaimID() != "" {
		t.Errorf("slot not freed: %v", s)
	}
	if _, err := f.assign("12", "ben"); err != nil {
		t.Errorf("the slot must take work again: %v", err)
	}
}

// Return and transfer free the slot the same way when the tracker no longer
// resolves the issue's comments, claims and state (#27).
func TestReturnFreesASlotWhoseIssueNoLongerResolves(t *testing.T) {
	f := newMoveFx(t)
	f.be.gone = map[string]bool{"12": true}
	res, err := f.ret("ben", "gone", nil)
	if err != nil {
		t.Fatalf("return must not fail on an unresolvable issue: %v", err)
	}
	if !res.Changed || len(res.Warnings) != 3 {
		t.Fatalf("%+v", res)
	}
	if s := f.slot("ben"); s.Task() != "" || s.State() != "idle" {
		t.Errorf("slot not freed: %v", s)
	}
}

func TestTransferToHumanFreesASenderWhoseIssueNoLongerResolves(t *testing.T) {
	f := newMoveFx(t)
	f.be.gone = map[string]bool{"12": true}
	res, err := f.transfer("12", HumanTarget, nil)
	if err != nil {
		t.Fatalf("transfer must not fail on an unresolvable issue: %v", err)
	}
	if !res.Changed || len(res.Warnings) != 3 {
		t.Fatalf("%+v", res)
	}
	if s := f.slot("ben"); s.Task() != "" || s.State() != "idle" {
		t.Errorf("sender not freed: %v", s)
	}
}

// A handoff comment hv posted before the rename (#236) still names the branch
// the next worker continues from.
func TestLatestHandoffBranchReadsLegacyMarker(t *testing.T) {
	b := &moveBoard{comments: map[string][]string{
		"#5": {"**hv handoff** (return, from ben)\nBranch: `ben/5-x`\n\n<!-- hv:handoff ben@1 -->"},
	}}
	if got := latestHandoffBranch(b, "#5"); got != "ben/5-x" {
		t.Errorf("branch %q", got)
	}
}

// Scope open offers every open item, so one taken outside the round (the
// in-progress label or an open claim, no slot holding it) is not offered (#29
// review). Other scopes keep their behaviour.
func TestOpenScopeSkipsItemsTakenOutsideTheRound(t *testing.T) {
	f := newMoveFx(t)
	f.forge.labels = map[int][]string{12: {"in-progress"}, 13: {"in-progress"}}
	f.be.claims["14"] = "kit@9"
	cands, err := f.env.Candidates(bg, f.root, f.be, CandidateOpts{Scope: roundcfg.ScopeOpen})
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 0 {
		t.Fatalf("labelled 13 and claimed 14 are taken outside the round: %v", ids(cands))
	}
	if cands, _ = f.env.Candidates(bg, f.root, f.be, CandidateOpts{Scope: roundcfg.ScopeMilestone}); len(cands) != 2 {
		t.Errorf("milestone scope is unchanged: %v", ids(cands))
	}
	f.forge.labels[13] = nil
	delete(f.be.claims, "14")
	cands, _ = f.env.Candidates(bg, f.root, f.be, CandidateOpts{Scope: roundcfg.ScopeOpen})
	if got := ids(cands); len(got) != 2 {
		t.Errorf("released items are offered again: %v", got)
	}
}
