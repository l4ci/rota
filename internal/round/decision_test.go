package round

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/worker"
)

func TestDecisionOf(t *testing.T) {
	issues, file := backlog.Capabilities{IssueIDs: true}, backlog.Capabilities{}
	cases := []struct {
		name string
		caps backlog.Capabilities
		it   backlog.Item
		want string
		by   string
	}{
		{"none", issues, backlog.Item{Labels: []string{"bug"}}, "", ""},
		{"research label", issues, backlog.Item{Labels: []string{"decision:research"}}, DecisionResearch, ""},
		{"grilling label", issues, backlog.Item{Labels: []string{"x", "decision:grilling"}}, DecisionGrilling, ""},
		{"two labels", issues, backlog.Item{Labels: []string{"decision:research", "decision:grilling"}}, "", BlockDecisionLabel},
		{"unknown type", issues, backlog.Item{Labels: []string{"decision:prototype"}}, "", BlockDecisionLabel},
		{"file field", file, backlog.Item{Line: "- [B07] x Decision: research"}, DecisionResearch, ""},
		{"file bad", file, backlog.Item{Line: "- [B07] x Decision: nope"}, "", BlockDecisionLabel},
		{"file none", file, backlog.Item{Line: "- [B07] x"}, "", ""},
	}
	for _, c := range cases {
		got, err := DecisionOf(c.caps, c.it)
		if got != c.want || (c.by == "") != (err == nil) || (err != nil && blockedBy(t, err) != c.by) {
			t.Errorf("%s: %q %v", c.name, got, err)
		}
	}
}

func TestAssignResearchStartsLightAndBriefsAFinding(t *testing.T) {
	f := newAssignFixture(t)
	f.be.items["12"].Labels = []string{"decision:research"}
	res, err := f.assign("12", "ben", nil)
	if err != nil || res.Tier != "light" || res.Model != "haiku" {
		t.Fatalf("%v %+v", err, res)
	}
	for _, want := range []string{"rota --json item comment add #12 --kind finding", "ROTA-DONE ben finding:<comment-id>", "open no PR", "Worker tier: light (haiku)"} {
		if !strings.Contains(f.host.sent, want) {
			t.Errorf("brief lacks %q:\n%s", want, f.host.sent)
		}
	}
	if strings.Contains(f.host.sent, "ROTA-DONE ben <pr-url>") {
		t.Errorf("a research brief names no PR sentinel:\n%s", f.host.sent)
	}
}

func TestAssignResearchTierFlagWins(t *testing.T) {
	f := newAssignFixture(t)
	f.be.items["12"].Labels = []string{"decision:research"}
	res, err := f.assign("12", "ben", func(o *AssignOpts) { o.Tier = "standard" })
	if err != nil || res.Tier != "standard" {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestAssignRefusesGrilling(t *testing.T) {
	f := newAssignFixture(t)
	f.be.items["12"].Labels = []string{"decision:grilling"}
	for _, accept := range []bool{false, true} {
		_, err := f.assign("12", "ben", func(o *AssignOpts) { o.AcceptOverlap, o.AcceptBatch, o.AcceptOpenPR = accept, accept, accept })
		if err == nil || blockedBy(t, err) != BlockHumanDecision || !strings.Contains(err.Error(), "/rota-brainstorm 12") {
			t.Fatalf("accept flags %v: %v", accept, err)
		}
	}
	if len(f.be.claims) != 0 {
		t.Errorf("a refusal marks nothing: %v", f.be.claims)
	}
}

func TestTransferRefusesGrilling(t *testing.T) {
	f := newMoveFx(t)
	f.be.items["12"].Labels = []string{"decision:grilling"}
	_, err := f.transfer("12", "dana", nil)
	if err == nil || blockedBy(t, err) != BlockHumanDecision {
		t.Fatalf("%v", err)
	}
}

func TestCandidatesGrillingNotReady(t *testing.T) {
	root := newRepo(t, nil)
	milestoneDoc(t, root, "M01", "active")
	be := &fakeRemote{}
	be.add("10", "plain", "M01", false, "")
	be.add("11", "grill me", "M01", false, "")
	be.add("12", "research", "M01", false, "")
	be.items["11"].Labels = []string{"decision:grilling"}
	be.items["12"].Labels = []string{"decision:research"}
	e := Env{Git: git.Exec, Base: "main", Worker: wk(worker.Env{Getenv: noEnv, Config: diskConfig}), Lease: fakeLease("h", 100)}
	cs, err := e.RoundCandidates(bg, root, be, roundcfg.Settings{Scope: roundcfg.ScopeMilestone}, "")
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Candidate{}
	for _, c := range cs {
		by[c.ID] = c
	}
	if g := by["11"]; g.Ready() || g.BlockedBy != BlockHumanDecision || g.Decision != DecisionGrilling {
		t.Errorf("grilling: %+v", g)
	}
	if r := by["12"]; r.BlockedBy != "" || r.Decision != DecisionResearch {
		t.Errorf("research: %+v", r)
	}
}
