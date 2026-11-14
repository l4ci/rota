package round

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"os"
	"path/filepath"

	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/cfgdir"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/proc"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/worker"
)

// poolCfg is a pool of one Claude and two codex accounts, and a codex tier map.
const poolCfg = `{"round":{"dispatch":"herdr","harnesses":["claude","codex"],"accounts":[{"name":"a","configDir":"/nowhere"},{"kind":"codex","name":"c1","dir":"/h1"},{"kind":"codex","name":"c2","dir":"/h2"}],"tiers":{"codex":{"light":"c-l","standard":"c-s","heavy":"c-h"}}}}`

// poolRig fakes the meters of both kinds: used is each account's highest used
// percent (Claude's five-hour window, codex's weekly one), 100 = spent with a
// reset an hour ahead, a missing account reads as unknown.
type poolRig struct {
	used  map[string]float64
	reads atomic.Int32
}

func (f *assignFixture) pool(t *testing.T, cfg string, used map[string]float64) *poolRig {
	t.Helper()
	f.config(t, cfg)
	f.env.Forge = f.be.asForge()
	(&codexRig{version: "codex-cli 0.159.2\n", loggedIn: true}).install(f)
	f.host.name = "herdr"
	r := &poolRig{used: used}
	now := func() time.Time { return quotaNow }
	withNow(&f.env, now)
	f.env = f.env.WithAccounts(&worker.Accounts{Env: worker.Env{Config: diskConfig, Now: now},
		Login: worker.Credentials{GOOS: "darwin", Run: func(context.Context, proc.Cmd) (proc.Result, error) {
			return proc.Result{Stdout: `{"claudeAiOauth":{"accessToken":"t"}}`}, nil
		}},
		Fetch: func(_ context.Context, name, _ string) (*jsonx.Object, string) {
			u, ok := r.used[name]
			if !ok {
				return nil, "fake: unreadable"
			}
			reset := "null"
			if u >= 100 {
				reset = `"` + quotaNow.Add(time.Hour).Format(time.RFC3339) + `"`
			}
			v, err := jsonx.Decode([]byte(fmt.Sprintf(`{"five_hour":{"utilization":%g,"resets_at":%s}}`, u, reset)))
			if err != nil {
				t.Fatal(err)
			}
			return v.(*jsonx.Object), ""
		}})
	f.env.Quotas = &worker.Quotas{Env: worker.Env{Config: diskConfig, Now: now, Home: func() (string, error) { return "/home/x", nil }},
		Options: harness.QuotaOptions{Now: now},
		Read: func(_ context.Context, _ harness.QuotaReporter, req harness.QuotaRequest, _ harness.QuotaOptions) harness.QuotaSnapshot {
			r.reads.Add(1)
			s := harness.QuotaSnapshot{Kind: harness.Codex, Name: req.Name, Status: "unavailable", Reason: "fake: unreadable", Windows: []harness.QuotaWindow{}}
			name := req.Name
			if req.Ambient {
				name = ""
			}
			if u, ok := r.used[name]; ok {
				mins, reset := 10080, quotaNow.Add(time.Hour)
				s.Status, s.Reason = "available", ""
				s.Windows = []harness.QuotaWindow{{DurationMins: &mins, UsedPercent: &u, ResetsAt: &reset, Status: "available"}}
			}
			return s
		}}
	return r
}

// place runs the shared preflight for item 12 on slot ben, which ran under
// claude account a (or none when cur is "").
func (f *assignFixture) place(t *testing.T, mod func(*preflightIn)) (preflightOut, error) {
	t.Helper()
	in := preflightIn{Item: f.be.items["12"], Settings: f.set, Tier: "standard", Slot: "ben", SlotKind: harness.Claude, SlotAccount: "a"}
	if mod != nil {
		mod(&in)
	}
	return f.env.preflight(bg, f.root, f.be, in)
}

func wantPlacement(t *testing.T, pf preflightOut, err error, kind, account string, kept bool) {
	t.Helper()
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if pf.Placement == nil || pf.Kind != kind || pf.Placement.Account != account || pf.Placement.Kept != kept {
		t.Fatalf("kind %q placement %+v, want %s/%s kept=%v", pf.Kind, pf.Placement, kind, account, kept)
	}
}

