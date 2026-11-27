package reap

import (
	"testing"
	"time"
)

// One preview reads the process table once, whatever kinds hold candidates:
// on a host without /proc each read is a whole lsof run (#4287).
func TestFindReadsProcessTableOnce(t *testing.T) {
	in := tmpInput(t, tmpRoot(t, map[string]time.Duration{"run-a": 5 * time.Hour}), nil, nil)
	in.SystemTemp = sysTemp(t, map[string]time.Duration{"rota-smoke.old": 3 * time.Hour})
	calls := 0
	in.ProcessUses = func() ([]ProcUse, error) {
		calls++
		return nil, nil
	}
	res := find(t, in, KindTmp, KindSysTmp)
	if len(res.Candidates) != 2 {
		t.Fatalf("candidates = %v", ids(res.Candidates))
	}
	if calls != 1 {
		t.Fatalf("ProcessUses calls = %d, want 1", calls)
	}
}
