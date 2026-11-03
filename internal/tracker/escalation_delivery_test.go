package tracker

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/tracker/itsaplantest"
	"github.com/l4ci/rota/internal/tracker/jiratest"
	"github.com/l4ci/rota/internal/tracker/lineartest"
)

const deliveryReference = "Delivery reference: rota-escalation/0123456789abcdef0123456789abcdef"
const deliveryBody = "**rota escalation e1**: Pick\n" + deliveryReference + "\n\nWhich option?\n\n<!-- rota:escalation e1 -->\n"

// The issue and PR/MR readers must return the visible operation identity on
// the same route that accepted it, without relying on a reusable eN marker.
func TestEscalationDeliveryReferenceForgeRoutes(t *testing.T) {
	ctx := context.Background()
	for _, provider := range []string{"github", "gitlab"} {
		for _, pr := range []bool{false, true} {
			name := provider + "/issue"
			if pr {
				name = provider + "/pr"
			}
			t.Run(name, func(t *testing.T) {
				var stored string
				creates := 0
				s := &scripted{answer: func(_ string, args []string) (string, string, int) {
					if strings.Contains(strings.Join(args, " "), "-X POST") {
						creates++
						for _, a := range args {
							if strings.HasPrefix(a, "body=") {
								stored = strings.TrimPrefix(a, "body=")
							}
						}
						return `{"id":41}`, "", 0
					}
					b, _ := json.Marshal([]map[string]any{{"id": 41, "body": stored, "user": map[string]string{"login": "rota"}, "author": map[string]string{"username": "rota"}}})
					return string(b), "", 0
				}}
				a := newAdapter(t, provider, s)
				var id string
				var cs []Comment
				var err error
				if pr {
					id, err = a.AddMRNote(ctx, 12, deliveryBody)
					if err == nil {
						cs, err = a.MRNotes(ctx, 12)
					}
				} else {
					id, err = a.AddComment(ctx, NumKey(12), deliveryBody)
					if err == nil {
						cs, err = a.Comments(ctx, NumKey(12))
					}
				}
				if err != nil || creates != 1 || id != "41" || len(cs) != 1 || cs[0].ID != "41" || cs[0].Body != deliveryBody {
					t.Fatalf("create=%d id=%q comments=%+v err=%v", creates, id, cs, err)
				}
				wantRoute := "issues/12/"
				if provider == "gitlab" && pr {
					wantRoute = "merge_requests/12/"
				}
				for _, call := range s.calls {
					if !strings.Contains(call, wantRoute) {
						t.Errorf("wrong thread: %s", call)
					}
				}
			})
		}
	}
}

type deliveryTransport func(*http.Request) (*http.Response, error)

func (f deliveryTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type deliveryInterrupted struct{}

func interruptDelivery(t *testing.T, send func()) {
	t.Helper()
	defer func() {
		if _, ok := recover().(deliveryInterrupted); !ok {
			t.Fatal("send did not stop at the auxiliary metadata write")
		}
	}()
	send()
}

// Interruption after Jira accepted its ADF/string comment but before the
// issue-property request leaves the visible operation reference recoverable.
func TestEscalationDeliveryReferenceJiraWithoutProperty(t *testing.T) {
	ctx := context.Background()
	eachJira(t, jiratest.Options{}, func(t *testing.T, j *Jira, s *jiratest.Server) {
		key := mustCreate(t, j, "t", "", nil, "")
		j.rest.Client = &http.Client{Transport: deliveryTransport(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/properties/") {
				panic(deliveryInterrupted{})
			}
			return http.DefaultTransport.RoundTrip(r)
		})}
		interruptDelivery(t, func() { _, _ = j.AddComment(ctx, key, deliveryBody) })
		j.rest.Client = nil
		cs, err := j.Comments(ctx, key)
		if err != nil || len(cs) != 1 || !strings.Contains(cs[0].Body, "\n"+deliveryReference+"\n") || strings.Contains(cs[0].Body, "<!--") {
			t.Fatalf("visible fallback comments=%+v err=%v", cs, err)
		}
		if _, ok := s.Property(string(key), propComment+cs[0].ID); ok {
			t.Fatal("interrupted comment unexpectedly has auxiliary state")
		}
		if len(s.CommentBodies(string(key))) != 1 {
			t.Fatal("interruption created more than one comment")
		}
	})
}

// Linear's first comment write also strips hidden markers. Stopping before
// attachment creation must leave the canonical reference in the fallback body.
func TestEscalationDeliveryReferenceLinearWithoutAttachment(t *testing.T) {
	ctx := context.Background()
	l, s := fakeLinear(t, lineartest.Options{})
	key := linearCreate(t, l, "t", "")
	l.rest.Client = &http.Client{Transport: deliveryTransport(func(r *http.Request) (*http.Response, error) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		r.Body = io.NopCloser(bytes.NewReader(b))
		var body struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(b, &body); err != nil {
			return nil, err
		}
		if strings.Contains(body.Query, "mutation AttachmentCreate(") {
			panic(deliveryInterrupted{})
		}
		return http.DefaultTransport.RoundTrip(r)
	})}
	interruptDelivery(t, func() { _, _ = l.AddComment(ctx, key, deliveryBody) })
	l.rest.Client = nil
	cs, err := l.Comments(ctx, key)
	if err != nil || len(cs) != 1 || !strings.Contains(cs[0].Body, "\n"+deliveryReference+"\n") || strings.Contains(cs[0].Body, "<!--") {
		t.Fatalf("visible fallback comments=%+v err=%v", cs, err)
	}
	if len(s.Attachments(string(key))) != 0 || len(s.CommentBodies(string(key))) != 1 {
		t.Fatalf("attachments=%v comments=%v", s.Attachments(string(key)), s.CommentBodies(string(key)))
	}
}

func TestEscalationDeliveryReferenceItsaplanLaterFeedPage(t *testing.T) {
	ctx := context.Background()
	it, s := fakeItsaplan(t, itsaplantest.Options{FeedPage: 2})
	key := IssueKey(s.Add(itsaplantest.Issue{Title: "t", Comments: []string{"first", "second", "third"}}))
	id, err := it.AddComment(ctx, key, deliveryBody)
	if err != nil {
		t.Fatal(err)
	}
	before := len(feedRequests(s))
	cs, err := it.Comments(ctx, key)
	if err != nil || len(cs) != 4 || cs[3].ID != id || cs[3].Body != deliveryBody {
		t.Fatalf("comments=%+v id=%q err=%v", cs, id, err)
	}
	if pages := len(feedRequests(s)) - before; pages != 2 {
		t.Fatalf("feed pages=%d, want 2", pages)
	}
}
