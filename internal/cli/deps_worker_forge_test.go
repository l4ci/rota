package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/tracker"
)

// TestWorkerEnvForgeUsesTrackerOptions pins the one forge build site: the forge
// the worker env builds (gate, train and poll) runs through the TrackerOptions
// executor and the invocation's read cache, so freshReads covers its reads.
func TestWorkerEnvForgeUsesTrackerOptions(t *testing.T) {
	var calls []string
	d := testDeps()
	d.TrackerOptions = []tracker.Option{tracker.WithExec(func(_ context.Context, _, name string, args []string, _ []byte) ([]byte, []byte, int, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return []byte(`{"headRefName":"dana/7-x","state":"OPEN","title":"t"}`), nil, 0, nil
	}, func(n string) (string, error) { return "/fake/" + n, nil })}

	e := d.workerEnv()
	if e.Forge == nil {
		t.Fatal("workerEnv leaves Forge nil: the gate falls back to a forge without TrackerOptions")
	}
	f, err := e.Forge("github", t.TempDir(), tracker.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	view := func() {
		t.Helper()
		if _, err := f.PRView(context.Background(), 7); err != nil {
			t.Fatal(err)
		}
	}
	view()
	if len(calls) != 1 {
		t.Fatalf("PRView did not reach the TrackerOptions executor once: %v", calls)
	}
	view()
	if len(calls) != 1 {
		t.Errorf("a repeated PRView missed the read cache: %v", calls)
	}
	d.freshReads()
	view()
	if len(calls) != 2 {
		t.Errorf("freshReads did not clear the forge's cached read: %v", calls)
	}
}
