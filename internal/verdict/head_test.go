package verdict

import (
	"errors"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/git"
)

type headGit func(args ...string) (git.Result, error)

func (f headGit) Run(args ...string) (git.Result, error) { return f(args...) }

func TestBindHeadRefusesLocalDrift(t *testing.T) {
	run := headGit(func(args ...string) (git.Result, error) {
		switch strings.Join(args, " ") {
		case "rev-parse --verify -q refs/remotes/origin/feat/x":
			return git.Result{}, nil
		case "rev-parse feat/x":
			return git.Result{Stdout: strings.Repeat("a", 40) + "\n"}, nil
		case "rev-parse origin/feat/x":
			return git.Result{Stdout: strings.Repeat("b", 40) + "\n"}, nil
		default:
			t.Fatalf("unexpected git call: %v", args)
			return git.Result{}, nil
		}
	})
	_, err := BindHead(run, HeadTarget{Branch: "feat/x", Ref: "feat/x", Head: "aaaaaaa"}, "")
	var refused *HeadError
	if !errors.As(err, &refused) || refused.NotFound || refused.Message != "local feat/x (aaaaaaa) differs from origin/feat/x (bbbbbbb)" || refused.Hint != "pass --sha <the sha the reviewers were briefed on> to bind the verdict to it" {
		t.Fatalf("want local drift refusal with reviewed-sha hint, got %#v", err)
	}
}

const (
	localHead  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	remoteHead = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	oldHead    = "cccccccccccccccccccccccccccccccccccccccc"
)

// The fake models refs and commit identities; unknown revisions fail like git.
func headRunner(t *testing.T, refs map[string]string, ancestor bool, log string) Runner {
	t.Helper()
	return headGit(func(args ...string) (git.Result, error) {
		switch args[0] {
		case "rev-parse":
			ref := strings.TrimSuffix(args[len(args)-1], "^{commit}")
			full, ok := refs[ref]
			if !ok {
				return git.Result{ExitCode: 1}, nil
			}
			if len(args) > 1 && args[1] == "--short" {
				full = full[:7]
			}
			return git.Result{Stdout: full + "\n"}, nil
		case "merge-base":
			if ancestor {
				return git.Result{}, nil
			}
			return git.Result{ExitCode: 1}, nil
		case "log":
			return git.Result{Stdout: log}, nil
		default:
			t.Fatalf("unexpected git call: %v", args)
			return git.Result{}, nil
		}
	})
}

func TestBindHeadRefusals(t *testing.T) {
	cases := []struct {
		name, remote, sha, tip, log, message, hint string
		pushed, ancestor, notFound                 bool
	}{
		{name: "unknown commit", sha: "deadbeef", pushed: true, tip: remoteHead,
			message: "--sha deadbeef is not a commit here", notFound: true},
		{name: "stale reviewed commit", sha: oldHead, pushed: true, tip: remoteHead, log: "bbbbbbb worker push\n",
			message: "--sha " + oldHead + " is not the head of origin/feat/x (bbbbbbb), which has commits after it:\nbbbbbbb worker push",
			hint:    "review the current head, then pass its sha"},
		{name: "tracking ref behind", sha: localHead, pushed: true, tip: remoteHead, ancestor: true,
			message: "--sha " + localHead + " is ahead of origin/feat/x (bbbbbbb): the remote-tracking ref is behind",
			hint:    "git fetch origin, then retry"},
		{name: "named remote behind", remote: "up", sha: localHead, pushed: true, tip: remoteHead, ancestor: true,
			message: "--sha " + localHead + " is ahead of up/feat/x (bbbbbbb): the remote-tracking ref is behind",
			hint:    "git fetch up, then retry"},
		{name: "named remote stale sha", remote: "up", sha: oldHead, pushed: true, tip: remoteHead,
			message: "--sha " + oldHead + " is not the head of up/feat/x (bbbbbbb)",
			hint:    "review the current head, then pass its sha"},
		{name: "unpublished stale sha", sha: oldHead,
			message: "--sha " + oldHead + " is not the head of feat/x (aaaaaaa)",
			hint:    "review the current head, then pass its sha"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			remote := tc.remote
			if remote == "" {
				remote = "origin"
			}
			ref := "feat/x"
			if tc.remote != "" {
				ref = remote + "/feat/x"
			}
			refs := map[string]string{"feat/x": localHead, localHead: localHead, remoteHead: remoteHead, oldHead: oldHead}
			if tc.pushed {
				refs["refs/remotes/"+remote+"/feat/x"] = tc.tip
				refs[remote+"/feat/x"] = tc.tip
			}
			got, err := BindHead(headRunner(t, refs, tc.ancestor, tc.log), HeadTarget{Branch: "feat/x", Ref: ref, Remote: tc.remote, Head: "aaaaaaa"}, tc.sha)
			var he *HeadError
			if got != "" || !errors.As(err, &he) || he.Message != tc.message || he.Hint != tc.hint || he.NotFound != tc.notFound {
				t.Fatalf("got head %q, error %#v; want %q, hint %q, notFound %v", got, err, tc.message, tc.hint, tc.notFound)
			}
		})
	}
}

