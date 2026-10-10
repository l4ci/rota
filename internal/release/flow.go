package release

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/tracker"
)

// The release flow's ordering rules: where goreleaser builds releases the tag
// is pushed first, and the branch follows the tag, never leads it, onto a
// published, usable release. The cli verbs parse flags, clear the manual gate
// and render; the decisions live here. Failures are exit-coded errors, and
// errors from a port pass through unchanged.

// Git runs git in the release checkout and returns stdout without its
// trailing newlines; ok is false when git exited non-zero.
type Git interface {
	Query(args ...string) (out string, ok bool, err error)
}

// Forge is the part of the tracker adapter the release flow reads.
type Forge interface {
	ReleaseDrafts() bool
	ReleaseView(ctx context.Context, tag string) (rel tracker.Release, checked bool, err error)
}

// Assets is what the release workflow must attach before a draft is
// finished: one bare binary per platform (the names bin/rota downloads) and the
// checksums. The tarballs are not part of that contract, but every asset that
// is attached, tarballs included, must carry a minisign signature (see
// Missing).
var Assets = []string{
	"rota_linux_amd64", "rota_linux_arm64", "rota_darwin_amd64", "rota_darwin_arm64", "checksums.txt",
}

// Goreleaser is the goreleaser config in dir, or "".
func Goreleaser(dir string) string {
	for _, f := range []string{".goreleaser.yaml", ".goreleaser.yml"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return f
		}
	}
	return ""
}

// RemoteForge is the forge a remote runs ("" for none we publish to).
func RemoteForge(url string) string {
	if p := tracker.ProviderFromURL(url); p != tracker.ProviderUnknown {
		return p
	}
	return ""
}

// Missing lists what a draft still lacks: each required asset, then the
// <name>.minisig of every required or attached asset. install.sh refuses an
// unsigned binary, so a release without its signatures breaks every install.
func Missing(assets []string) []string {
	have := map[string]bool{}
	for _, a := range assets {
		have[a] = true
	}
	const sig = ".minisig"
	var signed []string
	signed = append(signed, Assets...)
	for _, a := range assets {
		if !strings.HasSuffix(a, sig) {
			signed = append(signed, a)
		}
	}
	var missing []string
	seen := map[string]bool{}
	for _, want := range Assets {
		if !have[want] {
			missing = append(missing, want)
		}
	}
	for _, a := range signed {
		if !seen[a] && !have[a+sig] {
			missing = append(missing, a+sig)
		}
		seen[a] = true
	}
	return missing
}

// Usable reports whether publish should edit an existing release. A draft
// must carry every binary, the checksums and a signature for each asset before
// it is finished; and where goreleaser builds the repo, no release at all
// means the workflow has not run, so creating one here would put the plugin
// version ahead of its binaries.
func Usable(rel tracker.Release, dir, tag string) (bool, error) {
	if !rel.Found {
		if cfg := Goreleaser(dir); cfg != "" {
			return false, exitcode.Errf(exitcode.ExitResolution, "no release for %s yet, and %s builds releases", tag, cfg).
				WithHint("wait for the release workflow to create the draft with the binaries")
		}
		return false, nil
	}
	if rel.IsDraft {
		if missing := Missing(rel.Assets); len(missing) > 0 {
			return false, exitcode.Errf(exitcode.ExitResolution, "the draft release for %s lacks %s", tag, strings.Join(missing, ", ")).
				WithHint("wait for the release workflow to attach them")
		}
	}
	return true, nil
}

// PushMode is which refs a push sends.
type PushMode int

const (
	PushBoth       PushMode = iota // the commit and the tag land together
	PushTagOnly                    // the release workflow builds from the tag
	PushBranchOnly                 // once the tag's release has its binaries
)

// PushRequest is one push. Branch is empty for the current branch.
type PushRequest struct {
	Dir, Tag, Branch string
	Mode             PushMode
}

// PushPorts are what PlanPush cannot decide itself. Forge builds the forge
// client for a provider; it is only called when the branch-follows-tag rule
// has to look at the release.
type PushPorts struct {
	Git   Git
	Forge func(provider string) (Forge, error)
}

// PushPlan is the refs to push, checked and ready for the gate.
type PushPlan struct {
	Tag, Branch, SHA string
	Refs             []string
	Scope, Text      string
}

// CheckPushMode refuses a push of both refs where goreleaser builds releases:
// one push would put the new plugin version on the branch before the workflow
// has built the binaries it names.
func CheckPushMode(dir string, mode PushMode) error {
	cfg := Goreleaser(dir)
	if cfg == "" || mode != PushBoth {
		return nil
	}
	e := exitcode.Errf(exitcode.ExitRefused, "%s builds releases here, so the branch must not be pushed with the tag", cfg).
		WithHint("push the tag with --tag-only, wait for the release workflow and publish, then push with --branch-only")
	o := jsonx.NewObject()
	o.Set("blockedBy", "release order")
	o.Set("changed", false)
	e.Data = o
	return e
}

