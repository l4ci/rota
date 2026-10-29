package round

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// noStatusBoard satisfies Workflow but reports ItemStatus=false, like a
// backend whose Status is refused: a caller that reads the capability never
// calls Status, one that asserts Workflow would.
type noStatusBoard struct {
	*fakeRemote
	statusCalls int
}

func (b *noStatusBoard) Capabilities() backlog.Capabilities {
	return backlog.Capabilities{IssueIDs: true, ClaimWrites: true, Forge: true}
}

func (b *noStatusBoard) Status(ref string) (*backlog.Status, error) {
	b.statusCalls++
	return b.fakeRemote.Status(ref)
}

func TestTakenOutsideSkipsStatusWithoutItemStatus(t *testing.T) {
	root := newRepo(t, nil)
	milestoneDoc(t, root, "M01", "active")
	fr := &fakeRemote{}
	fr.add("12", "claimed by comment", "M01", false, "")
	fr.claims = map[string]string{"12": "mac-ben@1"}
	be := &noStatusBoard{fakeRemote: fr}
	e := Env{Git: git.Exec, Forge: fr.asForge(), Label: "in-progress", Worker: wk(worker.Env{Getenv: noEnv, Config: diskConfig})}
	if _, err := e.Candidates(bg, root, be, CandidateOpts{Scope: roundcfg.ScopeOpen, Support: "mac", Role: roundcfg.RoleMain}); err != nil {
		t.Fatal(err)
	}
	if be.statusCalls != 0 {
		t.Errorf("Status called %d times on a backend without ItemStatus", be.statusCalls)
	}
}

func TestSupportQueueMarksItemsUnknownWithoutItemStatus(t *testing.T) {
	fr := &fakeRemote{}
	label := "machine:mac"
	for _, id := range []string{"1", "2"} {
		fr.add(id, "work "+id, "", false, "")
		fr.items[id].Labels = []string{label}
	}
	fr.claims = map[string]string{"2": "mac-ben"}
	be := &noStatusBoard{fakeRemote: fr}
	e := Env{SupportLabel: label, Worker: wk(worker.Env{})}
	got, err := e.SupportQueue(bg, be, "mac")
	if err != nil {
		t.Fatal(err)
	}
	var unknown []string
	for _, s := range got {
		if s.Unknown {
			unknown = append(unknown, s.ID)
		}
	}
	if !reflect.DeepEqual(unknown, []string{"1", "2"}) || be.statusCalls != 0 {
		t.Errorf("unknown %v, Status calls %d; want [1 2] and 0", unknown, be.statusCalls)
	}
}

// historyBoard adds the claim history a single-repo issues backend reads from
// its tracker.
type historyBoard struct {
	*fakeRemote
	comments []tracker.Comment
}

func (b *historyBoard) ClaimComments(_ context.Context, _ tracker.IssueKey) ([]tracker.Comment, bool, error) {
	return b.comments, true, nil
}

func reclaimedFixture(t *testing.T) (string, Env, *fakeRemote) {
	t.Helper()
	root, e, fr := adoptFixture(t)
	fr.claims = map[string]string{"12": "dana@7"}
	return root, e, fr
}

func TestAdoptClaimBlocksOnReclaimedHistory(t *testing.T) {
	root, e, fr := reclaimedFixture(t)
	be := &historyBoard{fakeRemote: fr, comments: []tracker.Comment{
		{Body: marker.Claim("ben@3")},
		{Body: marker.Line(marker.KindRelease, "ben@3")},
		{Body: marker.Claim("dana@7")},
	}}
	_, err := e.checkAdoptClaim(bg, root, be, "ben/12-thing", "12")
	if err == nil || blockedBy(t, err) != BlockClaimed {
		t.Fatalf("want claimed block, got %v", err)
	}
}

func TestAdoptClaimWithoutHistoryWarnsUnavailable(t *testing.T) {
	root, e, fr := reclaimedFixture(t)
	warnings, err := e.checkAdoptClaim(bg, root, fr, "ben/12-thing", "12")
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "claim history on 12 unavailable") {
		t.Errorf("warnings %v", warnings)
	}
}
