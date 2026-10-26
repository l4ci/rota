package host

import (
	"context"
	"strings"
	"testing"
)

// A pane moved to another tab keeps its agent name, which still carries the
// tab it was started in; `agent get` is the one place the live tab shows.
func TestHerdrCurrentTabReadsThePanesLiveTab(t *testing.T) {
	f := &fake{handler: func(_ string, a []string) Result {
		if a[0] == "agent" && a[1] == "get" && a[2] == "rota-ben-w1-t2" {
			return Result{Stdout: `{"id":"cli","result":{"agent":{"tab_id":"w2:t79","pane_id":"w2:p4"}}}`}
		}
		return Result{ExitCode: 1, Stderr: `{"error":{"code":"agent_not_found"}}`}
	}}
	tf := New("herdr", deps(f, nil, &clock{})).(TabFollower)
	if got := tf.CurrentTab(context.Background(), "ben", "w1:t2"); got != "w2:t79" {
		t.Errorf("CurrentTab = %q, want w2:t79", got)
	}
	if got := tf.CurrentTab(context.Background(), "gone", "w1:t9"); got != "" {
		t.Errorf("CurrentTab of a missing agent = %q, want empty", got)
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
