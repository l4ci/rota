package backlog

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/tracker"
)

// AC-5: a key-based tracker's issue is queued by its key, with no
// issue number to print.
func TestReviewQueueKeyIssue(t *testing.T) {
	b, f := rrBackend(t, rrSeed{Cfg: `{}`})
	f.Fake.Issues = append(f.Fake.Issues, tracker.Issue{Key: "PROJ-123", Title: "Widen the seam", State: "open", Labels: []string{"needs-review"}})
	rows, err := b.ReviewQueue()
	if err != nil || len(rows) != 1 {
		t.Fatalf("queue: %v %v", rows, err)
	}
	if r := rows[0]; r.ID != "PROJ-123" || r.Number != 0 {
		t.Errorf("row: %+v", r)
	}
}

// AC-4: a key tracker's item is its bare key; `{ID}` in the body reads PROJ-1,
// never a letter-prefixed FPROJ-1.
func TestCreateKeyItemIsTheBareKey(t *testing.T) {
	b, tr := newIssues(t, `{}`)
	tr.KeyPrefix = "PROJ"
	res, err := b.Create(CreateInput{Kind: "features", Title: "F", Tag: "Major", Body: []byte("\nSee {ID}\n"), HasBody: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "PROJ-1" || res.Type != "F" {
		t.Fatalf("result %+v", res)
	}
	if got := tr.Issues[0].Body; !strings.Contains(got, "See PROJ-1") || strings.Contains(got, "FPROJ") {
		t.Errorf("body %q", got)
	}
}
