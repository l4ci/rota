package cli

import (
	"context"
	"flag"
	"os"
	"strings"

	"github.com/l4ci/rota/internal/gate"
	"github.com/l4ci/rota/internal/release"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/tracker"
)

// The B1 (#54) release verbs: the two release steps that create public state,
// each behind its manual gate.

// releaseTagArg is the one <X.Y.Z> positional of push and publish.
func releaseTagArg(args []string, usage string) (string, error) {
	if len(args) != 1 {
		return "", Usage("usage: %s", usage)
	}
	if !release.IsSemver(args[0]) {
		return "", Usage("version must be a bare X.Y.Z, got %q", args[0])
	}
	return "v" + args[0], nil
}

// releaseGitOK runs git in dir and reports whether it exited 0, with stdout.
func releaseGitOK(c *Ctx, dir string, args ...string) (string, bool, error) {
	res, err := shipGit(c, dir, args...)
	if err != nil {
		return "", false, err
	}
	return shipLine(res.Stdout), res.ExitCode == 0, nil
}

func releasePush(fs *flag.FlagSet) RunFunc {
	branchFlag := fs.String("branch", "", "the branch to push with the tag (default: the current branch)")
	tagOnly := fs.Bool("tag-only", false, "push only the tag (the release workflow builds from it)")
	branchOnly := fs.Bool("branch-only", false, "push only the branch, once the tag's release has its binaries")
	confirm := confirmFlags(fs)
	return func(c *Ctx, args []string) (Result, error) {
		tag, err := releaseTagArg(args, "rota release push <X.Y.Z> [--branch <name>] [--tag-only|--branch-only] --confirm --confirm-note <answer>")
		if err != nil {
			return Result{}, err
		}
		if *tagOnly && *branchOnly {
			return Result{}, Usage("--tag-only and --branch-only are mutually exclusive")
		}
		dir, err := releaseDir(c)
		if err != nil {
			return Result{}, err
		}
		mode := release.PushBoth
		switch {
		case *tagOnly:
			mode = release.PushTagOnly
		case *branchOnly:
			mode = release.PushBranchOnly
		}
		if err := release.CheckPushMode(dir, mode); err != nil {
			return Result{Data: err.(*Error).Data}, err
		}
		conf, err := confirm()
		if err != nil {
			return Result{}, err
		}
		plan, err := release.PlanPush(c.Context(), release.PushPorts{
			Git: releaseGit{c, dir},
			Forge: func(provider string) (release.Forge, error) {
				cl, err := c.deps().forge(c.Context(), releaseConfig(dir), provider, dir)
				if err != nil {
					return nil, trackerErr(err)
				}
				return releaseForgeAdapter{cl}, nil
			},
		}, release.PushRequest{Dir: dir, Tag: tag, Branch: *branchFlag, Mode: mode})
		if err != nil {
			return Result{}, err
		}
		if res, err := clearGate(c, gate.TagPush, tag, conf, nil, nil); err != nil {
			return res, err
		}
		res, err := shipGit(c, dir, append([]string{"push", "origin"}, plan.Refs...)...)
		if err != nil {
			return Result{}, err
		}
		if res.ExitCode != 0 {
			return Result{}, Unavailable("git push origin %s: %s (tag %s is at %s)", strings.Join(plan.Refs, " "), strutil.FirstLine(res.Stderr), tag, plan.SHA)
		}
		return Result{Data: gitObj("tag", tag, "branch", plan.Branch, "remote", "origin", "scope", plan.Scope, "changed", true), Text: plan.Text}, nil
	}
}

func releasePublish(fs *flag.FlagSet) RunFunc {
	title := fs.String("title", "", "release title")
	bodyFile := fs.String("body-file", "", "release notes: a path, or - for stdin")
	draft := fs.Bool("draft", false, "create a draft release (GitHub only)")
	confirm := confirmFlags(fs)
	return func(c *Ctx, args []string) (Result, error) {
		tag, err := releaseTagArg(args, "rota release publish <X.Y.Z> --title <text> --body-file <path|-> [--draft] --confirm --confirm-note <answer>")
		if err != nil {
			return Result{}, err
		}
		conf, err := confirm()
		if err != nil {
			return Result{}, err
		}
		if *title == "" {
			return Result{}, Usage("--title is required")
		}
		body, err := shipBodyArg(c, *bodyFile, "the release notes")
		if err != nil {
			return Result{}, err
		}
		dir, err := releaseDir(c)
		if err != nil {
			return Result{}, err
		}
		url, _, err := releaseGitOK(c, dir, "remote", "get-url", "origin")
		if err != nil {
			return Result{}, err
		}
		host := release.Host(url)
		data := func(url string, changed bool) any {
			return gitObj("tag", tag, "host", host, "url", url, "draft", *draft, "changed", changed)
		}
		provider := release.RemoteForge(url)
		if provider == "" {
			c.Warn("no recognized remote; nothing published")
			return Result{Data: data("", false)}, nil
		}
		ctx := c.Context()
		cl, err := c.deps().forge(ctx, releaseConfig(dir), provider, dir)
		if err != nil {
			return Result{}, trackerErr(err)
		}
		existing, err := release.PlanPublish(ctx, releaseGit{c, dir}, releaseForgeAdapter{cl}, release.PublishRequest{Dir: dir, Tag: tag, Draft: *draft})
		if err != nil {
			return Result{}, err
		}
		if res, err := clearGate(c, gate.ReleasePublish, tag, conf, nil, nil); err != nil {
			return res, err
		}
		notes, err := os.CreateTemp("", "rota-release-notes-*.md")
		if err != nil {
			return Result{}, err
		}
		defer os.Remove(notes.Name())
		_, werr := notes.WriteString(body + "\n")
		if cerr := notes.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return Result{}, werr
		}
		spec := tracker.ReleaseSpec{Tag: tag, Title: *title, Notes: notes.Name(), Draft: *draft}
		publish := cl.ReleaseCreate
		if existing {
			// The workflow already made the draft: finish it, never make a second.
			publish = cl.ReleaseEdit
		}
		link, err := publish(ctx, spec)
		if err != nil {
			return Result{}, trackerErr(err)
		}
		return Result{Data: data(link, true), Text: link}, nil
	}
}

// releaseGit adapts git in dir to release.Git.
type releaseGit struct {
	c   *Ctx
	dir string
}

func (g releaseGit) Query(args ...string) (string, bool, error) {
	return releaseGitOK(g.c, g.dir, args...)
}

// releaseForgeAdapter adapts the tracker client to release.Forge, mapping a tracker
// failure onto the exit table.
type releaseForgeAdapter struct{ tracker.Adapter }

func (f releaseForgeAdapter) ReleaseView(ctx context.Context, tag string) (tracker.Release, bool, error) {
	rel, checked, err := f.Adapter.ReleaseView(ctx, tag)
	if err != nil {
		return rel, checked, trackerErr(err)
	}
	return rel, checked, nil
}
