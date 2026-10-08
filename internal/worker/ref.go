package worker

import (
	"regexp"
	"strconv"
	"strings"
)

// The one grammar for naming a PR and for naming the issue a slot holds.
// round, roundwatch, roundtick and cli call these rather than re-parse.

var (
	rePRRef       = regexp.MustCompile(`^(?:#|.*/(?:pull|merge_requests)/)?(\d+)/?$`)
	reIssueBranch = regexp.MustCompile(`^[^/]+/(\d+)-`)
	reIssueToken  = regexp.MustCompile(`(?:(?:^|[^a-z0-9])issue-(\d+)|#(\d+))`)
)

// PRRefNumber reads the PR number from `#N`, `N` or a PR URL.
func PRRefNumber(ref string) (int, bool) {
	m := rePRRef.FindStringSubmatch(strings.TrimSpace(ref))
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	return n, err == nil
}

// HeldID is the item a slot holds, in the backend's spelling: the task when
// set (`#12`, `12`, `B07`), else the number leading `<agent>/<issue>-<slug>`.
// A parked slot (`park/<name>`) with no task holds none.
func HeldID(task, branch, name string) string {
	if t := strings.TrimPrefix(strings.TrimSpace(task), "#"); t != "" {
		return strings.ToUpper(t)
	}
	if branch == "park/"+name {
		return ""
	}
	if m := reIssueBranch.FindStringSubmatch(branch); m != nil {
		return m[1]
	}
	return ""
}

// BranchIssue is the issue number leading a `<agent>/<issue>-<slug>` branch.
func BranchIssue(branch string) (int, bool) {
	m := reIssueBranch.FindStringSubmatch(branch)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	return n, err == nil
}

// HeldID is the item the slot holds ("" when none).
func (s *Slot) HeldID() string { return HeldID(s.Task(), s.Branch(), s.Name()) }

// Slot states that wait on the orchestrator rather than on the worker.
var attention = map[string]bool{"blocked": true, "needs-permission": true, "limited": true, "dead": true, "unknown": true}

// NeedsAttention reports whether a recorded slot state waits on the
// orchestrator. done is not one: the autopilot (roundtick) merges a done
// slot's PR itself, so it never needs a human.
func NeedsAttention(state string) bool { return attention[strings.ToLower(state)] }

// NeedsWake is NeedsAttention plus done: `round wait` and the prompt hook
// return on a finished slot so the orchestrator can review its PR.
func NeedsWake(state string) bool {
	return strings.EqualFold(state, "done") || NeedsAttention(state)
}

// prNumText is PRRefNumber as text, "" when ref names no PR.
func prNumText(ref string) string {
	if n, ok := PRRefNumber(ref); ok {
		return strconv.Itoa(n)
	}
	return ""
}

// RoundBranch reports whether name has the `<agent>/<issue>-<slug>` shape a
// round cuts for a worker.
func RoundBranch(name string) bool { return reIssueBranch.MatchString(name) }

// IssueFromBranch is the issue number a branch name carries: the number leading
// `<agent>/<issue>-<slug>`, else an `issue-N` or `#N` token. "" when none.
func IssueFromBranch(name string) string {
	if m := reIssueBranch.FindStringSubmatch(name); m != nil {
		return m[1]
	}
	if m := reIssueToken.FindStringSubmatch(name); m != nil {
		return firstNonEmptyStr(m[1], m[2])
	}
	return ""
}

func firstNonEmptyStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
