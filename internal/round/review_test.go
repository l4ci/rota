package round

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

type reviewForgeFake struct {
	notes   []tracker.Comment
	reviews []tracker.Review
	head    string
}

func (f *reviewForgeFake) MRNotes(context.Context, int) ([]tracker.Comment, error) {
	return f.notes, nil
}
func (f *reviewForgeFake) Reviews(context.Context, int) ([]tracker.Review, error) {
	return f.reviews, nil
}
func (f *reviewForgeFake) PRView(context.Context, int) (tracker.PRInfo, error) {
	return tracker.PRInfo{HeadSHA: f.head, State: "OPEN"}, nil
}

// reviewFx is a started round whose slot ben holds issue 12 and has handed its
// PR back (done), with one reviewer comment waiting on it.
func reviewFx(t *testing.T) (*moveFx, *reviewForgeFake, ReviewOpts) {
	t.Helper()
	f := newMoveFx(t)
	if _, err := worker.UpdateSlot(f.root, "ben", func(s *worker.Slot) {
		s.SetPR("https://github.com/o/r/pull/9")
		_ = s.MarkState("done", "")
	}); err != nil {
		t.Fatal(err)
	}
	fg := &reviewForgeFake{head: "abc123", notes: []tracker.Comment{
		{ID: "1", Author: "rev", Body: "please rename x", CreatedAt: time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)},
	}}
	o := ReviewOpts{Slot: "ben", Forge: fg, MaxBounces: 3,
		Escalate: func(context.Context, int, string, string, string) error {
			t.Error("escalated below the cap")
			return nil
		}}
	return f, fg, o
}

func TestReviewRelayBounces(t *testing.T) {
	f, _, o := reviewFx(t)
	f.host.sents = nil
	got, err := f.env.ReviewRelay(bg, f.root, o)
	if err != nil {
		t.Fatal(err)
	}
	reg := worker.LoadRegistry(f.root)
	s := reg.Slot("ben")
	if got.Items != 1 || got.Bounces != 1 || reg.Bounces("12") != 1 {
		t.Fatalf("relayed %+v, bounces %d", got, reg.Bounces("12"))
	}
	if s.State() != "busy" || len(s.Relays()) != 1 || s.ReviewSeen() == "" {
		t.Fatalf("state %s relays %d seen %q", s.State(), len(s.Relays()), s.ReviewSeen())
	}
	if len(f.host.sents) != 1 {
		t.Fatalf("sent %d briefs", len(f.host.sents))
	}
	sent := f.host.sents[0]
	for _, want := range []string{"--- ORCHESTRATOR (round", "REVIEW https://github.com/o/r/pull/9 (1 items)", "- rev: please rename x", "untrusted text"} {
		if !strings.Contains(sent, want) {
			t.Errorf("relay lacks %q:\n%s", want, sent)
		}
	}
	// The batch is consumed: a second pass finds nothing, and a busy slot is not relayed to.
	if _, err := f.env.ReviewRelay(bg, f.root, o); !isBlockedBy(err, "state") {
		t.Fatalf("busy slot: %v", err)
	}
}

func TestReviewRelayNothingPending(t *testing.T) {
	f, fg, o := reviewFx(t)
	fg.notes = nil
	got, err := f.env.ReviewRelay(bg, f.root, o)
	if err != nil || !got.Nothing || len(f.host.sents) > 0 && strings.Contains(f.host.sents[len(f.host.sents)-1], "REVIEW") {
		t.Fatalf("%+v %v", got, err)
	}
	if worker.LoadRegistry(f.root).Bounces("12") != 0 {
		t.Fatal("a bounce was counted")
	}
}

func TestReviewRelayAtCapEscalates(t *testing.T) {
	f, _, o := reviewFx(t)
	for i := 0; i < 3; i++ {
		if _, err := worker.RecordBounce(f.root, "12", ""); err != nil {
			t.Fatal(err)
		}
	}
	var posts []string
	o.Escalate = func(_ context.Context, n int, slot, title, body string) error {
		posts = append(posts, title+"|"+body)
		return nil
	}
	got, err := f.env.ReviewRelay(bg, f.root, o)
	if !isBlockedBy(err, "maxBounces") || !got.Escalated || len(posts) != 1 {
		t.Fatalf("%+v %v posts=%d", got, err, len(posts))
	}
	if !strings.Contains(posts[0], "please rename x") || !strings.Contains(posts[0], "12") {
		t.Errorf("escalation %q", posts[0])
	}
	s := worker.LoadRegistry(f.root).Slot("ben")
	if s.State() != "done" || s.ReviewSeen() != "" || worker.LoadRegistry(f.root).Bounces("12") != 3 {
		t.Fatalf("state %s seen %q bounces %d", s.State(), s.ReviewSeen(), worker.LoadRegistry(f.root).Bounces("12"))
	}
}

func TestReviewRelayRefusedDispatchCountsNothing(t *testing.T) {
	f, _, o := reviewFx(t)
	f.env.Worker.NewHost = func(string) host.Host { return &failingHost{hostFake: f.host} }
	if _, err := f.env.ReviewRelay(bg, f.root, o); err == nil {
		t.Fatal("want the dispatch failure")
	}
	reg := worker.LoadRegistry(f.root)
	if reg.Bounces("12") != 0 || reg.Slot("ben").ReviewSeen() != "" {
		t.Fatalf("bounces %d seen %q", reg.Bounces("12"), reg.Slot("ben").ReviewSeen())
	}
}

func TestReviewRelayTruncatesLongItems(t *testing.T) {
	b := worker.ReviewBatch{PR: "u", Items: []tracker.Review{{Comment: tracker.Comment{Author: "rev", Body: strings.Repeat("x", 4000)}}}}
	text := ReviewRelayText(b)
	if len(text) > 1700 || !strings.Contains(text, "read the rest on the PR") {
		t.Fatalf("len %d", len(text))
	}
}

func isBlockedBy(err error, by string) bool {
	var we *exitcode.Error
	if !errors.As(err, &we) || we.Exit != exitcode.ExitRefused {
		return false
	}
	bd, ok := exitcode.DataOf[worker.BlockData](err)
	return ok && bd.BlockedBy == by
}
