package ship

import (
	"regexp"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/pystr"
)

var issueRe = regexp.MustCompile(`(?:GH|GL):[` + pystr.SpaceClass + `]*#(\p{Nd}+)`)

// TitleOf returns an item's origin line and title, ok false when unknown.
type TitleOf func(id string) (line, title string, ok bool)

// CorpusTitles is the TitleOf of a backlog file's text.
func CorpusTitles(corpus string) TitleOf {
	return func(id string) (string, string, bool) { return backlog.FindOrigin(corpus, id) }
}

// NoCommitsError is a branch with nothing past its base.
type NoCommitsError struct{ Branch string }

func (e *NoCommitsError) Error() string { return "no commits between base and " + e.Branch }

// Body builds the PR body of branch from the commits past base: a summary
// bullet per subject and, for every item ID the commits name, the item's title
// and the `Closes #n` lines its origin line carries. titles opens the lookup
// (backlog file or tracker) and is only called when a commit names an item.
func Body(g Git, base, branch string, titles func() (TitleOf, error)) (string, error) {
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
		return "", &GitError{Msg: "git log " + rng + ": " + firstLine(subj.Stderr+full.Stderr)}
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
	return b.String(), nil
}
