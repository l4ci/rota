package worker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/tracker"
)

type acceptanceIssues struct {
	seamIssues
	items    map[tracker.IssueKey]tracker.Issue
	notesErr error
}

func (s *acceptanceIssues) Get(ctx context.Context, key tracker.IssueKey, comments bool) (tracker.Issue, error) {
	if comments && s.notesErr != nil {
		return tracker.Issue{}, s.notesErr
	}
	if is, ok := s.items[key]; ok {
		return is, nil
	}
	return s.seamIssues.Get(ctx, key, comments)
}

func TestCheckClosesUnmetAcceptance(t *testing.T) {
	for _, tc := range []struct {
		name                                          string
		final, closed, met, extra, legacy, unreported bool
		want                                          string
	}{
		{name: "unmet", want: "unmet acceptance"},
		{name: "milestone closure absent from forge refs", unreported: true, want: "unmet acceptance"},
		{name: "final slice", final: true, want: "unmet acceptance"},
		{name: "other closing issue", extra: true, want: "unmet acceptance"},
		{name: "met", met: true},
		{name: "closed with explicit override", closed: true},
		{name: "legacy", legacy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := tracker.NumKey(5)
			body := "Closes #5"
			rep := &seamReporter{nums: []int{5}, known: true}
			if tc.unreported {
				rep.nums = nil
			}
			if tc.extra {
				key = tracker.NumKey(9)
				rep.nums = append(rep.nums, 9)
				body += "\nCloses #9"
			}
			is := tracker.Issue{Key: key, State: "open", Body: "## Acceptance\n- [ ] AC-1: ready\n"}
			if tc.closed {
				is.State = "closed"
			}
			if tc.legacy {
				is.Body = "## Acceptance\n- [ ] ready\n"
			}
			if tc.met {
				is.Comments = []tracker.Comment{
					{ID: "1", Body: "<!-- rota:acceptance -->\n- AC-1 · 2026-10-26 · abc1234 · check · ready\n"},
					{ID: "2", Body: "<!-- rota:proof -->\n## Proof\n- 2026-10-26 · check · PASS · abc1234 · ok\n"},
				}
			}
			issues := &acceptanceIssues{items: map[tracker.IssueKey]tracker.Issue{key: is}}
			out := checkCloses(context.Background(), closesInput{Slot: "kit", PR: "https://github.com/o/r/pull/12", Keys: keys(5), Source: tracker.ClosingSource{Body: body}, Issues: issues, Forge: rep, FinalSlice: tc.final})
			if out.Verdict != tc.want || out.Broke != "" {
				t.Fatalf("outcome %+v, want verdict %q", out, tc.want)
			}
			if tc.want != "" && !strings.Contains(out.Msg, "AC-1: ready") {
				t.Fatalf("missing unmet criterion: %+v", out)
			}
		})
	}
}

func TestCheckClosesUnmetKeyAcceptance(t *testing.T) {
	key := tracker.IssueKey("ENG-1")
	issues := &acceptanceIssues{items: map[tracker.IssueKey]tracker.Issue{key: {Key: key, State: "open", Body: "## Acceptance\n- [ ] AC-1: ready\n"}}}
	out := checkCloses(context.Background(), closesInput{Slot: "kit", PR: "https://github.com/o/r/pull/12", Keys: []tracker.IssueKey{key}, Source: tracker.ClosingSource{Body: "Closes ENG-1"}, Issues: issues})
	if out.Verdict != "unmet acceptance" {
		t.Fatalf("keyed item outcome %+v, want unmet acceptance", out)
	}
}

func TestCheckClosesAcceptanceNotesReadFailure(t *testing.T) {
	key := tracker.NumKey(5)
	issues := &acceptanceIssues{notesErr: errors.New("notes unavailable"), items: map[tracker.IssueKey]tracker.Issue{key: {Key: key, State: "open", Body: "## Acceptance\n- [ ] AC-1: ready\n"}}}
	out := checkCloses(context.Background(), closesInput{Slot: "kit", PR: "https://github.com/o/r/pull/12", Keys: []tracker.IssueKey{key}, Source: tracker.ClosingSource{Body: "Closes #5"}, Issues: issues})
	if !strings.Contains(out.Broke, "could not read acceptance notes") || !strings.Contains(out.Broke, "notes unavailable") {
		t.Fatalf("note read failure was ignored: %+v", out)
	}
}
