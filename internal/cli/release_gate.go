package cli

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/gate"
	"github.com/l4ci/rota/internal/release"
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
	return shipLine(res.Stdout), res.Code == 0, nil
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
		dir0, err := releaseDir(c)
		if err != nil {
			return Result{}, err
		}
		if cfg := releaseGoreleaser(dir0); cfg != "" && !*tagOnly && !*branchOnly {
			// One push would put the new plugin version on the branch before
			// the workflow has built the binaries it names.
			return Result{Data: gitObj("blockedBy", "release order", "changed", false)},
				Refused("%s builds releases here, so the branch must not be pushed with the tag", cfg).
					WithHint("push the tag with --tag-only, wait for the release workflow and publish, then push with --branch-only")
		}
		conf, err := confirm()
		if err != nil {
			return Result{}, err
		}
		dir, err := releaseDir(c)
		if err != nil {
			return Result{}, err
		}
		sha, ok, err := releaseGitOK(c, dir, "rev-parse", "-q", "--verify", "refs/tags/"+tag+"^{commit}")
		if err != nil {
			return Result{}, err
		}
		if !ok {
			return Result{}, Resolution("tag %s does not exist", tag)
		}
		branch := *branchFlag
		if branch == "" {
			cur, ok, err := releaseGitOK(c, dir, "symbolic-ref", "-q", "--short", "HEAD")
			if err != nil {
				return Result{}, err
			}
			if !ok {
				return Result{}, Resolution("HEAD is detached; pass --branch")
			}
			branch = cur
		} else if _, ok, err := releaseGitOK(c, dir, "rev-parse", "-q", "--verify", "refs/heads/"+branch); err != nil {
			return Result{}, err
		} else if !ok {
			return Result{}, Resolution("branch %s does not exist", branch)
		}
		if _, ok, err := releaseGitOK(c, dir, "remote", "get-url", "origin"); err != nil {
			return Result{}, err
		} else if !ok {
			return Result{}, Resolution("no 'origin' remote")
		}
		if *branchOnly {
			// The plugin version on the branch points at the tag's binaries,
			// so the branch follows the tag, never leads it.
			remote, ok, err := releaseGitOK(c, dir, "ls-remote", "--tags", "origin", "refs/tags/"+tag)
			if err != nil {
				return Result{}, err
			}
			if !ok {
				return Result{}, Unavailable("git ls-remote origin failed")
			}
			if remote == "" {
				return Result{}, Resolution("tag %s is not on origin", tag).WithHint("push the tag first: rota release push " + strings.TrimPrefix(tag, "v") + " --tag-only")
			}
			// On GitHub the tag alone is not enough: the release must be
			// published, so the binaries resolve for the version the branch names.
			url, _, err := releaseGitOK(c, dir, "remote", "get-url", "origin")
			if err != nil {
				return Result{}, err
			}
			if h := release.Host(url); h == "github" || h == "github-enterprise" {
				cl, err := tracker.NewCLI(c.Context(), tracker.SettingsFromConfig(releaseConfig(dir)), "github", dir, trackerOptions...)
				if err != nil {
					return Result{}, trackerErr(err)
				}
				rel, err := releaseView(c.Context(), cl, tag)
				if err != nil {
					return Result{}, err
				}
				if !rel.Found || rel.IsDraft {
					return Result{}, Resolution("the release for %s is not published", tag).
						WithHint("finish it first: rota release publish " + strings.TrimPrefix(tag, "v"))
				}
			}
		}
		if res, err := clearGate(c, gate.TagPush, tag, conf, nil, nil); err != nil {
			return res, err
		}
		// The default is one push, so the commit and the tag land together.
		refs, scope, text := []string{branch, tag}, "both", "pushed "+branch+" and "+tag+" to origin"
		switch {
		case *tagOnly:
			refs, scope, text = []string{tag}, "tag", "pushed "+tag+" to origin"
		case *branchOnly:
			refs, scope, text = []string{branch}, "branch", "pushed "+branch+" to origin"
		}
		res, err := shipGit(c, dir, append([]string{"push", "origin"}, refs...)...)
		if err != nil {
			return Result{}, err
		}
		if res.Code != 0 {
			return Result{}, Unavailable("git push origin %s: %s (tag %s is at %s)", strings.Join(refs, " "), shipFirstLine(res.Stderr), tag, sha)
		}
		return Result{Data: gitObj("tag", tag, "branch", branch, "remote", "origin", "scope", scope, "changed", true), Text: text}, nil
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
		provider := ""
		switch host {
		case "github", "github-enterprise":
			provider = "github"
		case "gitlab", "gitlab-self-hosted":
			provider = "gitlab"
			if *draft {
				return Result{}, Usage("--draft: GitLab has no draft releases")
			}
		default:
			c.Warn("no recognized remote; nothing published")
			return Result{Data: data("", false)}, nil
		}
		remote, ok, err := releaseGitOK(c, dir, "ls-remote", "--tags", "origin", "refs/tags/"+tag)
		if err != nil {
			return Result{}, err
		}
		if !ok {
			return Result{}, Unavailable("git ls-remote origin failed")
		}
		if remote == "" {
			return Result{}, Resolution("tag %s is not on origin", tag).WithHint("push it first: rota release push " + strings.TrimPrefix(tag, "v"))
		}
		// Look before the gate, so a wait for the workflow (exit 3) does not
		// spend the maintainer's approval.
		ctx := c.Context()
		cl, err := tracker.NewCLI(ctx, tracker.SettingsFromConfig(releaseConfig(dir)), provider, dir, trackerOptions...)
		if err != nil {
			return Result{}, trackerErr(err)
		}
		existing := false
		if provider == "github" {
			rel, err := releaseView(ctx, cl, tag)
			if err != nil {
				return Result{}, err
			}
			if existing, err = releaseUsable(rel, dir, tag); err != nil {
				return Result{}, err
			}
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
		cliArgs := []string{"release", "create", tag, "--title", *title, "--notes-file", notes.Name()}
		verb := "create"
		if provider == "gitlab" {
			cliArgs = []string{"release", "create", tag, "--name", *title, "--notes-file", notes.Name()}
		} else if *draft {
			cliArgs = append(cliArgs, "--draft")
		}
		if existing {
			// The workflow already made the draft: finish it, never make a second.
			verb = "edit"
			cliArgs = []string{"release", "edit", tag, "--title", *title, "--notes-file", notes.Name(), "--draft=" + strconv.FormatBool(*draft)}
		}
		r, err := cl.Run(ctx, cliArgs, nil)
		if err != nil {
			return Result{}, trackerErr(err)
		}
		if r.ExitCode != 0 {
			return Result{}, Unavailable("%s release %s failed: %s", cliName(provider), verb, strings.TrimSpace(string(r.Stderr)))
		}
		lines := strings.Split(strings.TrimSpace(string(r.Stdout)), "\n")
		link := strings.TrimSpace(lines[len(lines)-1])
		return Result{Data: data(link, true), Text: link}, nil
	}
}

