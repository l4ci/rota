package round

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// followForge is a review forge that knows who may push: everyone but mallory.
type followForge struct{ reviewForgeFake }

func (followForge) CanPush(_ context.Context, login string) (bool, error) {
	return login != "mallory", nil
}

// followFx is a round where ben finished 12 (PR 7) and moved on to 13, so 12's
// PR waits in the queue with its cursor at the move.
func followFx(t *testing.T) (*moveFx, *followForge) {
	t.Helper()
	f := newMoveFx(t)
	f.finish(t, "ben", pr7)
	if _, err := f.assign("13", "ben"); err != nil {
		t.Fatal(err)
	}
	q := f.queued()
	if len(q) != 1 || q[0].ReviewSeen == "" {
		t.Fatalf("the queued record keeps a review cursor: %+v", q)
	}
	return f, &followForge{reviewForgeFake{head: "abc123"}}
}

func (f *moveFx) later(d time.Duration) time.Time { return f.now.Add(d).UTC() }

// AC-1: only new input from a person with write access makes a follow-up;
// bots, strangers, rota's own marked posts and input from before the move do not.
func TestFollowUpsTellPeopleFromAutomation(t *testing.T) {
	f, fg := followFx(t)
	note := func(id, who, body string, at time.Time) tracker.Comment {
		return tracker.Comment{ID: id, Author: who, Body: body, CreatedAt: at}
	}
	automated := []tracker.Comment{
		note("1", "dependabot[bot]", "bump x", f.later(time.Hour)),
		note("2", "mallory", "please also do y", f.later(time.Hour)),
		note("3", "alice", marker.Handoff("ben", 1)+"\nhandoff", f.later(time.Hour)),
		note("4", "alice", "fix the typo", f.later(-time.Hour)),
		note("5", "project_12_bot_ab12", "pipeline failed", f.later(time.Hour)),
	}
	fg.notes = automated
	got, warns, err := f.env.FollowUps(bg, f.root, fg)
	if err != nil || len(warns) != 0 {
		t.Fatalf("%v %v", err, warns)
	}
	if len(got) != 0 {
		t.Fatalf("follow-ups %+v, want none: no person asked", got)
	}

	fg.notes = append(automated, note("6", "alice", "please handle the empty case", f.later(2*time.Hour)))
	got, _, err = f.env.FollowUps(bg, f.root, fg)
	if err != nil || len(got) != 1 {
		t.Fatalf("follow-ups %+v %v, want one", got, err)
	}
	u := got[0]
	if u.Issue != "12" || u.PR != pr7 || u.From != "ben" || strings.Join(u.Humans, ",") != "alice" || u.Head != "abc123" {
		t.Fatalf("follow-up %+v", u)
	}
	// The bot and the stranger ride along in the brief; the cursor covers them all.
	if len(u.Batch.Items) != 4 || u.Batch.Cursor == "" {
		t.Fatalf("batch %+v, want the four new unmarked items", u.Batch)
	}

	// A PR that is no longer open is no follow-up.
	fg.state = "MERGED"
	if got, _, _ := f.env.FollowUps(bg, f.root, fg); len(got) != 0 {
		t.Fatalf("merged PR: %+v", got)
	}
}

// Rota posts as the maintainer's login on a support PR, so main's own
// review-bounce comment comes from a writer who is no bot. It rides along in
// the brief once a person asked, but never makes a follow-up by itself: rota
// would otherwise restart its own work until round.maxBounces.
func TestFollowUpsIgnoreRotaReviewBounce(t *testing.T) {
	f, fg := followFx(t)
	bounce := tracker.Comment{ID: "7", Author: "alice", Body: marker.ReviewBounce("abc123") + "\nplease fix the failing test", CreatedAt: f.later(time.Hour)}
	fg.notes = []tracker.Comment{bounce}
	got, warns, err := f.env.FollowUps(bg, f.root, fg)
	if err != nil || len(warns) != 0 {
		t.Fatalf("%v %v", err, warns)
	}
	if len(got) != 0 {
		t.Fatalf("follow-ups %+v, want none: a rota review-bounce is no person asking", got)
	}

	fg.notes = append(fg.notes, tracker.Comment{ID: "8", Author: "alice", Body: "and the empty case", CreatedAt: f.later(2 * time.Hour)})
	got, _, err = f.env.FollowUps(bg, f.root, fg)
	if err != nil || len(got) != 1 {
		t.Fatalf("follow-ups %+v %v, want one", got, err)
	}
	if !got[0].At.Equal(f.later(2*time.Hour)) || len(got[0].Batch.Items) != 2 {
		t.Fatalf("follow-up %+v: asked at the person's comment, the bounce rides along", got[0])
	}
}

