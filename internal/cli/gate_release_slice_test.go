package cli

import (
	"context"
	"io"
	"slices"
	"testing"

	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// A landed slice of a partial-slice issue gives up its claim (#3629): the claim
// comment goes, in-progress goes, and the slot's claimId is cleared so
// reconcile does not post the claim again. An issue without the label keeps all
// three, and so does a slot that does not hold the claim.
func TestReleaseLandedSlice(t *testing.T) {
	ctx := context.Background()
	f := newJiraFx(t, "dana", "kit")
	c := &Ctx{Deps: f.deps, Stderr: io.Discard, ctx: ctx}
	release := releaseLandedSlice(c)

	mk := func(title string, labels []string, slot string) tracker.IssueKey {
		t.Helper()
		k, err := f.it.Create(ctx, title, "## Acceptance\n- [ ] works\n", labels, "")
		if err != nil {
			t.Fatal(err)
		}
		if code, out, errOut := f.assign(t, string(k), slot); code != 0 {
			t.Fatalf("assign %s: %d %s %s", k, code, out, errOut)
		}
		return k
	}
	state := func(k tracker.IssueKey, slot string) (claims []string, inProgress bool, claimID string) {
		t.Helper()
		cs, err := f.claims(string(k))
		if err != nil {
			t.Fatal(err)
		}
		is, err := f.it.Get(ctx, k, false)
		if err != nil {
			t.Fatal(err)
		}
		return cs, slices.Contains(is.Labels, "in-progress"), worker.LoadRegistryTolerant(f.root).Slot(slot).ClaimID()
	}

	partial := mk("Slice one", []string{"partial-slice"}, "dana")
	if cs, ip, id := state(partial, "dana"); len(cs) != 1 || !ip || id == "" {
		t.Fatalf("setup: claims %v in-progress %v claimId %q", cs, ip, id)
	}
	// Another slot's release names no claim of its own: nothing happens.
	if err := release(f.root, string(partial), "kit"); err != nil {
		t.Fatal(err)
	}
	if cs, ip, id := state(partial, "dana"); len(cs) != 1 || !ip || id == "" {
		t.Errorf("a slot without the claim released it: claims %v in-progress %v claimId %q", cs, ip, id)
	}
	if err := release(f.root, string(partial), "dana"); err != nil {
		t.Fatal(err)
	}
	if cs, ip, id := state(partial, "dana"); len(cs) != 0 || ip || id != "" {
		t.Errorf("partial-slice landing must release: claims %v in-progress %v claimId %q", cs, ip, id)
	}

	plain := mk("Whole issue", nil, "kit")
	if err := release(f.root, string(plain), "kit"); err != nil {
		t.Fatal(err)
	}
	if cs, ip, id := state(plain, "kit"); len(cs) != 1 || !ip || id == "" {
		t.Errorf("an issue without partial-slice keeps its claim: claims %v in-progress %v claimId %q", cs, ip, id)
	}
}
