package tracker

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestMergedPRsReadsTheHeadSHAAndAsksForMergedByBranch(t *testing.T) {
	for _, tc := range []struct {
		provider, out string
		want          []string // argv fragments the CLI must see
	}{
		{"github", `[{"number":7,"title":"T","body":"B","headRefName":"codex/12-x","headRefOid":"abc123","url":"https://github.com/o/r/pull/7"}]`,
			[]string{"pr", "list", "--state", "merged", "--head", "codex/12-x"}},
		{"gitlab", `[{"iid":7,"title":"T","description":"B","source_branch":"codex/12-x","sha":"abc123","web_url":"https://gitlab.com/o/r/-/merge_requests/7"}]`,
			[]string{"mr", "list", "--merged", "--source-branch", "codex/12-x"}},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			s := &scripted{answer: func(string, []string) (string, string, int) { return tc.out, "", 0 }}
			got, err := newAdapter(t, tc.provider, s).MergedPRs(context.Background(), "codex/12-x")
			if err != nil || len(got) != 1 {
				t.Fatalf("got %+v, %v", got, err)
			}
			if p := got[0]; p.Number != 7 || p.Branch != "codex/12-x" || p.HeadSHA != "abc123" || p.Title != "T" || p.Body != "B" || p.URL == "" {
				t.Errorf("PR not mapped: %+v", p)
			}
			argv := strings.Fields(s.calls[0])
			for _, w := range tc.want {
				if !slices.Contains(argv, w) {
					t.Errorf("argv %v lacks %q", argv, w)
				}
			}
		})
	}
}

func TestMergedPRsReportsAForgeFailureAndBadJSON(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		t.Run(provider, func(t *testing.T) {
			bad := &scripted{answer: func(string, []string) (string, string, int) { return "not json", "", 0 }}
			if got, err := newAdapter(t, provider, bad).MergedPRs(context.Background(), "b"); err == nil || got != nil {
				t.Errorf("unparseable output: got %v, %v", got, err)
			}
			down := &scripted{answer: func(string, []string) (string, string, int) { return "", "boom", 1 }}
			if got, err := newAdapter(t, provider, down).MergedPRs(context.Background(), "b"); err == nil || got != nil {
				t.Errorf("failing CLI: got %v, %v", got, err)
			}
		})
	}
}