// pushErrForge cannot tell who may push.
type pushErrForge struct{ reviewForgeFake }

func (pushErrForge) CanPush(context.Context, string) (bool, error) {
	return false, errors.New("permission lookup failed")
}

// A write-access lookup that fails closes the follow-up: no start, a warning.
func TestFollowUpsFailClosedOnLookupError(t *testing.T) {
	f, _ := followFx(t)
	fg := &pushErrForge{reviewForgeFake{head: "abc123", notes: []tracker.Comment{
		{ID: "6", Author: "alice", Body: "more please", CreatedAt: f.later(time.Hour)},
	}}}
	got, warns, err := f.env.FollowUps(bg, f.root, fg)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 || len(warns) != 1 || !strings.Contains(warns[0], "permission lookup failed") {
		t.Fatalf("follow-ups %+v warns %v, want none and one warning", got, warns)
	}
}

func TestFollowUpsSkipRecordWithoutCursor(t *testing.T) {
	f, fg := followFx(t)
	if err := worker.Update(f.root, func(d *worker.Doc) {
		q := f.queued()[0]
		q.ReviewSeen = ""
		d.QueuePR(q)
	}); err != nil {
		t.Fatal(err)
	}
	fg.notes = []tracker.Comment{{ID: "6", Author: "alice", Body: "more please", CreatedAt: f.later(time.Hour)}}
	if got, _, _ := f.env.FollowUps(bg, f.root, fg); len(got) != 0 {
		t.Fatalf("a record from before cursors is never a follow-up: %+v", got)
	}
}

func TestIsBotLogin(t *testing.T) {
	for login, want := range map[string]bool{
		"dependabot[bot]": true, "github-actions[bot]": true, "project_12_bot_9f2c": true,
		"group_7_bot": true, "alice": false, "robot": false, "bot": false, "": false,
	} {
		if got := IsBotLogin(login); got != want {
			t.Errorf("IsBotLogin(%q) = %v, want %v", login, got, want)
		}
	}
}

// AC-2, AC-5: Continue moves the queued PR onto the receiver with the review as
// its brief, counts the bounce and starts the receiver's cursor at the batch,
// so the same input is not a follow-up again.
func TestContinueTransfersQueuedPR(t *testing.T) {
	f, fg := followFx(t)
	fg.notes = []tracker.Comment{{ID: "6", Author: "alice", Body: "please handle the empty case", CreatedAt: f.later(time.Hour)}}
	list, _, err := f.env.FollowUps(bg, f.root, fg)
	if err != nil || len(list) != 1 {
		t.Fatalf("%+v %v", list, err)
	}
	f.host.sents = nil
	res, err := f.env.Continue(bg, f.root, f.be, ContinueOpts{FollowUp: list[0], To: "dana", MaxBounces: 3})
	if err != nil || !res.Changed {
		t.Fatalf("%+v %v", res, err)
	}
	d := f.slot("dana")
	if d.HeldID() != "12" || d.PR() != pr7 || d.ReviewSeen() != list[0].Batch.Cursor {
		t.Fatalf("dana: held %q pr %q seen %q", d.HeldID(), d.PR(), d.ReviewSeen())
	}
	if len(f.queued()) != 0 {
		t.Fatalf("the queued record moved with the PR: %+v", f.queued())
	}
	if n := worker.LoadRegistryTolerant(f.root).Bounces("12"); n != 1 {
		t.Fatalf("bounces %d, want 1", n)
	}
	if len(f.host.sents) == 0 || !strings.Contains(f.host.sents[len(f.host.sents)-1], "please handle the empty case") {
		t.Fatalf("brief lacks the review input: %v", f.host.sents)
	}
	// AC-6: the same snapshot again is refused, not started twice.
	if _, err := f.env.Continue(bg, f.root, f.be, ContinueOpts{FollowUp: list[0], To: "nia", MaxBounces: 3}); err == nil {
		t.Fatal("a second start of the same follow-up must refuse")
	} else if !errors.As(err, new(*BlockedError)) {
		t.Fatalf("want a refusal, got %v", err)
	}
}

