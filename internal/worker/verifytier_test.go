package worker

import (
	"errors"
	"reflect"
	"testing"

	"github.com/l4ci/rota/internal/testledger"
)

func TestTierOutcomeOfEmptyFullSaysNoVerify(t *testing.T) {
	out := tierOutcomeOf(tierFull, "s1", VerifyResult{NoCommands: true})
	if !out.NoCommands || !out.OK || len(out.Verified) != 0 {
		t.Fatalf("empty tier: %+v", out)
	}
	want := []string{
		"NO-VERIFY s1 — test.full is empty; the merged tree was NOT gated by a command.",
		"set test.full via rota config set to make this gate real",
	}
	if !reflect.DeepEqual(out.Notes, want) {
		t.Errorf("notes %q, want %q", out.Notes, want)
	}
}

func TestTierOutcomeOfLabelsFailuresByTier(t *testing.T) {
	vr := VerifyResult{Verified: []string{"a"}, Failed: []string{"b"}, Excluded: []testledger.Entry{{}}}
	for tier, want := range map[string]string{tierFull: "verify FAILED: b", tierE2E: "e2e FAILED: b"} {
		out := tierOutcomeOf(tier, "train", vr)
		if out.OK || out.NoCommands || !reflect.DeepEqual(out.Notes, []string{want}) {
			t.Errorf("%s: %+v", tier, out)
		}
		if !reflect.DeepEqual(out.Verified, vr.Verified) || len(out.Excluded) != 1 {
			t.Errorf("%s: verified/excluded lost: %+v", tier, out)
		}
	}
}

func TestNoteTextIsPinned(t *testing.T) {
	if got, want := noVerifyFlagNote("s1"), "NO-VERIFY s1 — --no-verify: merged tree was NOT gated by a command."; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := noE2ENote(2), "NO-E2E land-green — the verified first 2 member(s) land without test.e2e; it never ran on that prefix."; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestVerifyTiersSequence(t *testing.T) {
	cfg := GateConfig{Full: []string{"f"}, E2E: []string{"e"}}
	for _, c := range []struct {
		name  string
		cfg   GateConfig
		first string
		red   string
		want  []string
		ok    bool
	}{
		{"full then e2e", cfg, tierFull, "", []string{tierFull, tierE2E}, true},
		{"empty e2e is skipped", GateConfig{Full: []string{"f"}}, tierFull, "", []string{tierFull}, true},
		{"empty full is still visited", GateConfig{E2E: []string{"e"}}, tierFull, "", []string{tierFull, tierE2E}, true},
		{"a red full skips e2e", cfg, tierFull, tierFull, []string{tierFull}, false},
		{"first e2e skips full", cfg, tierE2E, "", []string{tierE2E}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			var seen []string
			ok, err := verifyTiers(c.cfg, c.first, func(tier string, _ []string) (bool, error) {
				seen = append(seen, tier)
				return tier != c.red, nil
			})
			if err != nil || ok != c.ok || !reflect.DeepEqual(seen, c.want) {
				t.Errorf("ok=%v err=%v seen=%v, want ok=%v %v", ok, err, seen, c.ok, c.want)
			}
		})
	}
}

