package initproj

import (
	"strings"

	"github.com/l4ci/rota/internal/rotatree"
)

const (
	rotaHeader      = "# ── rota ──"
	umbrellaHeader  = "# ── rota umbrella ──"
	worktreesHeader = "# Worker worktrees (rota worker pool, parallel rounds)"
)

func rel(parts ...string) string { return rotatree.Rel(parts...) }
func relDir(name string) string  { return rotatree.Rel(name) + "/" }

// ignoreLines is the managed block: most of .rota/ travels with the repo, so only
// machine-specific or regenerated paths are ignored. The header is first.
var ignoreLines = []string{
	rotaHeader,
	rel(rotatree.StatusFile),
	rel(rotatree.ReposFile),
	rel(rotatree.ConfigLocalFile),
	relDir(rotatree.HandoffDir),
	relDir(rotatree.DebugDir),
	relDir(rotatree.ReviewDir),
	relDir(rotatree.QARunsDir),
	rel(rotatree.VerdictsFile),
	rel(rotatree.GateAuditFile),
	rel(rotatree.LedgerFile),
	rel(rotatree.FlakesFile),
	rel(rotatree.BugReportsFile),
	rel(rotatree.TrainCacheFile),
	rel(rotatree.WorkersFile),
	rel(rotatree.ReceiptsFile),
	rel("**", rotatree.KnowledgeHits+"*"),
	rel("**", "*.lock"),
	rel("**", "*.proof"),
	".codex/",
}

// worktreesLine keeps slot worktrees out of git.
const worktreesLine = rotatree.WorktreeRoot + "/"

// entryKey is the line-matching rule: CR dropped (CRLF files), and every
// spelling git treats as the root worktrees directory folded into one.
func entryKey(line string) string {
	line = strings.TrimRight(line, "\r")
	if strings.Trim(line, "/") == rotatree.WorktreeRoot {
		return worktreesLine
	}
	return line
}

// mergeIgnore is the one .gitignore merge: it adds the wanted lines the text
// lacks (exists false: no file yet). Missing lines go directly under the first
// header line when the text has one, else a header block is appended. Existing
// lines are never rewritten; new ones use the file's line ends.
func mergeIgnore(text string, exists bool, header string, wanted []string) string {
	have := map[string]bool{}
	lines := strings.Split(text, "\n")
	for _, l := range lines {
		have[entryKey(l)] = true
	}
	var missing []string
	for _, w := range wanted {
		if !have[entryKey(w)] {
			missing = append(missing, w)
		}
	}
	if len(missing) == 0 {
		return text
	}
	eol := "\n"
	if strings.Contains(text, "\r\n") {
		eol = "\r\n"
	}
	for i, l := range lines {
		if strings.TrimRight(l, "\r") == header {
			ins := append([]string{}, lines[:i+1]...)
			for _, m := range missing {
				ins = append(ins, m+strings.TrimSuffix(eol, "\n"))
			}
			return strings.Join(append(ins, lines[i+1:]...), "\n")
		}
	}
	if !exists {
		text = ""
	}
	var b strings.Builder
	b.WriteString(text)
	if text != "" {
		if !strings.HasSuffix(text, "\n") {
			b.WriteString(eol)
		}
		if last := strings.TrimSuffix(strings.TrimSuffix(text, "\n"), "\r"); !strings.HasSuffix(last, "\n") && last != "" {
			b.WriteString(eol)
		}
	}
	b.WriteString(header + eol + strings.Join(missing, eol) + eol)
	return b.String()
}

// MergeGitignore is the project init's .gitignore handling: the managed block,
// then the worktrees directory under its own header.
func MergeGitignore(cur string, exists bool) string {
	out := mergeIgnore(cur, exists, rotaHeader, ignoreLines[1:])
	return mergeIgnore(out, true, worktreesHeader, []string{worktreesLine})
}

// MissingIgnoreLines are the managed ignore lines (the header aside) that the
// .gitignore text lacks.
func MissingIgnoreLines(text string) []string {
	have := map[string]bool{}
	for _, l := range strings.Split(text, "\n") {
		have[entryKey(l)] = true
	}
	var missing []string
	for _, l := range ignoreLines[1:] {
		if !have[entryKey(l)] {
			missing = append(missing, l)
		}
	}
	return missing
}