// AC-6: at the bounce cap a follow-up does not start and its cursor stays.
func TestContinueAtCapRefuses(t *testing.T) {
	f, fg := followFx(t)
	for i := 0; i < 3; i++ {
		if _, err := recordBounceVia(worker.Env{Config: diskConfig}, f.root, "12", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	fg.notes = []tracker.Comment{{ID: "6", Author: "alice", Body: "again", CreatedAt: f.later(time.Hour)}}
	list, _, _ := f.env.FollowUps(bg, f.root, fg)
	if len(list) != 1 {
		t.Fatalf("%+v", list)
	}
	seen := f.queued()[0].ReviewSeen
	_, err := f.env.Continue(bg, f.root, f.be, ContinueOpts{FollowUp: list[0], To: "dana", MaxBounces: 3})
	if blockedBy(t, err) != BlockMaxBounces {
		t.Fatalf("want the bounce cap, got %v", err)
	}
	if q := f.queued(); len(q) != 1 || q[0].ReviewSeen != seen || f.slot("dana").HeldID() != "" {
		t.Fatalf("nothing moves at the cap: %+v dana %q", q, f.slot("dana").HeldID())
	}
}

// AC-3, AC-6: a reserve slot takes a follow-up, but never backlog work, and a
// roster slot past the reserve takes neither.
func TestTransferReserveSlot(t *testing.T) {
	f, fg := followFx(t)
	f.set.FollowupReserve = 1
	f.env.Round = f.set
	if _, err := f.env.Start(bg, f.root, startOptsWith(f.set)); err != nil {
		t.Fatal(err)
	}
	if f.slot("nia") == nil {
		t.Fatal("round start provisions the reserve slot nia")
	}
	if f.slot("kit") != nil {
		t.Fatal("round start provisions nothing past the reserve")
	}
	if got := Reserve(worker.LoadRegistryTolerant(f.root), f.set); strings.Join(got, ",") != "nia" {
		t.Fatalf("reserve %v, want nia", got)
	}
	if _, err := f.assign("14", "nia"); blockedBy(t, err) != BlockDraining {
		t.Fatalf("backlog onto the reserve slot: %v", err)
	}
	fg.notes = []tracker.Comment{{ID: "6", Author: "alice", Body: "one more thing", CreatedAt: f.later(time.Hour)}}
	list, _, _ := f.env.FollowUps(bg, f.root, fg)
	if len(list) != 1 {
		t.Fatalf("%+v", list)
	}
	// The reserve flag does not open a slot outside the reserve.
	f.set.FollowupReserve = 0
	f.env.Round = f.set
	if _, err := f.env.Continue(bg, f.root, f.be, ContinueOpts{FollowUp: list[0], To: "nia", Reserve: true, MaxBounces: 3}); blockedBy(t, err) != BlockDraining {
		t.Fatalf("reserve off: %v", err)
	}
	f.set.FollowupReserve = 1
	f.env.Round = f.set
	res, err := f.env.Continue(bg, f.root, f.be, ContinueOpts{FollowUp: list[0], To: "nia", Reserve: true, MaxBounces: 3})
	if err != nil || !res.Changed || f.slot("nia").HeldID() != "12" {
		t.Fatalf("%+v %v", res, err)
	}
}

func startOptsWith(set roundcfg.Settings) StartOpts {
	o := startOpts(roundcfg.ScopeMilestone, 100)
	o.Settings = set
	return o
}
