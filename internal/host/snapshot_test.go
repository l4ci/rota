package host

import (
	"reflect"
	"testing"
)

func TestHerdrSnapshotReadsAgents(t *testing.T) {
	f := &fake{handler: func(name string, args []string) Result {
		return Result{Stdout: `{"id":"cli:api:snapshot","result":{"snapshot":{"agents":[
			{"agent":"claude","agent_status":"working","cwd":"/p/.worktrees/dana","name":"dana","tab_id":"w1:t1"},
			{"agent":"claude","agent_status":"idle","cwd":"/p","tab_id":"w2:t1"}],"panes":[{"pane_id":"w9:p1"}]}}}`}
	}}
	got, err := New("herdr", deps(f, nil, &clock{})).(Snapshotter).Snapshot(bg)
	if err != nil {
		t.Fatal(err)
	}
	want := []Agent{{Tab: "w1:t1", Name: "dana", Cwd: "/p/.worktrees/dana", Status: "working"}, {Tab: "w2:t1", Cwd: "/p", Status: "idle"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("agents = %+v", got)
	}
	if f.log() != "herdr api snapshot" {
		t.Errorf("calls = %q", f.log())
	}
}

func TestSnapshotFailuresAreErrors(t *testing.T) {
	for _, kind := range []string{"herdr", "tmux"} {
		f := &fake{handler: func(string, []string) Result { return Result{ExitCode: 1, Stderr: "not running"} }}
		if _, err := New(kind, deps(f, nil, &clock{})).(Snapshotter).Snapshot(bg); err == nil {
			t.Errorf("%s: a failing host must be an error, not an empty round", kind)
		}
	}
	f := &fake{handler: func(string, []string) Result { return Result{Stdout: "not json"} }}
	if _, err := New("herdr", deps(f, nil, &clock{})).(Snapshotter).Snapshot(bg); err == nil {
		t.Error("an unreadable herdr reply must be an error")
	}
}

func TestTmuxSnapshotListsWindows(t *testing.T) {
	f := &fake{handler: func(string, []string) Result {
		return Result{Stdout: "rota:ben\t/p/.worktrees/ben\nrota:kit\t/p/.worktrees/kit\n\n"}
	}}
	got, err := New("tmux", deps(f, nil, &clock{})).(Snapshotter).Snapshot(bg)
	if err != nil {
		t.Fatal(err)
	}
	want := []Agent{{Tab: "rota:ben", Cwd: "/p/.worktrees/ben"}, {Tab: "rota:kit", Cwd: "/p/.worktrees/kit"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("agents = %+v", got)
	}
}
