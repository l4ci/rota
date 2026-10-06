package cli

import (
	"strconv"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/repos"
	"github.com/l4ci/rota/internal/verdict"
)

// debugItem uses the same canonical identity and scope as other item verbs.
// File IDs remain literal. Issue-mode legacy records are consolidated before
// any count is checked or changed, including a counter's old raw bug_id.
func debugItem(c *Ctx, ref string) (string, error) {
	root, err := backlogScope(c)
	if err != nil {
		return "", err
	}
	be, err := openBacklog(c, root, false, "")
	if err != nil {
		return "", err
	}
	id, _, err := resolveItem(be, ref)
	if err != nil {
		return "", err
	}
	if !be.Capabilities().Tracker {
		return id, nil
	}
	registered := repos.Load(root)
	err = verdict.CanonicalizeItems(root, func(key string) []string {
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
	})
	return id, err
}
