package land

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/proc"
	"github.com/l4ci/rota/internal/tracker"
)

type reply struct {
	out  string
	code int
}

// TestMergeForgeThroughTheAdapters runs MergeForge over the real GitHub and
// GitLab adapters with scripted forge and git processes, so the line between a
// refused merge request and a confirmation that failed after it is tested where
// the two modules meet.
func TestMergeForgeThroughTheAdapters(t *testing.T) {
	const head = "aaaa1111"
	ok := reply{}
	broke := reply{"boom", 1}
	view := func(json string) reply { return reply{out: json} }
	ff := view(`{"state":"merged","sha":"` + head + `","target_branch":"main"}`)

	const (
		landed      = iota // merged and confirmed
		refused            // proven: nothing merged
		unconfirmed        // the request went through, the confirmation did not
	)
	cases := []struct {
		name            string
		provider        string
		merge, read     reply
		fetch, ancestor reply
		want            int
		sha             string
		errHas          string
	}{
		{name: "github merged", provider: "github", merge: ok, read: view(`{"mergeCommit":{"oid":"mmmm"}}`), want: landed, sha: "mmmm"},
		{name: "github rejected", provider: "github", merge: broke, want: refused, errHas: "boom"},
		{name: "github read fails", provider: "github", merge: ok, read: broke, want: unconfirmed, errHas: "boom"},
		{name: "github no merge commit", provider: "github", merge: ok, read: view(`{"mergeCommit":null}`), want: unconfirmed, errHas: "cannot read the merge commit"},
		{name: "gitlab merge commit", provider: "gitlab", merge: ok, read: view(`{"state":"merged","merge_commit_sha":"mmmm"}`), want: landed, sha: "mmmm"},
		{name: "gitlab squash", provider: "gitlab", merge: ok, read: view(`{"state":"merged","merge_commit_sha":null,"squash_commit_sha":"ssss"}`), want: landed, sha: "ssss"},
		{name: "gitlab fast-forward on base", provider: "gitlab", merge: ok, read: ff, fetch: ok, ancestor: ok, want: landed, sha: head},
		{name: "gitlab rejected", provider: "gitlab", merge: broke, want: refused, errHas: "boom"},
		{name: "gitlab read fails", provider: "gitlab", merge: ok, read: broke, want: unconfirmed, errHas: "boom"},
		{name: "gitlab merged without identity", provider: "gitlab", merge: ok, read: view(`{"state":"merged"}`), want: unconfirmed, errHas: "cannot read the merge commit"},
		{name: "gitlab fetch fails", provider: "gitlab", merge: ok, read: ff, fetch: broke, want: unconfirmed, sha: head, errHas: "git fetch origin failed: boom"},
		{name: "gitlab head not on base", provider: "gitlab", merge: ok, read: ff, fetch: ok, ancestor: reply{code: 1}, want: unconfirmed, sha: head, errHas: "not on origin/main"},
		{name: "gitlab merge-base breaks", provider: "gitlab", merge: ok, read: ff, fetch: ok, ancestor: reply{"boom", 128}, want: unconfirmed, sha: head, errHas: "boom"},
		{name: "gitlab scheduled", provider: "gitlab", merge: ok, read: view(`{"state":"opened","sha":"` + head + `"}`), want: refused, errHas: "nothing was merged"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			answer := func(r reply) (proc.Result, error) {
				if r.code != 0 {
					return proc.Result{Stderr: r.out, ExitCode: r.code}, nil
				}
				return proc.Result{Stdout: r.out}, nil
			}
			exec := func(_ context.Context, cmd proc.Cmd) (proc.Result, error) {
				switch cmd.Args[1] {
				case "merge":
					return answer(c.merge)
				case "view":
					return answer(c.read)
				}
				t.Errorf("unexpected %s %q", cmd.Name, cmd.Args)
				return answer(broke)
			}
			run := func(ctx context.Context, _ string, args ...string) (git.Result, error) {
				r := broke
				switch strings.Join(args, " ") {
				case "fetch -q origin":
					r = c.fetch
				case "merge-base --is-ancestor " + head + " origin/main":
					r = c.ancestor
				default:
					t.Errorf("unexpected git %q", args)
				}
				return git.Result{Stderr: r.out, ExitCode: r.code}, nil
			}
			a, err := tracker.New(context.Background(), tracker.Settings{Provider: c.provider}, "", "/repo",
				tracker.WithExec(exec, func(string) (string, error) { return "/fake", nil }), tracker.WithGit(run))
			if err != nil {
				t.Fatal(err)
			}
			forge, isMerger := a.(Merger)
			if !isMerger {
				t.Fatalf("%T cannot merge", a)
			}

			sha, err := MergeForge(context.Background(), forge, 4, head, false)

			if sha != c.sha {
				t.Errorf("sha = %q, want %q", sha, c.sha)
			}
			var mr *MergeRefusedError
			if got := errors.As(err, &mr); got != (c.want == refused) {
				t.Errorf("refused = %v for %v", got, err)
			}
			if c.want == landed {
				if err != nil {
					t.Errorf("err = %v", err)
				}
				return
			}
			var ue *tracker.UnconfirmedError
			if got := errors.As(err, &ue); got != (c.want == unconfirmed) {
				t.Errorf("unconfirmed = %v for %v", got, err)
			} else if got && (ue.PR != 4 || ue.SHA != c.sha) {
				t.Errorf("unconfirmed = %+v", ue)
			}
			var te *tracker.Error
			if !errors.As(err, &te) || !strings.Contains(err.Error(), c.errHas) {
				t.Errorf("underlying error lost: %v, want %q", err, c.errHas)
			}
		})
	}
}
