package cli

import (
	"errors"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/repos"
	"github.com/l4ci/rota/internal/verdict"
)

// debugItem uses the same canonical identity and scope as other item verbs.
// File IDs remain literal. Issue-mode legacy records are consolidated before
// any count is checked or changed, including a counter's old raw bug_id.
func debugItem(c *Ctx, ref string) (string, error) {
	root, id, issueIDs, err := resolveDebugItem(c, ref)
	if err != nil || !issueIDs {
		return id, err
	}
	return id, verdict.CanonicalizeItems(root, debugKeys(root))
}

// resolveDebugItem is debugItem's lookup without the store rewrite.
func resolveDebugItem(c *Ctx, ref string) (root, id string, issueIDs bool, err error) {
	if root, err = backlogScope(c); err != nil {
		return
	}
	be, err := openBacklog(c, root, false, "")
	if err != nil {
		return
	}
	if id, _, err = resolveItem(be, ref); err != nil {
		return
	}
	return root, id, be.Capabilities().IssueIDs, nil
}

// debugKeys maps a legacy issue-mode store key to its canonical identities.
func debugKeys(root string) func(string) []string {
	registered := repos.Load(root)
	return func(key string) []string {
		old, err := backlog.ParseRef(key)
		if err != nil {
			return nil // Unknown legacy keys remain available for inspection.
		}
		n := strconv.Itoa(old.Number)
		if old.Repo != "" {
			return []string{old.Repo + ":" + n}
		}
		if len(registered) == 0 {
			return []string{n}
		}
		// The old store never recorded the repo for a bare ID. Preserve its
		// history for every possible owner, once, before scoped resets.
		ids := make([]string, 0, len(registered))
		for _, repo := range registered {
			ids = append(ids, repo.Name+":"+n)
		}
		return ids
	}
}

// debugReadCount is the halting count of a session's bug_id for a read-only
// verb: nothing is written. When the tracker cannot be read, the stored bug_id
// is canonicalized locally and a warning says so, so the halt note survives.
// A reference that names no item (exit 2 or 3) is still an error.
func debugReadCount(c *Ctx, ref string) (int, error) {
	root, id, issueIDs, err := resolveDebugItem(c, ref)
	if err != nil {
		_, ferr := backlogFailRead(err)
		var e *exitcode.Error
		if errors.As(ferr, &e) && (e.Exit == exitcode.ExitUsage || e.Exit == exitcode.ExitResolution) {
			return 0, err
		}
		if root, err = c.Root(); err != nil {
			return 0, err
		}
		id, issueIDs = ref, true
		if ids := debugKeys(root)(ref); len(ids) == 1 {
			id = ids[0]
		}
		c.Warn("tracker read failed, counting locally for %s: %s", id, strings.Join(strings.Fields(ferr.Error()), " "))
	}
	s := verdict.Load(root)
	if issueIDs {
		verdict.Canonicalize(&s, debugKeys(root))
	}
	return verdict.FailedFixes(s.Items[id]), nil
}
