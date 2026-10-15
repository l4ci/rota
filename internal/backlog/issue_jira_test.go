package backlog

import (
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/connections"
	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/tracker/jiratest"
)

// On Jira, claims are plain "Claimed by" comments (tracker.ClaimStore) and
// every other marker comment keeps its markers in properties: the comment
// bodies people read carry none.
func TestIssuesOnJiraKeepMarkersOutOfComments(t *testing.T) {
	s := jiratest.New(t, jiratest.Options{})
	j := tracker.NewJira(connections.Connection{Name: "work", Kind: connections.KindJira, Site: s.URL, User: "me@example.com", Deployment: connections.DeploymentCloud}, "tok", "PROJ", "")
	b := &Issues{Cfg: mustDecode(t, `{}`), Tracker: j, CountProof: stubCountProof}
	key, err := j.Create(b.ctx(), "Widen", "body", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	ref := string(key)

	if won, holder, err := b.Claim(ref, "ben@18"); !won || holder != "ben@18" || err != nil {
		t.Fatal(won, holder, err)
	}
	claims := func() []string {
		cs, err := j.Claims(b.ctx(), key)
		if err != nil {
			t.Fatal(err)
		}
		return cs
	}
	if got := claims(); !slices.Equal(got, []string{"ben@18"}) || len(s.CommentBodies(ref)) != 1 {
		t.Fatalf("a claim is one plain comment: %v %v", got, s.CommentBodies(ref))
	}
	if won, holder, err := b.Claim(ref, "nia@18"); won || holder != "ben@18" || err != nil {
		t.Fatalf("second claimer: %v %q %v", won, holder, err)
	}
	if won, _, _ := b.Claim(ref, "ben@18"); !won {
		t.Fatal("holder lost its own claim")
	}
	if got := claims(); !slices.Equal(got, []string{"ben@18"}) {
		t.Fatalf("the losing claim was left: %v", got)
	}
	if _, err := b.NotePut(ref, "proof", "- row one"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.AddComment(ref, "feedback", "In progress"); err != nil {
		t.Fatal(err)
	}
	if _, err := j.AddComment(b.ctx(), key, marker.Handoff("ben", 18)+"\nHanded off"); err != nil {
		t.Fatal(err)
	}

	st, err := b.Status(ref)
	if err != nil || st.Claim != "ben@18" || !slices.Equal(st.Claims, []string{"ben@18"}) || !strings.Contains(st.State, "in-progress") {
		t.Fatalf("status: %+v %v", st, err)
	}
	if text, ok, err := b.NoteGet(ref, "proof"); !ok || err != nil || text != "- row one" {
		t.Fatalf("proof note: %q %v %v", text, ok, err)
	}
	if rows, _ := b.Comments(ref, "feedback"); len(rows) != 1 || rows[0].Text != "In progress" {
		t.Fatalf("feedback comments: %+v", rows)
	}
	cs, err := j.Comments(b.ctx(), key)
	if err != nil || !slices.ContainsFunc(cs, func(c tracker.Comment) bool { return marker.HasHandoff(c.Body) }) {
		t.Fatalf("handoff marker lost: %+v %v", cs, err)
	}

	if ok, err := b.Release(ref, "ben@18"); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if got := claims(); len(got) != 0 {
		t.Fatalf("release left the claim comment: %v", got)
	}
	if st, _ := b.Status(ref); st.Claim != "" || strings.Contains(st.State, "in-progress") {
		t.Fatalf("after release: %+v", st)
	}
	for _, body := range append(s.CommentBodies(ref), s.Description(ref)) {
		if strings.Contains(body, "<!--") || strings.Contains(body, "rota:") {
			t.Errorf("a rota marker reached Jira: %s", body)
		}
	}
}
