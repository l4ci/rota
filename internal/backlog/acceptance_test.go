package backlog

import (
	"context"
	"errors"
	"strings"
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
	ids, unchecked, err := TrustedDecisionIDs(comments, trust)
	if err != nil || len(ids) != 1 || ids[0] != "2" {
		t.Fatalf("ids %v err %v", ids, err)
	}
	if unchecked == nil || !strings.Contains(unchecked.Error(), "ghost") || !strings.Contains(unchecked.Error(), "not a login") {
		t.Errorf("unchecked %v does not name the author and cause", unchecked)
	}
}

// A cancelled context is an error, not an untrusted author.
func TestTrustedDecisionIDsPropagatesCancellation(t *testing.T) {
	comments := []tracker.Comment{{ID: "1", Author: "a", Body: "<!-- rota:comment decision -->\nx"}}
	_, _, err := TrustedDecisionIDs(comments, func(string) (bool, error) { return false, context.Canceled })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v, want context.Canceled", err)
	}
}
