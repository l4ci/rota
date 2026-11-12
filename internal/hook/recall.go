package hook

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/knowledge"
	"github.com/l4ci/rota/internal/stamp"
)

// RecallRequest supplies the process reads needed for Edit/Write recall.
// Entries queries the project at cwd for an absolute file path.
type RecallRequest struct {
	Payload    Payload
	DefaultCwd string
	Entries    func(cwd, file string) ([]knowledge.PathEntry, error)
	CommonDir  func(cwd string) (string, error)
	Now        func() time.Time
}

// DecideRecall resolves the edited path and emits unseen project context.
// Missing input, lookup failures and failed receipt writes all pass silently.
func DecideRecall(in RecallRequest) string {
	tool := in.Payload.String("tool_name")
	file := in.Payload.String("tool_input", "file_path")
	if (tool != "Edit" && tool != "Write") || file == "" {
		return ""
	}
	cwd := in.Payload.CwdOr(in.DefaultCwd)
	if !filepath.IsAbs(file) {
		file = filepath.Join(cwd, file)
	}
	entries, err := in.Entries(cwd, file)
	if err != nil || len(entries) == 0 {
		return ""
	}
	cd, err := in.CommonDir(cwd)
	if err != nil {
		return ""
	}
	return Recall(cd, in.Payload.SessionID, entries, in.Now())
}

// Recall claims unseen entries under the session lock before returning context.
// A failed claim emits nothing so concurrent invocations cannot repeat entries.
func Recall(commonDir, sessionID string, entries []knowledge.PathEntry, now time.Time) string {
	if len(entries) == 0 {
		return ""
	}
	p, err := StatePath(commonDir, sessionID)
	if err != nil {
		return ""
	}
	// Recall receipts outlive expiring statusline readings: a session may
	// remain open after StateTTL. The normal session sweep ignores this dir.
	p = filepath.Join(filepath.Dir(p), "recall", filepath.Base(p))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return ""
	}
	var fresh []string
	err = UpdateState(p, func(st State, _ bool) State {
		if st.RecalledEntries == nil {
			st.RecalledEntries = map[string]bool{}
		}
		for _, entry := range entries {
			if !st.RecalledEntries[entry.ID] {
				fresh = append(fresh, entry.Text)
				st.RecalledEntries[entry.ID] = true
			}
		}
		st.SessionID = sessionID
		st.UpdatedAt = stamp.Format(now)
		return st
	})
	if err != nil || len(fresh) == 0 {
		return ""
	}
	return "Path-scoped project context (before editing):\n\n" + strings.Join(fresh, "\n\n")
}
