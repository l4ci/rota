package reap

import (
	"testing"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/round"
)

func TestOwnershipTable(t *testing.T) {
	const wtDir = "/r/.worktrees"
	rep := &round.Report{
		Rows: []round.Row{
			{Name: "slot", Registered: true, Branch: "kit/3-a", Tab: "w6:t1"},
			{Name: "matched", Agent: "claude"},
		},
		Agents: []host.Agent{
			{Tab: "w3:t9", Cwd: "/r/.worktrees/live/sub"},
			{Tab: "w9:t1", Cwd: "/elsewhere"},
		},
	}
	checkouts := []checkout{
		{path: "/r", branch: "main"},
		{path: "/r/.worktrees/free", branch: "kit/1-a"},
		{path: "/r/.worktrees/slot", branch: "kit/3-a"},
		{path: "/r/.worktrees/matched", branch: "kit/4-a"},
		{path: "/r/.worktrees/live", branch: "kit/2-a"},
		{path: "/r/.worktrees/locked", branch: "kit/5-a", isLocked: true},
		{path: "/r/.worktrees/ben", branch: "park/ben"},
		{path: "/r/.worktrees/onbase", branch: "main"},
	}
	o := newOwnership(rep, "main", wtDir, checkouts)

	for _, c := range []struct {
		name string
		want bool
	}{
		{"free", false}, {"slot", true}, {"matched", true}, {"live", true},
		{"locked", true}, {"ben", true}, {"onbase", true},
	} {
		var co *checkout
		for i := range checkouts {
			if checkouts[i].path == wtDir+"/"+c.name {
				co = &checkouts[i]
			}
		}
		if got := o.Worktree(co); got != c.want {
			t.Errorf("Worktree(%s) = %v, want %v", c.name, got, c.want)
		}
	}

	for b, want := range map[string]bool{"kit/1-a": false, "kit/3-a": true, "main": true, "park/ben": true} {
		if got := o.Branch(b); got != want {
			t.Errorf("Branch(%s) = %v, want %v", b, got, want)
		}
	}

	tab := func(id, cwd string, agentless bool) host.Tab {
		return host.Tab{ID: id, Agentless: agentless, Cwds: []string{cwd}}
	}
	for _, c := range []struct {
		name string
		tab  host.Tab
		want bool
	}{
		{"free checkout", tab("w1:t1", "/r/.worktrees/free", true), false},
		{"removed checkout", tab("w2:t1", "/r/.worktrees/gone", true), false},
		{"agent in checkout", tab("w3:t1", "/r/.worktrees/live", true), true},
		{"not provably agentless", tab("w4:t1", "/r/.worktrees/free", false), true},
		{"parked", tab("w5:t1", "/r/.worktrees/ben", true), true},
		{"slot tab", tab("w6:t1", "/r/.worktrees/free", true), true},
		{"other project", tab("w7:t1", "/elsewhere/proj", true), true},
		{"main checkout", tab("w8:t1", "/r", true), true},
		{"agent on tab", tab("w9:t1", "/r/.worktrees/gone2", true), true},
		{"no cwds", host.Tab{ID: "w10:t1", Agentless: true}, true},
	} {
		if got := o.Tab(c.tab); got != c.want {
			t.Errorf("Tab(%s) = %v, want %v", c.name, got, c.want)
		}
	}

	for _, c := range []struct {
		name string
		p    Process
		want bool
	}{
		{"free", Process{PID: 5, Tab: "w1:t1", Cwd: "/r/.worktrees/free"}, false},
		{"no pid", Process{Tab: "w1:t1", Cwd: "/r/.worktrees/free"}, true},
		{"live cwd", Process{PID: 5, Tab: "w1:t1", Cwd: "/r/.worktrees/live"}, true},
		{"agent tab", Process{PID: 5, Tab: "w3:t9", Cwd: "/r/.worktrees/free"}, true},
		{"slot tab", Process{PID: 5, Tab: "w6:t1", Cwd: "/r/.worktrees/free"}, true},
		{"outside", Process{PID: 5, Tab: "w1:t1", Cwd: "/elsewhere"}, true},
	} {
		if got := o.Process(c.p); got != c.want {
			t.Errorf("Process(%s) = %v, want %v", c.name, got, c.want)
		}
	}
}
