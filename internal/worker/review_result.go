package worker

import (
	"regexp"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
)

// ReviewResult is completion evidence for an architecture review. Its zero
// value is missing evidence; only an explicit issues:none completes an empty
// review. Construction, persistence and close notes share this contract.
type ReviewResult struct {
	complete bool
	issues   []string
}

var (
	reIssuesDone = regexp.MustCompile(`^issues:\s*(?:#?\d+|[A-Z][A-Z0-9_]*-\d+)(?:\s*,\s*(?:#?\d+|[A-Z][A-Z0-9_]*-\d+))*$`)
	reIssueRef   = regexp.MustCompile(`#?\d+|[A-Z][A-Z0-9_]*-\d+`)
)

// ParseReviewResult accepts the worker's issues:#a,#b or issues:none evidence.
// Malformed or absent evidence returns the missing result, never no findings.
func ParseReviewResult(evidence string) ReviewResult {
	ev := strings.TrimSpace(evidence)
	if ev == "issues:none" {
		return ReviewResult{complete: true}
	}
	if !reIssuesDone.MatchString(ev) {
		return ReviewResult{}
	}
	refs := reIssueRef.FindAllString(strings.TrimPrefix(ev, "issues:"), -1)
	for i, ref := range refs {
		if !strings.Contains(ref, "-") {
			refs[i] = "#" + strings.TrimPrefix(ref, "#")
		}
	}
	return ReviewResult{complete: true, issues: refs}
}

func (r ReviewResult) Complete() bool   { return r.complete }
func (r ReviewResult) NoFindings() bool { return r.complete && len(r.issues) == 0 }
func (r ReviewResult) Issues() []string { return slices.Clone(r.issues) }

// Equal compares completion evidence, including the completed-empty distinction.
func (r ReviewResult) Equal(other ReviewResult) bool {
	return r.complete == other.complete && slices.Equal(r.issues, other.issues)
}

// Note is the default close note. Missing evidence has no close note.
func (r ReviewResult) Note() string {
	if !r.complete {
		return ""
	}
	if r.NoFindings() {
		return "no findings"
	}
	return "filed " + strings.Join(r.issues, ", ")
}

// ReviewResult reads old issue-list records as well as explicitly empty ones.
func (s *Slot) ReviewResult() ReviewResult {
	if refs := s.Issues(); len(refs) > 0 {
		return ParseReviewResult("issues:" + strings.Join(refs, ","))
	}
	if jsonx.Bool(s.o, "reviewNoFindings") {
		return ParseReviewResult("issues:none")
	}
	return ReviewResult{}
}

// SetReviewResult writes the evidence atomically with the slot's state. The
// old nonempty issues field stays compatible; absence still means missing.
func (s *Slot) SetReviewResult(r ReviewResult) {
	setStrList(s.o, "issues", r.issues)
	s.o.Delete("reviewNoFindings")
	if r.NoFindings() {
		s.o.Set("reviewNoFindings", true)
	}
}
