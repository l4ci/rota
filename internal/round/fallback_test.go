package round

import (
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/worker"
)

const fallbackCfg = `{"round":{"dispatch":"herdr","harnesses":["claude","codex"],"accounts":[{"name":"a","configDir":"/nowhere"}],"tiers":{"codex":{"light":"c-l","standard":"c-s","heavy":"c-h"}}}}`

// fallbackRig is a fixture whose only Claude account is spent and whose Codex
// is installed and logged in.
func fallbackRig(t *testing.T, cfg string) *assignFixture {
	t.Helper()
	f := newAssignFixture(t)
	f.config(t, cfg)
	f.env.Forge = f.be.asForge()
	(&codexRig{version: "codex-cli 0.159.2\n", loggedIn: true}).install(f)
	f.host.name = "herdr"
	f.meters(t, map[string]bool{"a": true})
	return f
}

func TestAssignFallbackToTheNextHarness(t *testing.T) {
	f := fallbackRig(t, fallbackCfg)
	res, err := f.assign("12", "ben", nil)
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if res.Kind != harness.Codex || res.KindSource != KindFromFallback || res.Model != "c-s" {
		t.Fatalf("kind %q (%s) model %q", res.Kind, res.KindSource, res.Model)
	}
	if res.Fallback == nil || res.Fallback.From != harness.Claude || res.Fallback.To != harness.Codex || !strings.Contains(res.Fallback.Why, "cooling down") {
		t.Fatalf("fallback %+v", res.Fallback)
	}
	if !slices.Contains(f.be.items["12"].Labels, "harness:codex") {
		t.Errorf("labels %v", f.be.items["12"].Labels)
	}
	if s := worker.LoadRegistryTolerant(f.root).Slot("ben"); s.Kind() != harness.Codex {
		t.Errorf("slot kind %q", s.Kind())
	}
	e := lastOf(ledgerKinds(t, f.root), ledger.KindAssign)
	if e == nil || e.Harness != harness.Codex || e.Detail == nil {
		t.Fatalf("ledger %+v", e)
	}
	if v, _ := e.Detail.Get("fallbackFrom"); v != harness.Claude {
		t.Fatalf("ledger detail %v", e.Detail)
	}
}

