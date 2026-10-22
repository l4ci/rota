package postgate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

type parked struct{ issues []string }

func (p *parked) park(issue, _ string, _ roundcfg.Settings) error {
	p.issues = append(p.issues, issue)
	return nil
}

func bounces(root, issue string) int {
	return worker.LoadRegistryTolerant(root).Bounces(issue)
}

func TestGatePassEndsTheItemClockAndBestOf(t *testing.T) {
	root := t.TempDir()
	if err := worker.RecordItemStart(root, "12", time.Now()); err != nil {
		t.Fatal(err)
	}
	worker.Update(root, func(d *worker.Doc) { d.SetBestOf(worker.BestOf{Issue: "12"}) })
	// A verdict nobody bounces or lands on keeps the item running.
	if _, err := Gate(worker.Env{}, root, "12", worker.GateResult{Verdict: "other"}, nil); err != nil {
		t.Fatal(err)
	}
	reg := worker.LoadRegistryTolerant(root)
	if _, ok := reg.ItemStart("12"); !ok || reg.BestOf("12") == nil {
		t.Fatal("a non-pass verdict must keep the clock and the best-of record")
	}
	if _, err := Gate(worker.Env{}, root, "12", worker.GateResult{Verdict: worker.GatePass}, nil); err != nil {
		t.Fatal(err)
	}
	reg = worker.LoadRegistryTolerant(root)
	if _, ok := reg.ItemStart("12"); ok || reg.BestOf("12") != nil {
		t.Error("a gate pass must clear the item start and the best-of record")
	}
}

func TestGateBouncesAreParkedAtMaxBounces(t *testing.T) {
	root := t.TempDir() // round.maxBounces defaults to 3
	p := &parked{}
	for i := 1; i <= 3; i++ {
		o, err := Gate(worker.Env{}, root, "7", worker.GateResult{Verdict: worker.GateStale, Slot: "w1", SHA: "sha" + string(rune('a'+i))}, p.park)
		if err != nil {
			t.Fatal(err)
		}
		if o.Bounces != i || o.Parked != (i == 3) {
			t.Fatalf("bounce %d: %+v", i, o)
		}
	}
	if len(p.issues) != 1 || p.issues[0] != "7" {
		t.Fatalf("parked %v", p.issues)
	}
	if n := bounces(root, "7"); n != 0 {
		t.Errorf("parking must clear the count, got %d", n)
	}
}

func TestParkFailureIsReportedAndTheCountStands(t *testing.T) {
	root := t.TempDir()
	worker.RecordBounceCapped(worker.Env{}, nil, root, "7", "w1", "a", 0)
	worker.RecordBounceCapped(worker.Env{}, nil, root, "7", "w1", "b", 0)
	o, err := Gate(worker.Env{}, root, "7", worker.GateResult{Verdict: worker.GateProvenanceFail, SHA: "c"}, func(string, string, roundcfg.Settings) error { return errors.New("no lease") })
	if err == nil || o.Parked || o.Bounces != 3 {
		t.Fatalf("%+v %v", o, err)
	}
}

// The train counts a bounce like the gate does, for the culprit alone, and
// ends the items it landed.
func TestTrainBouncesTheCulpritAndEndsLandedItems(t *testing.T) {
	root := t.TempDir()
	if err := worker.RecordItemStart(root, "1", time.Now()); err != nil {
		t.Fatal(err)
	}
	r := worker.TrainResult{
		Verdict:     worker.GateStale,
		Culprit:     "w2",
		Members:     []worker.TrainMember{{Target: "w1", Landed: true}, {Target: "w2"}},
		CulpritGate: worker.GateResult{SHA: "abc1234"},
	}
	issues := map[string]string{"w1": "1", "w2": "2"}
	if fails := Train(worker.Env{}, nil, root, issues, r, (&parked{}).park); len(fails) != 0 {
		t.Fatal(fails)
	}
	if _, ok := worker.LoadRegistryTolerant(root).ItemStart("1"); ok {
		t.Error("a landed member must end its item clock")
	}
	if bounces(root, "2") != 1 || bounces(root, "1") != 0 {
		t.Errorf("bounces: culprit %d, landed %d", bounces(root, "2"), bounces(root, "1"))
	}
	// The same head again is the same refusal, not a second bounce.
	Train(worker.Env{}, nil, root, issues, r, (&parked{}).park)
	if bounces(root, "2") != 1 {
		t.Errorf("a repeat on the same head counted again: %d", bounces(root, "2"))
	}
}

