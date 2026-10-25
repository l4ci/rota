package round

import (
	"context"
	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
	"testing"
	"time"
)

func TestStaleClaimsTransferredAndBundledActivity(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		pr     tracker.PR
		branch string
	}{
		{name: "transferred PR", pr: tracker.PR{Branch: "dana/7-x", UpdatedAt: now.Add(-time.Minute)}},
		{name: "bundle member PR", pr: tracker.PR{Branch: "dana/6-lead", Body: "Closes #6\nCloses #7", UpdatedAt: now.Add(-time.Minute)}},
		{name: "transferred branch", branch: "dana/7-x"},
		{name: "bundle branch after old PR", pr: tracker.PR{Branch: "dana/6-lead", Body: "Closes #7", UpdatedAt: now.Add(-6 * time.Hour)}, branch: "dana/6-lead"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			be := &fakeRemote{claims: map[string]string{"7": "ben@3"}, claimTimes: map[string]map[string]time.Time{"7": {"ben@3": now.Add(-8 * time.Hour)}}}
			be.add("7", "Active work", "", false, "")
			be.items["7"].Labels = []string{"claimed:ben@main"}
			if tc.pr.Branch != "" {
				be.prs = []tracker.PR{tc.pr}
			}
			e := Env{Forge: be.asForge(), Round: roundcfg.Settings{ClaimStaleMinutes: 240}, Worker: wk(worker.Env{Now: func() time.Time { return now }}), Git: func(_ context.Context, _ string, args ...string) (git.Result, error) {
				if args[0] == "ls-remote" && tc.branch != "" {
					return git.Result{Stdout: "abc\trefs/heads/" + tc.branch + "\n"}, nil
				}
				if args[0] == "log" {
					return git.Result{Stdout: now.Add(-time.Minute).Format(time.RFC3339)}, nil
				}
				return git.Result{}, nil
			}}
			got, warnings := e.StaleClaims(bg, t.TempDir(), be, nil)
			if len(got) != 0 || len(warnings) != 0 {
				t.Fatalf("active claim stale=%+v warnings=%v", got, warnings)
			}
		})
	}
}

type staleReadBoard struct {
	*fakeRemote
	gets, statuses, lists int
}

func (b *staleReadBoard) List(closed bool) ([]backlog.Item, error) {
	b.lists++
	return b.fakeRemote.List(closed)
}

func (b *staleReadBoard) Get(id string) (*backlog.Item, error) { b.gets++; return b.fakeRemote.Get(id) }
func (b *staleReadBoard) Status(id string) (*backlog.Status, error) {
	b.statuses++
	return b.fakeRemote.Status(id)
}
func TestStaleClaimsOnlyReadsLabelledItems(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	remote := &fakeRemote{claims: map[string]string{"7": "ben@3"}, claimTimes: map[string]map[string]time.Time{"7": {"ben@3": now.Add(-8 * time.Hour)}}}
	for _, id := range []string{"1", "2", "3", "4", "5", "6", "7"} {
		remote.add(id, "work", "", false, "")
	}
	remote.items["7"].Labels = []string{"claimed:ben@main"}
	be := &staleReadBoard{fakeRemote: remote}
	e := Env{Forge: remote.asForge(), Round: roundcfg.Settings{ClaimStaleMinutes: 240}, Worker: wk(worker.Env{Now: func() time.Time { return now }}), Git: func(context.Context, string, ...string) (git.Result, error) { return git.Result{}, nil }}
	got, warnings := e.StaleClaims(bg, t.TempDir(), be, nil)
	if len(got) != 1 || len(warnings) != 0 {
		t.Fatalf("stale=%+v warnings=%v", got, warnings)
	}
	if be.gets != 0 || be.statuses != 1 {
		t.Fatalf("Get=%d Status=%d want Get=0 Status=1 for labelled claim only", be.gets, be.statuses)
	}
}

func TestStaleClaimsExplicitUnlabelledKey(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	remote := &fakeRemote{claims: map[string]string{"7": "ben@3"}, claimTimes: map[string]map[string]time.Time{"7": {"ben@3": now.Add(-8 * time.Hour)}}}
	remote.add("7", "Unlabelled claim", "", false, "")
	be := &staleReadBoard{fakeRemote: remote}
	e := Env{Forge: remote.asForge(), Round: roundcfg.Settings{ClaimStaleMinutes: 240}, Worker: wk(worker.Env{Now: func() time.Time { return now }}), Git: func(context.Context, string, ...string) (git.Result, error) { return git.Result{}, nil }}
	got, warnings := e.StaleClaims(bg, t.TempDir(), be, []tracker.IssueKey{tracker.NumKey(7)})
	if len(got) != 1 || got[0].Holder != "ben@3" || len(warnings) != 0 {
		t.Fatalf("explicit unlabelled stale claim=%+v warnings=%v", got, warnings)
	}
	if be.gets != 1 || be.statuses != 1 || be.lists != 0 {
		t.Fatalf("Get=%d Status=%d List=%d, want 1/1/0 for one explicit key", be.gets, be.statuses, be.lists)
	}
}
