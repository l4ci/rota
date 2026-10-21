package round

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// bundleFixture is a round whose issues 15..18 are small, ready and unrelated
// to each other on disk; 12 is the lead.
func bundleFixture(t *testing.T) *assignFixture {
	t.Helper()
	f := newAssignFixture(t)
	for _, n := range []string{"15", "16", "17", "18"} {
		f.be.add(n, "Member "+n, "M01", false, "## Acceptance\n- [ ] AC-of-"+n+" holds\n\n## Out of scope\n- nothing-of-"+n+"\n\n## Touches\n- GET /thing-"+n+"\n")
	}
	return f
}

func bundleOf(ids ...string) func(*AssignOpts) {
	return func(o *AssignOpts) { o.Bundle = ids }
}

func TestAssignBundleBindsLeadAndMembers(t *testing.T) {
	f := bundleFixture(t)
	res, err := f.assign("12", "ben", bundleOf("15", "16"))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Dispatched || !res.Ready() || !reflect.DeepEqual(res.Bundle, []string{"15", "16"}) || len(res.Members) != 2 || res.Members[0].ID != "15" {
		t.Fatalf("%+v", res)
	}
	s := worker.LoadRegistryTolerant(f.root).Slot("ben")
	if s.Task() != "12" || !reflect.DeepEqual(s.Bundle(), []string{"15", "16"}) || !reflect.DeepEqual(s.BundleIssues(), []string{"12", "15", "16"}) {
		t.Errorf("slot: task %q bundle %v", s.Task(), s.Bundle())
	}
	for _, id := range []string{"12", "15", "16"} {
		if f.be.claims[id] != "ben@1" || f.be.bstates[id] != "in-progress" {
			t.Errorf("%s: claim %q state %q", id, f.be.claims[id], f.be.bstates[id])
		}
	}
	if len(f.be.claims) != 3 {
		t.Errorf("only the bundle is claimed: %v", f.be.claims)
	}
	for _, id := range []string{"15", "16"} {
		c := f.be.comments[id]
		if len(c) != 1 || !strings.HasPrefix(c[0], assignMarker) || !strings.Contains(c[0], "bundled with #12") || !strings.Contains(c[0], res.Branch) {
			t.Errorf("%s comment: %q", id, c)
		}
	}
	for _, want := range []string{"bundle of #12, #15, #16", "ONE PR", "Closes #N", "AC-of-15", "AC-of-16", "nothing-of-15", "GET /thing-16", "rota proof record"} {
		if !strings.Contains(f.host.sent, want) {
			t.Errorf("brief lacks %q:\n%s", want, f.host.sent)
		}
	}
	if !strings.Contains(f.host.sent, "- [ ] works") {
		t.Errorf("brief lacks the lead's acceptance:\n%s", f.host.sent)
	}
	if !strings.HasSuffix(strings.TrimSpace(f.host.sent), "(your PR's URL) as the last line of your last message, then stop. The poll reads only that line; a summary alone leaves your slot looking stuck.") {
		t.Errorf("the ROTA-DONE line must stay last:\n%s", f.host.sent)
	}
}

func TestAssignBundleRefusals(t *testing.T) {
	// One refused call per case: nothing is claimed, marked or dispatched.
	cases := []struct {
		name   string
		setup  func(f *assignFixture)
		lead   string
		mod    func(*AssignOpts)
		by     string
		inText string
	}{
		{"oversize", nil, "12", func(o *AssignOpts) { o.Bundle = []string{"15", "16"}; o.Settings.BundleMax = 1 }, BlockBundle, "round.bundleMax"},
		{"member held by another slot", func(f *assignFixture) {
			if _, err := f.assign("15", "dana", nil); err != nil {
				t.Fatal(err)
			}
		}, "12", bundleOf("15"), BlockClaimed, "15"},
		{"member in another slot's bundle", func(f *assignFixture) {
			if _, err := f.assign("16", "dana", bundleOf("17")); err != nil {
				t.Fatal(err)
			}
		}, "12", bundleOf("17"), BlockClaimed, "17"},
		{"member not ready", nil, "12", bundleOf("15", "14"), BlockNotReady, "14"},
		{"member closed", func(f *assignFixture) { f.be.items["15"].Closed = true }, "12", bundleOf("15"), BlockBundle, "closed"},
		{"best-of member", func(f *assignFixture) { f.labelled("15", BestOfLabel) }, "12", bundleOf("15"), BlockBundle, "best-of:2"},
		{"best-of lead", func(f *assignFixture) { f.labelled("12", BestOfLabel) }, "12", bundleOf("15"), BlockBundle, "best-of:2"},
		{"member overlaps another slot", func(f *assignFixture) {
			f.be.add("30", "Holder", "M01", false, "## Acceptance\n- [ ] x\n\n## Touches\n- POST /items\n")
			f.be.add("31", "Clasher", "M01", false, "## Acceptance\n- [ ] x\n\n## Touches\n- post /items\n")
			if _, err := f.assign("30", "dana", nil); err != nil {
				t.Fatal(err)
			}
		}, "12", func(o *AssignOpts) { o.Bundle = []string{"15", "31"}; o.Settings.ScopeOverlap = "block" }, BlockOverlap, "31"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := bundleFixture(t)
			if c.setup != nil {
				c.setup(f)
			}
			claims, states, spawned := len(f.be.claims), len(f.be.bstates), len(f.host.spawned)
			_, err := f.assign(c.lead, "ben", c.mod)
			if by := blockedBy(t, err); by != c.by || !strings.Contains(err.Error(), c.inText) {
				t.Fatalf("want %s naming %q: %v", c.by, c.inText, err)
			}
			if len(f.be.claims) != claims || len(f.be.bstates) != states || len(f.host.spawned) != spawned {
				t.Errorf("a refused bundle must touch nothing: %v %v %v", f.be.claims, f.be.bstates, f.host.spawned)
			}
			if s := worker.LoadRegistryTolerant(f.root).Slot("ben"); s.Task() != "" || len(s.Bundle()) != 0 {
				t.Errorf("ben was bound: %v", s)
			}
		})
	}
}

