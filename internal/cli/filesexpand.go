package cli

import "github.com/l4ci/rota/internal/proof"

// expandFiles collects the worker's changed paths and delegates spelling and
// substitution to proof. This set includes committed and uncommitted changes
// and untracked, non-ignored files against the merge-base, minus deletions.
// The gate selects its own committed set, including deletions.
func expandFiles(c *Ctx, dir, base string, cmds ...[]string) ([]string, error) {
	var files []string
	for _, args := range cmds {
		if !proof.HasFiles(args) {
			continue
		}
		var err error
		files, err = changedFiles(c, dir, base)
		if err != nil {
			return nil, err
		}
		break
	}
	return proof.Commands(files, cmds...), nil
}
