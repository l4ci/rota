package worker

import (
	"strings"

	"github.com/l4ci/rota/internal/shlex"
)

// filesToken is the {files} placeholder of test.fast and test.small. The CLI
// expands it for the worker's own checks (internal/cli/filesexpand.go); the
// gate and the train expand it here for test.small.
const filesToken = "{files}"

// anyHasFiles reports whether a command uses {files}.
func anyHasFiles(cmds []string) bool {
	for _, c := range cmds {
		if strings.Contains(c, filesToken) {
			return true
		}
	}
	return false
}

// expandTierFiles replaces {files} in each shell line with the single-quoted
// files, the same token the CLI's expandFiles writes.
func expandTierFiles(cmds, files []string) []string {
	quoted := make([]string, len(files))
	for i, f := range files {
		quoted[i] = shlex.QuoteAlways(f)
	}
	list := strings.Join(quoted, " ")
	out := make([]string, len(cmds))
	for i, c := range cmds {
		out[i] = strings.ReplaceAll(c, filesToken, list)
	}
	return out
}
