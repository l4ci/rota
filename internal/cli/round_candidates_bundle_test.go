package cli

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/round"
)

func TestCandidateOutputShowsBundleHintOnlyWhenSet(t *testing.T) {
	cs := []round.Candidate{
		{ID: "10", Title: "a", BundleWith: []string{"11", "12"}},
		{ID: "13", Title: "b"},
	}
	lines := candidateLines(cs)
	if !strings.Contains(lines[0], "bundle? with 11, 12") {
		t.Errorf("hint missing: %q", lines[0])
	}
	if strings.Contains(lines[1], "bundle?") {
		t.Errorf("no overlap, no hint: %q", lines[1])
	}
	list := candidateList(cs)
	first, second := list[0].(*jsonx.Object), list[1].(*jsonx.Object)
	if _, ok := first.Get("bundleWith"); !ok {
		t.Error("bundleWith missing on the overlapping item")
	}
	if _, ok := second.Get("bundleWith"); ok {
		t.Error("bundleWith must be absent without an overlap")
	}
}
