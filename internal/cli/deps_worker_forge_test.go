package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/tracker"
)

// workerForge builds the forge of Deps.workerEnv over an executor fake that
// counts its calls. The Deps carry a read cache, as in production.
func workerForge(t *testing.T, calls *[]string) *Deps {
	t.Helper()
	d := testDeps()
	d.TrackerOptions = []tracker.Option{tracker.WithExec(func(_ context.Context, _, name string, args []string, _ []byte) ([]byte, []byte, int, error) {
		*calls = append(*calls, name+" "+strings.Join(args, " "))
		return []byte(`{"headRefName":"dana/7-x","state":"OPEN","title":"t","mergeable":"UNKNOWN"}`), nil, 0, nil
	}, func(n string) (string, error) { return "/fake/" + n, nil })}
	if d.ReadCache == nil {
		t.Fatal("testDeps has no ReadCache: the test would not prove the forge skips it")
	}
	return d
}

// TestWorkerEnvForgeUsesTrackerOptions pins the one forge build site: the forge
// the worker env builds (gate, train and poll) runs through the TrackerOptions
// executor.
func TestWorkerEnvForgeUsesTrackerOptions(t *testing.T) {
	var calls []string
	d := workerForge(t, &calls)
	e := d.workerEnv()
	if e.Forge == nil {
		t.Fatal("workerEnv leaves Forge nil: the gate falls back to a forge without TrackerOptions")
	}
	f, err := e.Forge("github", t.TempDir(), tracker.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.PRView(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("PRView did not reach the TrackerOptions executor once: %v", calls)
	}
}

// TestWorkerEnvForgeSkipsReadCache pins that the gate's forge reads are never
// served from memory: forgeRefuses retries PRMergeable until GitHub has
// computed it, the CI verifier polls CommitChecks until the checks settle and
// the gate re-reads PRView after a push. A cached first answer would end each
// loop early or never.
func TestWorkerEnvForgeSkipsReadCache(t *testing.T) {
	var calls []string
	d := workerForge(t, &calls)
	f, err := d.workerEnv().Forge("github", t.TempDir(), tracker.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	reads := map[string]func() error{
		"PRMergeable": func() error { _, err := f.PRMergeable(ctx, 7); return err },
		"PRView":      func() error { _, err := f.PRView(ctx, 7); return err },
		"CommitChecks": func() error {
			_, err := f.CommitChecks(ctx, "abc123")
			return err
		},
	}
	for name, read := range reads {
		calls = nil
		if err := read(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		first := len(calls)
		if err := read(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if second := len(calls) - first; second != first {
			t.Errorf("%s: the repeat made %d forge calls, the first %d: it was served from the read cache", name, second, first)
		}
	}
}
