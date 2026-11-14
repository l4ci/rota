package backlog

import (
	"errors"
	"testing"

	"github.com/l4ci/rota/internal/tracker"
)

// A failed access lookup for one author leaves that comment untrusted and does
// not fail the check for the others.
func TestTrustedDecisionIDsSkipsAFailedLookup(t *testing.T) {
	comments := []tracker.Comment{
		{ID: "1", Author: "ghost", Body: "<!-- rota:comment decision -->\nx"},
		{ID: "2", Author: "maintainer", Body: "<!-- rota:comment decision -->\ny"},
	}
	trust := func(a string) (bool, error) {
		if a == "ghost" {
			return false, errors.New("not a login")
		}
		return true, nil
	}
	ids, err := TrustedDecisionIDs(comments, trust)
	if err != nil || len(ids) != 1 || ids[0] != "2" {
		t.Fatalf("ids %v err %v", ids, err)
	}
}
