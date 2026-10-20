package worker

import (
	"github.com/l4ci/rota/internal/strutil"
	"regexp"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/tracker"
)

// The one grammar for naming a PR and for naming the issue a slot holds.
// round, roundwatch, roundtick and cli call these rather than re-parse.

var (
	reIssueToken = regexp.MustCompile(`(?:(?:^|[^a-z0-9])issue-(\d+)|#(\d+))`)
)

// PRRefNumber reads the PR number from `#N`, `N` or a PR URL.
func PRRefNumber(ref string) (int, bool) {
	ref = strings.TrimSpace(ref)
	if p, ok := tracker.ParsePRURL(ref); ok {
		return p.Number, true
	}
	digits := strings.TrimPrefix(strings.TrimSuffix(ref, "/"), "#")
	if digits == "" || strings.Trim(digits, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.Atoi(digits)
	return n, err == nil
}

// NormID is the one spelling item IDs are compared and keyed by: trimmed, no
// leading `#`, upper case. `#12`, ` 12 `, `b07` and `B07` normalise to `12`,
// `12`, `B07` and `B07`. Never hand-roll this at a call site.
func NormID(id string) string {
	return tracker.NormRef(id)
}

// HeldID is the item a slot holds, in the backend's spelling: the task when
// set (`#12`, `12`, `B07`), else the number leading `<agent>/<issue>-<slug>`.
// A parked slot (`park/<name>`) with no task holds none.
func HeldID(task, branch, name string) string {
	if t := NormID(task); t != "" {
		return t
	}
	if IsOwnPark(branch, name) {
		return ""
	}
	id, _ := branchIssue(branch)
	return id
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

// IssueFromBranch is the issue number a branch name carries: the number leading
// `<agent>/<issue>-<slug>`, else an `issue-N` or `#N` token. "" when none.
func IssueFromBranch(name string) string {
	if id, ok := branchIssue(name); ok {
		return id
	}
	if m := reIssueToken.FindStringSubmatch(name); m != nil {
		return strutil.FirstNonEmpty(m[1], m[2])
	}
	return ""
}
