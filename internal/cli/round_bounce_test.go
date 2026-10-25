package cli

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// Bounce's contract keeps exit 70 specifically for invalid round.maxBounces.
func TestRoundBounceReportsInvalidMaxBounces(t *testing.T) {
	code, out, stderr := roundConfigRun(t, `{"round":{"maxBounces":"many"}}`, "round", "bounce", "31")
	if code != ExitInternal || !strings.Contains(out+stderr, "round.maxBounces must be an integer (got many)") {
		t.Fatalf("exit %d, out %q, stderr %q; want exit %d naming round.maxBounces", code, out, stderr, ExitInternal)
	}
}

func TestRoundBounceCountsAndRefusesAtTheCap(t *testing.T) {
	deps := testDeps()
	dir := workerProject(t, `{"round":{"maxBounces":2}}`)

	for want := 1; want <= 2; want++ {
		code, out, _ := rotaInWith(t, deps, dir, "round", "bounce", "#31", "--head", string(rune('a'+want)), "--json")
		if d := data(t, out); code != 0 || d["bounces"] != float64(want) || d["max"] != float64(2) {
			t.Fatalf("bounce %d: %d %v", want, code, d)
		}
	}
	code, out, errOut := rotaInWith(t, deps, dir, "round", "bounce", "31", "--head", "z", "--json")
	if d := data(t, out); code != 4 || d["bounces"] != float64(2) || d["changed"] != false {
		t.Fatalf("at the cap: %d %v %s", code, d, errOut)
	}
	if !strings.Contains(errOut, "acceptance criteria") || !strings.Contains(errOut, "--cap-reason ambiguous-criteria") {
		t.Errorf("hint must name the audit step:\n%s", errOut)
	}
	if n := worker.LoadRegistryTolerant(dir).Bounces("31"); n != 2 {
		t.Errorf("a refused bounce must not count, registry says %d", n)
	}
	// The cap is per item.
	if code, _, _ := rotaInWith(t, deps, dir, "round", "bounce", "32"); code != 0 {
		t.Errorf("another item: %d", code)
	}
	if code, _, _ := rotaInWith(t, deps, dir, "round", "bounce"); code != 2 {
		t.Errorf("no item: %d, want 2", code)
	}
}

func TestRoundBounceCapZeroIsOff(t *testing.T) {
	deps := testDeps()
	dir := workerProject(t, `{"round":{"maxBounces":0}}`)
	for i := 0; i < 5; i++ {
		if code, _, _ := rotaInWith(t, deps, dir, "round", "bounce", "31"); code != 0 {
			t.Fatalf("bounce %d refused with the cap off: %d", i+1, code)
		}
	}
}

func TestRoundBounceSlotMustBeInThePool(t *testing.T) {
	deps := testDeps()
	dir := workerProject(t, `{}`)
	rotaInWith(t, deps, dir, "worker", "pool", "init", "--slots", "1", "--base", "main")

	code, _, errOut := rotaInWith(t, deps, dir, "round", "bounce", "31", "--slot", "w9", "--head", "a", "--json")
	if code != 4 || !strings.Contains(errOut, "w9") {
		t.Fatalf("unknown slot: %d %s", code, errOut)
	}
	if n := worker.LoadRegistryTolerant(dir).Bounces("31"); n != 0 {
		t.Errorf("a refused slot must write nothing, registry says %d bounces", n)
	}
	if code, _, errOut := rotaInWith(t, deps, dir, "round", "bounce", "31", "--slot", "w1", "--head", "a", "--json"); code != 0 {
		t.Fatalf("known slot: %d %s", code, errOut)
	}
	if n := worker.LoadRegistryTolerant(dir).Bounces("31"); n != 1 {
		t.Errorf("known slot must count, registry says %d", n)
	}
}

// A bundle bounces once, on its lead: a member is refused before it counts.
func TestRoundBounceBundleMember(t *testing.T) {
	deps := testDeps()
	dir := workerProject(t, `{}`)
	rotaInWith(t, deps, dir, "worker", "pool", "init", "--slots", "1", "--base", "main")
	slots := worker.LoadRegistryTolerant(dir).Slots()
	name := slots[0].Name()
	if _, err := worker.UpdateSlot(dir, name, func(s *worker.Slot) { s.SetTask("31"); s.SetBundle([]string{"32", "33"}) }); err != nil {
		t.Fatal(err)
	}
	if err := worker.Update(dir, func(d *worker.Doc) {
		d.QueuePR(worker.QueuedPR{Issue: "40", PR: "#9", From: "kit", Bundle: []string{"41"}})
	}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ member, lead, slot string }{{"#32", "31", name}, {"41", "40", "kit"}} {
		code, out, errOut := rotaInWith(t, deps, dir, "round", "bounce", c.member, "--json")
		d := data(t, out)
		if code != 4 || d["blockedBy"] != "bundle" || d["lead"] != c.lead || d["changed"] != false {
			t.Fatalf("%s: %d %v %s", c.member, code, d, errOut)
		}
		if !strings.Contains(errOut, "bundled under #"+c.lead+" (slot "+c.slot+")") || !strings.Contains(errOut, "rota round bounce "+c.lead) {
			t.Errorf("%s: %s", c.member, errOut)
		}
	}
	reg := worker.LoadRegistryTolerant(dir)
	if reg.Bounces("32") != 0 || reg.Bounces("41") != 0 {
		t.Error("a refused member bounce counts nothing")
	}
	// The lead counts as for any item.
	if code, _, _ := rotaInWith(t, deps, dir, "round", "bounce", "31", "--head", "a"); code != 0 {
		t.Errorf("the lead bounces: %d", code)
	}
	if n := worker.LoadRegistryTolerant(dir).Bounces("31"); n != 1 {
		t.Errorf("lead bounces %d", n)
	}
}

