package ship

import (
	"regexp"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/proof"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/strutil"
)

var issueRe = regexp.MustCompile(`(?:GH|GL):[` + pystr.SpaceClass + `]*#(\p{Nd}+)`)

// TitleOf returns an item's origin line and title, ok false when unknown.
type TitleOf func(id string) (line, title string, ok bool)

// CorpusTitles is the TitleOf of a backlog file's text.
func CorpusTitles(corpus string) TitleOf {
	return func(id string) (string, string, bool) { return backlog.FindOrigin(corpus, id) }
}

// ProofOf returns an item's proof rows.
type ProofOf func(id string) ([]proof.Row, error)

// NoCommitsError is a branch with nothing past its base.
type NoCommitsError struct{ Branch string }

func (e *NoCommitsError) Error() string { return "no commits between base and " + e.Branch }

// Body builds the PR body of branch from the commits past base: a summary
// bullet per subject and, for every item ID the commits name, the item's title
// and the `Closes #n` lines its origin line carries. titles opens the lookup
// (backlog file or tracker) and is only called when a commit names an item.
// proofs, when non-nil, adds an "## Evidence" section from those items' proof
// rows; an item whose rows cannot be read is left out, the body being advisory.
func Body(g Git, base, branch string, titles func() (TitleOf, error), proofs ProofOf) (string, error) {
	rng := base + ".." + branch
	subj, err := g.Run("log", "--no-merges", "--format=%s", rng)
	if err != nil {
		return "", err
	}
	full, err := g.Run("log", "--no-merges", "--format=%B", rng)
	if err != nil {
		return "", err
	}
	if subj.ExitCode != 0 || full.ExitCode != 0 {
		return "", &GitError{Msg: "git log " + rng + ": " + strutil.FirstLine(subj.Stderr+full.Stderr)}
	}
	var subjects []string
	for _, l := range pystr.Splitlines(subj.Stdout) {
		if pystr.Strip(l) != "" {
			subjects = append(subjects, pystr.Strip(l))
		}
	}
	if len(subjects) == 0 {
		return "", &NoCommitsError{Branch: branch}
	}
	var b strings.Builder
	b.WriteString("## Summary\n\n")
	for _, s := range subjects {
		b.WriteString("- " + s + "\n")
	}
	b.WriteString("\n")
	if ids := backlog.FindItemIDs(full.Stdout, ""); len(ids) > 0 {
		titleOf, err := titles()
		if err != nil {
			return "", err
		}
		origin := map[string]string{}
		b.WriteString("## Items resolved\n\n")
		for _, id := range ids {
			line, title, ok := titleOf(id)
			if ok {
				origin[id] = line
			}
			if ok && title != "" {
				b.WriteString("- [" + id + "] " + title + "\n")
			} else {
				b.WriteString("- [" + id + "]\n")
			}
		}
		b.WriteString("\n")
		seen := map[string]bool{}
		var closes []string
		for _, id := range ids {
			for _, m := range issueRe.FindAllStringSubmatch(origin[id], -1) {
				if !seen[m[1]] {
					seen[m[1]] = true
					closes = append(closes, m[1])
				}
			}
		}
		if len(closes) > 0 {
			for _, n := range closes {
				b.WriteString("Closes #" + n + "\n")
			}
			b.WriteString("\n")
		}
	}
	if ids := backlog.FindItemIDs(full.Stdout, ""); len(ids) > 0 && proofs != nil {
		writeEvidence(&b, ids, proofs)
	}
	return b.String(), nil
}

// writeEvidence adds the "## Evidence" section: a check/result/evidence table
// per item that has proof rows, nothing when none does.
func writeEvidence(b *strings.Builder, ids []string, proofs ProofOf) {
	var sec strings.Builder
	for _, id := range ids {
		rows, err := proofs(id)
		if err != nil || len(rows) == 0 {
			continue
		}
		sec.WriteString("**[" + id + "]**\n\n| Check | Result | Evidence |\n| --- | --- | --- |\n")
		for _, r := range rows {
			sec.WriteString("| " + cell(r.Check) + " | " + cell(r.Result) + " | " + cell(r.Evidence) + " |\n")
		}
		sec.WriteString("\n")
	}
	if sec.Len() > 0 {
		b.WriteString("## Evidence\n\n" + sec.String())
	}
}

// cell escapes a table cell: a pipe would split it.
func cell(s string) string { return strings.ReplaceAll(s, "|", "\\|") }
