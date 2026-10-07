package round

import (
	"errors"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/worker"
)

func TestBestOfOfReadsTheLabel(t *testing.T) {
	issue := backlog.Capabilities{IssueIDs: true}
	for _, c := range []struct {
		name   string
		caps   backlog.Capabilities
		labels []string
		want   bool
		by     string
		in     string
	}{
		{"none", issue, nil, false, "", ""},
		{"other labels", issue, []string{"bug", "harness:codex"}, false, "", ""},
		{"best-of:2", issue, []string{"bug", "best-of:2"}, true, "", ""},
		{"best-of:3", issue, []string{"best-of:3"}, false, BlockBestOfLabel, "best-of:3"},
		{"two labels", issue, []string{"best-of:2", "best-of:3"}, false, BlockBestOfLabel, "best-of:2, best-of:3"},
		{"file items carry no labels", backlog.Capabilities{}, []string{"best-of:3"}, false, "", ""},
	} {
		got, err := BestOfOf(c.caps, backlog.Item{ID: "12", Labels: c.labels})
		if c.by == "" {
			if err != nil || got != c.want {
				t.Errorf("%s: %v %v", c.name, got, err)
			}
			continue
		}
		var blk *BlockedError
		if !errors.As(err, &blk) || blk.By != c.by || !strings.Contains(blk.Msg, c.in) || !strings.Contains(blk.Msg, "only best-of:2 is supported") {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

// bestOfFixture is a round with issue 12 labelled best-of:2.
func bestOfFixture(t *testing.T) *assignFixture {
	t.Helper()
	f := newAssignFixture(t)
	f.labelled("12", BestOfLabel)
	return f
}

func TestAssignBestOfBindsTwoSlots(t *testing.T) {
	f := bestOfFixture(t)
	res, err := f.assign("12", "", nil)
	if err != nil || len(res.BestOf) != 2 {
		t.Fatalf("%v %+v", err, res)
	}
	a, b := res.BestOf[0], res.BestOf[1]
	if a.Agent != "ben" || b.Agent != "dana" || a.Branch == b.Branch || !a.Dispatched || !b.Dispatched {
		t.Fatalf("two slots, two branches: %+v %+v", a, b)
	}
	if res.Agent != "ben" || res.Branch != a.Branch {
		t.Errorf("the result is the first attempt's: %+v", res)
	}
	if f.be.claims["12"] != "ben@1" || strings.Join(f.be.more["12"], ",") != "dana@1" || f.be.bstates["12"] != "in-progress" {
		t.Errorf("both claims on one in-progress issue: %v %v %v", f.be.claims, f.be.more, f.be.bstates)
	}
	rec := worker.LoadRegistry(f.root).BestOf("12")
	if rec == nil || len(rec.Attempts) != 2 || rec.Attempts[0] != (worker.BestOfAttempt{Slot: "ben", Branch: a.Branch, ClaimID: "ben@1"}) ||
		rec.Attempts[1] != (worker.BestOfAttempt{Slot: "dana", Branch: b.Branch, ClaimID: "dana@1"}) {
		t.Errorf("record: %+v", rec)
	}
	reg := worker.LoadRegistry(f.root)
	if reg.Slot("ben").HeldID() != "12" || reg.Slot("dana").HeldID() != "12" {
		t.Errorf("both slots hold the issue")
	}
}

func TestAssignBestOfGetsDistinctSmokeSections(t *testing.T) {
	f := smokeFixture(t)
	f.labelled("15", BestOfLabel)
	commitSections(t, f.root, "125")
	res, err := f.assign("15", "", nil)
	if err != nil || len(res.BestOf) != 2 || res.BestOf[0].SmokeSection != 126 || res.BestOf[1].SmokeSection != 127 {
		t.Fatalf("each attempt gets its own number: %v %+v", err, res)
	}
	again, err := f.assign("15", "", nil)
	if err != nil || len(again.BestOf) != 2 || again.BestOf[0].SmokeSection != 126 || again.BestOf[1].SmokeSection != 127 {
		t.Fatalf("a resumed assign keeps both numbers: %v %+v", err, again)
	}
}

func TestAssignBestOfNeedsTwoFreeSlots(t *testing.T) {
	f := bestOfFixture(t)
	if _, err := f.assign("13", "ben", func(o *AssignOpts) { o.AcceptOverlap = true }); err != nil {
		t.Fatal(err)
	}
	before := worker.LoadRegistry(f.root).Slot("dana").HeldID()
	claims := len(f.be.claims)
	_, err := f.assign("12", "", nil)
	var blk *BlockedError
	if !errors.As(err, &blk) || blk.By != BlockBestOfSlots || !strings.Contains(blk.Msg, "needs two free slots; 1 free") {
		t.Fatalf("%v", err)
	}
	reg := worker.LoadRegistry(f.root)
	if reg.BestOf("12") != nil || reg.Slot("dana").HeldID() != before || len(f.be.claims) != claims || f.be.claims["12"] != "" || f.be.bstates["12"] != "" {
		t.Errorf("nothing changed: %v %v %v", f.be.claims, f.be.bstates, reg.BestOf("12"))
	}
}

func TestAssignBestOfKinds(t *testing.T) {
	f := bestOfFixture(t)
	f.config(t, codexCfg)
	(&codexRig{version: "codex-cli 0.159.2\n", loggedIn: true}).install(f)
	f.host.name = "herdr"
	res, err := f.assign("12", "", nil)
	if err != nil || len(res.BestOf) != 2 {
		t.Fatalf("%v %+v", err, res)
	}
	if a, b := res.BestOf[0], res.BestOf[1]; a.Kind != "claude" || b.Kind != "codex" || b.KindSource != "best-of" || b.Model != "c-s" {
		t.Errorf("one claude, one codex: %+v %+v", a, b)
	}

	// A harness: label pins both attempts to that kind.
	g := bestOfFixture(t)
	g.labelled("12", BestOfLabel, "harness:codex")
	g.config(t, codexCfg)
	(&codexRig{version: "codex-cli 0.159.2\n", loggedIn: true}).install(g)
	g.host.name = "herdr"
	res, err = g.assign("12", "", nil)
	if err != nil || len(res.BestOf) != 2 || res.BestOf[0].Kind != "codex" || res.BestOf[1].Kind != "codex" || res.BestOf[1].KindSource != KindFromLabel {
		t.Fatalf("both codex: %v %+v", err, res)
	}

	// Codex not configured: both attempts take the resolved kind.
	h := bestOfFixture(t)
	res, err = h.assign("12", "", nil)
	if err != nil || res.BestOf[0].Kind != "claude" || res.BestOf[1].Kind != "claude" {
		t.Fatalf("both claude: %v %+v", err, res)
	}
}

func TestAssignBestOfCheckOnlyChangesNothing(t *testing.T) {
	f := bestOfFixture(t)
	res, err := f.assign("12", "", func(o *AssignOpts) { o.CheckOnly = true })
	if err != nil || !res.Ready() || res.Dispatched {
		t.Fatalf("%v %+v", err, res)
	}
	if worker.LoadRegistry(f.root).BestOf("12") != nil || len(f.be.claims) != 0 || len(f.be.bstates) != 0 {
		t.Errorf("check-only wrote something")
	}
}

func TestAssignBestOfRefusesOtherLabels(t *testing.T) {
	for _, labels := range [][]string{{"best-of:3"}, {"best-of:2", "best-of:3"}} {
		f := newAssignFixture(t)
		f.labelled("12", labels...)
		_, err := f.assign("12", "", nil)
		var blk *BlockedError
		if !errors.As(err, &blk) || blk.By != BlockBestOfLabel || !strings.Contains(blk.Msg, labels[len(labels)-1]) {
			t.Fatalf("%v: %v", labels, err)
		}
		if len(f.be.claims) != 0 || worker.LoadRegistry(f.root).Slot("ben").HeldID() != "" {
			t.Errorf("a refusal marks nothing")
		}
	}
}

func TestAssignBestOfBriefNamesTheSibling(t *testing.T) {
	f := bestOfFixture(t)
	res, err := f.assign("12", "", nil)
	if err != nil || len(f.host.sents) != 2 {
		t.Fatalf("%v %d briefs", err, len(f.host.sents))
	}
	for i, sib := range []Assigned{res.BestOf[1], res.BestOf[0]} {
		for _, want := range []string{"best-of:2", "slot " + sib.Agent + " builds it too", sib.Branch, "Do not read, fetch, check out or diff that branch", "Closes #12"} {
			if !strings.Contains(f.host.sents[i], want) {
				t.Errorf("brief %d lacks %q:\n%s", i, want, f.host.sents[i])
			}
		}
	}
}

func TestAssignSinglyDropsAStaleBestOfRecord(t *testing.T) {
	f := newAssignFixture(t)
	worker.Update(f.root, func(d *worker.Doc) {
		d.SetBestOf(worker.BestOf{Issue: "12", Attempts: []worker.BestOfAttempt{{Slot: "kit", Branch: "kit/12-x", ClaimID: "kit@1"}}})
	})
	if _, err := f.assign("12", "ben", nil); err != nil {
		t.Fatal(err)
	}
	if worker.LoadRegistry(f.root).BestOf("12") != nil {
		t.Errorf("a singly assigned issue keeps no best-of record")
	}
}

func TestBestOfAttemptsAreNotClaimDrift(t *testing.T) {
	f := bestOfFixture(t)
	if _, err := f.assign("12", "", nil); err != nil {
		t.Fatal(err)
	}
	f.env.Board, f.env.Forge = f.be, f.be.asForge()
	rep, err := f.env.Status(bg, f.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, fd := range rep.Findings {
		if fd.Kind == ClaimMismatch {
			t.Errorf("no claim-mismatch for either attempt: %+v", fd)
		}
	}
	rows := map[string]string{}
	for _, r := range rep.Rows {
		rows[r.Name] = r.BestOf
	}
	if rows["ben"] != "dana" || rows["dana"] != "ben" {
		t.Errorf("each row names its sibling: %v", rows)
	}
	cands, err := f.env.Candidates(bg, f.root, f.be, CandidateOpts{Scope: "milestone"})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cands {
		if c.ID == "12" {
			t.Errorf("an issue either attempt holds is no candidate")
		}
	}

	// A claim that is gone is still drift for an attempt.
	f.be.Release("12", "dana@1")
	rep, _ = f.env.Status(bg, f.root)
	found := false
	for _, fd := range rep.Findings {
		found = found || (fd.Kind == ClaimMismatch && fd.Slot == "dana")
	}
	if !found {
		t.Errorf("dana's lost claim must be reported: %+v", rep.Findings)
	}
}

func TestStatusRowsOfASingleIssueHaveNoBestOf(t *testing.T) {
	f := newAssignFixture(t)
	if _, err := f.assign("12", "ben", nil); err != nil {
		t.Fatal(err)
	}
	rep, _ := f.env.Status(bg, f.root)
	for _, r := range rep.Rows {
		if r.BestOf != "" {
			t.Errorf("%+v", r)
		}
	}
}

func TestOpenClaimWithFindsTheSecondClaim(t *testing.T) {
	be := &fakeRemote{}
	be.ClaimShared("12", "ben@1", 2)
	be.ClaimShared("12", "dana@1", 2)
	if got := openClaimWith(be, "12", "dana@"); got != "dana@1" {
		t.Errorf("a sweep must find the later claim: %q", got)
	}
}

func TestReturnOfOneAttemptKeepsTheStateWhileTheSiblingClaims(t *testing.T) {
	f := newMoveFx(t)
	f.be.ClaimShared("12", "dana@1", 2)
	if _, err := f.ret("ben", "stuck", nil); err != nil {
		t.Fatal(err)
	}
	if f.be.bstates["12"] != "in-progress" || f.be.claims["12"] != "dana@1" {
		t.Errorf("the sibling still builds it: %v %v", f.be.bstates, f.be.claims)
	}
	// The last claim out clears the state.
	f2 := newMoveFx(t)
	if _, err := f2.ret("ben", "stuck", nil); err != nil || f2.be.bstates["12"] != "" {
		t.Errorf("a lone claim clears the state: %v %v", err, f2.be.bstates)
	}
}

func TestTransferRefusesABestOfIssue(t *testing.T) {
	f := newMoveFx(t)
	worker.Update(f.root, func(d *worker.Doc) {
		d.SetBestOf(worker.BestOf{Issue: "12", Attempts: []worker.BestOfAttempt{
			{Slot: "ben", Branch: "ben/12-x", ClaimID: "ben@1"}, {Slot: "dana", Branch: "dana/12-x", ClaimID: "dana@1"}}})
	})
	_, err := f.transfer("12", "dana", nil)
	var blk *BlockedError
	if !errors.As(err, &blk) || blk.By != BlockBestOf || !strings.Contains(blk.Msg, "ben and dana") {
		t.Fatalf("%v", err)
	}
	if f.slot("ben").HeldID() != "12" {
		t.Errorf("nothing moved")
	}
}

func TestStaleQueuedPRDropsOnlyItsOwnRecord(t *testing.T) {
	f := newAssignFixture(t)
	worker.Update(f.root, func(d *worker.Doc) {
		d.QueuePR(worker.QueuedPR{Issue: "12", PR: "#7", From: "ben", Branch: "ben/12-x"})
		d.QueuePR(worker.QueuedPR{Issue: "12", PR: "#8", From: "dana", Branch: "dana/12-x"})
	})
	f.env.Forge = (&fakeRemote{states: map[int]string{7: "merged", 8: "open"}}).asForge()
	if _, err := f.env.Reconcile(bg, f.root, true); err != nil {
		t.Fatal(err)
	}
	prs := worker.LoadRegistry(f.root).PRs()
	if len(prs) != 1 || prs[0].PR != "#8" {
		t.Errorf("only the merged PR's record goes: %+v", prs)
	}
}
