package worker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/tracker"
)

// seamIssues is an in-memory Issues: labels and state by number, a body-keyword
// closing convention, and the label removals it was asked for.
type seamIssues struct {
	labels  map[int][]string
	closed  map[int]bool
	getErr  error
	removed map[int][]string
}

func (s *seamIssues) Get(_ context.Context, key tracker.IssueKey, _ bool) (tracker.Issue, error) {
	n, _ := key.Number()
	if s.getErr != nil {
		return tracker.Issue{}, s.getErr
	}
	state := "open"
	if s.closed[n] {
		state = "closed"
	}
	return tracker.Issue{Key: key, State: state, Labels: s.labels[n]}, nil
}

func (s *seamIssues) RemoveLabels(_ context.Context, key tracker.IssueKey, labels []string) error {
	n, _ := key.Number()
	if s.removed == nil {
		s.removed = map[int][]string{}
	}
	s.removed[n] = append(s.removed[n], labels...)
	return nil
}

func (s *seamIssues) ClosesIssue(key tracker.IssueKey, c tracker.ClosingSource) bool {
	return strings.Contains(c.Body, "Closes "+key.Ref())
}

type seamReporter struct {
	nums  []int
	known bool
	err   error
	asked []int
}

func (r *seamReporter) ClosingRefs(_ context.Context, n int) ([]int, bool, error) {
	r.asked = append(r.asked, n)
	return r.nums, r.known, r.err
}

func keys(ns ...int) []tracker.IssueKey {
	out := make([]tracker.IssueKey, len(ns))
	for i, n := range ns {
		out[i] = tracker.NumKey(n)
	}
	return out
}

func TestCheckCloses(t *testing.T) {
	partial := []string{"in-progress", PartialSliceLabel}
	cases := []struct {
		name    string
		body    string
		keys    []tracker.IssueKey
		labels  map[int][]string
		closed  map[int]bool
		rep     *seamReporter
		final   bool
		verdict string
		inErr   []string // substrings of Msg/Hint
		notes   int
	}{
		{name: "body closes the issue", body: "Closes #5", keys: keys(5)},
		{name: "body leaves the issue open", body: "Refs #5", keys: keys(5), verdict: GateNotClosing, inErr: []string{"does not close #5", "Closes #5"}},
		{name: "partial-slice label excuses the body", body: "Refs #5", keys: keys(5), labels: map[int][]string{5: partial}, notes: 1},
		{name: "bundle names every open member", body: "Closes #5", keys: keys(5, 6, 7), verdict: GateNotClosing, inErr: []string{"#6, #7", "the lines"}},
		{name: "closed member left the bundle", body: "Closes #5", keys: keys(5, 6), closed: map[int]bool{6: true}},
		{name: "no issue, nothing to check", body: "", keys: nil},
		{name: "forge says it closes a partial-slice issue", body: "Closes #5", keys: keys(5), labels: map[int][]string{5: partial},
			rep: &seamReporter{nums: []int{5}, known: true}, verdict: GateNotClosing, inErr: []string{"slices still to come", "--final-slice"}},
		{name: "final slice may close it", body: "Closes #5", keys: keys(5), labels: map[int][]string{5: partial},
			rep: &seamReporter{nums: []int{5}, known: true}, final: true},
		{name: "forge lists another issue", body: "Closes #5", keys: keys(5), labels: map[int][]string{5: partial},
			rep: &seamReporter{nums: []int{6}, known: true}},
		{name: "forge lists a partial-slice parent beyond the slot", body: "Closes #5\nCloses #9", keys: keys(5), labels: map[int][]string{9: partial},
			rep: &seamReporter{nums: []int{5, 9}, known: true}, verdict: GateNotClosing, inErr: []string{"#9"}},
		{name: "forge cannot report: noted once", body: "Closes #5", keys: keys(5), labels: map[int][]string{5: partial},
			rep: &seamReporter{}, notes: 1},
		{name: "forge errors: noted once", body: "Closes #5", keys: keys(5), labels: map[int][]string{5: partial},
			rep: &seamReporter{err: errors.New("boom")}, notes: 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := closesInput{Slot: "w1", PR: "https://github.com/o/r/pull/12", Keys: c.keys, FinalSlice: c.final,
				Issues: &seamIssues{labels: c.labels, closed: c.closed}, Source: tracker.ClosingSource{Body: c.body}}
			if c.rep != nil {
				in.Forge = c.rep
			}
			out := checkCloses(context.Background(), in)
			if out.Verdict != c.verdict || out.Broke != "" {
				t.Fatalf("outcome = %+v, want verdict %q", out, c.verdict)
			}
			for _, want := range c.inErr {
				if !strings.Contains(out.Msg+out.Hint, want) {
					t.Errorf("%q missing from %q / %q", want, out.Msg, out.Hint)
				}
			}
			if len(out.Notes) != c.notes {
				t.Errorf("notes = %q, want %d", out.Notes, c.notes)
			}
		})
	}
}

// A PR ref the slice-close check cannot parse is a broken check naming the ref,
// not a closing-refs query for PR 0.
func TestCheckClosesNamesAnUnparsablePRRef(t *testing.T) {
	rep := &seamReporter{}
	out := checkCloses(context.Background(), closesInput{Slot: "w1", PR: "not-a-pr", Keys: keys(5), Issues: &seamIssues{}, Forge: rep})
	if !strings.Contains(out.Broke, "not-a-pr") {
		t.Fatalf("outcome = %+v, want a broken check naming the ref", out)
	}
	if len(rep.asked) != 0 {
		t.Errorf("closing refs asked for PR %v despite the unparsable ref", rep.asked)
	}
}

func TestCheckClosesBreaksOnAnUnreadableIssue(t *testing.T) {
	out := checkCloses(context.Background(), closesInput{Slot: "w1", PR: "https://github.com/o/r/pull/12", Keys: keys(5),
		Issues: &seamIssues{getErr: errors.New("403")}, Source: tracker.ClosingSource{Body: "Refs #5"}})
	if !strings.Contains(out.Broke, "#5") || !strings.Contains(out.Broke, "403") {
		t.Fatalf("outcome = %+v, want a broken check naming #5", out)
	}
}

func TestReleaseClaimLabelsNotesAnUnreadableIssue(t *testing.T) {
	notes := releaseClaimLabels(context.Background(), releaseInput{Slot: "w1", Label: "in-progress", Keys: keys(5), Issues: &seamIssues{getErr: errors.New("gone")}})
	if len(notes) != 1 || !strings.Contains(notes[0], "LABEL-KEPT") {
		t.Fatalf("notes = %q, want one LABEL-KEPT", notes)
	}
	if got := releaseClaimLabels(context.Background(), releaseInput{Slot: "w1", Keys: keys(5), Issues: &seamIssues{}}); got != nil {
		t.Errorf("no label configured must release nothing: %q", got)
	}
}