// AC-1: with no pick the account with the most headroom wins across harnesses,
// and the slot moves when it is ahead by the margin.
func TestPlaceMovesToTheBestAccountOfAnotherHarness(t *testing.T) {
	f := newAssignFixture(t)
	f.pool(t, poolCfg, map[string]float64{"a": 70, "c1": 20, "c2": 60})
	pf, err := f.place(t, nil)
	wantPlacement(t, pf, err, harness.Codex, "c1", false)
	if pf.KindSource != KindFromUsage || pf.Model != "c-s" {
		t.Errorf("source %q model %q", pf.KindSource, pf.Model)
	}
	if h := pf.Placement.Headroom; h == nil || *h != 80 {
		t.Errorf("headroom %v", h)
	}
	if got := pf.Placement.String(); !strings.Contains(got, "c1 (codex, headroom 80%) moved") {
		t.Errorf("output %q", got)
	}
}

// AC-3: a gap below the margin keeps the account; an unknown best never wins;
// a cooling current moves.
func TestPlaceKeepsAnAccountWithinTheMargin(t *testing.T) {
	for name, c := range map[string]struct {
		used     map[string]float64
		kind, to string
		kept     bool
	}{
		"gap 15 at margin 20 keeps":    {map[string]float64{"a": 40, "c1": 25, "c2": 90}, harness.Claude, "a", true},
		"unknown best keeps":           {map[string]float64{"a": 90}, harness.Claude, "a", true},
		"cooling current moves":        {map[string]float64{"a": 100, "c1": 90, "c2": 95}, harness.Codex, "c1", false},
		"best known beats unknown cur": {map[string]float64{"c1": 10, "c2": 10}, harness.Claude, "a", true},
	} {
		t.Run(name, func(t *testing.T) {
			f := newAssignFixture(t)
			f.pool(t, poolCfg, c.used)
			pf, err := f.place(t, nil)
			wantPlacement(t, pf, err, c.kind, c.to, c.kept)
		})
	}
}

// AC-4: margin 0 never moves a usable account.
func TestPlaceMarginZeroKeeps(t *testing.T) {
	f := newAssignFixture(t)
	f.pool(t, strings.Replace(poolCfg, `"round":{`, `"round":{"accountRebalanceMargin":0,`, 1), map[string]float64{"a": 99, "c1": 1, "c2": 1})
	pf, err := f.place(t, nil)
	wantPlacement(t, pf, err, harness.Claude, "a", true)
}