func TestTierWalk(t *testing.T) {
	cfg := GateConfig{Full: []string{"f"}, E2E: []string{"e"}}
	green := func(cmd string) VerifyResult { return VerifyResult{Verified: []string{cmd}} }
	type setup struct {
		results map[string]VerifyResult
		err     map[string]error
		stop    bool
		redStop bool
	}
	for _, c := range []struct {
		name                         string
		s                            setup
		ended, fullEmpty, fullRed    bool
		e2eGreen, wantErr            bool
		at                           string
		ran, redCalls                []string
		notes, verified, e2eVerified []string
	}{
		{name: "green full and e2e", s: setup{results: map[string]VerifyResult{tierFull: green("f"), tierE2E: green("e")}},
			e2eGreen: true, at: tierE2E, ran: []string{tierFull, tierE2E}, verified: []string{"f"}, e2eVerified: []string{"e"}},
		{name: "red full skips e2e, red stops", s: setup{redStop: true, results: map[string]VerifyResult{tierFull: {Failed: []string{"f"}}}},
			ended: true, fullRed: true, at: tierFull, ran: []string{tierFull}, redCalls: []string{tierFull}, notes: []string{"verify FAILED: f"}},
		{name: "red full, red false does not end", s: setup{results: map[string]VerifyResult{tierFull: {Failed: []string{"f"}}}},
			fullRed: true, at: tierFull, ran: []string{tierFull}, redCalls: []string{tierFull}, notes: []string{"verify FAILED: f"}},
		{name: "empty full still runs e2e", s: setup{results: map[string]VerifyResult{tierFull: {NoCommands: true}, tierE2E: green("e")}},
			fullEmpty: true, e2eGreen: true, at: tierE2E, ran: []string{tierFull, tierE2E},
			notes: []string{"NO-VERIFY slot — test.full is empty; the merged tree was NOT gated by a command.", noVerifyHint}, e2eVerified: []string{"e"}},
		{name: "stop ends before recording", s: setup{stop: true, results: map[string]VerifyResult{tierFull: green("f")}},
			ended: true, at: tierFull, ran: []string{tierFull}},
		{name: "run error", s: setup{err: map[string]error{tierFull: errors.New("boom")}},
			ended: true, wantErr: true, at: tierFull, ran: []string{tierFull}},
		{name: "red e2e", s: setup{redStop: true, results: map[string]VerifyResult{tierFull: green("f"), tierE2E: {Failed: []string{"e"}}}},
			ended: true, at: tierE2E, ran: []string{tierFull, tierE2E}, redCalls: []string{tierE2E}, verified: []string{"f"}, e2eVerified: nil, notes: []string{"e2e FAILED: e"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var ran, redCalls, notes, verified, e2eVerified []string
			var excluded []testledger.Entry
			w := &tierWalk{
				subject: "slot",
				run: func(tier string, _ []string) (VerifyResult, error) {
					ran = append(ran, tier)
					return c.s.results[tier], c.s.err[tier]
				},
				stop: func(VerifyResult) bool { return c.s.stop },
				red: func(tier string, _ VerifyResult) (bool, error) {
					redCalls = append(redCalls, tier)
					return c.s.redStop, nil
				},
				notes: &notes, verified: &verified, e2eVerified: &e2eVerified, excluded: &excluded,
			}
			ended, err := w.walk(cfg, tierFull)
			if ended != c.ended || (err != nil) != c.wantErr || w.at != c.at {
				t.Errorf("ended=%v err=%v at=%q", ended, err, w.at)
			}
			if w.fullEmpty != c.fullEmpty || w.fullRed != c.fullRed || w.e2eGreen != c.e2eGreen {
				t.Errorf("fullEmpty=%v fullRed=%v e2eGreen=%v", w.fullEmpty, w.fullRed, w.e2eGreen)
			}
			for name, p := range map[string][2][]string{"ran": {ran, c.ran}, "red": {redCalls, c.redCalls}, "notes": {notes, c.notes}, "verified": {verified, c.verified}, "e2eVerified": {e2eVerified, c.e2eVerified}} {
				if !reflect.DeepEqual(p[0], p[1]) {
					t.Errorf("%s = %q, want %q", name, p[0], p[1])
				}
			}
		})
	}
}

func TestTierWalkNilE2EVerifiedSinkDiscards(t *testing.T) {
	var notes, verified []string
	var excluded []testledger.Entry
	w := &tierWalk{
		subject: "slot",
		run: func(tier string, _ []string) (VerifyResult, error) {
			return VerifyResult{Verified: []string{tier}}, nil
		},
		notes: &notes, verified: &verified, excluded: &excluded,
	}
	ended, err := w.walk(GateConfig{Full: []string{"f"}, E2E: []string{"e"}}, tierFull)
	if ended || err != nil || !w.e2eGreen || !reflect.DeepEqual(verified, []string{tierFull}) {
		t.Errorf("ended=%v err=%v e2eGreen=%v verified=%v", ended, err, w.e2eGreen, verified)
	}
}

