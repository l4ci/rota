package config

import (
	"errors"
	"reflect"
	"testing"
)

// A refusal in any step of Migrate --apply writes no file: the earlier steps
// plan first, so the files keep their bytes.
func TestMigrateRefusalWritesNothing(t *testing.T) {
	const (
		oldRound    = `{"work":{"workerSlots":3}}`
		oldAccounts = `{"round":{"codexAccounts":[{"name":"c","codexHome":"/c"}]}}`
	)
	for _, tc := range []struct {
		name                   string
		global, project, local string
		conflict               any // pointer the error must be as
	}{
		{"round step", "", `{"work":{"workerSlots":3},"round":{"slots":4}}`, "", new(*ConflictError)},
		{"accounts step", "", oldRound, `{"round":{"codexAccounts":[{"name":"x","codexHome":"/c"}],"opencodeAccounts":[{"name":"x","configDir":"/o"}]}}`, new(*AccountConflictError)},
		{"merges step", oldAccounts, oldRound, `{"round":{"codexCommand":"a","commands":{"codex":"b"}}}`, new(*MergeConflictError)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newAcctRig(t, tc.global, tc.project, tc.local)
			before := []string{readFileOr0(GlobalPath(r.gdir)), readFileOr0(r.cfg()), readFileOr0(r.local())}
			_, err := Migrate(r.env, r.root, true, nil)
			if err == nil || !errors.As(err, tc.conflict) {
				t.Fatalf("err = %v, want %T", err, tc.conflict)
			}
			after := []string{readFileOr0(GlobalPath(r.gdir)), readFileOr0(r.cfg()), readFileOr0(r.local())}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("files changed on refusal:\nbefore %q\nafter  %q", before, after)
			}
		})
	}
}

// Without a refusal Migrate previews, then applies, every step in order.
func TestMigrateStepsRun(t *testing.T) {
	r := newAcctRig(t, "", `{"work":{"workerSlots":3},"models":{"orchestrator":"sonnet"}}`, "")
	m, err := Migrate(r.env, r.root, false, nil)
	if err != nil || !m.Round.Changed() || !m.Merges.Changed() {
		t.Fatalf("preview = %+v, %v", m, err)
	}
	if got := readFileOr0(r.cfg()); got != `{"work":{"workerSlots":3},"models":{"orchestrator":"sonnet"}}` {
		t.Fatalf("preview wrote: %s", got)
	}
	if lookupWork, _ := mergeVal(t, readFileOr0(r.cfg()), "work.workerSlots"); lookupWork == nil {
		t.Fatal("preview lost work.workerSlots")
	}
	m, err = Migrate(r.env, r.root, true, nil)
	if err != nil || !m.Changed() {
		t.Fatalf("apply = %+v, %v", m, err)
	}
	if v, ok := mergeVal(t, readFileOr0(r.local()), "orchestrator.model"); !ok || v != "sonnet" {
		t.Fatalf("orchestrator.model = %v, %v", v, ok)
	}
}

func readFileOr0(path string) string { return string(readFileOr(path)) }
