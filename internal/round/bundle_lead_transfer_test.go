package round

import (
	"errors"
	"reflect"
	"testing"

	"github.com/l4ci/rota/internal/worker"
)

func TestTransferBundleLeadToHumanPromotesNextMember(t *testing.T) {
	f := newMoveFx(t)
	f.bundled(t, "15", "16")
	before := f.slot("ben")
	branch := gitIn(t, f.wt("ben"), "symbolic-ref", "--short", "HEAD")
	res, err := f.transfer("12", HumanTarget, nil)
	if err != nil {
		t.Fatalf("transfer bundle lead to human must succeed: %v", err)
	}
	if res.From != "ben" || res.Issue != "12" || !res.Changed || res.Dispatched {
		t.Fatalf("transfer: %+v", res)
	}
	s := f.slot("ben")
	if s.Task() != "15" || s.Issue() != "15" || !reflect.DeepEqual(s.Bundle(), []string{"16"}) {
		t.Errorf("promoted slot: task %q issue %q bundle %v", s.Task(), s.Issue(), s.Bundle())
	}
	if s.Branch() != before.Branch() || s.PR() != before.PR() || s.Attempt() != before.Attempt() || s.State() != before.State() {
		t.Errorf("promotion must preserve the running assignment: before %+v after %+v", before.Raw(), s.Raw())
	}
	if got := gitIn(t, f.wt("ben"), "symbolic-ref", "--short", "HEAD"); got != branch || len(f.killed) != 0 {
		t.Errorf("worker must keep its checkout and session: branch %s killed %v", got, f.killed)
	}
	if f.be.claims["12"] != "" || f.be.bstates["12"] != "" {
		t.Errorf("dropped lead still held: claim %q state %q", f.be.claims["12"], f.be.bstates["12"])
	}
	if got := f.forge.labels[12]; !reflect.DeepEqual(got, []string{"needs-human"}) {
		t.Errorf("dropped lead labels: %v", got)
	}
	for _, id := range []string{"15", "16"} {
		if f.be.claims[id] != "ben@1" || f.be.bstates[id] != "in-progress" {
			t.Errorf("remaining issue %s lost its claim/state: %v %v", id, f.be.claims, f.be.bstates)
		}
	}
	f.env.Board = f.be
	rep, err := f.env.Status(bg, f.root)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rep.Rows {
		if row.Name == "ben" {
			found = true
			if row.Issue != "15" || !reflect.DeepEqual(row.Bundle, []string{"16"}) {
				t.Errorf("status still shows the old lead: %+v", row)
			}
		}
	}
	if !found {
		t.Fatal("promoted slot missing from status")
	}
}

func TestTransferPromotedLastIssueToHumanFreesSlot(t *testing.T) {
	f := newMoveFx(t)
	f.bundled(t, "15")
	if _, err := f.transfer("12", HumanTarget, nil); err != nil {
		t.Fatal(err)
	}
	if s := f.slot("ben"); s.HeldID() != "15" || len(s.Bundle()) != 0 {
		t.Fatalf("last member must become an ordinary assignment: %+v", s.Raw())
	}
	if _, err := f.transfer("15", HumanTarget, nil); err != nil {
		t.Fatal(err)
	}
	if s := f.slot("ben"); s.Task() != "" || len(s.Bundle()) != 0 || s.ClaimID() != "" || !worker.IsIdle(s.State()) {
		t.Errorf("last transfer did not free the slot: %+v", s.Raw())
	}
	if branch := gitIn(t, f.wt("ben"), "symbolic-ref", "--short", "HEAD"); branch != "park/ben" {
		t.Errorf("last transfer must park the worker: %s", branch)
	}
	if f.be.claims["15"] != "" || f.be.bstates["15"] != "" || !reflect.DeepEqual(f.forge.labels[15], []string{"needs-human"}) {
		t.Errorf("last issue not released to human: %v %v %v", f.be.claims, f.be.bstates, f.forge.labels)
	}
}

func TestTransferQueuedBundleLeadToHumanPromotesNextMember(t *testing.T) {
	f := newMoveFx(t)
	f.bundled(t, "15", "16")
	f.finish(t, "ben", pr7)
	if _, err := f.assign("13", "ben"); err != nil {
		t.Fatal(err)
	}
	before := f.queued()[0]
	if _, err := f.transfer("12", HumanTarget, nil); err != nil {
		t.Fatal(err)
	}
	q := f.queued()
	if len(q) != 1 {
		t.Fatalf("promotion lost queued PR: %+v", q)
	}
	want := before
	want.Issue, want.Bundle = "15", []string{"16"}
	if !reflect.DeepEqual(q[0], want) {
		t.Errorf("queued promotion: got %+v want %+v", q[0], want)
	}
	if f.be.claims["12"] != "" || f.be.claims["15"] != "ben@1" || f.be.claims["16"] != "ben@1" || f.slot("ben").HeldID() != "13" {
		t.Errorf("promotion must release only the lead: %v slot %+v", f.be.claims, f.slot("ben").Raw())
	}
	f.env.Board = f.be
	rep, err := f.env.Status(bg, f.root)
	if err != nil || len(rep.Queued) != 1 || rep.Queued[0].Issue != "15" || !reflect.DeepEqual(rep.Queued[0].Bundle, []string{"16"}) {
		t.Fatalf("queued status: %+v error %v", rep, err)
	}
	// Promotion can repeat down to an ordinary queued PR, whose final transfer removes it.
	for _, id := range []string{"15", "16"} {
		if _, err := f.transfer(id, HumanTarget, nil); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.queued()) != 0 || f.slot("ben").HeldID() != "13" {
		t.Errorf("last queued transfer affected the reused slot: queued %+v slot %+v", f.queued(), f.slot("ben").Raw())
	}
}

func TestTransferBundleLeadRetryAfterStateFailure(t *testing.T) {
	f := newMoveFx(t)
	f.bundled(t, "15", "16")
	f.be.failState = errors.New("tracker unavailable")
	if _, err := f.transfer("12", HumanTarget, nil); err == nil {
		t.Fatal("transfer must report the tracker failure")
	}
	if s := f.slot("ben"); s.HeldID() != "12" || !reflect.DeepEqual(s.Bundle(), []string{"15", "16"}) {
		t.Fatalf("failed transfer promoted before releasing state: %+v", s.Raw())
	}
	if _, err := f.transfer("12", HumanTarget, nil); err != nil {
		t.Fatal(err)
	}
	if s := f.slot("ben"); s.HeldID() != "15" || !reflect.DeepEqual(s.Bundle(), []string{"16"}) {
		t.Fatalf("retry did not promote exactly once: %+v", s.Raw())
	}
}
