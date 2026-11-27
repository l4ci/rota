package cli

import (
	"context"
	"io"
	"slices"
	"testing"

	"github.com/l4ci/rota/internal/landing"
	"github.com/l4ci/rota/internal/postgate"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// A landed slice of a partial-slice issue gives up its claim (#3629): the claim
// comment goes, in-progress goes, and the slot's claimId is cleared so
// reconcile does not post the claim again. An issue without the label keeps all
// three, and so does a slot that does not hold the claim.
func TestGateLandedSliceOwnership(t *testing.T) {
	for _, arg := range []string{"dana", "42", "#42"} {
		t.Run(arg, func(t *testing.T) {
			ctx := context.Background()
			f := newJiraFx(t, "dana", "kit")
			c := &Ctx{Deps: f.deps, Stderr: io.Discard, ctx: ctx}
			book := landingEnv(c, ctx, f.root).Book
			gate := func(k tracker.IssueKey, arg, slot string) error {
				return postgate.Gate(book, f.root, string(k), landing.GateResult{
					Verdict: landing.GatePass, Slot: arg, TargetSlot: slot,
				})
			}

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
			plain := mk("Whole issue", nil, "kit")
			if cs, ip, id := state(partial, "dana"); len(cs) != 1 || !ip || id == "" {
				t.Fatalf("setup: claims %v in-progress %v claimId %q", cs, ip, id)
			}
			// Another slot's release names no claim of its own: nothing happens.
			if err := gate(partial, "#41", "kit"); err != nil {
				t.Fatal(err)
			}
			if cs, ip, id := state(partial, "dana"); len(cs) != 1 || !ip || id == "" {
				t.Errorf("a slot without the claim released it: claims %v in-progress %v claimId %q", cs, ip, id)
			}
			// No resolved owner must stay detached, even when the original argument
			// happens to name the slot that still holds the issue.
			for _, r := range []landing.GateResult{
				{Verdict: landing.GatePass, Slot: "dana"},
				{Verdict: landing.GatePass, Slot: "#42"},
				{Verdict: landing.GatePass, Slot: "dana", IntegrationMembers: []string{string(partial)}},
				{Verdict: landing.GateStale, Slot: "dana", TargetSlot: "dana"},
				{Verdict: landing.GateProvenanceFail, Slot: "#42", TargetSlot: "dana"},
			} {
				if err := postgate.Gate(book, f.root, string(partial), r); err != nil {
					t.Fatal(err)
				}
				if cs, ip, id := state(partial, "dana"); !slices.Equal(cs, []string{"dana@1"}) || !ip || id != "dana@1" {
					t.Errorf("gate %+v changed ownership: claims %v in-progress %v claimId %q", r, cs, ip, id)
				}
			}
			if err := gate(partial, arg, "dana"); err != nil {
				t.Fatal(err)
			}
			if cs, ip, id := state(partial, "dana"); len(cs) != 0 || ip || id != "" {
				t.Errorf("partial-slice landing must release: claims %v in-progress %v claimId %q", cs, ip, id)
			}

			if err := gate(plain, "43", "kit"); err != nil {
				t.Fatal(err)
			}
			if cs, ip, id := state(plain, "kit"); !slices.Equal(cs, []string{"kit@1"}) || !ip || id != "kit@1" {
				t.Errorf("an issue without partial-slice keeps its claim: claims %v in-progress %v claimId %q", cs, ip, id)
			}
		})
	}
}
