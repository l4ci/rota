package round

import (
	"context"
	"errors"
	"github.com/l4ci/rota/internal/exitcode"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/worker"
)

func registryHost(root string) string { return worker.RegistryHost(root) }

// noHost makes any host construction fail the test: under solo a round verb
// must not build, ask or drive a host.
func noHost(t *testing.T, e *Env) {
	t.Helper()
	e.Worker.NewHost = func(d string) host.Host {
		t.Fatalf("solo round reached host.New(%q)", d)
		return nil
	}
	e.Snapshot = func(context.Context) ([]host.Agent, error) {
		t.Fatal("solo round asked the host for a snapshot")
		return nil, nil
	}
}

func setHost(t *testing.T, root, h string) {
	t.Helper()
	if err := worker.Update(root, func(d *worker.Doc) { d.SetHost(h) }); err != nil {
		t.Fatal(err)
	}
}

func TestStartRecordsTheResolvedHostAndKeepsItForTheRound(t *testing.T) {
	f := newAssignFixture(t) // started with Dispatch tmux
	if got := registryHost(f.root); got != "tmux" {
		t.Fatalf("recorded host = %q, want tmux", got)
	}
	// A repeat start of the same round keeps it, whatever resolves now.
	o := startOpts("milestone", 100)
	o.Dispatch = ""
	st, err := f.env.Start(bg, f.root, o)
	if err != nil {
		t.Fatal(err)
	}
	if st.Host != "tmux" || registryHost(f.root) != "tmux" {
		t.Errorf("a restarted start must keep tmux: %q / %q", st.Host, registryHost(f.root))
	}
	// A clean wind-down clears it with the lease; the next round resolves again.
	res, err := f.windDown(nil)
	if err != nil || res.Verdict != VerdictClean {
		t.Fatalf("%+v %v", res, err)
	}
	if got := registryHost(f.root); got != "" {
		t.Errorf("wind-down must clear the host, left %q", got)
	}
	o.Dispatch = "subagent"
	st, err = f.env.Start(bg, f.root, o)
	if err != nil {
		t.Fatal(err)
	}
	if st.Host != host.Solo || registryHost(f.root) != host.Solo {
		t.Errorf("a new round resolves again: %q / %q", st.Host, registryHost(f.root))
	}
	// And honours the environment: herdr in a herdr pane with the binary.
	f.windDown(nil)
	f.env.Getenv = func(k string) string {
		if k == "HERDR_ENV" {
			return "1"
		}
		return ""
	}
	o.LookPath = func(string) (string, error) { return "/bin/herdr", nil }
	if st, err = f.env.Start(bg, f.root, o); err != nil || st.Host != "herdr" {
		t.Errorf("herdr pane: %q %v", st.Host, err)
	}
}

func TestWindDownKeepsTheHostWhileTheLeaseIsKept(t *testing.T) {
	f := newAssignFixture(t)
	f.verifyWith(t, `["exit 3"]`)
	if res, err := f.windDown(nil); err != nil || res.Verdict != VerdictVerifyFailed {
		t.Fatalf("%+v %v", res, err)
	}
	if registryHost(f.root) != "tmux" {
		t.Error("the round is still on: its host stays until the lease goes")
	}
}

func soloAssign(t *testing.T) *assignFixture {
	t.Helper()
	f := newAssignFixture(t)
	setHost(t, f.root, host.Solo)
	f.env.Worker.NewHost = func(d string) host.Host {
		t.Fatalf("solo assign reached host.New(%q)", d)
		return nil
	}
	return f
}

