package initproj

import (
	"regexp"
	"strings"
)

// ignoreLines is the managed block: most of .rota/ travels with the repo, so only
// machine-specific or regenerated paths are ignored.
var ignoreLines = []string{
	"# ── rota ──",
	".rota/status.json",
	".rota/repos.json",
	".rota/config.local.json",
	".rota/handoff/",
	".rota/review/",
	".rota/qa-runs/",
	".rota/verdicts.json",
	".rota/gate-audit.jsonl",
	".rota/ledger.jsonl",
	".rota/train-cache.json",
	".rota/workers.json",
	".rota/**/*.lock",
}

const worktreesBlock = "\n# Worker worktrees (rota worker pool, parallel rounds)\n.worktrees/\n"

var worktreesRe = regexp.MustCompile(`(?m)^/?\.worktrees/?$`)

func hasLine(text, line string) bool {
	for _, l := range strings.Split(text, "\n") {
		if l == line {
			return true
		}
	}
	return false
}

// MergeGitignore is hv-bootstrap's .gitignore handling on the file's current
// text (exists false: no file yet). Like the grep -x and printf the shell used
// it works on raw bytes, so a CRLF line does not match the managed lines.
//
//  1. a missing file gets the managed block;
//  2. an existing one gets the whole block appended when any line but the
//     header is missing;
//  3. `.worktrees/` is added unless an equivalent line exists: any spelling git
//     treats the same, with or without a slash and with CRLF line ends.
func MergeGitignore(cur string, exists bool) string {
	var out string
	if !exists {
		out = strings.Join(ignoreLines, "\n") + "\n"
	} else {
		out = cur
		missing := false
		for _, l := range ignoreLines[1:] {
			if !hasLine(out, l) {
				missing = true
			}
		}
		if missing {
			out += "\n" + strings.Join(ignoreLines, "\n") + "\n"
		}
	}
	if !worktreesRe.MatchString(strings.ReplaceAll(out, "\r", "")) {
		out += worktreesBlock
	}
	return out
}
