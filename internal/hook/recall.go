package hook

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/knowledge"
)

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
		st.UpdatedAt = now.UTC().Format(time.RFC3339)
		return st
	})
	if err != nil || len(fresh) == 0 {
		return ""
	}
	return "Path-scoped project context (before editing):\n\n" + strings.Join(fresh, "\n\n")
}