// AC-2: a harness: label or --kind leaves one kind's accounts; none usable is
// a quota refusal naming the restriction. A model names the kind whose tiers hold it.
func TestPlaceRestrictions(t *testing.T) {
	t.Run("harness label", func(t *testing.T) {
		f := newAssignFixture(t)
		f.pool(t, poolCfg, map[string]float64{"a": 90, "c1": 5, "c2": 5})
		f.labelled("12", "harness:claude")
		pf, err := f.place(t, nil)
		wantPlacement(t, pf, err, harness.Claude, "a", true)
		if pf.KindSource != KindFromLabel {
			t.Errorf("source %q", pf.KindSource)
		}
	})
	t.Run("--kind", func(t *testing.T) {
		f := newAssignFixture(t)
		f.pool(t, poolCfg, map[string]float64{"a": 5, "c1": 95, "c2": 50})
		pf, err := f.place(t, func(in *preflightIn) { in.FlagKind = harness.Codex })
		wantPlacement(t, pf, err, harness.Codex, "c2", false)
	})
	t.Run("every account of the kind cooling refuses", func(t *testing.T) {
		f := newAssignFixture(t)
		f.pool(t, poolCfg, map[string]float64{"a": 100, "c1": 5, "c2": 5})
		f.labelled("12", "harness:claude")
		_, err := f.place(t, nil)
		if by := blockedBy(t, err); by != blockedby.Quota || !strings.Contains(err.Error(), "cooling down") || !strings.Contains(err.Error(), "the claude harness") {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("model label names the owning kind", func(t *testing.T) {
		f := newAssignFixture(t)
		f.pool(t, poolCfg, map[string]float64{"a": 5, "c1": 95, "c2": 40})
		f.labelled("12", "model:c-s")
		pf, err := f.place(t, nil)
		wantPlacement(t, pf, err, harness.Codex, "c2", false)
		if pf.Model != "c-s" {
			t.Errorf("model %q", pf.Model)
		}
	})
	t.Run("--model of the claude tier map", func(t *testing.T) {
		f := newAssignFixture(t)
		f.pool(t, poolCfg, map[string]float64{"a": 95, "c1": 5, "c2": 5})
		claudeModel := f.set.Model(harness.Claude, "standard")
		pf, err := f.place(t, func(in *preflightIn) { in.FlagModel = claudeModel })
		wantPlacement(t, pf, err, harness.Claude, "a", true)
	})
}

// One tick fills several slots off one reading: equal accounts take one slot
// each instead of all landing on the first.
func TestPlaceSpreadsFreshSlotsAcrossEqualAccounts(t *testing.T) {
	f := newAssignFixture(t)
	f.pool(t, poolCfg, map[string]float64{"a": 50, "c1": 50, "c2": 50})
	got := map[string]int{}
	for _, slot := range []string{"ben", "dana"} {
		res, err := f.assign("12", slot, func(o *AssignOpts) { o.ID = map[string]string{"ben": "12", "dana": "13"}[slot] })
		if err != nil {
			t.Fatalf("assign on %s: %v", slot, err)
		}
		got[res.Placement.Kind+"/"+res.Account]++
	}
	if len(got) != 2 {
		t.Fatalf("both slots on one account: %v", got)
	}
}

// AC-5: a slot moved to codex starts on codex's tier model, and the account is
// the slot's home account for the dispatch.
func TestAssignMovesTheSlotToCodex(t *testing.T) {
	f := newAssignFixture(t)
	f.pool(t, poolCfg, map[string]float64{"a": 90, "c1": 10, "c2": 40})
	res, err := f.assign("12", "ben", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != harness.Codex || res.KindSource != KindFromUsage || res.Model != "c-s" || res.Account != "c1" {
		t.Fatalf("%+v", res)
	}
	s := worker.LoadRegistryTolerant(f.root).Slot("ben")
	if s.Kind() != harness.Codex || s.HomeAccount() != "c1" {
		t.Errorf("slot kind %q home account %q", s.Kind(), s.HomeAccount())
	}
	if f.be.items["12"].Labels != nil && len(f.be.items["12"].Labels) > 0 {
		for _, l := range f.be.items["12"].Labels {
			if strings.HasPrefix(l, "harness:") {
				t.Errorf("a usage pick must not pin the issue: %v", f.be.items["12"].Labels)
			}
		}
	}
}

// AC-6: a slot resuming its issue is not re-placed (it keeps its harness and
// account); a transfer keeps the receiver's.
func TestPlaceResumeAndTransferKeepTheAccount(t *testing.T) {
	for name, mod := range map[string]func(*preflightIn){
		"resuming": func(in *preflightIn) { in.Resuming = true },
		"transfer": func(in *preflightIn) { in.NoFallback = true },
	} {
		t.Run(name, func(t *testing.T) {
			f := newAssignFixture(t)
			// b is far ahead of a within the same kind: only Keep stops the move.
			f.pool(t, strings.Replace(poolCfg, `"accounts":[`, `"accounts":[{"name":"b","configDir":"/nowhere"},`, 1), map[string]float64{"a": 95, "b": 1, "c1": 1, "c2": 1})
			pf, err := f.place(t, mod)
			if name == "resuming" {
				if err != nil || pf.Placement != nil || pf.Kind != harness.Claude {
					t.Fatalf("%v %+v", err, pf)
				}
				return
			}
			wantPlacement(t, pf, err, harness.Claude, "a", true)
		})
	}
}

// Codex readings are cached: a second placement does not read the accounts again.
func TestCodexReadingsAreCached(t *testing.T) {
	f := newAssignFixture(t)
	r := f.pool(t, poolCfg, map[string]float64{"a": 50, "c1": 50, "c2": 50})
	for range 3 {
		if _, err := f.place(t, nil); err != nil {
			t.Fatal(err)
		}
	}
	if n := r.reads.Load(); n != 2 {
		t.Fatalf("%d codex reads for two accounts over three placements", n)
	}
}

// A failed read is unknown: it never beats a known account and never refuses.
func TestPlaceTreatsAFailedCodexReadAsUnknown(t *testing.T) {
	f := newAssignFixture(t)
	f.pool(t, poolCfg, map[string]float64{"a": 90})
	pf, err := f.place(t, func(in *preflightIn) { in.SlotKind, in.SlotAccount = "", "" })
	wantPlacement(t, pf, err, harness.Claude, "a", false)
}

// No pool configured: nothing to balance, the slot's kind (else claude) runs.
func TestPlaceWithoutAPoolChangesNothing(t *testing.T) {
	f := newAssignFixture(t)
	f.config(t, `{"round":{"dispatch":"herdr"}}`)
	pf, err := f.place(t, func(in *preflightIn) { in.SlotKind, in.SlotAccount = harness.Codex, "" })
	if err != nil || pf.Placement != nil || pf.Kind != harness.Codex || pf.KindSource != KindFromSlot {
		t.Fatalf("%v %+v", err, pf)
	}
}

// With every Claude account cooling and codex free, the tick must not stop:
// the cap stays open while another harness the round may use has headroom.
func TestQuotaCapStaysOpenWhileAnotherHarnessHasHeadroom(t *testing.T) {
	f := newAssignFixture(t)
	f.pool(t, poolCfg, map[string]float64{"a": 100, "c1": 10, "c2": 10})
	if c := f.env.QuotaCap(bg, f.root, f.set); c.Reduced() {
		t.Fatalf("cap %+v", c)
	}
}

// Transfer keeps the receiver's kind: with its accounts cooling it refuses,
// whatever another harness has.
func TestTransferKeepsItsQuotaRefusal(t *testing.T) {
	f := newMoveFx(t)
	if err := os.WriteFile(filepath.Join(f.root, ".rota", "config.json"), []byte(poolCfg), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := roundcfg.FromConfig(config.Load(cfgdir.OSEnv(), f.root))
	if err != nil {
		t.Fatal(err)
	}
	f.set, f.env.Round = set, set
	withNow(&f.env, func() time.Time { return quotaNow })
	f.env.Worker = f.env.Worker.WithAccounts(&worker.Accounts{Env: worker.Env{Config: diskConfig, Now: func() time.Time { return quotaNow }},
		Fetch: func(context.Context, string, string) (*jsonx.Object, string) {
			v, _ := jsonx.Decode([]byte(fmt.Sprintf(`{"five_hour":{"utilization":100,"resets_at":"%s"}}`, quotaNow.Add(time.Hour).Format(time.RFC3339))))
			return v.(*jsonx.Object), ""
		}})
	_, err = f.transfer("12", "dana", func(o *TransferOpts) { o.Kind = harness.Claude })
	if by := blockedBy(t, err); by != blockedby.Quota {
		t.Fatalf("err %v", err)
	}
}

// A slot resuming its own issue keeps the kind it was assigned with.
func TestResumeKeepsTheSlotKind(t *testing.T) {
	for name, c := range map[string]struct {
		labels   []string
		wantKind string
		wantSrc  string
	}{
		"no label":     {wantKind: harness.Codex, wantSrc: KindFromSlot + " (resume)"},
		"claude label": {labels: []string{"harness:claude"}, wantKind: harness.Claude, wantSrc: KindFromLabel},
	} {
		t.Run(name, func(t *testing.T) {
			f := newAssignFixture(t)
			f.pool(t, poolCfg, map[string]float64{"a": 50, "c1": 50, "c2": 50})
			if _, err := f.assign("12", "ben", func(o *AssignOpts) { o.Kind = harness.Codex }); err != nil {
				t.Fatalf("first assign: %v", err)
			}
			f.labelled("12", c.labels...)
			res, err := f.assign("12", "ben", nil)
			if err != nil {
				t.Fatalf("resume: %v", err)
			}
			if res.Kind != c.wantKind || res.KindSource != c.wantSrc {
				t.Fatalf("kind %q (%s), want %q (%s)", res.Kind, res.KindSource, c.wantKind, c.wantSrc)
			}
		})
	}
}

// A transfer keeps a codex receiver on codex and on its account under
// [claude, codex], whatever claude has.
func TestTransferKeepsACodexReceiver(t *testing.T) {
	f := newAssignFixture(t)
	f.pool(t, poolCfg, map[string]float64{"a": 1, "c1": 1, "c2": 90})
	pf, err := f.place(t, func(in *preflightIn) { in.NoFallback, in.SlotKind, in.SlotAccount = true, harness.Codex, "c2" })
	wantPlacement(t, pf, err, harness.Codex, "c2", true)
	if pf.KindSource != KindFromUsage && pf.KindSource != KindFromSlot {
		t.Errorf("source %q", pf.KindSource)
	}
}

// A logged-out codex account is dropped before the claim, not at dispatch.
func TestPlaceDropsALoggedOutCodexAccount(t *testing.T) {
	f := newAssignFixture(t)
	f.pool(t, poolCfg, map[string]float64{"a": 90, "c1": 1, "c2": 1})
	f.env.Worker = loggedOut{f.env.Worker, "c1"}
	f.labelled("12", "harness:codex")
	pf, err := f.place(t, nil)
	wantPlacement(t, pf, err, harness.Codex, "c2", false)
	f.env.Worker = loggedOut{f.env.Worker, "/h2"}
	if _, err := f.place(t, nil); blockedBy(t, err) != blockedby.NoLogin {
		t.Fatalf("every codex account logged out: %v", err)
	}
}

// loggedOut fails the login probe of codex homes whose dir contains the text.
type loggedOut struct {
	Worker
	dir string
}

func (l loggedOut) LoginProblem(ctx context.Context, kind, home string) string {
	if kind == harness.Codex && (l.dir == "c1" && home == "/h1" || strings.Contains(home, l.dir)) {
		return "not logged in"
	}
	return l.Worker.LoginProblem(ctx, kind, home)
}

// A model no allowed harness's tiers name is refused before the claim; one a
// tier names narrows to that harness.
func TestPlaceModelNamedByNoTierIsRefused(t *testing.T) {
	f := newAssignFixture(t)
	f.pool(t, poolCfg, map[string]float64{"a": 50, "c1": 50, "c2": 50})
	f.labelled("12", "model:nobody-owns-this")
	if _, err := f.place(t, nil); blockedBy(t, err) != blockedby.ModelLabel {
		t.Fatalf("err %v", err)
	}
}

// Ruling 6: an allowed harness with no round.accounts entry is one ambient
// candidate; placing a slot there drops the account it recorded, so dispatch
// does not launch under the old config dir.
func TestAmbientPlacementClearsTheSlotAccount(t *testing.T) {
	f := newAssignFixture(t)
	f.pool(t, `{"round":{"dispatch":"herdr","harnesses":["claude","codex"],"tiers":{"codex":{"light":"c-l","standard":"c-s","heavy":"c-h"}}}}`, map[string]float64{})
	f.env.Login = worker.Credentials{GOOS: "darwin", Run: func(context.Context, proc.Cmd) (proc.Result, error) {
		return proc.Result{Stdout: `{"claudeAiOauth":{"accessToken":"t"}}`}, nil
	}}
	worker.UpdateSlot(f.root, "ben", func(s *worker.Slot) { s.SetAccount("old", "/old/dir") })
	pf, err := f.place(t, func(in *preflightIn) { in.SlotKind, in.SlotAccount = harness.Claude, "old" })
	if err != nil || pf.Placement == nil || pf.Placement.Account != "" || pf.Kind != harness.Claude {
		t.Fatalf("%v %+v", err, pf)
	}
	res, err := f.assign("12", "ben", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != harness.Claude || res.Account != "" {
		t.Fatalf("%+v", res)
	}
	if s := worker.LoadRegistryTolerant(f.root).Slot("ben"); s.Account() != "" || s.ConfigDir() != "" || s.HomeAccount() != "" {
		t.Errorf("slot still records %q %q %q", s.Account(), s.ConfigDir(), s.HomeAccount())
	}
}

// A slot's first placement is placed, never moved.
func TestFirstPlacementIsPlaced(t *testing.T) {
	f := newAssignFixture(t)
	f.pool(t, poolCfg, map[string]float64{"a": 50, "c1": 50, "c2": 50})
	pf, err := f.place(t, func(in *preflightIn) { in.SlotKind, in.SlotAccount = "", "" })
	if err != nil || !strings.Contains(pf.Placement.String(), "placed") {
		t.Fatalf("%v %v", err, pf.Placement)
	}
}
