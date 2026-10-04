package escalation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/tracker"
)

func TestDerivedStatus(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name string
		e    Entry
		want string
	}{
		{"pending no deadline", Entry{Status: StatusPending}, StatusPending},
		{"pending before deadline", Entry{Status: StatusPending, Deadline: "2026-10-03T12:00:01Z"}, StatusPending},
		{"pending at deadline", Entry{Status: StatusPending, Deadline: "2026-10-03T12:00:00Z"}, StatusPending},
		{"pending past deadline", Entry{Status: StatusPending, Deadline: "2026-10-03T11:59:59Z"}, StatusTimedOut},
		{"answered past deadline", Entry{Status: StatusAnswered, Deadline: "2026-10-03T11:00:00Z"}, StatusAnswered},
		{"unparseable deadline", Entry{Status: StatusPending, Deadline: "soon"}, StatusPending},
	} {
		if got := c.e.Derived(now); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

func TestNextID(t *testing.T) {
	if got := NextID(nil); got != "e1" {
		t.Errorf("empty: %s", got)
	}
	if got := NextID([]Entry{{ID: "e2"}, {ID: "e10"}, {ID: "e3"}, {ID: "junk"}}); got != "e11" {
		t.Errorf("highest wins: %s", got)
	}
}

func TestComposeEndsWithMarker(t *testing.T) {
	got := Compose("e4", "Pick", "\nbody line\n\n")
	want := "**rota escalation e4**: Pick\n\nbody line\n\nAnswer in a new comment on this thread.\n\n<!-- rota:escalation e4 -->\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
	if IsAnswer(got) {
		t.Error("rota's own comment must never read as an answer")
	}
}

type stubForge struct{ posted []string }

func (s *stubForge) Comments(context.Context, int) ([]tracker.Comment, error) { return nil, nil }
func (s *stubForge) MRNotes(context.Context, int) ([]tracker.Comment, error)  { return nil, nil }
func (s *stubForge) AddComment(_ context.Context, _ int, b string) (string, error) {
	s.posted = append(s.posted, b)
	return "77", nil
}
func (s *stubForge) AddMRNote(ctx context.Context, n int, b string) (string, error) {
	return s.AddComment(ctx, n, b)
}
func (s *stubForge) CommentURL(context.Context, bool, int, string) (string, error) {
	return "", errors.New("no url")
}

// A record that cannot be written after the comment went out is exit 1 with
// the comment's id and url in the failure data, never a silent success.
func TestSendRecordWriteFailureKeepsTheURL(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".rota", "workers.json"), 0o755); err != nil { // a directory where the file belongs
		t.Fatal(err)
	}
	f := &stubForge{}
	env := Env{Forge: func(context.Context, string) (Forge, error) { return f, nil }, Getenv: func(string) string { return "" }}
	res, err := Send(context.Background(), env, root, SendOpts{Number: 3, Title: "t", Body: "b"})
	var ee *Error
	if !errors.As(err, &ee) || ee.Exit != 1 {
		t.Fatalf("err %v", err)
	}
	if len(f.posted) != 1 || ee.Data == nil || !strings.Contains(ee.Message, "77") {
		t.Errorf("posted %d, data %v, message %q", len(f.posted), ee.Data, ee.Message)
	}
	if len(res.Warnings) == 0 {
		t.Error("the url read failure should be a warning")
	}
}
