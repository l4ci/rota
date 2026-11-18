package landing

import (
	"github.com/l4ci/rota/internal/tracker"
	"strconv"
)

// ApprovalThread is the forge thread a merge's approval request lives on (C5).
type ApprovalThread struct {
	Kind   string           // pr | issue
	Number int              // the PR, or a numbered issue
	Key    tracker.IssueKey // a key-based tracker's issue; empty for a numbered one
	Slot   string
	Title  string
}

// Ref is the thread's number as users read it: `#12`, or the key PROJ-123.
func (t ApprovalThread) Ref() string {
	if t.Key != "" {
		return t.Key.Ref()
	}
	return "#" + strconv.Itoa(t.Number)
}