// `rota worker gate 847` after the slot moved on sets the result's slot to the
// PR number. The stale refusal's bounce must fold into the issue's own row: no
// slot or account total named by the PR number.
func TestStaleGateByPRNumberAfterTheSlotMovedOnMakesNoPseudoSlot(t *testing.T) {
	root := t.TempDir()
	at := time.Date(2026, 10, 17, 21, 0, 0, 0, time.UTC)
	if err := ledger.Append(root, ledger.Entry{TS: at, Kind: ledger.KindAssign, Issue: "12", Slot: "nia", Account: "work", Harness: "claude", PR: "847"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Gate(worker.Env{Now: func() time.Time { return at }}, root, "12", worker.GateResult{Verdict: worker.GateStale, Slot: "847", PR: "847", SHA: "h1"}, nil); err != nil {
		t.Fatal(err)
	}
	es, err := ledger.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	s := ledger.Fold(es, nil, 0)
	if len(s.Issues) != 1 || s.Issues[0].Slot != "nia" || s.Issues[0].Bounces != 1 {
		t.Fatalf("issue rows %+v, want one row for nia carrying the bounce", s.Issues)
	}
	for _, tot := range append(append([]ledger.Total{}, s.Slots...), s.Accounts...) {
		if tot.Name == "847" || tot.Name == "-" {
			t.Errorf("total named %q: slots %+v accounts %+v", tot.Name, s.Slots, s.Accounts)
		}
	}
}

// The gate result carries the slot its target resolved to; the bounce is
// recorded under it even when the registry no longer holds that slot, so the
// attribution never depends on a late pool lookup.
func TestGateBounceByPRNumberUsesTheResolvedSlot(t *testing.T) {
	root := t.TempDir()
	at := time.Date(2026, 10, 17, 21, 0, 0, 0, time.UTC)
	res := worker.GateResult{Verdict: worker.GateStale, Slot: "847", TargetSlot: "nia", PR: "847", SHA: "h1"}
	if _, err := Gate(worker.Env{Now: func() time.Time { return at }}, root, "12", res, nil); err != nil {
		t.Fatal(err)
	}
	es, err := ledger.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 1 || es[0].Kind != ledger.KindBounce || es[0].Slot != "nia" {
		t.Fatalf("entries %+v, want one bounce for nia", es)
	}
}

// The bounce accounting reads round.maxBounces from Env.Config, not from the
// project config on disk: root holds no .rota/config.json here.
func TestBounceReadsMaxBouncesFromTheInjectedConfig(t *testing.T) {
	root := t.TempDir()
	cfg, err := jsonx.Decode([]byte(`{"round":{"maxBounces":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	env := worker.Env{Config: func(string) any { return cfg }}
	p := &parked{}
	o, err := Gate(env, root, "9", worker.GateResult{Verdict: worker.GateStale, Slot: "w1", SHA: "sha1"}, p.park)
	if err != nil {
		t.Fatal(err)
	}
	if o.Bounces != 1 || !o.Parked || len(p.issues) != 1 {
		t.Fatalf("maxBounces 1 from the injected config must park on the first bounce: %+v, parked %v", o, p.issues)
	}
}

// issueComments is one tracker's comments, for the issue bounce store.
type issueComments struct{ bodies []string }

func (c *issueComments) Comments(context.Context, tracker.IssueKey) ([]tracker.Comment, error) {
	out := make([]tracker.Comment, len(c.bodies))
	for i, b := range c.bodies {
		out[i] = tracker.Comment{ID: strconv.Itoa(i), Body: b}
	}
	return out, nil
}

func (c *issueComments) AddComment(_ context.Context, _ tracker.IssueKey, body string) (string, error) {
	c.bodies = append(c.bodies, body)
	return strconv.Itoa(len(c.bodies) - 1), nil
}

func (c *issueComments) DeleteComment(context.Context, tracker.IssueKey, string) error { return nil }

// #1598 AC-7: with round.support set the gate counts and parks from the issue,
// never the registry, and parking posts the reset.
func TestGateBouncesOnTheIssueWithSupport(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".rota", "config.json"), []byte(`{"issues":{"source":"github"},"round":{"support":"mac"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ic := &issueComments{}
	nf := &noteForge{}
	env := worker.Env{
		IssueComments: func(string, tracker.Settings) (worker.IssueComments, error) { return ic, nil },
		Forge:         func(string, string, tracker.Settings) (worker.Forge, error) { return nf, nil },
	}
	p := &parked{}
	for i, sha := range []string{"a1", "a1", "b2", "c3"} {
		o, err := Gate(env, root, "7", worker.GateResult{Verdict: worker.GateStale, Slot: "ext-1", SHA: sha,
			Branch: "mac-ben/7-thing", PR: "https://github.com/o/r/pull/70", Err: "stale: the PR head moved"}, p.park)
		if err != nil {
			t.Fatal(err)
		}
		if want := max(i, 1); o.Bounces != want || o.Parked != (i == 3) {
			t.Fatalf("gate %d at %s: %+v", i, sha, o)
		}
	}
	if len(p.issues) != 1 || bounces(root, "7") != 0 {
		t.Fatalf("parked %v, registry count %d", p.issues, bounces(root, "7"))
	}
	if n := len(ic.bodies); n != 4 || !strings.HasPrefix(ic.bodies[3], "<!-- rota:bounce reset -->") {
		t.Fatalf("issue comments %q", ic.bodies)
	}
	if _, err := Gate(env, root, "7", worker.GateResult{Verdict: worker.GatePass}, nil); err != nil || len(ic.bodies) != 4 {
		t.Errorf("a pass with no bounces posts nothing: %v %q", err, ic.bodies)
	}
	// Each counted bounce short of the cap reaches the support worker on the
	// PR; the deduped re-gate and the parking one do not.
	if len(nf.bodies) != 2 || !strings.HasPrefix(nf.bodies[0], "<!-- rota:review-bounce head=a1 -->") || !strings.HasPrefix(nf.bodies[1], "<!-- rota:review-bounce head=b2 -->") {
		t.Errorf("PR comments %q", nf.bodies)
	}
}

// noteForge records the PR comments posted through it.
type noteForge struct {
	worker.Forge
	bodies []string
}

func (f *noteForge) AddMRNote(_ context.Context, _ int, body string) (string, error) {
	f.bodies = append(f.bodies, body)
	return "1", nil
}
