package tracker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// PRSpec is a pull request (GitLab: merge request) to open. Base is read only
// where PRNeedsBase.
type PRSpec struct {
	Title string
	Body  string
	Head  string
	Base  string
}

// ReleaseSpec is a release to create or finish. Notes is the path of a file
// holding the release notes. Draft is honoured only where ReleaseDrafts.
type ReleaseSpec struct {
	Tag   string
	Title string
	Notes string
	Draft bool
}

// Release is what the forge says about a release tag.
type Release struct {
	Found   bool
	IsDraft bool
	Assets  []string
}

// failWith words a forge call that exited non-zero.
type failWith func(code int, stderr string) string

// lastLine runs args and returns the last non-blank stdout line, the URL both
// forges print on success. A non-zero exit is KindFailed worded by fail (never
// KindNotFound: the caller named the object, so "not found" is not a lookup).
func (b *base) lastLine(ctx context.Context, args []string, stdin string, fail failWith) (string, error) {
	var in io.Reader
	if stdin != "" {
		in = strings.NewReader(stdin)
	}
	res, err := b.cli.Run(ctx, args, in)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", &Error{Kind: KindFailed, Code: 1, Message: fail(res.ExitCode, string(res.Stderr))}
	}
	var last string
	for _, l := range strings.Split(string(res.Stdout), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			last = l
		}
	}
	return last, nil
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

// prFail words a failed PR create: "<cli> <verb> exited <code>: <first line>".
func prFail(what string) failWith {
	return func(code int, stderr string) string {
		return fmt.Sprintf("%s exited %d: %s", what, code, firstLine(stderr))
	}
}

// relFail words a failed release call: "<cli> release <verb> failed: <stderr>".
func relFail(what string) failWith {
	return func(_ int, stderr string) string { return what + " failed: " + strings.TrimSpace(stderr) }
}

// ---- GitHub -----------------------------------------------------------------

func (g *GitHub) PRNeedsBase() bool { return false }

func (g *GitHub) PRCreate(ctx context.Context, s PRSpec) (string, error) {
	return g.lastLine(ctx, []string{"pr", "create", "--title", s.Title, "--body-file", "-"}, s.Body,
		prFail("gh pr create"))
}

func (g *GitHub) ReleaseDrafts() bool { return true }

// ReleaseView asks GitHub about the release for tag. gh 2.45 finds drafts too
// (a GraphQL lookup by pending tag), so "release not found" means none.
func (g *GitHub) ReleaseView(ctx context.Context, tag string) (Release, bool, error) {
	r, err := g.cli.Run(ctx, []string{"release", "view", tag, "--json", "isDraft,assets"}, nil)
	if err != nil {
		return Release{}, true, err
	}
	if r.ExitCode != 0 {
		if strings.Contains(strings.ToLower(string(r.Stderr)), "release not found") {
			return Release{}, true, nil
		}
		return Release{}, true, &Error{Kind: KindFailed, Code: 1, Message: "gh release view failed: " + strings.TrimSpace(string(r.Stderr))}
	}
	var out struct {
		IsDraft bool `json:"isDraft"`
		Assets  []struct {
			Name string `json:"name"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(r.Stdout, &out); err != nil {
		return Release{}, true, &Error{Kind: KindFailed, Code: 1, Message: "gh release view: unreadable output: " + err.Error()}
	}
	rel := Release{Found: true, IsDraft: out.IsDraft}
	for _, a := range out.Assets {
		rel.Assets = append(rel.Assets, a.Name)
	}
	return rel, true, nil
}

func (g *GitHub) ReleaseCreate(ctx context.Context, s ReleaseSpec) (string, error) {
	args := []string{"release", "create", s.Tag, "--title", s.Title, "--notes-file", s.Notes}
	if s.Draft {
		args = append(args, "--draft")
	}
	return g.lastLine(ctx, args, "", relFail("gh release create"))
}

func (g *GitHub) ReleaseEdit(ctx context.Context, s ReleaseSpec) (string, error) {
	args := []string{"release", "edit", s.Tag, "--title", s.Title, "--notes-file", s.Notes, "--draft=" + strconv.FormatBool(s.Draft)}
	return g.lastLine(ctx, args, "", relFail("gh release edit"))
}

// ---- GitLab -----------------------------------------------------------------

func (g *GitLab) PRNeedsBase() bool { return true }

func (g *GitLab) PRCreate(ctx context.Context, s PRSpec) (string, error) {
	return g.lastLine(ctx, []string{"mr", "create", "--title", s.Title, "--description", s.Body,
		"--source-branch", s.Head, "--target-branch", s.Base, "--yes"}, "", prFail("glab mr create"))
}

func (g *GitLab) ReleaseDrafts() bool { return false }

// ReleaseView: glab is not asked; checked is false, so a caller treats every
// tag as unreleased.
func (g *GitLab) ReleaseView(context.Context, string) (Release, bool, error) {
	return Release{}, false, nil
}

func (g *GitLab) ReleaseCreate(ctx context.Context, s ReleaseSpec) (string, error) {
	return g.lastLine(ctx, []string{"release", "create", s.Tag, "--name", s.Title, "--notes-file", s.Notes},
		"", relFail("glab release create"))
}

func (g *GitLab) ReleaseEdit(ctx context.Context, s ReleaseSpec) (string, error) {
	return "", failed("glab: finishing an existing release is not supported")
}
