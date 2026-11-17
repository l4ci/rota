package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/rotatree"
)

type reopenSpy struct {
	backlog.Backend
	issues bool
	calls  int
}

func (r *reopenSpy) Capabilities() backlog.Capabilities {
	return backlog.Capabilities{IssueIDs: r.issues}
}
func (r *reopenSpy) Reopen(string) (bool, error) { r.calls++; return true, nil }

func TestRestoreBackend(t *testing.T) {
	root := t.TempDir()
	path := rotatree.Backlog(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# Backlog\n\n## Active\n\n- **[B01]** open item\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	file := &reopenSpy{}
	rb := restoreBackend{file, root}
	if changed, err := rb.Reopen("B01"); err != nil || changed || file.calls != 0 {
		t.Errorf("active file item: changed=%v err=%v calls=%d", changed, err, file.calls)
	}
	if changed, _ := rb.Reopen("B02"); !changed || file.calls != 1 {
		t.Errorf("inactive file item: changed=%v calls=%d", changed, file.calls)
	}
	if got := rb.State(); got != "active in BACKLOG.md" {
		t.Errorf("file state %q", got)
	}

	tr := &reopenSpy{issues: true}
	tb := restoreBackend{tr, root}
	if changed, _ := tb.Reopen("B01"); !changed || tr.calls != 1 {
		t.Errorf("tracker must always reopen: changed=%v calls=%d", changed, tr.calls)
	}
	if got := tb.State(); got != "open on the tracker" {
		t.Errorf("tracker state %q", got)
	}
}
