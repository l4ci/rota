package round

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// workflowForge models the remote PR and its auto-close over the same tracker
// used by the real backlog and reconcile. Its merge fast-forwards a bare origin.
type workflowForge struct {
	claimLabelRemote
	t      *testing.T
	origin string
	info   tracker.PRInfo
}

func (f *workflowForge) PRView(context.Context, int) (tracker.PRInfo, error) {
	return f.info, nil
}

func (f *workflowForge) ClosedNumbers(string) []int { return []int{1} }

func (f *workflowForge) ClosesIssue(key tracker.IssueKey, source tracker.ClosingSource) bool {
	return strings.Contains(source.Body, "Closes "+key.Ref())
}

func (f *workflowForge) PRMergeable(context.Context, int) (tracker.Mergeability, error) {
	return tracker.Mergeability{State: tracker.MergeClean}, nil
}

func (f *workflowForge) PRRequestMerge(ctx context.Context, _ int, opts tracker.MergeOpts) error {
	if opts.HeadSHA != f.info.HeadSHA {
		return fmt.Errorf("merge pin %q differs from head %q", opts.HeadSHA, f.info.HeadSHA)
	}
	gittest.Run(f.t, f.origin, "update-ref", "refs/heads/main", f.info.HeadSHA)
	if err := f.Fake.Close(ctx, tracker.NumKey(1), tracker.CloseCompleted, ""); err != nil {
		return err
	}
	f.info.State, f.info.MergeSHA = "MERGED", f.info.HeadSHA
	return nil
}

func (*workflowForge) CommitChecks(context.Context, string) ([]tracker.CheckRun, error) {
	return nil, nil
}

func (*workflowForge) AddMRNote(context.Context, int, string) (string, error) {
	return "", nil
}

func TestClaimWorkflowGateReconcileAndDriftAgree(t *testing.T) {
	root := newRepo(t, map[string]string{"ben": "ben/1-work"}, "ben")
	origin := filepath.Join(t.TempDir(), "github-origin.git")
	gittest.Run(t, root, "init", "-q", "--bare", "-b", "main", origin)
	gittest.Run(t, root, "remote", "add", "origin", origin)
	gittest.Run(t, root, "push", "-q", "origin", "main", "ben/1-work")
	head := gittest.Run(t, root, "rev-parse", "ben/1-work")
	const progress = "working"
	cfg, err := jsonx.Decode([]byte(`{"issues":{"source":"github","labels":{"inProgress":"working"}},"ship":{"review":"none","mergeApproval":"none"}}`))
	if err != nil {
		t.Fatal(err)
	}
	remote := &trackertest.Fake{Issues: []tracker.Issue{
		{Key: tracker.NumKey(1), Title: "To merge", State: "open", Labels: []string{progress, "claimed:ben@main", "keep", "in-progress"},
			Comments: []tracker.Comment{{Body: "<!-- rota:claim ben@47 -->"}}},
		{Key: tracker.NumKey(2), Title: "Already closed", State: "closed", Labels: []string{progress, "claimed:kit@main", "keep", "in-progress"},
			Comments: []tracker.Comment{{Body: "<!-- rota:claim kit@47 -->"}}},
		{Key: tracker.NumKey(3), Title: "Still open", State: "open", Labels: []string{progress, "claimed:dana@main", "keep"},
			Comments: []tracker.Comment{{Body: "<!-- rota:claim dana@47 -->"}}},
		{Key: tracker.NumKey(4), Title: "Label only", State: "closed", Labels: []string{"claimed:legacy", "keep", "in-progress"}},
	}}
	be := &backlog.Issues{Tracker: remote, Cfg: cfg}
	add, remove, err := be.ClaimLabelDrift("2")
	if err != nil || len(add) != 0 || !slices.Equal(remove, []string{"claimed:kit@main", progress}) {
		t.Fatalf("closed drift must remove both workflow labels: add=%v remove=%v err=%v", add, remove, err)
	}
	forge := &workflowForge{claimLabelRemote: claimLabelRemote{remote}, t: t, origin: origin,
		info: tracker.PRInfo{Head: "ben/1-work", HeadSHA: head, Base: "main", State: "OPEN", Body: "Closes #1\n\n## Approvals\nNone\n\n## Rulings\nNone\n\n## Assumptions\nNone\n"}}
	writeRegistry(t, root, slot(root, "ben", "ben/1-work", nil))
	if _, err := worker.UpdateSlot(root, "ben", func(s *worker.Slot) {
		s.SetTask("1")
		s.SetPR("https://github.com/o/r/pull/7")
		s.Raw().Set("claimId", "ben@46")
	}); err != nil {
		t.Fatal(err)
	}
	gate := worker.Env{
		Config: func(string) any { return cfg },
		Getenv: func(string) string { return "" },
		Forge:  func(string, string, tracker.Settings) (worker.Forge, error) { return forge, nil },
		Issues: func(string, tracker.Settings) (worker.Issues, error) { return forge, nil },
	}
	refused, err := gate.Gate(bg, root, worker.GateOpts{Slot: "ben", Base: "main", CheckOnly: true})
	if err != nil || refused.Verdict != worker.GateClaimLost {
		t.Fatalf("old claim was not fenced: %+v err=%v", refused, err)
	}
	if _, err := worker.UpdateSlot(root, "ben", func(s *worker.Slot) { s.Raw().Set("claimId", "ben@47") }); err != nil {
		t.Fatal(err)
	}
	merged, err := gate.Gate(bg, root, worker.GateOpts{Slot: "ben", Base: "main", NoVerify: true})
	if err != nil || merged.Verdict != worker.GatePass {
		t.Fatalf("gate did not merge: %+v err=%v", merged, err)
	}
	if got := remote.Issues[0].Labels; !slices.Equal(got, []string{"keep", "in-progress"}) {
		t.Fatalf("gate kept %v, want [keep in-progress]", got)
	}
	// The slot has finished; reconcile below exercises only label repairs.
	if err := os.Remove(filepath.Join(root, ".rota", "workers.json")); err != nil {
		t.Fatal(err)
	}
	e := env([]host.Agent{}, forge)
	e.Label, e.Board, e.SweepClosedClaims = progress, be, true
	if out, err := e.Reconcile(bg, root, true); err != nil || len(out.Report.Warnings) > 0 {
		t.Fatalf("reconcile: %+v err=%v", out, err)
	}
	for _, i := range []int{0, 1, 3} {
		if got := remote.Issues[i].Labels; !slices.Equal(got, []string{"keep", "in-progress"}) {
			t.Errorf("closed #%s kept %v, want [keep in-progress]", remote.Issues[i].Key, got)
		}
		add, remove, err := be.ClaimLabelDrift(remote.Issues[i].Key.String())
		if err != nil || len(add)+len(remove) != 0 {
			t.Errorf("closed drift after cleanup: add=%v remove=%v err=%v", add, remove, err)
		}
	}
	if got := remote.Issues[2].Labels; !slices.Equal(got, []string{progress, "claimed:dana@main", "keep"}) {
		t.Errorf("open issue changed: %v", got)
	}
}
