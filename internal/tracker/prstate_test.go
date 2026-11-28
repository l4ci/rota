package tracker

import (
	"context"
	"fmt"
	"testing"
)

func TestGitLabPRStateContract(t *testing.T) {
	for _, tc := range []struct {
		state string
		want  string
	}{
		{"opened", "open"},
		{"locked", "open"},
		{"merged", "merged"},
		{"closed", "closed"},
		{"OPENED", "open"},
		{"unexpected", ""},
		{"", ""},
	} {
		t.Run(tc.state, func(t *testing.T) {
			s := &scripted{answer: func(string, []string) (string, string, int) {
				return fmt.Sprintf(`{"state":%q}`, tc.state), "", 0
			}}
			got, err := newAdapter(t, "gitlab", s).PRState(context.Background(), 12)
			t.Logf("GitLab state %q -> %q, error %v", tc.state, got, err)
			if tc.want == "" {
				if got != "" || !IsKind(err, KindFailed) {
					t.Fatalf("state %q, error %v; want no state and a failure", got, err)
				}
			} else if err != nil || got != tc.want {
				t.Fatalf("state %q, error %v; want %q", got, err, tc.want)
			}
		})
	}
}
