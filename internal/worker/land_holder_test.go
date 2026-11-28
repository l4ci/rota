package worker

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/rotastatetest"
)

// gateSeenByVerify runs a landing path whose verify reads the seam while the
// path holds the land lock, so writer and reader are checked against each other.
func gateSeenByVerify(t *testing.T, w *world, run func(Env) error) LandGate {
	t.Helper()
	e := w.env(false)
	var seen LandGate
	e.Shell = func(context.Context, string, string) (string, int) {
		g, err := e.WithDefaults().LandGate(bg, w.dir)
		if err != nil {
			t.Errorf("LandGate under the lock: %v", err)
		}
		seen = g
		return "", 0
	}
	if err := run(e); err != nil {
		t.Fatal(err)
	}
	return seen
}

func checkLandGate(t *testing.T, got LandGate, all bool, holder string, slots ...string) {
	t.Helper()
	var gated []string
	for s := range got.Slots {
		gated = append(gated, s)
	}
	slices.Sort(gated)
	if !got.Held || got.All != all || got.Holder != holder || !slices.Equal(gated, slots) {
		t.Errorf("LandGate = held %v all %v holder %q slots %v; want held true all %v holder %q slots %v",
			got.Held, got.All, got.Holder, gated, all, holder, slots)
	}
}

func TestLandGateReadsWhatTheGateWrites(t *testing.T) {
	w := newWorld(t, ghURL)
	got := gateSeenByVerify(t, w, func(e Env) error {
		_, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main"})
		return err
	})
	// The refusal names the slot by its recorded PR.
	checkLandGate(t, got, false, "#7", "w1")
}

func TestLandGateReadsWhatTheTrainWrites(t *testing.T) {
	w := trainWorld(t, "true", "b1", "b2")
	got := gateSeenByVerify(t, w, func(e Env) error {
		_, err := e.Train(bg, w.dir, TrainOpts{Base: "main", Targets: []string{"b1", "b2"}})
		return err
	})
	checkLandGate(t, got, false, "b1, b2", "b1", "b2")
}

func TestLandGateReadsWhatFreezeRecoveryWrites(t *testing.T) {
	w, _ := frozenWorld(t)
	got := gateSeenByVerify(t, w, func(e Env) error {
		_, err := e.Freeze(bg, w.dir, FreezeOpts{Verify: true})
		return err
	})
	checkLandGate(t, got, true, "freeze recovery")
}

// landGateRepo is a repo whose registry has slots with and without PRs and one
// queued PR per origin-slot state.
func landGateRepo(t *testing.T) (Env, string) {
	t.Helper()
	root := gittest.NewRepo(t, "main")
	err := Update(root, func(d *Doc) {
		ben := NewSlot("ben", "ben/1-x", "/w/ben", "main", "h")
		ben.SetPR("https://github.com/l4ci/rota/pull/304")
		d.AppendSlot(ben)
		d.AppendSlot(NewSlot("dana", "dana/2-y", "/w/dana", "main", "h"))
		d.AppendSlot(NewSlot("kit", "park/kit", "/w/kit", "main", "h"))
		d.AppendSlot(NewSlot("nia", "nia/3-z", "/w/nia", "main", "h"))
		d.QueuePR(QueuedPR{Issue: "3", Branch: "nia/3-z", PR: "#306", From: "nia"})
		d.QueuePR(QueuedPR{Issue: "4", Branch: "dana/4-old", PR: "#484", From: "dana"})
	})
	if err != nil {
		t.Fatal(err)
	}
	return Env{Config: diskConfig}.WithDefaults(), root
}

// #2639, #2745: a holder gates the slots it names, by slot name or by a PR a
// slot or its review-list record carries; a queued PR does not gate an origin
// slot that moved on. A target that is neither gates every slot.
func TestLandGateResolvesTargets(t *testing.T) {
	env, root := landGateRepo(t)
	cases := []struct {
		targets []string
		all     bool
		holder  string
		slots   []string
	}{
		{[]string{"304", "306", "307"}, false, "304, 306, 307", []string{"ben", "nia"}},
		{[]string{"#306"}, false, "#306", []string{"nia"}},
		{[]string{"#484"}, false, "#484", nil},
		{[]string{"dana"}, false, "dana", []string{"dana"}},
		{[]string{"ben", "#2001"}, false, "#304, #2001", []string{"ben"}},
		{[]string{"ben", "stranger"}, true, "#304, stranger", nil},
		{nil, true, "", nil},
	}
	for _, tc := range cases {
		err := env.withLandLock(bg, root, EncodeLandHolder(root, tc.targets...), func(bool) error {
			got, err := env.LandGate(bg, root)
			if err != nil {
				return err
			}
			checkLandGate(t, got, tc.all, tc.holder, tc.slots...)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// #4246, #4248: an empty, unreadable or older-rota holder file gates every
// slot. An older rota wrote the targets as plain text.
func TestLandGateUnreadableHolderGatesEverySlot(t *testing.T) {
	env, root := landGateRepo(t)
	common, err := rotastatetest.CommonDir(root)
	if err != nil {
		t.Fatal(err)
	}
	path := rotastate.File(common, "land") + LandHolderSuffix
	for _, tc := range []struct{ file, holder string }{
		{"", ""},
		{"dana", "dana"},
		{"304", "304"},
		{"{}", ""},
		{`{"targets":[{}]}`, ""},
		{`{"targets":[{"pr":"nonsense"}]}`, "nonsense"},
	} {
		err := env.withLandLock(bg, root, nil, func(bool) error {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(tc.file), 0o644); err != nil {
				return err
			}
			got, err := env.LandGate(bg, root)
			if err != nil {
				return err
			}
			checkLandGate(t, got, true, tc.holder)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestLandGateFreeLockGatesNothing(t *testing.T) {
	env, root := landGateRepo(t)
	got, err := env.LandGate(bg, root)
	if err != nil || got.Held || got.All || len(got.Slots) != 0 {
		t.Errorf("LandGate with a free lock = %+v, %v; want zero", got, err)
	}
}
