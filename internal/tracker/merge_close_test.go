package tracker

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/tracker/itsaplantest"
	"github.com/l4ci/rota/internal/tracker/jiratest"
	"github.com/l4ci/rota/internal/tracker/lineartest"
)

// Exercise the real adapters through their HTTP fakes, so native status reads
// and close writes both participate in the close-on-merge contract.
func TestCloseAfterMerge(t *testing.T) {
	type mergeTracker interface {
		IssueTracker
		MergeCloser
	}
	type fixture struct {
		tracker  mergeTracker
		missing  IssueKey
		requests func() []string
		isWrite  func(string) bool
	}
	restWrite := func(request string) bool { return !strings.HasPrefix(request, "GET ") }
	adapters := []struct {
		name string
		new  func(*testing.T) fixture
	}{
		{"Linear", func(t *testing.T) fixture {
			l, s := fakeLinear(t, lineartest.Options{})
			return fixture{l, "ENG-999", s.Requests, func(op string) bool {
				doc, _ := s.Last(op)
				return strings.HasPrefix(doc, "mutation ")
			}}
		}},
		{"Itsaplan", func(t *testing.T) fixture {
			it, s := fakeItsaplan(t, itsaplantest.Options{})
			return fixture{it, "MKT-999", s.Requests, restWrite}
		}},
	}
	for _, version := range jiraVersions {
		adapters = append(adapters, struct {
			name string
			new  func(*testing.T) fixture
		}{fmt.Sprintf("Jira/v%d", version), func(t *testing.T) fixture {
			j, s := fakeJira(t, jiratest.Options{Version: version})
			return fixture{j, IssueKey(s.Project() + "-999"), s.Requests, restWrite}
		}})
	}
	ctx := context.Background()
	for _, adapter := range adapters {
		t.Run(adapter.name, func(t *testing.T) {
			for _, state := range []string{"open", CloseCompleted, CloseNotPlanned} {
				t.Run(state, func(t *testing.T) {
					f := adapter.new(t)
					key, err := f.tracker.Create(ctx, "merge me", "", nil, "")
					if err != nil {
						t.Fatal(err)
					}
					if state != "open" {
						if err := f.tracker.Close(ctx, key, state, ""); err != nil {
							t.Fatal(err)
						}
					}
					before := len(f.requests())
					if err := f.tracker.CloseAfterMerge(ctx, key, "https://example.test/pr/9"); err != nil {
						t.Fatal(err)
					}
					if state != "open" {
						for _, request := range f.requests()[before:] {
							if f.isWrite(request) {
								t.Errorf("already closed issue was written to: %s", request)
							}
						}
					}
					issue, err := f.tracker.Get(ctx, key, true)
					if err != nil {
						t.Fatal(err)
					}
					if issue.State != "closed" {
						t.Errorf("state = %q, want closed", issue.State)
					}
					if state == "open" {
						if issue.StateReason != CloseCompleted || len(issue.Comments) != 1 || issue.Comments[0].Body != "Closed by rota: https://example.test/pr/9 merged." {
							t.Errorf("closed issue: reason %q, comments %+v", issue.StateReason, issue.Comments)
						}
					} else if len(issue.Comments) != 0 {
						t.Errorf("already closed issue gained comments: %+v", issue.Comments)
					}
				})
			}
			t.Run("missing", func(t *testing.T) {
				f := adapter.new(t)
				if err := f.tracker.CloseAfterMerge(ctx, f.missing, "PR #9"); !IsKind(err, KindNotFound) {
					t.Errorf("missing issue: %v, want not found", err)
				}
				for _, request := range f.requests() {
					if f.isWrite(request) {
						t.Errorf("failed read wrote: %s", request)
					}
				}
			})
		})
	}
}