// A re-assign reads the label the fallback wrote as an explicit pick.
func TestFallbackLabelPinsTheIssueOnItsNextAssign(t *testing.T) {
	f := fallbackRig(t, fallbackCfg)
	if _, err := f.assign("12", "ben", nil); err != nil {
		t.Fatal(err)
	}
	res, err := f.assign("12", "ben", func(o *AssignOpts) { o.CheckOnly = true })
	if err != nil || res.KindSource != KindFromLabel || res.Kind != harness.Codex {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestFallbackRefusesPinnedIssues(t *testing.T) {
	for name, c := range map[string]struct {
		labels []string
		mod    func(*AssignOpts)
	}{
		"--kind":            {mod: func(o *AssignOpts) { o.Kind = "claude" }},
		"harness label":     {labels: []string{"harness:claude"}},
		"model label":       {labels: []string{"model:opus"}},
		"--model":           {mod: func(o *AssignOpts) { o.Model = "opus" }},
		"harness + --model": {labels: []string{"harness:claude"}, mod: func(o *AssignOpts) { o.Model = "opus" }},
	} {
		t.Run(name, func(t *testing.T) {
			f := fallbackRig(t, fallbackCfg)
			f.labelled("12", c.labels...)
			_, err := f.assign("12", "ben", c.mod)
			if by := blockedBy(t, err); by != BlockQuota {
				t.Fatalf("err %v", err)
			}
			if ls := f.be.items["12"].Labels; slices.Contains(ls, "harness:codex") {
				t.Errorf("label written: %v", ls)
			}
		})
	}
}

func TestFallbackRefusesWhenNoLaterHarnessHasHeadroom(t *testing.T) {
	f := fallbackRig(t, strings.Replace(fallbackCfg, `"harnesses":["claude","codex"]`, `"harnesses":["claude"]`, 1))
	_, err := f.assign("12", "ben", nil)
	if by := blockedBy(t, err); by != BlockQuota || !strings.Contains(err.Error(), "cooling down") {
		t.Fatalf("err %v", err)
	}
}

func TestFallbackUnsetChangesNothing(t *testing.T) {
	f := fallbackRig(t, strings.Replace(fallbackCfg, `"harnesses":["claude","codex"],`, "", 1))
	_, err := f.assign("12", "ben", nil)
	if by := blockedBy(t, err); by != BlockQuota {
		t.Fatalf("err %v", err)
	}
	if st := f.env.Fallback(bg, f.root, f.set); st.Active {
		t.Fatalf("fallback %+v", st)
	}
}

// With round.harnesses set the first entry is the project default: a slot
// that once ran a fallback does not carry codex to the next issue.
func TestFallbackSlotKindDoesNotStick(t *testing.T) {
	f := fallbackRig(t, fallbackCfg)
	f.meters(t, nil)
	pf, err := f.env.preflight(bg, f.root, f.be, preflightIn{Item: f.be.items["13"], Settings: f.set, Tier: "standard", SlotKind: harness.Codex})
	if err != nil || pf.Kind != harness.Claude {
		t.Fatalf("%v %+v", err, pf)
	}
	f.config(t, strings.Replace(fallbackCfg, `"harnesses":["claude","codex"],`, "", 1))
	if pf, err = f.env.preflight(bg, f.root, f.be, preflightIn{Item: f.be.items["13"], Settings: f.set, Tier: "standard", SlotKind: harness.Codex}); err != nil || pf.Kind != harness.Codex || pf.KindSource != KindFromSlot {
		t.Fatalf("unset keeps the slot's kind: %v %+v", err, pf)
	}
}

func TestFallbackState(t *testing.T) {
	f := fallbackRig(t, fallbackCfg)
	st := f.env.Fallback(bg, f.root, f.set)
	if !st.Active || st.From != harness.Claude || st.To != harness.Codex || !st.ResetsAt.Equal(quotaNow.Add(1e9*3600)) {
		t.Fatalf("%+v", st)
	}
	f.meters(t, nil)
	if st := f.env.Fallback(bg, f.root, f.set); st.Active {
		t.Fatalf("claude has headroom: %+v", st)
	}
}

// A claude-only workerKind with every Claude account cooling must not stop
// the tick: the cap stays open while a later harness has headroom.
func TestQuotaCapStaysOpenWithAFallbackHarness(t *testing.T) {
	f := fallbackRig(t, strings.Replace(fallbackCfg, `"harnesses"`, `"workerKind":"claude","harnesses"`, 1))
	if c := f.env.QuotaCap(bg, f.root, f.set); c.Reduced() {
		t.Fatalf("cap %+v", c)
	}
}

// fileBoard is a labelless backend: the fallback writes the Harness: field.
type fileBoard struct {
	*fakeRemote
	harness map[string]string
}

func (b *fileBoard) Capabilities() backlog.Capabilities {
	return backlog.Capabilities{ClaimWrites: true}
}

func (b *fileBoard) SetHarness(ref, kind string) (bool, error) {
	b.harness[ref] = kind
	return true, nil
}

func TestHarnessStepWritesTheFieldOnTheFileBackend(t *testing.T) {
	b := &fileBoard{fakeRemote: &fakeRemote{}, harness: map[string]string{}}
	e := Env{}
	if !e.canRecordHarness(b) {
		t.Fatal("a backend with SetHarness records the pick")
	}
	st := harnessStep(bg, e, b, &backlog.Item{ID: "B02"}, &Fallback{From: "claude", To: "codex"})
	if err := st.do(); err != nil || b.harness["B02"] != "codex" {
		t.Fatalf("do: %v %v", err, b.harness)
	}
	if err := st.undo(); err != nil || b.harness["B02"] != "" {
		t.Fatalf("undo: %v %v", err, b.harness)
	}
	if e.canRecordHarness(&fakeRemote{}) {
		t.Error("an issue backend with no forge cannot record a label")
	}
	if !harnessStep(bg, e, b, &backlog.Item{ID: "B02"}, nil).skip() {
		t.Error("no fallback, no step")
	}
}
