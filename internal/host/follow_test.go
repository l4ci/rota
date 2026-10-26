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