// releaseAssets is what the release workflow must attach before a draft is
// finished: one bare binary per platform (the names bin/rota downloads) and the
// checksums. The tarballs are not part of that contract.
var releaseAssets = []string{
	"rota_linux_amd64", "rota_linux_arm64", "rota_darwin_amd64", "rota_darwin_arm64", "checksums.txt",
}

// releaseGoreleaser is the goreleaser config in dir, or "".
func releaseGoreleaser(dir string) string {
	for _, f := range []string{".goreleaser.yaml", ".goreleaser.yml"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return f
		}
	}
	return ""
}

// releaseInfo is what `gh release view` says about a tag.
type releaseInfo struct {
	Found   bool
	IsDraft bool
	Assets  []string
}

// releaseView asks GitHub about the release for tag. gh 2.45 finds drafts
// too (a GraphQL lookup by pending tag), so "release not found" means none.
func releaseView(ctx context.Context, cl *tracker.CLI, tag string) (releaseInfo, error) {
	r, err := cl.Run(ctx, []string{"release", "view", tag, "--json", "isDraft,assets"}, nil)
	if err != nil {
		return releaseInfo{}, trackerErr(err)
	}
	if r.ExitCode != 0 {
		if strings.Contains(strings.ToLower(string(r.Stderr)), "release not found") {
			return releaseInfo{}, nil
		}
		return releaseInfo{}, Unavailable("gh release view failed: %s", strings.TrimSpace(string(r.Stderr)))
	}
	var out struct {
		IsDraft bool `json:"isDraft"`
		Assets  []struct {
			Name string `json:"name"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(r.Stdout, &out); err != nil {
		return releaseInfo{}, Unavailable("gh release view: unreadable output: %v", err)
	}
	info := releaseInfo{Found: true, IsDraft: out.IsDraft}
	for _, a := range out.Assets {
		info.Assets = append(info.Assets, a.Name)
	}
	return info, nil
}

// releaseUsable reports whether publish should edit an existing release. A
// draft must carry every binary and the checksums before it is finished; and
// where goreleaser builds the repo, no release at all means the workflow has
// not run, so creating one here would put the plugin version ahead of its
// binaries.
func releaseUsable(rel releaseInfo, dir, tag string) (bool, error) {
	if !rel.Found {
		if cfg := releaseGoreleaser(dir); cfg != "" {
			return false, Resolution("no release for %s yet, and %s builds releases", tag, cfg).
				WithHint("wait for the release workflow to create the draft with the binaries")
		}
		return false, nil
	}
	if rel.IsDraft {
		have := map[string]bool{}
		for _, a := range rel.Assets {
			have[a] = true
		}
		var missing []string
		for _, want := range releaseAssets {
			if !have[want] {
				missing = append(missing, want)
			}
		}
		if len(missing) > 0 {
			return false, Resolution("the draft release for %s lacks %s", tag, strings.Join(missing, ", ")).
				WithHint("wait for the release workflow to attach them")
		}
	}
	return true, nil
}

func cliName(provider string) string {
	if provider == "gitlab" {
		return "glab"
	}
	return "gh"
}
