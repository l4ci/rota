package host

import (
	"context"
	"strings"
	"testing"
)

// A pane moved to another tab keeps its agent name, which still carries the
// tab it was started in; `agent get` is the one place the live tab shows. A
// tab shared with other panes is not the slot's to close, so the pane is its
// handle there.
func TestHerdrCurrentHandleNamesThePaneInASharedTabAndTheTabAlone(t *testing.T) {
	for name, tc := range map[string]struct{ panes, want string }{"shared": {"6", "w2:p4"}, "alone": {"1", "w2:t79"}} {
		t.Run(name, func(t *testing.T) {
			f := &fake{handler: func(_ string, a []string) Result {
				switch {
				case a[0] == "agent" && a[1] == "get" && a[2] == "rota-ben-w1-t2":
					return Result{Stdout: `{"id":"cli","result":{"agent":{"tab_id":"w2:t79","pane_id":"w2:p4"}}}`}
				case a[0] == "tab" && a[1] == "get":
					return Result{Stdout: `{"id":"cli","result":{"tab":{"tab_id":"w2:t79","pane_count":` + tc.panes + `}}}`}
				}
				return Result{ExitCode: 1, Stderr: `{"error":{"code":"agent_not_found"}}`}
			}}
			tf := New("herdr", deps(f, nil, &clock{})).(TabFollower)
			if got := tf.CurrentHandle(context.Background(), "ben", "w1:t2"); got != tc.want {
				t.Errorf("CurrentHandle = %q, want %s", got, tc.want)
			}
			if got := tf.CurrentHandle(context.Background(), "gone", "w1:t9"); got != "" {
				t.Errorf("CurrentHandle of a missing agent = %q, want empty", got)
			}
		})
	}
}

// Every lookup derives the agent name from the recorded handle, so moving the
// handle to the new tab renames the agent with it.
func TestHerdrFollowTabRenamesTheAgentToTheNewTab(t *testing.T) {
	f := &fake{handler: func(string, []string) Result { return Result{Stdout: `{"id":"cli","result":{"type":"ok"}}`} }}
	tf := New("herdr", deps(f, nil, &clock{})).(TabFollower)
	if err := tf.FollowTab(context.Background(), "ben", "w1:t2", "w2:t79"); err != nil {
		t.Fatal(err)
	}
	if want := "herdr agent rename rota-ben-w1-t2 rota-ben-w2-t79"; !strings.Contains(f.log(), want) {
		t.Errorf("log = %s\nwant %s", f.log(), want)
	}
}

func TestHerdrFollowTabReportsAFailedRename(t *testing.T) {
	f := &fake{handler: func(string, []string) Result {
		return Result{ExitCode: 1, Stderr: `{"error":{"code":"agent_not_found"}}`}
	}}
	tf := New("herdr", deps(f, nil, &clock{})).(TabFollower)
	if err := tf.FollowTab(context.Background(), "ben", "w1:t2", "w2:t79"); err == nil {
		t.Error("a rename herdr refused must fail, or the registry would point at a name nothing answers to")
	}
}

// A slot moved into the orchestrator's tab records its pane. Ending it must
// close that pane and leave the tab, and the panes in it, alone.
func TestHerdrKillOfAMovedSlotLeavesTheSharedTabAlone(t *testing.T) {
	gone := false
	f := &fake{handler: func(_ string, a []string) Result {
		switch {
		case a[0] == "agent" && a[1] == "get":
			return Result{Stdout: `{"id":"cli","result":{"agent":{"tab_id":"w2:t79","pane_id":"w2:p57"}}}`}
		case a[0] == "pane" && a[1] == "close":
			gone = true
		case a[0] == "pane" && a[1] == "get" && gone:
			return Result{ExitCode: 1, Stderr: `{"error":{"code":"pane_not_found"}}`}
		case a[0] == "pane" && a[1] == "process-info":
			return Result{Stdout: `{"result":{"processes":[]}}`}
		}
		return Result{Stdout: `{"id":"cli","result":{"type":"ok"}}`}
	}}
	if err := New("herdr", deps(f, nil, &clock{})).Kill(context.Background(), "ben", "w2:p57"); err != nil {
		t.Fatal(err)
	}
	if f.count("herdr tab close") != 0 || f.count("herdr tab get") != 0 {
		t.Errorf("the shared tab was touched:\n%s", f.log())
	}
	if want := "herdr pane close w2:p57"; !strings.Contains(f.log(), want) {
		t.Errorf("log = %s\nwant %s", f.log(), want)
	}
}

