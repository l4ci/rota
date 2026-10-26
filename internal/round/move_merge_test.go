package round

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/blockedby"
)

// A worker resolving a merge conflict is mid-work: moving its issue now would
// park the worktree under the merge (#2072). Transfer refuses, to the human
// and to a slot alike, until the merge is committed or aborted.
func TestTransferRefusesWhileTheSendersWorktreeIsMerging(t *testing.T) {
	f := newMoveFx(t)
	if _, err := f.assign("12", "ben"); err != nil {
		t.Fatal(err)
	}
	mergeHead := gitIn(t, f.wt("ben"), "rev-parse", "--path-format=absolute", "--git-path", "MERGE_HEAD")
	if err := os.MkdirAll(filepath.Dir(mergeHead), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mergeHead, []byte(gitIn(t, f.wt("ben"), "rev-parse", "HEAD")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, to := range []string{HumanTarget, "dana"} {
		_, err := f.transfer("12", to, nil)
		if err == nil || blockedBy(t, err) != blockedby.MergeInProgress {
			t.Fatalf("transfer to %s while merging: %v, want blockedBy %q", to, err, blockedby.MergeInProgress)
		}
	}
	if err := os.Remove(mergeHead); err != nil {
		t.Fatal(err)
	}
	if _, err := f.transfer("12", HumanTarget, nil); err != nil {
		t.Fatalf("transfer after the merge ended: %v", err)
	}
}