// PlanPush resolves the tag, branch and origin and, for a branch-only push,
// proves the tag is on origin with a published release.
func PlanPush(ctx context.Context, p PushPorts, r PushRequest) (PushPlan, error) {
	tag, bare := r.Tag, strings.TrimPrefix(r.Tag, "v")
	sha, ok, err := p.Git.Query("rev-parse", "-q", "--verify", "refs/tags/"+tag+"^{commit}")
	if err != nil {
		return PushPlan{}, err
	}
	if !ok {
		return PushPlan{}, exitcode.Errf(exitcode.ExitResolution, "tag %s does not exist", tag)
	}
	branch := r.Branch
	if branch == "" {
		cur, ok, err := p.Git.Query("symbolic-ref", "-q", "--short", "HEAD")
		if err != nil {
			return PushPlan{}, err
		}
		if !ok {
			return PushPlan{}, exitcode.Errf(exitcode.ExitResolution, "HEAD is detached; pass --branch")
		}
		branch = cur
	} else if _, ok, err := p.Git.Query("rev-parse", "-q", "--verify", "refs/heads/"+branch); err != nil {
		return PushPlan{}, err
	} else if !ok {
		return PushPlan{}, exitcode.Errf(exitcode.ExitResolution, "branch %s does not exist", branch)
	}
	url, ok, err := p.Git.Query("remote", "get-url", "origin")
	if err != nil {
		return PushPlan{}, err
	}
	if !ok {
		return PushPlan{}, exitcode.Errf(exitcode.ExitResolution, "no 'origin' remote")
	}
	if r.Mode == PushBranchOnly {
		// The plugin version on the branch points at the tag's binaries,
		// so the branch follows the tag, never leads it.
		if err := requireTagOnOrigin(p.Git, tag, "push the tag first: rota release push "+bare+" --tag-only"); err != nil {
			return PushPlan{}, err
		}
		// On GitHub the tag alone is not enough: the release must be
		// published, so the binaries resolve for the version the branch names.
		if prov := RemoteForge(url); prov != "" {
			cl, err := p.Forge(prov)
			if err != nil {
				return PushPlan{}, err
			}
			rel, checked, err := cl.ReleaseView(ctx, tag)
			if err != nil {
				return PushPlan{}, err
			}
			if checked && (!rel.Found || rel.IsDraft) {
				return PushPlan{}, exitcode.Errf(exitcode.ExitResolution, "the release for %s is not published", tag).
					WithHint("finish it first: rota release publish " + bare)
			}
		}
	}
	// The default is one push, so the commit and the tag land together.
	plan := PushPlan{Tag: tag, Branch: branch, SHA: sha, Refs: []string{branch, tag}, Scope: "both", Text: "pushed " + branch + " and " + tag + " to origin"}
	switch r.Mode {
	case PushTagOnly:
		plan.Refs, plan.Scope, plan.Text = []string{tag}, "tag", "pushed "+tag+" to origin"
	case PushBranchOnly:
		plan.Refs, plan.Scope, plan.Text = []string{branch}, "branch", "pushed "+branch+" to origin"
	}
	return plan, nil
}

// PublishRequest is one publish. Draft is whether a draft was asked for.
type PublishRequest struct {
	Dir, Tag string
	Draft    bool
}

// PlanPublish checks a publish before its gate, so a wait for the workflow
// (exit 3) does not spend the maintainer's approval. It reports whether the
// workflow already made a release to finish (edit) rather than create.
func PlanPublish(ctx context.Context, g Git, cl Forge, r PublishRequest) (existing bool, err error) {
	if r.Draft && !cl.ReleaseDrafts() {
		return false, exitcode.Errf(exitcode.ExitUsage, "--draft: GitLab has no draft releases")
	}
	if err := requireTagOnOrigin(g, r.Tag, "push it first: rota release push "+strings.TrimPrefix(r.Tag, "v")); err != nil {
		return false, err
	}
	rel, checked, err := cl.ReleaseView(ctx, r.Tag)
	if err != nil {
		return false, err
	}
	if !checked {
		return false, nil
	}
	return Usable(rel, r.Dir, r.Tag)
}

// requireTagOnOrigin fails unless origin has tag; hint says how to put it there.
func requireTagOnOrigin(g Git, tag, hint string) error {
	remote, ok, err := g.Query("ls-remote", "--tags", "origin", "refs/tags/"+tag)
	if err != nil {
		return err
	}
	if !ok {
		return exitcode.Errf(exitcode.ExitUnavailable, "git ls-remote origin failed")
	}
	if remote == "" {
		return exitcode.Errf(exitcode.ExitResolution, "tag %s is not on origin", tag).WithHint(hint)
	}
	return nil
}
