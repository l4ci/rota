package backlog

import (
	"errors"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/tracker"
)

func TestIssuesSetDetail(t *testing.T) {
	withBlock := Issue{Key: tracker.NumKey(2), Title: "T", State: "open", Body: "old text\n\n<!-- rota:fields\nRelated: #77\nRepos: web\n-->"}
	closed := Issue{Key: tracker.NumKey(3), Title: "T", State: "closed"}
	for _, c := range []struct {
		name, ref string
		err       error
		calls     string
	}{
		{"keeps the fields block", "2", nil, `get[2] edit[2,{"body":"new text\n\n<!-- rota:fields\nRelated: #77\nRepos: web\n-->"}]`},
		{"missing", "99", ErrNotFound, `get[99]`},
		{"closed", "3", ErrClosed, `get[3]`},
	} {
		b, tr := newIssues(t, `{}`, withBlock, closed)
		err := b.SetDetail(c.ref, "new text\n")
		if !errors.Is(err, c.err) || (c.err == nil && err != nil) {
			t.Errorf("%s: %v, want %v", c.name, err, c.err)
		}
		wantCalls(t, tr, c.calls)
	}
}

func TestUmbrellaSetDetailDelegatesToOwner(t *testing.T) {
	u, fakes := msUmbrella(t, nil, nil)
	fakes["api"].Issues = []Issue{{Key: tracker.NumKey(1), Title: "T", State: "open", Body: "old"}}
	if err := u.SetDetail("api:1", "new text"); err != nil {
		t.Fatal(err)
	}
	if got := fakes["api"].Issues[0].Body; !strings.Contains(got, "new text") {
		t.Errorf("api body = %q", got)
	}
	if err := u.SetDetail("api:9", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown item: %v, want ErrNotFound", err)
	}
}