func TestTierWalkNilRedEndsOnRedTier(t *testing.T) {
	var notes, verified []string
	var excluded []testledger.Entry
	var ran []string
	w := &tierWalk{
		subject: "slot",
		run: func(tier string, _ []string) (VerifyResult, error) {
			ran = append(ran, tier)
			return VerifyResult{Failed: []string{tier}}, nil
		},
		notes: &notes, verified: &verified, excluded: &excluded,
	}
	ended, err := w.walk(GateConfig{Full: []string{"f"}, E2E: []string{"e"}}, tierFull)
	if !ended || err != nil || !w.fullRed || !reflect.DeepEqual(ran, []string{tierFull}) {
		t.Errorf("ended=%v err=%v fullRed=%v ran=%v", ended, err, w.fullRed, ran)
	}
}

// A run that errors on the e2e tier ends the walk with that error and leaves
// the full tier's record in place: the train maps full vs e2e errors apart by w.at.
func TestTierWalkE2ERunErrorEndsAtE2E(t *testing.T) {
	var notes, verified, e2eVerified, ran []string
	var excluded []testledger.Entry
	boom := errors.New("boom")
	w := &tierWalk{
		subject: "slot",
		run: func(tier string, _ []string) (VerifyResult, error) {
			ran = append(ran, tier)
			if tier == tierE2E {
				return VerifyResult{}, boom
			}
			return VerifyResult{Verified: []string{"f"}}, nil
		},
		notes: &notes, verified: &verified, e2eVerified: &e2eVerified, excluded: &excluded,
	}
	ended, err := w.walk(GateConfig{Full: []string{"f"}, E2E: []string{"e"}}, tierFull)
	if !ended || !errors.Is(err, boom) || w.at != tierE2E || w.e2eGreen {
		t.Errorf("ended=%v err=%v at=%q e2eGreen=%v", ended, err, w.at, w.e2eGreen)
	}
	if !reflect.DeepEqual(ran, []string{tierFull, tierE2E}) || !reflect.DeepEqual(verified, []string{"f"}) || e2eVerified != nil {
		t.Errorf("ran=%q verified=%q e2eVerified=%q", ran, verified, e2eVerified)
	}
}

// A red callback that returns an error ends the walk with it, whichever tier
// was red, and no later tier runs.
func TestTierWalkRedErrorEndsWalk(t *testing.T) {
	boom := errors.New("bisect failed")
	for _, red := range []string{tierFull, tierE2E} {
		t.Run(red, func(t *testing.T) {
			var notes, verified, ran []string
			var excluded []testledger.Entry
			w := &tierWalk{
				subject: "slot",
				run: func(tier string, _ []string) (VerifyResult, error) {
					ran = append(ran, tier)
					if tier == red {
						return VerifyResult{Failed: []string{tier}}, nil
					}
					return VerifyResult{Verified: []string{tier}}, nil
				},
				red:   func(string, VerifyResult) (bool, error) { return false, boom },
				notes: &notes, verified: &verified, excluded: &excluded,
			}
			ended, err := w.walk(GateConfig{Full: []string{"f"}, E2E: []string{"e"}}, tierFull)
			want := []string{tierFull}
			if red == tierE2E {
				want = []string{tierFull, tierE2E}
			}
			if !ended || !errors.Is(err, boom) || w.at != red || !reflect.DeepEqual(ran, want) {
				t.Errorf("ended=%v err=%v at=%q ran=%q, want ran %q", ended, err, w.at, ran, want)
			}
		})
	}
}

// stop on the e2e tier ends the walk before recording that tier, and keeps
// what the full tier recorded.
func TestTierWalkStopOnE2EEndsBeforeRecording(t *testing.T) {
	var notes, verified, e2eVerified []string
	var excluded []testledger.Entry
	w := &tierWalk{
		subject: "slot",
		run: func(tier string, _ []string) (VerifyResult, error) {
			return VerifyResult{Verified: []string{tier}}, nil
		},
		stop:  func(vr VerifyResult) bool { return reflect.DeepEqual(vr.Verified, []string{tierE2E}) },
		notes: &notes, verified: &verified, e2eVerified: &e2eVerified, excluded: &excluded,
	}
	ended, err := w.walk(GateConfig{Full: []string{"f"}, E2E: []string{"e"}}, tierFull)
	if !ended || err != nil || w.at != tierE2E || w.e2eGreen {
		t.Errorf("ended=%v err=%v at=%q e2eGreen=%v", ended, err, w.at, w.e2eGreen)
	}
	if !reflect.DeepEqual(verified, []string{tierFull}) || e2eVerified != nil || len(notes) != 0 {
		t.Errorf("verified=%q e2eVerified=%q notes=%q", verified, e2eVerified, notes)
	}
}

