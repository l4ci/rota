package round

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/tracker"
)

// claimLabelRemote keeps the real backlog and round paths over one tracker.
// Only the network boundary is faked; no claim-label logic lives in this fake.
type claimLabelRemote struct{ *trackertest.Fake }

func (r claimLabelRemote) List(ctx context.Context, filter tracker.ListFilter) ([]tracker.Issue, error) {
	issues, err := r.Fake.List(ctx, filter)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(issues, func(is tracker.Issue) bool {
		for _, label := range filter.Labels {
			if !slices.Contains(is.Labels, label) {
				return true
			}
		}
		return false
	}), nil
}

func (r claimLabelRemote) OpenPRs(context.Context) ([]tracker.PR, error)           { return nil, nil }
func (r claimLabelRemote) MergedPRs(context.Context, string) ([]tracker.PR, error) { return nil, nil }
func (r claimLabelRemote) PRState(context.Context, int) (string, error)            { return "open", nil }
func (r claimLabelRemote) PRView(context.Context, int) (tracker.PRInfo, error) {
	return tracker.PRInfo{}, nil
}
func (r claimLabelRemote) ClosedNumbers(string) []int { return nil }

type claimLabelListProbe struct {
	Forge
	lists []tracker.ListFilter
}

func (r *claimLabelListProbe) List(ctx context.Context, filter tracker.ListFilter) ([]tracker.Issue, error) {
	r.lists = append(r.lists, filter)
	return r.Forge.List(ctx, filter)
}

func TestStatusClosedListsStayFiltered(t *testing.T) {
	root := newRepo(t, nil)
	remote := claimLabelRemote{&trackertest.Fake{}}
	probe := &claimLabelListProbe{Forge: remote}
	e := env([]host.Agent{}, probe)
	e.Label = "in-progress"
	if _, err := e.Status(bg, root); err != nil {
		t.Fatal(err)
	}
	var closed int
	for _, list := range probe.lists {
		if list.State == "closed" {
			closed++
			if len(list.Labels) == 0 {
				t.Fatal("Status listed closed issues without a label filter")
			}
		}
	}
	if closed == 0 {
		t.Fatal("Status did not check closed in-progress issues")
	}
	probe.lists = nil
	if _, err := e.Reconcile(bg, root, false); err != nil {
		t.Fatal(err)
	}
	for _, list := range probe.lists {
		if list.State == "closed" && len(list.Labels) == 0 {
			return
		}
	}
	t.Fatal("reconcile did not sweep closed claim labels")
}

func TestReconcileClaimLabelWithoutClaimOrProgress(t *testing.T) {
	root := newRepo(t, nil)
	remote := claimLabelRemote{&trackertest.Fake{Issues: []tracker.Issue{{
		Key: tracker.NumKey(1), Title: "Released", State: "open",
		Labels: []string{"claimed:x@main", "keep"},
	}}}}
	e := env([]host.Agent{}, remote)
	e.Label = "in-progress"
	e.Board = &backlog.Issues{Tracker: remote, Cfg: map[string]any{}}
	out, err := e.Reconcile(bg, root, false)
	if err != nil {
		t.Fatal(err)
	}
	var drift []Finding
	for _, finding := range out.Drift {
		if finding.Kind == ClaimLabelDrift {
			drift = append(drift, finding)
		}
	}
	if len(drift) != 1 || drift[0].Issue != "1" {
		t.Fatalf("stray claim label findings: %v want one drift on #1", drift)
	}
	if !slices.Equal(remote.Issues[0].Labels, []string{"claimed:x@main", "keep"}) {
		t.Fatal("read-only reconcile changed labels")
	}
	if _, err := e.Reconcile(bg, root, true); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(remote.Issues[0].Labels, []string{"keep"}) || len(remote.Issues[0].Comments) != 0 {
		t.Fatalf("after repair: %+v", remote.Issues[0])
	}
}

