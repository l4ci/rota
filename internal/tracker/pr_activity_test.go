package tracker

import (
	"context"
	"testing"
	"time"
)

func TestOpenPRsUpdatedAt(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		t.Run(provider, func(t *testing.T) {
			s := &scripted{answer: func(string, []string) (string, string, int) {
				if provider == "github" {
					return `[{"number":7,"headRefName":"ben/7-work","updatedAt":"2026-10-25T11:00:00Z"}]`, "", 0
				}
				return `[{"iid":7,"source_branch":"ben/7-work","updated_at":"2026-10-25T11:00:00Z"}]`, "", 0
			}}
			prs, err := newAdapter(t, provider, s).OpenPRs(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			want := time.Date(2026, 10, 25, 11, 0, 0, 0, time.UTC)
			if len(prs) != 1 || !prs[0].UpdatedAt.Equal(want) {
				t.Fatalf("PRs %+v want UpdatedAt 11:00Z", prs)
			}
		})
	}
}

func TestOpenPRsHeadRepository(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		t.Run(provider, func(t *testing.T) {
			s := &scripted{answer: func(string, []string) (string, string, int) {
				if provider == "github" {
					return `[{"number":1,"isCrossRepository":true},{"number":2,"isCrossRepository":false},{"number":3}]`, "", 0
				}
				return `[{"iid":1,"source_project_id":2,"target_project_id":1},{"iid":2,"source_project_id":1,"target_project_id":1},{"iid":3}]`, "", 0
			}}
			prs, err := newAdapter(t, provider, s).OpenPRs(context.Background())
			if err != nil || len(prs) != 3 || prs[0].SameRepository || !prs[1].SameRepository || prs[2].SameRepository {
				t.Fatalf("fork, same-repo and unknown head identities: %+v, %v", prs, err)
			}
		})
	}
}
