package cli

import (
	"path/filepath"
	"testing"
)

func TestSlotApprovalThreadResolvesAQueuedPR(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".rota", "workers.json"), `{"slots":[
{"name":"a","branch":"park/a"},
{"name":"b","branch":"b/3-y","pr":"https://github.com/o/r/pull/9"}],
"prs":[{"issue":"5","branch":"a/5-x","pr":"https://github.com/o/r/pull/42","from":"a"}]}`)
	for _, ref := range []string{"#42", "42", "https://github.com/o/r/pull/42"} {
		th, err := slotApprovalThread(root, ref)
		if err != nil || th.Kind != "pr" || th.Number != 42 || th.Slot != "" || th.Title != "Merge approval: PR #42" {
			t.Errorf("%s: %+v %v", ref, th, err)
		}
	}
	if th, err := slotApprovalThread(root, "#9"); err != nil || th.Number != 9 || th.Slot != "b" {
		t.Errorf("a slot's PR by number: %+v %v", th, err)
	}
	if _, err := slotApprovalThread(root, "#77"); err == nil {
		t.Error("unknown PR")
	}
}
