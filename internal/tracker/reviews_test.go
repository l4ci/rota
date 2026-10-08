package tracker

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestGitHubReviews(t *testing.T) {
	s := &scripted{answer: func(_ string, args []string) (string, string, int) {
		switch call := strings.Join(args, " "); {
		case strings.Contains(call, "pulls/9/reviews"):
			return `[{"id":11,"body":"please split this","state":"CHANGES_REQUESTED","user":{"login":"rev"},"submitted_at":"2026-10-08T10:00:00Z"},` +
				`{"id":12,"body":"","state":"APPROVED","user":{"login":"rev2"},"submitted_at":"2026-10-08T12:00:00Z"},` +
				`{"id":13,"body":"draft","state":"PENDING","user":{"login":"rev"}}]`, "", 0
		case strings.Contains(call, "pulls/9/comments"):
			return `[{"id":5,"body":"nit: name","user":{"login":"rev"},"created_at":"2026-10-08T11:00:00Z"}]`, "", 0
		}
		t.Errorf("unexpected call %v", args)
		return "[]", "", 0
	}}
	got, err := newAdapter(t, "github", s).Reviews(context.Background(), 9)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 entries (pending dropped), got %+v", got)
	}
	want := []struct{ id, state, author string }{
		{"11", ReviewChangesRequested, "rev"}, {"5", ReviewCommented, "rev"}, {"12", ReviewApproved, "rev2"},
	}
	for i, w := range want {
		if got[i].ID != w.id || got[i].State != w.state || got[i].Author != w.author || got[i].CreatedAt.IsZero() {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], w)
		}
	}
	if got[0].Body != "please split this" {
		t.Errorf("body %q", got[0].Body)
	}
}

func TestCommentsCreatedAt(t *testing.T) {
	gh := &scripted{answer: func(string, []string) (string, string, int) {
		return `[{"id":1,"body":"b","user":{"login":"u"},"created_at":"2026-10-08T10:00:00Z"}]`, "", 0
	}}
	got, err := newAdapter(t, "github", gh).Comments(context.Background(), 3)
	want := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	if err != nil || len(got) != 1 || !got[0].CreatedAt.Equal(want) {
		t.Fatalf("github %+v %v", got, err)
	}
	gl := &scripted{answer: func(string, []string) (string, string, int) {
		return `[{"id":1,"body":"b","author":{"username":"u"},"system":false,"created_at":"2026-10-08T10:00:00.123Z"}]`, "", 0
	}}
	got, err = newAdapter(t, "gitlab", gl).MRNotes(context.Background(), 3)
	if err != nil || len(got) != 1 || got[0].CreatedAt.IsZero() {
		t.Fatalf("gitlab %+v %v", got, err)
	}
}

func TestGitLabReviewsEmpty(t *testing.T) {
	s := &scripted{answer: func(string, []string) (string, string, int) { t.Error("no forge call expected"); return "", "", 1 }}
	got, err := newAdapter(t, "gitlab", s).Reviews(context.Background(), 4)
	if err != nil || len(got) != 0 {
		t.Fatalf("%+v %v", got, err)
	}
}
