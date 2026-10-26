package tracker

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
)

type reportForge struct {
	Forge
	notes []Comment
}

func TestPostReportEditsProviderNotes(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		t.Run(provider, func(t *testing.T) {
			s := &scripted{answer: func(_ string, args []string) (string, string, int) {
				if strings.Contains(strings.Join(args, " "), "-X") {
					return `{}`, "", 0
				}
				return `[{"id":8,"body":"<!-- rota:report qa sha=abcdef1 -->\nFAIL\n","system":false}]`, "", 0
			}}
			err := PostReport(context.Background(), newAdapter(t, provider, s), 7, "<!-- rota:report qa sha=abcdef1 -->\nPASS\n")
			if err != nil {
				t.Fatal(err)
			}
			want := "-X PATCH repos/{owner}/{repo}/issues/comments/8"
			if provider == "gitlab" {
				want = "-X PUT projects/:id/merge_requests/7/notes/8"
			}
			if len(s.calls) != 2 || !strings.Contains(s.calls[1], want) || !strings.Contains(s.calls[1], "body=<!-- rota:report qa sha=abcdef1 -->\nPASS\n") {
				t.Fatalf("report edit calls: %v", s.calls)
			}
		})
	}
}

type reportOffline struct{ Forge }

func (*reportOffline) MRNotes(context.Context, int) ([]Comment, error) {
	return nil, errors.New("offline")
}

func TestPostReportReturnsReadFailure(t *testing.T) {
	err := PostReport(context.Background(), &reportOffline{}, 7, "<!-- rota:report qa sha=abcdef1 -->\nPASS\n")
	if err == nil || err.Error() != "offline" {
		t.Fatalf("read failure: %v", err)
	}
}

func (f *reportForge) MRNotes(context.Context, int) ([]Comment, error) { return f.notes, nil }
func (f *reportForge) AddMRNote(_ context.Context, _ int, body string) (string, error) {
	id := strconv.Itoa(len(f.notes) + 1)
	f.notes = append(f.notes, Comment{ID: id, Body: body})
	return id, nil
}
func (f *reportForge) EditMRNote(_ context.Context, _ int, id, body string) error {
	for i := range f.notes {
		if f.notes[i].ID == id {
			f.notes[i].Body = body
		}
	}
	return nil
}

func TestPostReportPRUpsert(t *testing.T) {
	f := &reportForge{notes: []Comment{{ID: "human", Body: "existing discussion"}}}
	const key = "<!-- rota:report qa sha=abcdef1 -->"
	for _, text := range []string{key + "\nFAIL\n", key + "\nPASS\n", "<!-- rota:report qa sha=abcdef2 -->\nPASS\n"} {
		if err := PostReport(context.Background(), f, 7, text); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.notes) != 3 || f.notes[0].Body != "existing discussion" || f.notes[1].ID != "2" || f.notes[1].Body != key+"\nPASS\n" || !strings.Contains(f.notes[2].Body, "sha=abcdef2") {
		t.Fatalf("report comments after same-head update and new head: %+v", f.notes)
	}
}
