package verdict

import "testing"

// ruleCase is one branch history and what each named blocking rule says of it.
// The rules differ on head scope, CONCERNS findings and second opinion; the
// table pins every difference once, and each rule has its own test below.
type ruleCase struct {
	name     string
	head     string
	list     []Record
	blocking bool // Blocking: ship, any head, FAIL verdicts, second opinion counts
	atHead   bool // BlockingAtHead: merge gate, at head, wide
	relay    bool // RelayFailing: relay, any head, FAIL verdicts only
	bounce   bool // BounceFailing: bounce, at head, wide
}

func ruleCases() []ruleCase {
	const head, other = "aaa1111", "bbb2222"
	blocker := []Finding{{Severity: "blocker", Title: "x"}}
	rev := func(kind, v, sha string, f []Finding) Record {
		return Record{Kind: kind, Verdict: v, Combined: v, Sha: sha, Findings: f}
	}
	cross := func(v, sha, harness string) Record {
		return Record{Kind: ReviewCross, Verdict: v, Sha: sha, Harness: harness, Model: "m"}
	}
	lens := LensKinds[0]
	return []ruleCase{
		{"review FAIL at head", head, []Record{rev(ReviewQuality, Fail, head, nil)}, true, true, true, true},
		{"review FAIL at another head", head, []Record{rev(ReviewQuality, Fail, other, nil)}, true, false, true, false},
		{"review CONCERNS with blocking finding at head", head, []Record{rev(ReviewQuality, Concerns, head, blocker)}, false, true, false, true},
		{"review CONCERNS clean", head, []Record{rev(ReviewQuality, Concerns, head, nil)}, false, false, false, false},
		{"review PASS", head, []Record{rev(ReviewQuality, Pass, head, nil)}, false, false, false, false},
		{"failed item under PASS at head", head, []Record{{Kind: ReviewSpec, Verdict: Pass, Combined: Pass, Sha: head, Items: []ItemVerdict{{"B07", Fail}}}}, false, true, false, true},
		{"cross FAIL at head", head, []Record{cross(Fail, head, "hx-a")}, true, true, true, true},
		{"cross FAIL at another head", head, []Record{cross(Fail, other, "hx-a")}, true, false, true, false},
		{"cross FAIL under newer PASS from another harness at head", head,
			[]Record{cross(Fail, head, "hx-a"), cross(Pass, head, "hx-b")}, false, true, false, true},
		{"newer cross PASS at a later head clears an older FAIL", other,
			[]Record{cross(Fail, head, "hx-b"), cross(Pass, other, "hx-a")}, false, false, false, false},
		{"lens FAIL at head", head, []Record{rev(lens, Fail, head, nil)}, true, true, true, true},
		{"lens FAIL at another head", head, []Record{rev(lens, Fail, other, nil)}, true, false, true, false},
		{"lens CONCERNS with blocking finding at head", head, []Record{rev(lens, Concerns, head, blocker)}, false, true, false, true},
		{"second opinion FAIL", head, []Record{{Kind: SecondOpinion, Verdict: Fail, Sha: other}}, true, false, false, false},
		{"second opinion CONCERNS with blocking finding", head, []Record{{Kind: SecondOpinion, Verdict: Concerns, Sha: head, Findings: blocker}}, false, false, false, false},
	}
}

func checkRule(t *testing.T, name string, want func(ruleCase) bool, got func(ruleCase) bool) {
	t.Helper()
	for _, c := range ruleCases() {
		if g := got(c); g != want(c) {
			t.Errorf("%s: %s = %v, want %v", c.name, name, g, want(c))
		}
	}
}

func TestBlockingRule(t *testing.T) {
	checkRule(t, "Blocking", func(c ruleCase) bool { return c.blocking },
		func(c ruleCase) bool { _, ok := Blocking(c.list); return ok })
}

func TestBlockingAtHeadRule(t *testing.T) {
	checkRule(t, "BlockingAtHead", func(c ruleCase) bool { return c.atHead },
		func(c ruleCase) bool { _, ok := BlockingAtHead(c.list, c.head); return ok })
}

func TestRelayFailingRule(t *testing.T) {
	checkRule(t, "RelayFailing", func(c ruleCase) bool { return c.relay },
		func(c ruleCase) bool { return len(RelayFailing(c.list)) > 0 })
}

func TestBounceFailingRule(t *testing.T) {
	checkRule(t, "BounceFailing", func(c ruleCase) bool { return c.bounce },
		func(c ruleCase) bool { return len(BounceFailing(c.list, c.head)) > 0 })
}
