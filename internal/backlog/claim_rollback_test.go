package backlog

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// A claim that fails after its comment was posted gives the claim back: no
// open claim, no in-progress label and no claimed:* label stay on the issue.
func TestIssuesClaimSharedRollsBackPartialClaim(t *testing.T) {
	for _, step := range []string{"edit", "assign_self"} {
		t.Run(step, func(t *testing.T) {
			b, tr := newIssues(t, `{}`, open(1))
			tr.Fail = map[string]error{step: errors.New(step + " unavailable")}
			won, _, err := b.ClaimShared("1", "dana@3", 1)
			if err == nil || won || !strings.Contains(err.Error(), step+" unavailable") {
				t.Fatalf("won=%v err=%v", won, err)
			}
			tr.Fail = nil
			st, serr := b.Status("1")
			if serr != nil || len(st.Claims) != 0 {
				t.Fatalf("open claims after failed claim: %+v err=%v", st, serr)
			}
			if got := tr.Issues[0].Labels; len(got) != 0 {
				t.Fatalf("labels left behind: %v", got)
			}
			if won, _, err := b.ClaimShared("1", "dana@3", 1); err != nil || !won {
				t.Fatalf("retry: won=%v err=%v", won, err)
			}
		})
	}
}

// The rollback releases only a claim this call posted.
func TestIssuesClaimSharedKeepsClaimItDidNotPost(t *testing.T) {
	b, tr := newIssues(t, `{}`, open(1))
	if won, _, err := b.ClaimShared("1", "dana@3", 1); err != nil || !won {
		t.Fatal(won, err)
	}
	tr.Issues[0].Labels = nil // the first call's state write was lost
	tr.Fail = map[string]error{"edit": errors.New("edit unavailable")}
	if _, _, err := b.ClaimShared("1", "dana@3", 1); err == nil {
		t.Fatal("want the state write error")
	}
	tr.Fail = nil
	st, err := b.Status("1")
	if err != nil || !slices.Equal(st.Claims, []string{"dana@3"}) {
		t.Fatalf("claim of the earlier call: %+v err=%v", st, err)
	}
}

// When the rollback fails too, the error names the residue and the release command.
func TestIssuesClaimSharedNamesResidueWhenRollbackFails(t *testing.T) {
	b, tr := newIssues(t, `{}`, open(1))
	tr.Fail = map[string]error{"assign_self": errors.New("assign unavailable"), "remove_labels": errors.New("labels unavailable")}
	_, _, err := b.ClaimShared("1", "dana@3", 1)
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"assign unavailable", "dana@3", "in-progress", "rota item release 1 --as dana@3"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}
