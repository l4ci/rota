package tracker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/connections"
	"github.com/l4ci/rota/internal/tracker/itsaplantest"
	"github.com/l4ci/rota/internal/tracker/jiratest"
	"github.com/l4ci/rota/internal/tracker/lineartest"
)

// closeOutcome is what a caller can observe of an issue after Close.
type closeOutcome struct {
	closed     bool
	notPlanned bool
	comments   []string
}

// closeFixture is one adapter over a fake whose comment write can be made to
// fail, with a way to read the outcome back.
type closeFixture struct {
	tracker IssueTracker
	key     IssueKey
	failing func(bool) // make the comment write fail (true) or succeed
	outcome func(*testing.T) closeOutcome
}

// failingProxy forwards to target, answering 500 to a request isComment
// recognises while *fail is set. The fakes stay untouched: only the comment
// write goes missing, whatever the adapter's order of calls.
func failingProxy(t *testing.T, target string, fail *bool, isComment func(*http.Request, []byte) bool) *httptest.Server {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	rp := httputil.NewSingleHostReverseProxy(u)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		if *fail && isComment(r, body) {
			http.Error(w, `{"errors":[{"message":"comment refused"}]}`, http.StatusInternalServerError)
			return
		}
		rp.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func outcomeOf(t *testing.T, tr IssueTracker, key IssueKey) closeOutcome {
	t.Helper()
	is, err := tr.Get(context.Background(), key, true)
	if err != nil {
		t.Fatal(err)
	}
	o := closeOutcome{closed: is.State == "closed", notPlanned: is.StateReason == CloseNotPlanned}
	for _, c := range is.Comments {
		o.comments = append(o.comments, c.Body)
	}
	return o
}

func closeFixtures(t *testing.T) map[string]func(*testing.T) closeFixture {
	ctx := context.Background()
	fx := map[string]func(*testing.T) closeFixture{}
	finish := func(t *testing.T, tr IssueTracker, fail *bool) closeFixture {
		key, err := tr.Create(ctx, "close me", "", nil, "")
		if err != nil {
			t.Fatal(err)
		}
		return closeFixture{tr, key, func(v bool) { *fail = v }, func(t *testing.T) closeOutcome { return outcomeOf(t, tr, key) }}
	}
	fx["Linear"] = func(t *testing.T) closeFixture {
		s := lineartest.New(t, lineartest.Options{})
		fail := new(bool)
		p := failingProxy(t, s.URL, fail, func(_ *http.Request, b []byte) bool { return strings.Contains(string(b), "commentCreate") })
		l := NewLinear(connections.Connection{Name: "work", Kind: connections.KindLinear, Site: p.URL}, linearKey, "ENG")
		return finish(t, l, fail)
	}
	fx["Itsaplan"] = func(t *testing.T) closeFixture {
		s := itsaplantest.New(t, itsaplantest.Options{})
		fail := new(bool)
		p := failingProxy(t, s.URL, fail, func(r *http.Request, _ []byte) bool {
			return r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/comments")
		})
		it := NewItsaplan(connections.Connection{Name: "plan", Kind: connections.KindItsaplan, Site: p.URL}, s.Key(), "MKT")
		return finish(t, it, fail)
	}
	for _, version := range jiraVersions {
		fx[fmt.Sprintf("Jira/v%d", version)] = func(t *testing.T) closeFixture {
			s := jiratest.New(t, jiratest.Options{Version: version})
			fail := new(bool)
			p := failingProxy(t, s.URL, fail, func(r *http.Request, _ []byte) bool {
				return r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/comment")
			})
			c := connections.Connection{Name: "work", Kind: connections.KindJira, Site: p.URL, User: "me@example.com", Deployment: connections.DeploymentCloud}
			if version == 2 {
				c.User, c.Deployment = "", connections.DeploymentDataCenter
			}
			j := NewJira(c, "tok", s.Project(), config.Label(nil, "notPlanned"))
			return finish(t, j, fail)
		}
	}
	// gh and glab have no stateful fake: this one answers the calls Close
	// makes and keeps the issue's state, as the forge would.
	cli := func(provider string) func(*testing.T) closeFixture {
		return func(t *testing.T) closeFixture {
			var out closeOutcome
			failComment := false
			f := &fakeCLI{}
			f.answer = func(_ int, args []string) (string, string, int) {
				joined := strings.Join(args, " ")
				switch {
				case provider == "github" && strings.HasPrefix(joined, "issue close"):
					// gh posts --comment, then closes: one call, and a refused
					// comment closes nothing.
					if i := slices.Index(args, "--comment"); i >= 0 {
						if failComment {
							return "", "comment refused", 1
						}
						out.comments = append(out.comments, args[i+1])
					}
					out.closed = true
					out.notPlanned = slices.Contains(args, "not planned")
				case provider == "gitlab" && strings.HasPrefix(joined, "issue note"):
					if failComment {
						return "", "comment refused", 1
					}
					out.comments = append(out.comments, args[slices.Index(args, "-m")+1])
				case provider == "gitlab" && strings.HasPrefix(joined, "issue close"):
					out.closed = true
				case provider == "gitlab" && strings.Contains(joined, "not-planned"):
					out.notPlanned = true
				}
				return "{}", "", 0
			}
			c := f.cli(provider)
			var tr IssueTracker
			if provider == "github" {
				tr = &GitHub{base: base{cli: c, closing: closingGH}}
			} else {
				tr = &GitLab{base: base{cli: c, closing: closingGL}, NotPlannedLabel: "not-planned"}
			}
			return closeFixture{tr, NumKey(7), func(v bool) { failComment = v }, func(*testing.T) closeOutcome { return out }}
		}
	}
	fx["GitHub"], fx["GitLab"] = cli("github"), cli("gitlab")
	return fx
}

// The Close contract (tracker.go, Adapter.Close): the comment is posted
// first, so a refused comment leaves the issue open and the comment unposted,
// never a closed issue without its comment. Run against every adapter.
func TestCloseContractFailedCommentLeavesIssueOpen(t *testing.T) {
	for name, mk := range closeFixtures(t) {
		t.Run(name, func(t *testing.T) {
			f := mk(t)
			f.failing(true)
			if err := f.tracker.Close(context.Background(), f.key, CloseCompleted, "bye"); err == nil {
				t.Fatal("Close with a refused comment returned nil")
			}
			if got := f.outcome(t); got.closed || len(got.comments) != 0 {
				t.Fatalf("after a failed comment: %+v, want an open issue with no comment", got)
			}
			// the retry the contract promises succeeds and closes once
			f.failing(false)
			if err := f.tracker.Close(context.Background(), f.key, CloseCompleted, "bye"); err != nil {
				t.Fatal(err)
			}
			if got := f.outcome(t); !got.closed || !slices.Equal(got.comments, []string{"bye"}) {
				t.Fatalf("after the retry: %+v, want closed with one comment", got)
			}
		})
	}
}

// One caller-side not-planned value, CloseNotPlanned, reads back as the same
// StateReason on every adapter, and CloseCompleted does not.
func TestCloseContractNotPlannedRoundTrips(t *testing.T) {
	for name, mk := range closeFixtures(t) {
		for _, reason := range []string{CloseCompleted, CloseNotPlanned} {
			t.Run(name+"/"+reason, func(t *testing.T) {
				f := mk(t)
				if err := f.tracker.Close(context.Background(), f.key, reason, "note"); err != nil {
					t.Fatal(err)
				}
				got := f.outcome(t)
				if !got.closed || got.notPlanned != (reason == CloseNotPlanned) || !slices.Equal(got.comments, []string{"note"}) {
					t.Fatalf("Close %s: %+v", reason, got)
				}
			})
		}
	}
}