func TestBindHeadAccepted(t *testing.T) {
	cases := []struct {
		name, remote, sha, local, tip, head, want string
		pushed                                    bool
	}{
		{name: "unpublished implicit", local: localHead, head: "aaaaaaa", want: "aaaaaaa"},
		{name: "unpublished explicit", sha: localHead, local: localHead, head: "aaaaaaa", want: "aaaaaaa"},
		{name: "matching local and remote", local: remoteHead, tip: remoteHead, pushed: true, head: "bbbbbbb", want: "bbbbbbb"},
		{name: "explicit remote despite local ahead", sha: remoteHead, local: localHead, tip: remoteHead, pushed: true, head: "aaaaaaa", want: "bbbbbbb"},
		{name: "explicit remote despite stale local", sha: remoteHead, local: oldHead, tip: remoteHead, pushed: true, head: "ccccccc", want: "bbbbbbb"},
		{name: "remote argument implicit", remote: "origin", local: localHead, tip: remoteHead, pushed: true, head: "bbbbbbb", want: "bbbbbbb"},
		{name: "named remote explicit", remote: "up", sha: remoteHead, local: localHead, tip: remoteHead, pushed: true, head: "bbbbbbb", want: "bbbbbbb"},
		{name: "abbreviated reviewed commit", sha: "bbbbbbb", local: localHead, tip: remoteHead, pushed: true, head: "aaaaaaa", want: "bbbbbbb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			remote, ref := tc.remote, "feat/x"
			if remote == "" {
				remote = "origin"
			} else {
				ref = remote + "/feat/x"
			}
			refs := map[string]string{"feat/x": tc.local, localHead: localHead, remoteHead: remoteHead, "bbbbbbb": remoteHead}
			if tc.pushed {
				refs["refs/remotes/"+remote+"/feat/x"] = tc.tip
				refs[remote+"/feat/x"] = tc.tip
			}
			got, err := BindHead(headRunner(t, refs, false, ""), HeadTarget{Branch: "feat/x", Ref: ref, Remote: tc.remote, Head: tc.head}, tc.sha)
			if err != nil || got != tc.want {
				t.Fatalf("head %q, error %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestBindHeadGitErrors(t *testing.T) {
	problem := errors.New("git interrupted")
	for _, command := range []string{
		"rev-parse --verify -q refs/remotes/origin/feat/x",
		"rev-parse --verify -q " + remoteHead + "^{commit}",
		"rev-parse " + remoteHead + "^{commit}",
		"rev-parse origin/feat/x",
		"rev-parse --short " + remoteHead,
		"merge-base --is-ancestor " + localHead + " " + remoteHead,
	} {
		t.Run(command, func(t *testing.T) {
			tip := remoteHead
			if strings.HasPrefix(command, "merge-base") {
				tip = localHead
			}
			base := headRunner(t, map[string]string{
				"refs/remotes/origin/feat/x": tip, "origin/feat/x": tip, remoteHead: remoteHead,
			}, false, "")
			run := headGit(func(args ...string) (git.Result, error) {
				if strings.Join(args, " ") == command {
					return git.Result{}, problem
				}
				return base.Run(args...)
			})
			_, err := BindHead(run, HeadTarget{Branch: "feat/x", Ref: "feat/x", Head: "aaaaaaa"}, remoteHead)
			if !errors.Is(err, problem) {
				t.Fatalf("want runner error, got %v", err)
			}
		})
	}
}

func TestResolveHead(t *testing.T) {
	run := headRunner(t, map[string]string{"feat/x": remoteHead}, false, "")
	got, err := ResolveHead(run, "feat/x")
	if err != nil || got != "bbbbbbb" {
		t.Fatalf("head %q, error %v", got, err)
	}
	_, err = ResolveHead(run, "missing")
	var ge *git.ExitError
	if !errors.As(err, &ge) || ge.ExitCode != 1 {
		t.Fatalf("want git exit error, got %v", err)
	}
}
