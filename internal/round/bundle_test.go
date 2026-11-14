package round

import (
	"reflect"
	"testing"
)

func bc(id, tag string, declared ...string) Candidate {
	return Candidate{ID: id, Tag: tag, Scopes: declared, Declared: declared}
}

func TestMarkBundleHintsListsOverlappingReadyItems(t *testing.T) {
	a := bc("10", "Minor", "a.go")
	b := bc("11", "Cosmetic", "a.go", "b.go")
	c := bc("12", "Minor", "c.go")
	cs := []Candidate{a, b, c}
	markBundleHints(cs)
	if want := []string{"11"}; !reflect.DeepEqual(cs[0].BundleWith, want) {
		t.Errorf("10 bundleWith %v, want %v", cs[0].BundleWith, want)
	}
	if want := []string{"10"}; !reflect.DeepEqual(cs[1].BundleWith, want) {
		t.Errorf("11 bundleWith %v, want %v", cs[1].BundleWith, want)
	}
	if cs[2].BundleWith != nil {
		t.Errorf("12 overlaps nothing, got %v", cs[2].BundleWith)
	}
}

func TestMarkBundleHintsSkipsMajorNotReadyAndInferred(t *testing.T) {
	major := bc("1", "Major", "a.go")
	notReady := bc("2", "Minor", "a.go")
	notReady.OpenPR = 9
	inferred := Candidate{ID: "3", Tag: "Minor", Scopes: []string{"a.go"}, ScopeSource: SourceInferred}
	minor := bc("4", "Minor", "a.go")
	cs := []Candidate{major, notReady, inferred, minor}
	markBundleHints(cs)
	for _, c := range cs {
		if c.ID == "1" || c.ID == "2" {
			if c.BundleWith != nil {
				t.Errorf("%s must carry no hint, got %v", c.ID, c.BundleWith)
			}
		}
	}
	// Inferred scope still earns the orchestrator's hint (AC-3: declared or inferred).
	if want := []string{"3"}; !reflect.DeepEqual(cs[3].BundleWith, want) {
		t.Errorf("4 bundleWith %v, want %v", cs[3].BundleWith, want)
	}
}

func TestPlanBundlesStrictSignals(t *testing.T) {
	a := bc("20", "Minor", "x.go", "y.go")
	b := bc("21", "Cosmetic", "y.go")
	c := bc("22", "Minor", "x.go")
	cs := []Candidate{c, b, a} // backlog order does not pick the lead
	got := PlanBundles(cs, 0)
	want := []Bundle{{Lead: "20", Members: []string{"21", "22"}, Shared: []string{"x.go", "y.go"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PlanBundles = %+v, want %+v", got, want)
	}
	if got := PlanBundles(cs, 2); len(got) != 1 || len(got[0].Members) != 1 || got[0].Members[0] != "21" {
		t.Errorf("max 2 items: %+v", got)
	}
}

func TestPlanBundlesRefusesWeakOrUnsafeGroups(t *testing.T) {
	for name, mod := range map[string]func(*Candidate){
		"inferred scope":  func(c *Candidate) { c.ScopeSource = SourceInferred; c.Declared = nil },
		"subsystem scope": func(c *Candidate) { c.ScopeSource = SourceSubsystem; c.Declared = nil },
		"major":           func(c *Candidate) { c.Tag = "Major" },
		"untagged":        func(c *Candidate) { c.Tag = "" },
		"decision label":  func(c *Candidate) { c.Decision = "research" },
		"best-of":         func(c *Candidate) { c.BestOf = true },
		"auto hold":       func(c *Candidate) { c.AutoHold = "epic" },
		"not ready":       func(c *Candidate) { c.OpenPR = 5 },
		"depends on lead": func(c *Candidate) { c.DependsOn = []string{"30"} },
		"lead depends":    func(c *Candidate) { c.DependsOn = nil },
	} {
		lead := bc("30", "Minor", "a.go")
		member := bc("31", "Minor", "a.go")
		if name == "lead depends" {
			lead.DependsOn = []string{"31"}
		}
		mod(&member)
		if got := PlanBundles([]Candidate{lead, member}, 3); len(got) != 0 {
			t.Errorf("%s: bundled %+v", name, got)
		}
	}
}
