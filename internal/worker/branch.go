package worker

import (
	"regexp"
	"strings"

	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/verdict"
)

// The one owner of the `<agent>/<issue>-<slug>` branch grammar a round cuts for
// an issue: BranchName writes it, branchIssue (and so BranchIssue, RoundBranch,
// HeldID, IssueFromBranch) parse it. Change one half and branch_test.go's
// round-trip fails.

var (
	slugNonWord = regexp.MustCompile(`[^a-z0-9]+`)
	// reIssueBranch: the issue is a number or a tracker key (PROJ-123, upper
	// case, as BranchName spells it).
	reIssueBranch = regexp.MustCompile(`^[^/]+/(\d+|[A-Z][A-Z0-9_]*-\d+)-`)
)

// BranchName is `<agent>/<issue>-<slug>`: the issue number or tracker key
// (PROJ-123, kept upper case; file mode: the lowercased ID), then the title
// lowercased with non-alphanumerics collapsed to `-`, cut to five words and 40
// characters.
func BranchName(agent, id, title string) string {
	slug := strings.Trim(slugNonWord.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if words := strings.Split(slug, "-"); len(words) > 5 {
		slug = strings.Join(words[:5], "-")
	}
	if len(slug) > 40 {
		slug = strings.TrimRight(slug[:40], "-")
	}
	name := agent + "/" + branchID(id)
	if slug != "" {
		name += "-" + slug
	}
	return name
}

// branchID is the issue part of a branch name: a tracker key keeps its case so
// the key reads in the branch as the tracker spells it, anything else is
// lowercased.
func branchID(id string) string {
	if k, ok := tracker.ParseKey(id); ok {
		if _, numbered := k.Number(); !numbered {
			return string(k)
		}
	}
	return strings.ToLower(id)
}

// branchIssue is the issue text leading a `<agent>/<issue>-<slug>` branch.
func branchIssue(branch string) (string, bool) {
	m := reIssueBranch.FindStringSubmatch(branch)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// BranchIssue is the issue key leading a `<agent>/<issue>-<slug>` branch.
func BranchIssue(branch string) (tracker.IssueKey, bool) {
	id, ok := branchIssue(branch)
	if !ok {
		return "", false
	}
	return tracker.ParseKey(id)
}

// BranchHasSpec reports whether branch leads with an issue: its hasSpec.
func BranchHasSpec(branch string) bool {
	id, _ := branchIssue(branch)
	return verdict.HasSpec(id)
}

// RoundBranch reports whether name has the `<agent>/<issue>-<slug>` shape a
// round cuts for a worker.
func RoundBranch(name string) bool {
	_, ok := branchIssue(name)
	return ok
}

// BranchFor is a separate concept from BranchName: the branch `worker reset`
// and task dispatch cut for a slot's task, not the `<agent>/<issue>-<slug>`
// branch a round cuts for an issue's PR. Nothing parses it back, so it shares no
// grammar with BranchName. It is rota-worker/<slot>-<task> with the task
// lowercased and every byte outside [a-z0-9._-] turned into '-' (tr, so a
// multi-byte character becomes several), or rota-worker/<slot> without a task.
// (Not rota-worker/<slot>/<task>: git cannot hold both refs.)
func BranchFor(slot, task string) string {
	if task == "" {
		return "rota-worker/" + slot
	}
	b := []byte(task)
	for i, c := range b {
		switch {
		case c >= 'A' && c <= 'Z':
			b[i] = c + 32
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
		default:
			b[i] = '-'
		}
	}
	return "rota-worker/" + slot + "-" + string(b)
}