// A tab handle recorded while the pane was alone goes stale once other panes
// join that tab: Kill asks at kill time and closes the pane only.
func TestHerdrKillOfATabHandleThatNowHoldsOtherPanesClosesOnlyThePane(t *testing.T) {
	for name, tabGet := range map[string]Result{
		"shared":     {Stdout: `{"result":{"tab":{"pane_count":3}}}`},
		"unreadable": {Stdout: `{"result":{"tab":{}}}`},
	} {
		t.Run(name, func(t *testing.T) {
			gone := false
			f := &fake{handler: func(_ string, a []string) Result {
				switch {
				case a[0] == "agent" && a[1] == "get":
					return Result{Stdout: `{"id":"cli","result":{"agent":{"tab_id":"w2:t79","pane_id":"w2:p57"}}}`}
				case a[0] == "tab" && a[1] == "get":
					return tabGet
				case a[0] == "pane" && a[1] == "close":
					gone = true
				case a[0] == "pane" && a[1] == "get" && gone:
					return Result{ExitCode: 1, Stderr: `{"error":{"code":"pane_not_found"}}`}
				case a[0] == "pane" && a[1] == "process-info":
					return Result{Stdout: `{"result":{"processes":[]}}`}
				}
				return Result{Stdout: `{"id":"cli","result":{"type":"ok"}}`}
			}}
			if err := New("herdr", deps(f, nil, &clock{})).Kill(context.Background(), "ben", "w2:t79"); err != nil {
				t.Fatal(err)
			}
			if f.count("herdr tab close") != 0 {
				t.Errorf("the shared tab was closed:\n%s", f.log())
			}
			if want := "herdr pane close w2:p57"; !strings.Contains(f.log(), want) {
				t.Errorf("log = %s\nwant %s", f.log(), want)
			}
		})
	}
}

func TestHerdrSnapshotReadsPaneAndTabPaneCount(t *testing.T) {
	f := &fake{handler: func(string, []string) Result {
		return Result{Stdout: `{"id":"cli:api:snapshot","result":{"snapshot":{
			"agents":[
				{"agent_status":"idle","cwd":"/p/.worktrees/ben","name":"rota-ben-w1-t2","tab_id":"w2:t79","pane_id":"w2:p57"},
				{"agent_status":"idle","cwd":"/p/.worktrees/kit","name":"rota-kit-w1-t3","tab_id":"w2:t7F","pane_id":"w2:p60"},
				{"agent_status":"idle","cwd":"/p/.worktrees/nia","name":"rota-nia-w1-t4","tab_id":"w2:t80","pane_id":"w2:p61"}],
			"tabs":[{"tab_id":"w2:t79","pane_count":6},{"tab_id":"w2:t7F","pane_count":1}]}}}`}
	}}
	got, err := New("herdr", deps(f, nil, &clock{})).(Snapshotter).Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// shared tab: the pane; alone: the tab; count missing: the pane, the safe side
	want := []string{"w2:p57", "w2:t7F", "w2:p61"}
	for i, a := range got {
		if a.Handle() != want[i] {
			t.Errorf("agent %s handle = %q, want %q (%+v)", a.Name, a.Handle(), want[i], a)
		}
	}
	if got[0].Pane != "w2:p57" || got[0].TabPanes != 6 || got[2].TabPanes != 0 {
		t.Errorf("agents = %+v", got)
	}
}
