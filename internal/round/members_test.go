package round

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/worker"
)

var fiveRoster = []string{"ben", "dana", "nia", "kit", "sam"}

// startN starts (or renews) the round with n slots over the five-name roster.
func startN(t *testing.T, e Env, root string, n int) Started {
	t.Helper()
	o := startOpts(roundcfg.ScopeMilestone, 100)
	o.Settings.Roster = fiveRoster
	o.Slots = n
	st, err := e.Start(bg, root, o)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func slotNames(st Started) (names, draining []string) {
	for _, s := range st.Slots {
		sl := worker.AsSlot(s)
		names = append(names, sl.Name())
		if d, _ := s.Get("draining"); d == true {
			draining = append(draining, sl.Name())
		}
	}
	return names, draining
}

func holdTask(t *testing.T, root, name, task string) {
	t.Helper()
	if found, err := worker.UpdateSlot(root, name, func(s *worker.Slot) { s.SetTask(task) }); err != nil || !found {
		t.Fatalf("hold %s on %s: %v %v", task, name, found, err)
	}
}

func TestStartUsesExactlyConfiguredSlots(t *testing.T) {
	root := newRepo(t, nil)
	e := Env{Git: git.Exec, Base: "main", Worker: wk(worker.Env{Getenv: noEnv, Config: diskConfig}), Lease: fakeLease("h", 100)}
	startN(t, e, root, 5)
	st := startN(t, e, root, 3)
	if names, _ := slotNames(st); !reflect.DeepEqual(names, []string{"ben", "dana", "nia"}) {
		t.Fatalf("slots %v, want the first three roster names", names)
	}
	reg := worker.LoadRegistryTolerant(root)
	for _, name := range []string{"kit", "sam"} {
		s := reg.Slot(name)
		if s == nil || s.Branch() != "park/"+name {
			t.Errorf("%s must stay registered and parked: %v", name, s)
		}
		if _, err := os.Stat(filepath.Join(root, ".worktrees", name)); err != nil {
			t.Errorf("%s worktree removed: %v", name, err)
		}
	}
}

func TestStartSlotsFlagProvisionsFive(t *testing.T) {
	root := newRepo(t, nil)
	e := Env{Git: git.Exec, Base: "main", Worker: wk(worker.Env{Getenv: noEnv, Config: diskConfig}), Lease: fakeLease("h", 100)}
	startN(t, e, root, 3)
	st := startN(t, e, root, 5)
	if names, _ := slotNames(st); !reflect.DeepEqual(names, fiveRoster) {
		t.Fatalf("--slots 5: %v", names)
	}
}

func TestStartKeepsBusyExtraSlotDraining(t *testing.T) {
	root := newRepo(t, nil)
	e := Env{Git: git.Exec, Base: "main", Worker: wk(worker.Env{Getenv: noEnv, Config: diskConfig}), Lease: fakeLease("h", 100)}
	startN(t, e, root, 5)
	holdTask(t, root, "kit", "41")
	st := startN(t, e, root, 3)
	names, draining := slotNames(st)
	if !reflect.DeepEqual(names, []string{"ben", "dana", "nia", "kit"}) || !reflect.DeepEqual(draining, []string{"kit"}) {
		t.Fatalf("slots %v draining %v, want kit draining beside the first three", names, draining)
	}
}

func TestDrainedSlotLeavesRoundAfterMerge(t *testing.T) {
	root := newRepo(t, nil)
	e := Env{Git: git.Exec, Base: "main", Worker: wk(worker.Env{Getenv: noEnv, Config: diskConfig}), Lease: fakeLease("h", 100)}
	startN(t, e, root, 5)
	holdTask(t, root, "kit", "41")
	startN(t, e, root, 3)
	set := roundcfg.Settings{Roster: fiveRoster, Slots: 3}
	before, _ := os.ReadFile(filepath.Join(root, ".rota", "workers.json"))
	if _, d := Members(worker.LoadRegistryTolerant(root), set); !reflect.DeepEqual(d, []string{"kit"}) {
		t.Fatalf("draining %v, want kit", d)
	}
	// Its work lands: the merge parks the slot, and the next read drops it.
	if _, err := worker.UpdateSlot(root, "kit", func(s *worker.Slot) { s.Park(true) }); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(root, ".rota", "workers.json"))
	active, d := Members(worker.LoadRegistryTolerant(root), set)
	if len(d) != 0 || !reflect.DeepEqual(active, []string{"ben", "dana", "nia"}) {
		t.Fatalf("after merge: active %v draining %v", active, d)
	}
	if again, _ := os.ReadFile(filepath.Join(root, ".rota", "workers.json")); string(again) != string(after) || string(before) == "" {
		t.Error("Members wrote the registry")
	}
	if names, _ := slotNames(startN(t, e, root, 3)); !reflect.DeepEqual(names, []string{"ben", "dana", "nia"}) {
		t.Errorf("next start still lists the drained slot: %v", names)
	}
}

// An idle roster slot past the round's count is outside the round: assign
// never hands it new work, with or without --agent.
func TestAssignSkipsDrainingSlot(t *testing.T) {
	f := newAssignFixture(t) // started with two slots: ben, dana
	o := startOpts(roundcfg.ScopeMilestone, 100)
	o.Slots = 4 // ben, dana, nia, kit: kit stays idle past the count
	if _, err := f.env.Start(bg, f.root, o); err != nil {
		t.Fatal(err)
	}
	holdTask(t, f.root, "nia", "41") // nia drains, kit is parked outside the round
	if _, err := f.env.Start(bg, f.root, startOpts(roundcfg.ScopeMilestone, 100)); err != nil {
		t.Fatal(err)
	}
	holdTask(t, f.root, "ben", "51")
	holdTask(t, f.root, "dana", "52")
	if _, err := f.assign("12", "", nil); blockedBy(t, err) != BlockNoFreeSlot {
		t.Fatalf("assign picked a slot outside the round: %v", err)
	}
	for _, agent := range []string{"nia", "kit"} {
		if _, err := f.assign("12", agent, nil); blockedBy(t, err) != BlockDraining {
			t.Fatalf("assign --agent %s: %v, want %q", agent, err, BlockDraining)
		}
	}
}
