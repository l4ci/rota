package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/tracker"
)

// A tracker boundary whose issue state survives the merge, as on GitHub when
// the PR targets a branch other than the default branch.
type milestoneIssues struct {
	*fakeForge
	state              string
	labels             []string
	labelErr, closeErr error
	others             map[tracker.IssueKey]*milestoneIssues
}

func (m *milestoneIssues) Get(ctx context.Context, key tracker.IssueKey, comments bool) (tracker.Issue, error) {
	if other := m.others[key]; other != nil {
		return other.Get(ctx, key, comments)
	}
	is, err := m.fakeForge.Get(ctx, key, comments)
	is.State, is.Labels = m.state, slices.Clone(m.labels)
	return is, err
}

func (m *milestoneIssues) Close(ctx context.Context, key tracker.IssueKey, reason, comment string) error {
	if other := m.others[key]; other != nil {
		return other.Close(ctx, key, reason, comment)
	}
	if m.closeErr != nil {
		return m.closeErr
	}
	m.state = "closed"
	return nil
}

func (m *milestoneIssues) AddLabels(ctx context.Context, key tracker.IssueKey, labels []string, create bool) error {
	if other := m.others[key]; other != nil {
		return other.AddLabels(ctx, key, labels, create)
	}
	if m.labelErr != nil {
		return m.labelErr
	}
	m.labels = append(m.labels, labels...)
	return nil
}

func TestMilestoneMergeFailuresKeepTheClaim(t *testing.T) {
	for _, failure := range []string{"label", "close", "verify", "merge"} {
		t.Run(failure, func(t *testing.T) {
			verify := "true"
			if failure == "verify" {
				verify = "false"
			}
			w := syncPRWorld(t, verify)
			m := &milestoneIssues{fakeForge: &fakeForge{w: w, provider: "github"}, state: "open", labels: []string{"in-progress"}}
			if failure == "label" {
				m.labelErr = errors.New("no label access")
			}
			if failure == "close" {
				m.closeErr = errors.New("no close access")
			}
			if failure == "merge" {
				w.mode = "fail"
			}
			e := w.env(false)
			e.Forge = func(string, string, tracker.Settings) (Forge, error) { return m, nil }
			res, err := e.Train(bg, w.dir, TrainOpts{Targets: []string{"#7"}, Base: "milestone/M01", SyncBase: "main"})
			want := GatePass
			if failure == "verify" {
				want = GateVerifyFailed
			}
			if failure == "merge" {
				want = GateMergeFailed
			}
			if err != nil || res.Verdict != want {
				t.Fatalf("train: %+v %v", res, err)
			}
			if m.state != "open" || !slices.Contains(m.labels, "in-progress") {
				t.Fatalf("state=%s labels=%v", m.state, m.labels)
			}
			if failure == "label" || failure == "close" {
				if !strings.Contains(strings.Join(res.Notes, "\n"), "ISSUE-OPEN") {
					t.Fatalf("missing tracker warning: %+v", res)
				}
			} else if slices.Contains(m.labels, "on-milestone/M01") {
				t.Fatalf("unmerged issue labelled: %v", m.labels)
			}
		})
	}
}

func TestGateMilestonePartialSliceAndDefaultBranch(t *testing.T) {
	for _, c := range []struct {
		name, base, body       string
		partial, final, closed bool
	}{
		{"default branch keeps auto-close", "main", "Closes #5", false, false, false},
		{"partial slice stays open", "milestone/M01", "Refs #5", true, false, false},
		{"final slice closes", "milestone/M01", "Closes #5", true, true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, ghURL)
			w.forge("body", c.body)
			if c.base != "main" {
				w.forge("base", c.base)
				gitq(t, w.dir, "push", "-q", "origin", "main:refs/heads/"+c.base)
				gitq(t, w.dir, "checkout", "-q", "-b", c.base)
			}
			m := &milestoneIssues{fakeForge: &fakeForge{w: w, provider: "github"}, state: "open", labels: []string{"in-progress"}}
			if c.partial {
				m.labels = append(m.labels, PartialSliceLabel)
			}
			e := w.env(false)
			e.Forge = func(string, string, tracker.Settings) (Forge, error) { return m, nil }
			res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: c.base, FinalSlice: c.final})
			if err != nil || res.Verdict != GatePass {
				t.Fatalf("gate: %+v %v", res, err)
			}
			if (m.state == "closed") != c.closed || slices.Contains(m.labels, "on-milestone/M01") != c.closed {
				t.Fatalf("state=%s labels=%v", m.state, m.labels)
			}
		})
	}
}

func (m *milestoneIssues) RemoveLabels(ctx context.Context, key tracker.IssueKey, labels []string) error {
	if other := m.others[key]; other != nil {
		return other.RemoveLabels(ctx, key, labels)
	}
	m.labels = slices.DeleteFunc(m.labels, func(l string) bool { return slices.Contains(labels, l) })
	return nil
}

func TestGateMilestoneClosesBundleAndOtherLinkedIssues(t *testing.T) {
	w := newWorld(t, ghURL)
	w.forge("base", "milestone/M01")
	w.forge("body", "Closes #5\nCloses #6\nCloses #8\nRefs #9")
	gitq(t, w.dir, "push", "-q", "origin", "main:refs/heads/milestone/M01")
	gitq(t, w.dir, "checkout", "-q", "-b", "milestone/M01")
	if err := os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"), []byte(fmt.Sprintf(`{"slots":[{"name":"w1","branch":"w1","task":"#5","pr":%q,"bundle":["#6"]}]}`, ghURL)), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fakeForge{w: w, provider: "github"}
	m := &milestoneIssues{fakeForge: f, state: "open", labels: []string{"in-progress"}, others: map[tracker.IssueKey]*milestoneIssues{}}
	for _, key := range []tracker.IssueKey{"6", "8", "9"} {
		m.others[key] = &milestoneIssues{fakeForge: f, state: "open", labels: []string{"in-progress"}}
	}
	e := w.env(false)
	e.Forge = func(string, string, tracker.Settings) (Forge, error) { return m, nil }
	res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "milestone/M01"})
	if err != nil || res.Verdict != GatePass {
		t.Fatalf("gate: %+v %v", res, err)
	}
	for _, key := range []tracker.IssueKey{"5", "6", "8", "9"} {
		is, err := m.Get(bg, key, false)
		if err != nil {
			t.Fatal(err)
		}
		wantClosed := key != "9"
		if (is.State == "closed") != wantClosed || slices.Contains(is.Labels, "on-milestone/M01") != wantClosed || slices.Contains(is.Labels, "in-progress") == wantClosed {
			t.Errorf("%s: state=%s labels=%v", key, is.State, is.Labels)
		}
	}
}

func TestTrainMilestoneClosesAndLabelsIssue(t *testing.T) {
	w := syncPRWorld(t, "true")
	m := &milestoneIssues{fakeForge: &fakeForge{w: w, provider: "github"}, state: "open", labels: []string{"in-progress"}}
	e := w.env(false)
	e.Forge = func(string, string, tracker.Settings) (Forge, error) { return m, nil }
	res, err := e.Train(bg, w.dir, TrainOpts{Targets: []string{"#7"}, Base: "milestone/M01", SyncBase: "main"})
	if err != nil || res.Verdict != GatePass {
		t.Fatalf("train: %+v %v", res, err)
	}
	if m.state != "closed" || !slices.Equal(m.labels, []string{"on-milestone/M01"}) {
		t.Fatalf("merged issue state=%s labels=%v; want closed and [on-milestone/M01]; warnings %v", m.state, m.labels, res.Notes)
	}
}
