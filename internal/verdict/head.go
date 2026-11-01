package verdict

import (
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/git"
)

// HeadTarget describes the resolved branch named by a verdict caller. Remote
// is empty for a bare local branch; Ref retains any explicit remote prefix.
// Head is the short hash already resolved from Ref.
type HeadTarget struct {
	Branch, Ref, Remote, Head string
}

// HeadError is a head-binding refusal, or a missing commit when NotFound is set.
type HeadError struct {
	Message, Hint string
	NotFound      bool
}

func (e *HeadError) Error() string { return e.Message }

// BindHead chooses the commit a new verdict records. An explicit sha must be
// the remote tip (the local tip before the first push). Without sha, a bare
// local branch must match its remote; an explicitly named remote uses its tip.
func BindHead(run Runner, t HeadTarget, sha string) (string, error) {
	remote := t.Remote
	if remote == "" {
		remote = "origin"
	}
	remoteRef := remote + "/" + t.Branch
	pushed, err := headOK(run, "rev-parse", "--verify", "-q", "refs/remotes/"+remoteRef)
	if err != nil {
		return "", err
	}
	rev := func(args ...string) (string, error) {
		return headOutput(run, append([]string{"rev-parse"}, args...)...)
	}
	if sha == "" {
		if !pushed || t.Remote != "" {
			return t.Head, nil
		}
		local, err := rev(t.Ref)
		if err != nil {
			return "", err
		}
		tip, err := rev(remoteRef)
		if err != nil {
			return "", err
		}
		if local != tip {
			return "", &HeadError{Message: fmt.Sprintf("local %s (%s) differs from %s (%s)", t.Branch, local[:7], remoteRef, tip[:7]), Hint: "pass --sha <the sha the reviewers were briefed on> to bind the verdict to it"}
		}
		return t.Head, nil
	}
	if found, err := headOK(run, "rev-parse", "--verify", "-q", sha+"^{commit}"); err != nil {
		return "", err
	} else if !found {
		return "", &HeadError{Message: fmt.Sprintf("--sha %s is not a commit here", sha), NotFound: true}
	}
	full, err := rev(sha + "^{commit}")
	if err != nil {
		return "", err
	}
	target := t.Ref
	if pushed {
		target = remoteRef
	}
	tip, err := rev(target)
	if err != nil {
		return "", err
	}
	if full != tip {
		if pushed {
			if behind, err := headOK(run, "merge-base", "--is-ancestor", tip, full); err != nil {
				return "", err
			} else if behind {
				return "", &HeadError{Message: fmt.Sprintf("--sha %s is ahead of %s (%s): the remote-tracking ref is behind", sha, target, tip[:7]), Hint: "git fetch " + remote + ", then retry"}
			}
		}
		extra, _ := headOutput(run, "log", "--format=%h %s", full+".."+tip)
		msg := fmt.Sprintf("--sha %s is not the head of %s (%s)", sha, target, tip[:7])
		if extra = strings.TrimSpace(extra); extra != "" {
			msg += ", which has commits after it:\n" + extra
		}
		return "", &HeadError{Message: msg, Hint: "review the current head, then pass its sha"}
	}
	return rev("--short", full)
}

// ResolveHead reads a revision's short commit hash through the verdict git seam.
func ResolveHead(run Runner, ref string) (string, error) {
	return headOutput(run, "rev-parse", "--short", ref)
}

func headOK(run Runner, args ...string) (bool, error) {
	res, err := run.Run(args...)
	return res.ExitCode == 0, err
}

func headOutput(run Runner, args ...string) (string, error) {
	res, err := run.Run(args...)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", &git.ExitError{Args: strings.Join(args, " "), ExitCode: res.ExitCode, Stderr: res.Stderr}
	}
	return strings.TrimSpace(res.Stdout), nil
}