func TestAssignBundleMaxZeroIsNoLimit(t *testing.T) {
	f := bundleFixture(t)
	res, err := f.assign("12", "ben", func(o *AssignOpts) { o.Bundle = []string{"15", "16", "17", "18"}; o.Settings.BundleMax = 0 })
	if err != nil || len(res.Bundle) != 4 {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestAssignBundleMemberWithAnOpenPRIsRefused(t *testing.T) {
	f := bundleFixture(t)
	for _, id := range []string{"12", "15"} {
		n, _ := strconv.Atoi(id)
		f.be.items[id].Number = n
	}
	f.env.Forge = (&fakeRemote{prs: []tracker.PR{{Number: 104, Branch: "kit/99-other", Body: "Closes #15"}}}).asForge()
	_, err := f.assign("12", "ben", bundleOf("15"))
	if by := blockedBy(t, err); by != BlockOpenPR || !strings.Contains(err.Error(), "15") || !strings.Contains(err.Error(), "#104") {
		t.Fatalf("an open PR on a member refuses the bundle: %v", err)
	}
	if len(f.be.claims) != 0 {
		t.Errorf("claims: %v", f.be.claims)
	}
	if _, err := f.assign("12", "ben", func(o *AssignOpts) { o.Bundle = []string{"15"}; o.AcceptOpenPR = true }); err != nil {
		t.Fatalf("--accept-open-pr: %v", err)
	}
}

func TestAssignBundleRepeatedMemberIsUsage(t *testing.T) {
	f := bundleFixture(t)
	for _, b := range [][]string{{"15", "15"}, {"12"}, {"#12"}, {"15", "#15"}} {
		_, err := f.assign("12", "ben", bundleOf(b...))
		var ee *exitcode.Error
		if !errors.As(err, &ee) || ee.Exit != exitcode.ExitUsage {
			t.Errorf("%v: want a usage error, got %v", b, err)
		}
	}
	if len(f.be.claims) != 0 {
		t.Errorf("claims: %v", f.be.claims)
	}
}

func TestAssignBundleCheckOnlyWritesNothing(t *testing.T) {
	f := bundleFixture(t)
	res, err := f.assign("12", "ben", func(o *AssignOpts) { o.CheckOnly = true; o.Bundle = []string{"15", "14"} })
	if err != nil {
		t.Fatal(err)
	}
	if res.Ready() || len(res.Members) != 2 || !res.Members[0].Ready() || res.Members[1].Ready() || res.Members[1].ID != "14" {
		t.Errorf("a not-ready member makes the bundle not ready: %+v", res)
	}
	if !res.Readiness.Ready() {
		t.Errorf("the lead itself is ready")
	}
	if len(f.be.claims) != 0 || len(f.be.bstates) != 0 || len(f.be.notes) != 0 || len(f.host.spawned) != 0 {
		t.Errorf("check-only wrote: %v %v %v %v", f.be.claims, f.be.bstates, f.be.notes, f.host.spawned)
	}
	if s := worker.LoadRegistryTolerant(f.root).Slot("ben"); s.Task() != "" || len(s.Bundle()) != 0 {
		t.Errorf("slot bound: %v", s)
	}
	ok, err := f.assign("12", "ben", func(o *AssignOpts) { o.CheckOnly = true; o.Bundle = []string{"15", "16"} })
	if err != nil || !ok.Ready() || len(ok.Members) != 2 {
		t.Errorf("all ready: %v %+v", err, ok)
	}
}

func TestAssignBundleResume(t *testing.T) {
	f := bundleFixture(t)
	// The lead and a member declare the same scope: the bundle never overlaps itself.
	f.be.add("40", "Lead", "M01", false, "## Acceptance\n- [ ] x\n\n## Touches\n- POST /items\n")
	f.be.add("41", "Member", "M01", false, "## Acceptance\n- [ ] x\n\n## Touches\n- POST /items\n")
	block := func(o *AssignOpts) { o.Settings.ScopeOverlap = "block"; o.Bundle = []string{"41"} }
	if _, err := f.assign("40", "ben", block); err != nil {
		t.Fatalf("a bundle's own scopes do not clash: %v", err)
	}
	notes := len(f.be.notes)
	// Again with the same bundle, and with none: the recorded bundle stays.
	for _, mod := range []func(*AssignOpts){block, func(o *AssignOpts) { o.Settings.ScopeOverlap = "block" }} {
		res, err := f.assign("40", "ben", mod)
		if err != nil || !reflect.DeepEqual(res.Bundle, []string{"41"}) {
			t.Fatalf("resume: %v %+v", err, res)
		}
	}
	if len(f.be.notes) != notes {
		t.Errorf("a resume comments again: %v", f.be.notes)
	}
	if got := worker.LoadRegistryTolerant(f.root).Slot("ben").Bundle(); !reflect.DeepEqual(got, []string{"41"}) {
		t.Errorf("bundle: %v", got)
	}
	_, err := f.assign("40", "ben", bundleOf("15"))
	if by := blockedBy(t, err); by != BlockBundle || !strings.Contains(err.Error(), "rota round transfer") {
		t.Errorf("a different bundle on a resume: %v", err)
	}
}

func TestInFlightItemsIncludeBundleMembers(t *testing.T) {
	f := bundleFixture(t)
	if _, err := f.assign("12", "ben", bundleOf("15", "16")); err != nil {
		t.Fatal(err)
	}
	slotOf := func() map[string]string {
		got := map[string]string{}
		for _, x := range f.env.InFlightItems(bg, f.root, f.be, f.env.trackedFiles(bg, f.root), nil) {
			got[x.Issue] = x.Slot
		}
		return got
	}
	if got := slotOf(); !reflect.DeepEqual(got, map[string]string{"12": "ben", "15": "ben", "16": "ben"}) {
		t.Errorf("slot members: %v", got)
	}
	// A member carries its own scope, so another assign clashing on it is refused.
	f.be.add("32", "Clasher", "M01", false, "## Acceptance\n- [ ] x\n\n## Touches\n- get /thing-15\n")
	_, err := f.assign("32", "dana", func(o *AssignOpts) { o.Settings.ScopeOverlap = "block" })
	if by := blockedBy(t, err); by != BlockOverlap {
		t.Errorf("a member's scope blocks another slot: %v", err)
	}
	// Queued: the record keeps the bundle and its members stay in flight.
	err = worker.Update(f.root, func(doc *worker.Doc) {
		doc.QueuePR(worker.QueuedPR{Issue: "20", Branch: "kit/20-x", PR: "https://github.com/o/r/pull/20", From: "kit", Bundle: []string{"17"}})
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := slotOf(); got["17"] != "queue:kit" {
		t.Errorf("queued member: %v", got)
	}
}

func TestHeldIDsCountBundleMembers(t *testing.T) {
	f := bundleFixture(t)
	if _, err := f.assign("12", "ben", bundleOf("15", "16")); err != nil {
		t.Fatal(err)
	}
	err := worker.Update(f.root, func(doc *worker.Doc) {
		doc.QueuePR(worker.QueuedPR{Issue: "20", Branch: "kit/20-x", PR: "https://github.com/o/r/pull/20", From: "kit", Bundle: []string{"17"}})
	})
	if err != nil {
		t.Fatal(err)
	}
	held := heldIDs(f.root)
	for _, id := range []string{"12", "15", "16", "20", "17"} {
		if !held[id] {
			t.Errorf("%s not held: %v", id, held)
		}
	}
	if held["18"] {
		t.Errorf("18 is free: %v", held)
	}
}

// A bundled member carries the slot's in-progress label, so reconcile must not
// report it as an orphan no slot holds.
func TestReconcileBundleMemberIsNotAnOrphan(t *testing.T) {
	root, e, _ := fixture(t)
	if err := editSlot(root, "kit", func(s *worker.Slot) error { s.SetBundle([]string{"99"}); return nil }); err != nil {
		t.Fatal(err)
	}
	rep, err := e.Status(bg, root)
	if err != nil {
		t.Fatal(err)
	}
	for _, fd := range rep.Findings {
		if fd.Kind == LabelOrphan {
			t.Errorf("member 99 reported as an orphan: %+v", fd)
		}
	}
}
