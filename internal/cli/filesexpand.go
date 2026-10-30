package cli

import (
	"strings"

	"github.com/l4ci/rota/internal/shlex"
	"github.com/l4ci/rota/internal/worker"
)

const filesPlaceholder = "{files}"

func anyContains(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// expandFiles owns the {files} token for the worker's own checks (the gate
// and train expand test.small's in internal/worker/filesexpand.go). Each element of cmds
// is one command (its arguments; a single argument is a shell line) and comes
// back as the shell line that runs and that proof records. {files} becomes the
// shell-quoted changed files of dir against base. git is asked once, and only
// when some command uses {files}.
//
// The changed set is the working tree against the merge-base of base and HEAD:
// committed and uncommitted changes plus untracked files git does not ignore,
// minus deletions (changedFiles). It is deliberately wider than the merge
// gate's set (worker.gate changedFiles, the committed range base...head): the
// verbs check what the worker holds right now, including work not yet
// committed, while the gate judges only what the merge would land.
func expandFiles(c *Ctx, dir, base string, cmds ...[]string) ([]string, error) {
	list := ""
	for _, args := range cmds {
		if !anyContains(args, filesPlaceholder) {
			continue
		}
		files, err := changedFiles(c, dir, base)
		if err != nil {
			return nil, err
		}
		quoted := make([]string, len(files))
		for i, f := range files {
			quoted[i] = shlex.QuoteAlways(f)
		}
		list = strings.Join(quoted, " ")
		break
	}
	out := make([]string, len(cmds))
	for i, args := range cmds {
		out[i] = recordedCommand(args, list)
	}
	return out, nil
}

// shellLines wraps each command as the single-argument form expandFiles takes:
// one argument is a shell line, not a program name.
func shellLines(cmds []string) [][]string {
	out := make([][]string, len(cmds))
	for i, cmd := range cmds {
		out[i] = []string{cmd}
	}
	return out
}

// recordedCommand is the shell line that ran: one argument is a shell line
// already; several are quoted one by one. {files} expands to list, which is
// already quoted, and stays unquoted when it is a whole argument.
func recordedCommand(args []string, list string) string {
	if len(args) == 1 {
		return strings.ReplaceAll(args[0], filesPlaceholder, list)
	}
	parts := make([]string, 0, len(args))
	for _, a := range args {
		switch {
		case a == filesPlaceholder:
			if list != "" {
				parts = append(parts, list)
			}
		default:
			parts = append(parts, shlex.QuoteAlways(strings.ReplaceAll(a, filesPlaceholder, list)))
		}
	}
	return strings.Join(parts, " ")
}

// fastTierForms are the two rows `proof record -- rota test run fast` can store:
// the one-line spelling and the multi-argument one, quoted per argument.
func fastTierForms() []string {
	return []string{
		recordedCommand([]string{worker.FastTierCheck}, ""),
		recordedCommand(strings.Fields(worker.FastTierCheck), ""),
	}
}