// take is called directly by the CI gate for the tier it already ran: pin each
// of its answers and the state it leaves.
func TestTierWalkTake(t *testing.T) {
	excusedEntry := testledger.Entry{}
	for _, c := range []struct {
		name                      string
		tier                      string
		vr                        VerifyResult
		stop                      bool
		red                       func(string, VerifyResult) (bool, error)
		nilRed                    bool
		ended, ok                 bool
		err                       error
		fullEmpty, fullRed, green bool
		notes, verified, e2e      []string
		excluded                  int
	}{
		{name: "stop ends unrecorded", tier: tierFull, vr: VerifyResult{Failed: []string{"f"}}, stop: true, ended: true},
		{name: "empty full", tier: tierFull, vr: VerifyResult{NoCommands: true}, ok: true, fullEmpty: true,
			notes: []string{noVerifyNote("slot"), noVerifyHint}},
		{name: "empty e2e is not an empty full", tier: tierE2E, vr: VerifyResult{NoCommands: true}, ok: true,
			notes: []string{noVerifyNote("slot"), noVerifyHint}},
		{name: "green full", tier: tierFull, vr: VerifyResult{Verified: []string{"f"}}, ok: true, verified: []string{"f"}},
		{name: "green e2e", tier: tierE2E, vr: VerifyResult{Verified: []string{"e"}}, ok: true, green: true, e2e: []string{"e"}},
		{name: "excused failure is green", tier: tierFull, vr: VerifyResult{Verified: []string{"f"}, Excluded: []testledger.Entry{excusedEntry}},
			ok: true, verified: []string{"f"}, excluded: 1},
		{name: "red full, nil red ends", tier: tierFull, vr: VerifyResult{Failed: []string{"f"}}, nilRed: true,
			ended: true, fullRed: true, notes: []string{"verify FAILED: f"}},
		{name: "red e2e, nil red ends, not fullRed", tier: tierE2E, vr: VerifyResult{Failed: []string{"e"}}, nilRed: true,
			ended: true, notes: []string{"e2e FAILED: e"}},
		{name: "red, red false goes on without ok", tier: tierFull, vr: VerifyResult{Failed: []string{"f"}},
			red: func(string, VerifyResult) (bool, error) { return false, nil }, fullRed: true, notes: []string{"verify FAILED: f"}},
		{name: "red, red error", tier: tierE2E, vr: VerifyResult{Failed: []string{"e"}},
			red:   func(string, VerifyResult) (bool, error) { return false, errors.New("bisect") },
			ended: true, err: errors.New("bisect"), notes: []string{"e2e FAILED: e"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var notes, verified, e2eVerified []string
			var excluded []testledger.Entry
			w := &tierWalk{
				subject: "slot",
				stop:    func(VerifyResult) bool { return c.stop },
				red:     c.red,
				notes:   &notes, verified: &verified, e2eVerified: &e2eVerified, excluded: &excluded,
			}
			if c.nilRed {
				w.red = nil
			}
			ended, ok, err := w.take(c.tier, c.vr)
			if ended != c.ended || ok != c.ok || (err == nil) != (c.err == nil) || (err != nil && err.Error() != c.err.Error()) {
				t.Errorf("ended=%v ok=%v err=%v", ended, ok, err)
			}
			if w.at != c.tier || w.fullEmpty != c.fullEmpty || w.fullRed != c.fullRed || w.e2eGreen != c.green {
				t.Errorf("at=%q fullEmpty=%v fullRed=%v e2eGreen=%v", w.at, w.fullEmpty, w.fullRed, w.e2eGreen)
			}
			if !reflect.DeepEqual(notes, c.notes) || !reflect.DeepEqual(verified, c.verified) || !reflect.DeepEqual(e2eVerified, c.e2e) || len(excluded) != c.excluded {
				t.Errorf("notes=%q verified=%q e2e=%q excluded=%d", notes, verified, e2eVerified, len(excluded))
			}
		})
	}
}
