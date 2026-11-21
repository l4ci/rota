package roundtick

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/l4ci/rota/internal/gate"
	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/worker"
)

// passWorld records what the pass told the operator and the observe flag the
// factory was handed.
type passWorld struct {
	*fakeWorld
	warns []string
}

func (w *passWorld) Warn(m string) { w.warns = append(w.warns, m) }

func passFixture(t *testing.T, observe bool) (Pass, *passWorld, string) {
	t.Helper()
	root := gittest.NewRepo(t, "main")
	path := rotatree.Workers(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"round":1,"slots":[{"name":"ben","state":"idle"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cd := t.TempDir()
	w := &passWorld{fakeWorld: &fakeWorld{root: root, observing: observe}}
	return Pass{
		CommonDir: cd, Round: 7, Observe: observe,
		Config: Config{Settings: roundcfg.Settings{Roster: []string{"ben"}}, MergeApproval: gate.MergeNone, Base: "main"},
		World:  func(_ State, observe bool) World { w.observing = observe; return w },
	}, w, cd
}

// A normal pass saves the round's state; an observing one saves nothing.
func TestRunPassSavesUnlessObserving(t *testing.T) {
	for _, tc := range []struct {
		name    string
		observe bool
		saved   bool
	}{{"normal", false, true}, {"observe", true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			p, _, cd := passFixture(t, tc.observe)
			if _, err := RunPass(context.Background(), p); err != nil {
				t.Fatal(err)
			}
			_, err := os.Stat(statePath(cd))
			if got := err == nil; got != tc.saved {
				t.Fatalf("state saved = %v, want %v (%v)", got, tc.saved, err)
			}
		})
	}
}

// The world is built from the state the pass loaded, and Observe reaches it
// once, from the pass.
func TestRunPassHandsTheWorldItsStateAndMode(t *testing.T) {
	p, w, cd := passFixture(t, true)
	if err := SaveState(cd, 7, Result{Held: map[string]string{"ben": "12"}}); err != nil {
		t.Fatal(err)
	}
	var got State
	var mode bool
	p.World = func(st State, observe bool) World { got, mode = st, observe; w.observing = observe; return w }
	if _, err := RunPass(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if got.Held["ben"] != "12" || !mode {
		t.Fatalf("world got state %+v observe %v", got, mode)
	}
}

// A state that cannot be saved is warned about; the pass still returns its result.
func TestRunPassWarnsWhenStateIsNotSaved(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory modes")
	}
	p, w, cd := passFixture(t, false)
	if err := os.Chmod(cd, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(cd, 0o700) })
	if _, err := RunPass(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if len(w.warns) != 1 {
		t.Fatalf("warnings %q", w.warns)
	}
}

// An assign's warnings are said even when it fails; its overlap and placement
// notes only when it went through.
func TestAssignNotesFollowTheOutcome(t *testing.T) {
	headroom, gap := 80.0, 60.0
	res := round.Assigned{Agent: "ben", Warnings: []string{"w1"}, Placement: &round.Placement{Kind: "codex", Account: "c1", Headroom: &headroom, Gap: &gap, Margin: 20}}
	const placed = "moved\t#12: c1 (codex, headroom 80%) moved, best is 60 points ahead (margin 20)"
	for _, tc := range []struct {
		name string
		err  error
		want []string
	}{{"ok", nil, []string{"w1", placed}}, {"refused", errors.New("no"), []string{"w1"}}} {
		t.Run(tc.name, func(t *testing.T) {
			w := &assignWorld{passWorld: &passWorld{fakeWorld: &fakeWorld{}}, res: res, err: tc.err}
			e := NewEnv(w, Config{})
			_, err := e.Assign(context.Background(), "12")
			if (err != nil) != (tc.err != nil) {
				t.Fatalf("err %v", err)
			}
			if !slices.Equal(w.warns, tc.want) {
				t.Fatalf("warnings %q, want %q", w.warns, tc.want)
			}
		})
	}
}

type assignWorld struct {
	*passWorld
	res round.Assigned
	err error
}

func (w *assignWorld) Assign(context.Context, string) (round.Assigned, error) { return w.res, w.err }

// The follow-up sources are built from ports alone: no queued PR asks the
// forge nothing, the humans join into Who and the reserve follows the settings.
func TestFollowUpSourcesFromPorts(t *testing.T) {
	root := gittest.NewRepo(t, "main")
	path := rotatree.Workers(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	reg := func(body string) {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	forged, started := 0, 0
	var warns []string
	set := roundcfg.Settings{Roster: []string{"ben", "dana", "kit"}, Slots: 1, FollowupReserve: 1, ReviewLoop: roundcfg.ReviewLoopAuto}
	src := NewFollowUpSources(FollowUpPorts{
		Settings: set,
		Registry: func() worker.Registry { return worker.LoadRegistryTolerant(root) },
		Forge:    func(context.Context) (round.ReviewForge, error) { forged++; return nil, nil },
		List: func(context.Context, round.ReviewForge) ([]round.FollowUp, []string, error) {
			return []round.FollowUp{{Issue: "#7", PR: "#70", Humans: []string{"alice", "bob"}}}, []string{"slow"}, nil
		},
		Start: func(context.Context, round.FollowUp, string, bool) ([]string, error) {
			started++
			return []string{"start-warn"}, nil
		},
		Warn: func(m string) { warns = append(warns, m) },
	})
	ctx := context.Background()
	reg(`{"round":1,"slots":[{"name":"ben","state":"idle"}]}`)
	if list, err := src.List(ctx); err != nil || list != nil || forged != 0 {
		t.Fatalf("no queued PR: list %v err %v forge calls %d", list, err, forged)
	}
	reg(`{"round":1,"slots":[{"name":"ben","state":"idle"}],"prs":[{"pr":"#70","issue":"#7"}]}`)
	list, err := src.List(ctx)
	if err != nil || len(list) != 1 || list[0].Who != "alice, bob" || forged != 1 || !slices.Equal(warns, []string{"slow"}) {
		t.Fatalf("list %+v err %v forge calls %d warns %q", list, err, forged, warns)
	}
	if err := src.Start(ctx, list[0], "ben", false); err != nil || started != 1 || warns[1] != "start-warn" {
		t.Fatalf("start err %v started %d warns %q", err, started, warns)
	}
	if !src.Auto || src.Size != 1 || !slices.Equal(src.Reserve(), []string{"dana"}) {
		t.Fatalf("auto %v size %d reserve %v", src.Auto, src.Size, src.Reserve())
	}
}

// A forge that cannot be opened fails the list, which the step reports.
func TestFollowUpSourcesForgeErrorFailsTheList(t *testing.T) {
	root := gittest.NewRepo(t, "main")
	path := rotatree.Workers(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"round":1,"slots":[],"prs":[{"pr":"#70","issue":"#7"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("no forge")
	src := NewFollowUpSources(FollowUpPorts{
		Registry: func() worker.Registry { return worker.LoadRegistryTolerant(root) },
		Forge:    func(context.Context) (round.ReviewForge, error) { return nil, boom },
	})
	if _, err := src.List(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("err %v", err)
	}
}

// A world that disagrees with Pass.Observe is refused before any read or write,
// whichever way it disagrees.
func TestRunPassRefusesAWorldInTheWrongMode(t *testing.T) {
	for _, observe := range []bool{true, false} {
		p, w, cd := passFixture(t, observe)
		p.World = func(State, bool) World { w.observing = !observe; return w }
		if _, err := RunPass(context.Background(), p); !errors.Is(err, ErrObserveMismatch) {
			t.Fatalf("observe=%v: err %v", observe, err)
		}
		if _, err := os.Stat(statePath(cd)); err == nil {
			t.Fatalf("observe=%v: a refused pass saved state", observe)
		}
	}
}