// bounceFakes are the tracker sides of a bounce on main with round.support set:
// the issue's comments and the PR's.
type bounceFakes struct {
	issue []string
	pr    map[int][]string
}

func (f *bounceFakes) Comments(context.Context, tracker.IssueKey) ([]tracker.Comment, error) {
	out := make([]tracker.Comment, len(f.issue))
	for i, b := range f.issue {
		out[i] = tracker.Comment{ID: fmt.Sprint(i), Author: "ann", Body: b}
	}
	return out, nil
}

func (f *bounceFakes) AddComment(_ context.Context, _ tracker.IssueKey, body string) (string, error) {
	f.issue = append(f.issue, body)
	return fmt.Sprint(len(f.issue) - 1), nil
}

func (f *bounceFakes) CanPush(context.Context, string) (bool, error) { return true, nil }

func (f *bounceFakes) DeleteComment(_ context.Context, _ tracker.IssueKey, id string) error {
	for i := range f.issue {
		if fmt.Sprint(i) == id {
			f.issue[i] = "" // a taken-back comment no longer parses as a bounce
		}
	}
	return nil
}

// bounces is the number of bounce comments left on the issue thread.
func (f *bounceFakes) bounces() int {
	n := 0
	for _, b := range f.issue {
		if strings.HasPrefix(b, "<!-- rota:bounce head=") {
			n++
		}
	}
	return n
}

// branchPRForge reports one open PR, as the forge does once a worker opened it.
type branchPRForge struct {
	returnForge
	pr tracker.PR
}

func (f branchPRForge) OpenPRs(context.Context) ([]tracker.PR, error) { return []tracker.PR{f.pr}, nil }

type bounceNotes struct {
	worker.Forge
	f *bounceFakes
}

func (n bounceNotes) CanPush(context.Context, string) (bool, error) { return true, nil }

func (n bounceNotes) MRNotes(_ context.Context, pr int) ([]tracker.Comment, error) {
	var out []tracker.Comment
	for i, b := range n.f.pr[pr] {
		out = append(out, tracker.Comment{ID: fmt.Sprint(i), Author: "ann", Body: b})
	}
	return out, nil
}

func (n bounceNotes) AddMRNote(_ context.Context, pr int, body string) (string, error) {
	n.f.pr[pr] = append(n.f.pr[pr], body)
	return "1", nil
}

// #1598 AC-7/AC-8: on main with round.support set, `round bounce` counts on the
// issue and, for the support machine's PR, posts the bounce on the PR.
func TestRoundBounceSupportPRPostsOnThePR(t *testing.T) {
	fk := &bounceFakes{pr: map[int][]string{}}
	deps := supportBounceDeps(fk, &trackertest.Fake{Issues: []backlog.Issue{
		{Key: tracker.NumKey(31), Title: "thing", State: "open"}, {Key: tracker.NumKey(32), Title: "other", State: "open"}}})
	dir := workerProject(t, `{"issues":{"source":"github"},"round":{"support":"mac","maxBounces":3}}`)
	if err := worker.RegisterExternal(dir, "ext-1", "mac-ben/31-thing", "", "main", "31", "https://github.com/o/r/pull/70"); err != nil {
		t.Fatal(err)
	}
	if err := worker.RegisterExternal(dir, "ext-2", "codex/32-other", "", "main", "32", "https://github.com/o/r/pull/71"); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := rotaInWith(t, deps, dir, "round", "bounce", "31", "--head", "abc", "--force", "--json"); code != 0 || data(t, out)["bounces"] != float64(1) {
		t.Fatalf("bounce: %d %s %s", code, out, errOut)
	}
	if code, _, errOut := rotaInWith(t, deps, dir, "round", "bounce", "32", "--head", "def", "--force"); code != 0 {
		t.Fatalf("bounce of a main PR: %d %s", code, errOut)
	}
	if len(fk.issue) != 2 || !strings.HasPrefix(fk.issue[0], "<!-- rota:bounce head=abc -->") {
		t.Errorf("issue comments %q", fk.issue)
	}
	if n := worker.LoadRegistryTolerant(dir).Bounces("31"); n != 0 {
		t.Errorf("the registry counted %d", n)
	}
	if got := fk.pr[70]; len(got) != 1 || !strings.HasPrefix(got[0], "<!-- rota:review-bounce head=abc -->") {
		t.Errorf("support PR comments %q", got)
	}
	if got := fk.pr[71]; len(got) != 0 {
		t.Errorf("a main PR got the support bounce: %q", got)
	}
}

