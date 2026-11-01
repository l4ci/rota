package proof

import (
	"strings"

	"github.com/l4ci/rota/internal/shlex"
)

const filesToken = "{files}"

// HasFiles reports whether a shell line or any argument uses {files}. Callers
// choose and collect their own changed-file set, only when this is true.
func HasFiles(cmds []string) bool {
	for _, cmd := range cmds {
		if strings.Contains(cmd, filesToken) {
			return true
		}
	}
	return false
}

// Commands spells commands as the shell lines that run and that proof rows
// store. One argument is already a shell line; several are quoted separately.
// Every {files} expands to the supplied paths, each single-quoted. A whole
// {files} argument stays a list (and disappears for an empty file set).
// Selecting the files belongs to the caller, not to proof.
func Commands(files []string, cmds ...[]string) []string {
	quoted := make([]string, len(files))
	for i, file := range files {
		quoted[i] = shlex.QuoteAlways(file)
	}
	list := strings.Join(quoted, " ")
	out := make([]string, len(cmds))
	for i, args := range cmds {
		out[i] = recordedCommand(args, list)
	}
	return out
}

// ShellLines wraps each line as one command argument, preserving its shell
// syntax when passed to Commands.
func ShellLines(cmds []string) [][]string {
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
		return strings.ReplaceAll(args[0], filesToken, list)
	}
	parts := make([]string, 0, len(args))
	for _, a := range args {
		switch {
		case a == filesToken:
			if list != "" {
				parts = append(parts, list)
			}
		default:
			parts = append(parts, shlex.QuoteAlways(strings.ReplaceAll(a, filesToken, list)))
		}
	}
	return strings.Join(parts, " ")
}

// FastTierCheck is the check `rota proof record <ID> -- rota test run fast`
// writes: the tier run as one command, which the worker contract recommends.
const FastTierCheck = "rota test run fast"

// MissingFastProof lists the fast commands without a PASS at head, unless a
// row proves the whole tier. Both record spellings of the tier are derived
// here, beside the command spelling used to write them. The latest row for
// each check decides; FAIL rows and rows at another sha prove nothing.
func MissingFastProof(rows []Row, head string, want []string) (missing []string) {
	for _, form := range Commands(nil, []string{FastTierCheck}, strings.Fields(FastTierCheck)) {
		if Proves(rows, head, form) {
			return nil
		}
	}
	for _, check := range want {
		if !Proves(rows, head, check) {
			missing = append(missing, check)
		}
	}
	return missing
}