func TestReconcileClaimLabelDrift(t *testing.T) {
	root := newRepo(t, nil)
	remote := claimLabelRemote{&trackertest.Fake{Issues: []tracker.Issue{{
		Key: tracker.NumKey(1), Title: "Working", State: "open",
		Labels:   []string{"in-progress", "claimed:ben@main", "keep"},
		Comments: []tracker.Comment{{ID: "1", Body: "<!-- rota:claim dana@3 -->"}},
	}}}}
	e := env([]host.Agent{}, remote)
	e.Label = "in-progress"
	e.Board = &backlog.Issues{Tracker: remote, Cfg: map[string]any{}}
	before := slices.Clone(remote.Issues[0].Comments)
	out, err := e.Reconcile(bg, root, false)
	if err != nil {
		t.Fatal(err)
	}
	var found []Finding
	for _, f := range out.Drift {
		if f.Kind == "claim-label-drift" {
			found = append(found, f)
		}
	}
	if len(found) != 1 || found[0].Issue != "1" || found[0].Repair != "sync claim labels" {
		t.Fatalf("claim-label findings: %v want one repairable drift on #1", found)
	}
	if got := remote.Issues[0].Labels; !slices.Equal(got, []string{"in-progress", "claimed:ben@main", "keep"}) {
		t.Fatalf("read-only changed labels: %v", got)
	}
	out, err = e.Reconcile(bg, root, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := remote.Issues[0].Labels; !slices.Equal(got, []string{"in-progress", "keep", "claimed:dana@main"}) {
		t.Fatalf("repaired labels: %v", got)
	}
	if !reflect.DeepEqual(before, remote.Issues[0].Comments) {
		t.Fatal("repair edited comment claims")
	}
	for _, f := range out.Drift {
		if f.Kind == "claim-label-drift" {
			t.Fatalf("unrepaired: %+v", f)
		}
	}
	out, err = e.Reconcile(bg, root, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range append(out.Drift, out.Repaired...) {
		if f.Kind == "claim-label-drift" {
			t.Fatalf("unexpected drift after repair: %+v", f)
		}
	}
}

func TestReconcileClaimLabelDriftRepairFailure(t *testing.T) {
	for _, failure := range []string{"add_labels", "remove_labels", "forge"} {
		t.Run(failure, func(t *testing.T) {
			root := newRepo(t, nil)
			remote := claimLabelRemote{&trackertest.Fake{Issues: []tracker.Issue{{
				Key: tracker.NumKey(1), Title: "Working", State: "open",
				Labels:   []string{"in-progress", "claimed:ben@main"},
				Comments: []tracker.Comment{{ID: "1", Body: "<!-- rota:claim dana@3 -->"}},
			}}, Fail: map[string]error{failure: errors.New("unavailable")}}}
			e := env([]host.Agent{}, remote)
			e.Label = "in-progress"
			e.Board = &backlog.Issues{Tracker: remote, Cfg: map[string]any{}}
			if failure == "forge" {
				e.Forge = closedListFails{e.Forge}
			}
			before := slices.Clone(remote.Issues[0].Comments)
			out, err := e.Reconcile(bg, root, true)
			if err != nil {
				t.Fatal(err)
			}
			var drift bool
			for _, f := range out.Drift {
				drift = drift || f.Kind == ClaimLabelDrift
			}
			if !drift || len(out.Report.Warnings) == 0 {
				t.Fatalf("failed repair disappeared: %+v", out)
			}
			if !reflect.DeepEqual(before, remote.Issues[0].Comments) {
				t.Fatal("failed repair edited claims")
			}
			if failure == "forge" && !slices.Equal(remote.Issues[0].Labels, []string{"in-progress", "claimed:ben@main"}) {
				t.Fatal("unavailable forge allowed a guessed repair")
			}
		})
	}
}

func TestReconcileDropsClaimLabelFromClosed(t *testing.T) {
	root := newRepo(t, nil)
	f := &fakeRemote{labels: map[int][]string{
		1: {"in-progress", "claimed:dana@main", "keep"},
		2: {"claimed:ben@mac", "keep"},
		3: {"claimed:kit@main", "in-progress"},
	}, closedIssues: []tracker.Issue{
		{Key: tracker.NumKey(1), State: "closed", Labels: []string{"in-progress", "claimed:dana@main", "keep"}},
		{Key: tracker.NumKey(2), State: "closed", Labels: []string{"claimed:ben@mac", "keep"}},
	}, closed: map[int]bool{1: true, 2: true}}
	f.add("1", "Closed", "", true, "")
	f.add("2", "Closed", "", true, "")
	f.add("3", "Open", "", false, "")
	for n, ls := range f.labels {
		f.items[tracker.NumKey(n).String()].Labels = slices.Clone(ls)
	}
	e := env([]host.Agent{}, f.asForge())
	e.Label = "in-progress"
	if _, err := e.Reconcile(bg, root, false); err != nil || len(f.removed) != 0 {
		t.Fatalf("read only: removed=%v err=%v", f.removed, err)
	}
	if _, err := e.Reconcile(bg, root, true); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"1", "2"} {
		if got := f.items[id].Labels; !slices.Equal(got, []string{"keep"}) {
			t.Errorf("closed %s labels: %v want [keep]", id, got)
		}
	}
	if got := f.items["3"].Labels; !slices.Equal(got, []string{"claimed:kit@main", "in-progress"}) {
		t.Errorf("open issue labels changed: %v", got)
	}
}