// A support-machine PR adopted with `worker adopt` bounces like a registered
// one: on the PR it recorded, or a refusal that names the way out when the
// adoption recorded none (#1850).
func TestRoundBounceAdoptedSupportSlot(t *testing.T) {
	fk := &bounceFakes{pr: map[int][]string{}}
	fake := &trackertest.Fake{Issues: []backlog.Issue{
		{Key: tracker.NumKey(31), Title: "thing", State: "open"}, {Key: tracker.NumKey(32), Title: "other", State: "open"}}}
	deps := supportBounceDeps(fk, fake)
	dir := workerProject(t, `{"issues":{"source":"github"},"round":{"support":"mac","maxBounces":3}}`)
	for _, b := range []string{"mac-kit/31-thing", "mac-kit/32-other"} {
		c := exec.Command("git", "branch", b)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
	}
	if err := worker.RegisterExternal(dir, "ext-1", "mac-kit/31-thing", "", "main", "31", "https://github.com/o/r/pull/70"); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := rotaInWith(t, deps, dir, "worker", "adopt", "mac-kit/32-other", "--issue", "32", "--name", "ext-2", "--accept-overlap"); code != 0 {
		t.Fatalf("adopt without a PR: %d %s %s", code, out, errOut)
	}
	if code, out, errOut := rotaInWith(t, deps, dir, "round", "bounce", "31", "--head", "abc", "--slot", "ext-1", "--force", "--json"); code != 0 || data(t, out)["bounces"] != float64(1) {
		t.Fatalf("bounce with a recorded PR: %d %s %s", code, out, errOut)
	}
	if got := fk.pr[70]; len(got) != 1 || !strings.HasPrefix(got[0], "<!-- rota:review-bounce head=abc -->") {
		t.Errorf("support PR comments %q", got)
	}
	code, out, errOut := rotaInWith(t, deps, dir, "round", "bounce", "32", "--head", "def", "--slot", "ext-2", "--force", "--json")
	if d := data(t, out); code != 4 || d["blockedBy"] != "no PR recorded" || d["changed"] != false || d["issue"] != "32" || d["branch"] != "mac-kit/32-other" {
		t.Errorf("bounce with no PR: %d %s %s", code, out, errOut)
	}
	if strings.Contains(out+errOut, "bug in rota") {
		t.Errorf("a refusal reads as a rota bug: %s %s", out, errOut)
	}
	if !strings.Contains(out, "rota round reconcile --apply") || !strings.Contains(out, "rota round bounce 32 --head def --slot ext-2") {
		t.Errorf("the refusal does not name the remedy: %s", out)
	}
	if n := fk.bounces(); n != 1 { // issue 31's only
		t.Errorf("the refused bounce stayed counted: %d bounce comments %q", n, fk.issue)
	}

	// The remedy works under a herdr/tmux round, where `round report` is refused.
	if err := worker.Update(dir, func(d *worker.Doc) { d.SetHost(host.Tmux) }); err != nil {
		t.Fatal(err)
	}
	re := deps.RoundEnv
	deps.RoundEnv = func(ctx context.Context, r string) round.Env {
		e := re(ctx, r)
		e.HostName = host.Tmux
		e.Snapshot = func(context.Context) ([]host.Agent, error) { return nil, nil }
		e.Forge = branchPRForge{returnForge{fake}, tracker.PR{Number: 71, Branch: "mac-kit/32-other", URL: "https://github.com/o/r/pull/71"}}
		return e
	}
	if code, out, errOut := rotaInWith(t, deps, dir, "round", "reconcile", "--apply"); code != 0 {
		t.Fatalf("reconcile: %d %s %s", code, out, errOut)
	}
	if got := worker.LoadRegistryTolerant(dir).Slot("ext-2").PR(); got != "https://github.com/o/r/pull/71" {
		t.Fatalf("reconcile recorded PR %q", got)
	}
	if code, out, errOut := rotaInWith(t, deps, dir, "round", "bounce", "32", "--head", "def", "--slot", "ext-2", "--force", "--json"); code != 0 { // the fake thread is shared by both issues, so the count reads 2
		t.Fatalf("bounce after the remedy: %d %s %s", code, out, errOut)
	}
	if got := fk.pr[71]; len(got) != 1 || !strings.HasPrefix(got[0], "<!-- rota:review-bounce head=def -->") {
		t.Errorf("support PR comments %q", got)
	}
}