func TestAssignUnderSoloReturnsTheBriefAndDispatchesNothing(t *testing.T) {
	f := soloAssign(t)
	res, err := f.assign("12", "ben", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Dispatched || res.Host != host.Solo || !res.Changed {
		t.Fatalf("%+v", res)
	}
	if !strings.HasPrefix(res.Brief, "--- ORCHESTRATOR (round 1) ---\nYou are ben. Read ") ||
		!strings.Contains(res.Brief, "ben/12-add-the-round-assign-verb") {
		t.Errorf("brief:\n%s", res.Brief)
	}
	wt := filepath.Join(f.root, ".worktrees", "ben")
	if res.Worktree != wt || !filepath.IsAbs(res.Worktree) {
		t.Errorf("worktree = %q, want %q", res.Worktree, wt)
	}
	s := worker.LoadRegistry(f.root).Slot("ben")
	if s.State() != "busy" || s.Task() != "12" || s.ClaimID() != "ben@1" {
		t.Errorf("slot: %v", s)
	}
	for _, k := range []string{"handle", "session", "window", "configDir", "account"} {
		if v, ok := s.Raw().Get(k); ok && v != nil {
			t.Errorf("a solo slot carries no %s: %v", k, v)
		}
	}
	if s.ActiveAt() == "" {
		t.Error("busy arms the stall clock like a dispatch does")
	}
	if f.be.claims["12"] != "ben@1" {
		t.Error("the claim is taken as in tab mode")
	}
	// The same call again resumes: still busy, same brief, no second comment.
	again, err := f.assign("12", "ben", nil)
	if err != nil || again.Brief != res.Brief {
		t.Errorf("resume: %v", err)
	}
}

func TestAssignUnderSoloRefusesACodexWorker(t *testing.T) {
	f := soloAssign(t)
	f.set.Models = map[string]map[string]string{harness.Codex: {"light": "c-l", "standard": "c-s", "heavy": "c-h"}}
	_, err := f.assign("12", "ben", func(o *AssignOpts) { o.Kind = harness.Codex })
	var we *exitcode.Error
	if !errors.As(err, &we) || we.Exit != exitcode.ExitUsage || !strings.Contains(we.Message, "Claude subagents") {
		t.Fatalf("a codex worker under solo is a usage error: %v", err)
	}
	if _, held := f.be.claims["12"]; held {
		t.Error("nothing is marked before the refusal")
	}
}

func TestAssignUnderSoloSkipsTheAccountPick(t *testing.T) {
	f := soloAssign(t)
	f.env.Accounts = &worker.Accounts{}
	f.config(t, `{"work":{"accounts":[{"name":"a","configDir":"/nonexistent"}]}}`)
	if _, err := f.assign("12", "ben", nil); err != nil {
		t.Fatalf("no account is picked under solo: %v", err)
	}
	if v := worker.LoadRegistry(f.root).Slot("ben").Account(); v != "" {
		t.Errorf("account = %q", v)
	}
}

func TestReportRecordsStateAndPRAndIsIdempotent(t *testing.T) {
	f := soloAssign(t)
	if _, err := f.assign("12", "ben", nil); err != nil {
		t.Fatal(err)
	}
	url := "https://github.com/o/r/pull/9"
	r, err := ReportSlot(f.root, ReportOpts{Slot: "ben", State: "DONE", PR: url, Evidence: "built it"})
	if err != nil {
		t.Fatal(err)
	}
	if r.State != "done" || r.Previous != "busy" || r.PR != url || r.Evidence != "built it" || !r.Changed {
		t.Fatalf("%+v", r)
	}
	s := worker.LoadRegistry(f.root).Slot("ben")
	if s.State() != "done" || s.PR() != url {
		t.Errorf("slot: %v", s)
	}
	if v, ok := s.Raw().Get("evidence"); ok {
		t.Errorf("evidence is echoed, never stored: %v", v)
	}
	r, err = ReportSlot(f.root, ReportOpts{Slot: "ben", State: "done", PR: url})
	if err != nil || r.Changed || r.Previous != "done" {
		t.Errorf("repeat: %+v %v", r, err)
	}
	// A PR number is stored as given; state alone leaves the PR.
	if r, err = ReportSlot(f.root, ReportOpts{Slot: "ben", State: "blocked", PR: "#9"}); err != nil || !r.Changed {
		t.Errorf("%+v %v", r, err)
	}
	if _, err = ReportSlot(f.root, ReportOpts{Slot: "ben", State: "idle"}); err != nil {
		t.Fatal(err)
	}
	if got := worker.LoadRegistry(f.root).Slot("ben").PR(); got != "#9" {
		t.Errorf("pr = %q", got)
	}
}

func TestReportRefusals(t *testing.T) {
	f := soloAssign(t)
	cases := []struct {
		name string
		o    ReportOpts
		want int
	}{
		{"busy is not reportable", ReportOpts{Slot: "ben", State: "busy"}, exitcode.ExitUsage},
		{"unknown state", ReportOpts{Slot: "ben", State: "finished"}, exitcode.ExitUsage},
		{"no state", ReportOpts{Slot: "ben"}, exitcode.ExitUsage},
		{"bad pr", ReportOpts{Slot: "ben", State: "done", PR: "soon"}, exitcode.ExitUsage},
		{"unknown slot", ReportOpts{Slot: "zed", State: "done"}, exitcode.ExitResolution},
	}
	for _, c := range cases {
		if _, err := ReportSlot(f.root, c.o); exitOf(err) != c.want {
			t.Errorf("%s: %v, want exit %d", c.name, err, c.want)
		}
	}
	if got := worker.LoadRegistry(f.root).Slot("ben").State(); got != "idle" {
		t.Errorf("a refusal writes nothing, state = %q", got)
	}
	for _, h := range []string{"tmux", "herdr", ""} {
		setHost(t, f.root, h)
		_, err := ReportSlot(f.root, ReportOpts{Slot: "ben", State: "done"})
		if exitOf(err) != exitcode.ExitUsage {
			t.Errorf("host %q: %v, want exit 2", h, err)
		}
	}
}

func TestStatusAndReconcileUnderSoloAreQuietAboutTheHost(t *testing.T) {
	f := soloAssign(t)
	if _, err := f.assign("12", "ben", nil); err != nil {
		t.Fatal(err)
	}
	f.env.HostName, f.env.Snapshot, f.env.HostErr = host.Solo, nil, ""
	f.env.Forge = (&fakeRemote{labelled: []int{12}}).asForge()
	rep, err := f.env.Status(bg, f.root)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Host != host.Solo {
		t.Errorf("host = %q", rep.Host)
	}
	for _, u := range rep.Unavailable {
		if u == SourceHost {
			t.Errorf("solo is not an unavailable source: %v", rep.Unavailable)
		}
	}
	for _, w := range rep.Warnings {
		if strings.Contains(w, "host") {
			t.Errorf("host warning under solo: %s", w)
		}
	}
	for _, fd := range rep.Findings {
		if fd.Kind == DeadTab || fd.Kind == UnclaimedTab {
			t.Errorf("tab finding under solo: %+v", fd)
		}
	}
	out, err := f.env.Reconcile(bg, f.root, true)
	if err != nil || out.Report.Host != host.Solo {
		t.Errorf("reconcile: %v %+v", err, out.Report)
	}
}

func TestReturnTransferReclaimUnderSoloNeverTouchTheHost(t *testing.T) {
	f := newMoveFx(t)
	setHost(t, f.root, host.Solo)
	noHost(t, &f.env)
	f.env.HostName, f.env.Snapshot = host.Solo, nil

	// transfer to a slot: the receiver is marked busy, the brief comes back.
	tr, err := f.transfer("12", "dana", nil)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Dispatched || tr.Host != host.Solo || !strings.Contains(tr.Brief, "handed to you by ben") || tr.Worktree != f.wt("dana") {
		t.Fatalf("%+v", tr)
	}
	if got := f.slot("dana").State(); got != "busy" {
		t.Errorf("receiver state = %q", got)
	}

	// return from dana: no host call.
	f.commit(t, "dana", "dana-work.txt")
	if _, err := f.ret("dana", "wrong premise", nil); err != nil {
		t.Fatalf("return: %v", err)
	}

}

func TestReclaimUnderSoloParksAStalledSlotWithoutAKill(t *testing.T) {
	f := newMoveFx(t)
	setHost(t, f.root, host.Solo)
	noHost(t, &f.env)
	f.env.HostName, f.env.Snapshot = host.Solo, nil
	f.now = f.now.Add(40 * time.Minute)
	res, err := f.reclaim("ben", nil)
	if err != nil || res.Health != HealthStalled || !res.Parked {
		t.Fatalf("reclaim: %v %+v", err, res)
	}
	if len(f.killed) != 0 {
		t.Errorf("killed %v", f.killed)
	}
	if _, st, _ := f.env.ReadLease(bg, f.root); st != roundlease.Live {
		t.Errorf("lease: %v", st)
	}
}

func TestReportRearmsWaitForASoloSlot(t *testing.T) {
	f := soloAssign(t)
	if _, err := f.assign("12", "ben", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := ReportSlot(f.root, ReportOpts{Slot: "ben", State: "done"}); err != nil {
		t.Fatal(err)
	}
	var e worker.Env
	wait := func() worker.WaitResult {
		res, err := e.Wait(context.Background(), f.root, worker.WaitOpts{Slots: []string{"ben"}})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if res := wait(); res.Slot != "ben" || res.State != "done" {
		t.Fatalf("first: %+v", res)
	}
	if res := wait(); !res.TimedOut {
		t.Fatalf("second: %+v, want timed out", res)
	}
	if _, err := ReportSlot(f.root, ReportOpts{Slot: "ben", State: "done"}); err != nil {
		t.Fatal(err)
	}
	if res := wait(); res.Slot != "ben" {
		t.Fatalf("after report: %+v", res)
	}
}
