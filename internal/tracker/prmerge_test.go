package tracker

import (
	"context"
	"strings"
	"testing"
)

// The gate's merge safety rule lives here, once for both forges: a merge
// pinned to the verified head carries that sha in the forge's own flag, an
// unpinned one carries none, and neither forge is left to schedule an
// auto-merge that reports success and merges nothing.
func TestPRRequestMergePinsTheVerifiedHead(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef01234567"
	cases := []struct {
		provider, bin, pinFlag string
		autoMergeOff           bool
	}{
		{"github", "gh", "--match-head-commit", false},
		{"gitlab", "glab", "--sha", true},
	}
	for _, c := range cases {
		for _, o := range []MergeOpts{{}, {HeadSHA: sha}, {HeadSHA: sha, DeleteBranch: true}, {DeleteBranch: true}} {
			s := &scripted{answer: func(string, []string) (string, string, int) { return "", "", 0 }}
			if err := newAdapter(t, c.provider, s).PRRequestMerge(context.Background(), 7, o); err != nil {
				t.Fatalf("%s %+v: %v", c.provider, o, err)
			}
			if len(s.calls) != 1 || !strings.HasPrefix(s.calls[0], c.bin+" ") {
				t.Fatalf("%s %+v: calls %q, want one %s call", c.provider, o, s.calls, c.bin)
			}
			call := s.calls[0]
			if got := strings.Contains(call, c.pinFlag+" "+sha); got != (o.HeadSHA != "") {
				t.Errorf("%s %+v: pin present = %v in %q", c.provider, o, got, call)
			}
			if got := strings.Contains(call, "--delete-branch") || strings.Contains(call, "--remove-source-branch"); got != o.DeleteBranch {
				t.Errorf("%s %+v: delete-branch present = %v in %q", c.provider, o, got, call)
			}
			if c.autoMergeOff && !strings.Contains(call, "--auto-merge=false") {
				t.Errorf("%s: auto-merge must be off in %q", c.provider, call)
			}
		}
	}
}

// A refusal (a head that moved past the pin, branch protection) comes back as
// an error carrying the forge's words, not as a merge.
func TestPRRequestMergeRefusalIsAnError(t *testing.T) {
	for _, p := range []string{"github", "gitlab"} {
		s := &scripted{answer: func(string, []string) (string, string, int) { return "", "head commit does not match", 1 }}
		err := newAdapter(t, p, s).PRRequestMerge(context.Background(), 7, MergeOpts{HeadSHA: "abc"})
		if err == nil || !strings.Contains(err.Error(), "head commit does not match") {
			t.Errorf("%s: %v", p, err)
		}
	}
}

// PRView hands the gate one shape for both forges: OPEN, MERGED or CLOSED, and
// the merge sha (a GitLab squash commit stands in for a missing merge commit).
func TestPRViewNormalizesBothForges(t *testing.T) {
	gh := func(state, merge string) string {
		return `{"headRefName":"w1","headRefOid":"aaaa","baseRefName":"main","state":"` + state + `","body":"the body","mergeCommit":` + merge + `}`
	}
	cases := []struct {
		name, provider, out string
		want                PRInfo
	}{
		{"github open", "github", gh("OPEN", "null"), PRInfo{"w1", "aaaa", "main", "OPEN", "", "the body"}},
		{"github merged", "github", gh("MERGED", `{"oid":"mmmm"}`), PRInfo{"w1", "aaaa", "main", "MERGED", "mmmm", "the body"}},
		{"gitlab open", "gitlab", `{"source_branch":"w1","sha":"aaaa","target_branch":"main","state":"opened","description":"the body"}`,
			PRInfo{"w1", "aaaa", "main", "OPEN", "", "the body"}},
		{"gitlab merge commit", "gitlab", `{"source_branch":"w1","sha":"aaaa","target_branch":"main","state":"merged","description":"the body","merge_commit_sha":"mmmm","squash_commit_sha":"ssss"}`,
			PRInfo{"w1", "aaaa", "main", "MERGED", "mmmm", "the body"}},
		{"gitlab squash", "gitlab", `{"source_branch":"w1","sha":"aaaa","target_branch":"main","state":"merged","description":"the body","merge_commit_sha":null,"squash_commit_sha":"ssss"}`,
			PRInfo{"w1", "aaaa", "main", "MERGED", "ssss", "the body"}},
		{"gitlab fast-forward has no merge commit", "gitlab", `{"source_branch":"w1","sha":"aaaa","target_branch":"main","state":"merged","description":"the body"}`,
			PRInfo{"w1", "aaaa", "main", "MERGED", "", "the body"}},
		{"gitlab closed", "gitlab", `{"source_branch":"w1","sha":"aaaa","target_branch":"main","state":"closed","description":"the body"}`,
			PRInfo{"w1", "aaaa", "main", "CLOSED", "", "the body"}},
	}
	for _, c := range cases {
		s := &scripted{answer: func(string, []string) (string, string, int) { return c.out, "", 0 }}
		got, err := newAdapter(t, c.provider, s).PRView(context.Background(), 7)
		if err != nil || got != c.want {
			t.Errorf("%s: %+v, %v; want %+v", c.name, got, err, c.want)
		}
	}
	for _, p := range []string{"github", "gitlab"} {
		s := &scripted{answer: func(string, []string) (string, string, int) { return `{}`, "", 0 }}
		if _, err := newAdapter(t, p, s).PRView(context.Background(), 7); !IsKind(err, KindFailed) {
			t.Errorf("%s: an empty PR must fail, got %v", p, err)
		}
	}
}
