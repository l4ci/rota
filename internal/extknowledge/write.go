package extknowledge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/l4ci/rota/internal/proc"
)

var (
	slugRe    = regexp.MustCompile(`[^a-z0-9]+`)
	stampTail = regexp.MustCompile(`\s*<!--[^>]*-->\s*$`)
	titleRe   = regexp.MustCompile(`^- \*\*([^*]+)\*\*`)
)

// Slugify is the topic file name for a topic name.
func Slugify(name string) string {
	return strings.Trim(slugRe.ReplaceAllString(strings.ToLower(name), "-"), "-")
}

// exact finds the index entry whose name or slug equals want
// (case-insensitive). Writes never fall back to a partial match.
func exact(want string, topics []Topic) (Topic, bool) {
	w := strings.ToLower(strings.TrimSpace(want))
	for _, t := range topics {
		if strings.ToLower(t.Name) == w || t.Slug == w || t.Slug == Slugify(want) {
			return t, true
		}
	}
	return Topic{}, false
}

// plan is the files a write changes, by repo-relative path.
type plan struct {
	files   map[string]string
	order   []string
	message string
	slug    string
}

func (p *plan) set(rel, content string) {
	if p.files == nil {
		p.files = map[string]string{}
	}
	if _, ok := p.files[rel]; !ok {
		p.order = append(p.order, rel)
	}
	p.files[rel] = content
}

// Add writes one bullet under the topic, creating the topic file and its
// INDEX.md line when the topic is new, then lands it by the repo's policy. A
// bullet whose title already exists (case-insensitive) is a no-op.
func (c Clone) Add(ctx context.Context, in AddIn) (WriteResult, error) {
	if err := c.syncForWrite(ctx); err != nil {
		return WriteResult{}, err
	}
	pol, err := c.Policy()
	if err != nil {
		return WriteResult{}, err
	}
	topics, err := c.Topics()
	if err != nil && !errors.Is(err, ErrNotFound) {
		return WriteResult{}, err
	}
	date := in.Date
	if date == "" {
		date = c.now().Format("2006-01-02")
	}
	stamp := date
	if in.Source != "" {
		stamp += " source:" + in.Source
	}
	bullet := fmt.Sprintf("- **%s** — %s <!-- %s -->", in.Title, strings.TrimRight(in.Body, "\n"), stamp)

	p := &plan{}
	if t, ok := exact(in.Topic, topics); ok {
		raw, err := c.readRegular(c.topicPath(t.Slug))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return WriteResult{}, err
		}
		body := string(raw)
		if body == "" {
			body = "# " + t.Name + "\n\n"
		}
		lower := strings.ToLower(in.Title)
		for _, l := range bulletLines(body) {
			if m := titleRe.FindStringSubmatch(l); m != nil && strings.ToLower(m[1]) == lower {
				return WriteResult{Policy: pol}, nil
			}
		}
		p.set(filepath.ToSlash(filepath.Join(TopicsDir, t.Slug+".md")), insertBullet(body, bullet))
		p.slug = t.Slug
	} else {
		slug := Slugify(in.Topic)
		if slug == "" {
			return WriteResult{}, fmt.Errorf("%w: topic %q has no usable file name", ErrNotFound, in.Topic)
		}
		hook := in.Hook
		if hook == "" {
			hook = in.Title
		}
		idx, err := c.readRegular(filepath.Join(c.Dir, IndexFile))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return WriteResult{}, err
		}
		text := string(idx)
		if text == "" {
			text = "# Knowledge index\n\n"
		} else if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += fmt.Sprintf("- [%s](%s/%s.md) — %s\n", in.Topic, TopicsDir, slug, hook)
		p.set(IndexFile, text)
		p.set(filepath.ToSlash(filepath.Join(TopicsDir, slug+".md")), "# "+in.Topic+"\n\n"+bullet+"\n")
		p.slug = slug
	}
	p.message = fmt.Sprintf("knowledge: add %q to %s", in.Title, in.Topic)
	return c.land(ctx, pol, p)
}

// insertBullet puts bullet above the first bullet of body, or at its end.
func insertBullet(body, bullet string) string {
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "- ") {
			return strings.Join(append(lines[:i:i], append([]string{bullet}, lines[i:]...)...), "\n")
		}
	}
	return strings.TrimRight(body, "\n") + "\n\n" + bullet + "\n"
}

// Amend appends text to the one bullet under the topic that contains the
// fragment (case-sensitive), before its date stamp, then lands it by policy.
func (c Clone) Amend(ctx context.Context, in AmendIn) (WriteResult, error) {
	if err := c.syncForWrite(ctx); err != nil {
		return WriteResult{}, err
	}
	pol, err := c.Policy()
	if err != nil {
		return WriteResult{}, err
	}
	topics, err := c.Topics()
	if err != nil {
		return WriteResult{}, err
	}
	t, ok := exact(in.Topic, topics)
	if !ok {
		return WriteResult{}, fmt.Errorf("%w: topic %q is not in %s", ErrNotFound, in.Topic, IndexFile)
	}
	raw, err := c.readRegular(c.topicPath(t.Slug))
	if err != nil {
		return WriteResult{}, fmt.Errorf("%w: topic file %s/%s.md: %v", ErrNotFound, TopicsDir, t.Slug, err)
	}
	lines := strings.Split(string(raw), "\n")
	hit := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "- ") && strings.Contains(l, in.Fragment) {
			if hit >= 0 {
				return WriteResult{}, fmt.Errorf("%w: fragment %q under %q", ErrMultiMatch, in.Fragment, in.Topic)
			}
			hit = i
		}
	}
	if hit < 0 {
		return WriteResult{}, fmt.Errorf("%w: no bullet in %q contains %q", ErrNotFound, in.Topic, in.Fragment)
	}
	l := lines[hit]
	tail := stampTail.FindString(l)
	lines[hit] = strings.TrimRight(strings.TrimSuffix(l, tail), " ") + " " + in.Text + tail
	p := &plan{message: fmt.Sprintf("knowledge: amend %q in %s", strings.TrimSpace(in.Fragment), in.Topic), slug: t.Slug}
	p.set(filepath.ToSlash(filepath.Join(TopicsDir, t.Slug+".md")), strings.Join(lines, "\n"))
	return c.land(ctx, pol, p)
}

// land writes the plan, commits it and follows the policy: push to the policy
// branch (one pull --rebase and retry when rejected), or push a branch and
// open a PR. The commit message carries no trailer.
func (c Clone) land(ctx context.Context, pol Policy, p *plan) (WriteResult, error) {
	res := WriteResult{Changed: true, Policy: pol}
	res.File = p.order[len(p.order)-1]
	remote := c.hasRemote(ctx)
	cur := ""
	if pol.Write == "pr" {
		var err error
		if cur, err = c.out(ctx, "rev-parse", "--abbrev-ref", "HEAD"); err != nil {
			return res, err
		}
		res.Branch = fmt.Sprintf("rota-knowledge/%s-%s", p.slug, c.now().UTC().Format("20060102T150405"))
		if _, err := c.out(ctx, "switch", "-q", "-c", res.Branch); err != nil {
			return res, err
		}
	}
	for _, rel := range p.order {
		dst := filepath.Join(c.Dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return res, err
		}
		if err := c.insideClone(filepath.Dir(dst)); err != nil {
			return res, err
		}
		if err := os.WriteFile(dst, []byte(p.files[rel]), 0o644); err != nil {
			return res, err
		}
	}
	if _, err := c.out(ctx, append([]string{"add", "--"}, p.order...)...); err != nil {
		return res, err
	}
	if _, err := c.out(ctx, "commit", "-q", "-m", p.message); err != nil {
		return res, err
	}
	if pol.Write == "pr" {
		defer func() { _, _ = c.out(ctx, "switch", "-q", cur) }()
		if !remote {
			return res, fmt.Errorf("%w: pr mode needs a remote on %s", ErrPush, c.Dir)
		}
		if _, err := c.out(ctx, "push", "-q", "-u", "origin", res.Branch); err != nil {
			return res, fmt.Errorf("%w: %v", ErrPush, err)
		}
		url, err := c.openPR(ctx, pol, res.Branch, p.message)
		if err != nil {
			return res, err
		}
		res.PR = url
		return res, nil
	}
	if !remote {
		return res, nil
	}
	refspec := "HEAD:" + pol.Branch
	if _, err := c.out(ctx, "push", "-q", "origin", refspec); err != nil {
		if _, perr := c.out(ctx, "pull", "-q", "--rebase", "origin", pol.Branch); perr != nil {
			_, _ = c.out(ctx, "rebase", "--abort")
			return res, fmt.Errorf("%w: rejected, and pull --rebase failed: %v (the commit stays local)", ErrPush, perr)
		}
		res.Retried = true
		if _, err := c.out(ctx, "push", "-q", "origin", refspec); err != nil {
			return res, fmt.Errorf("%w: %v (the commit stays local)", ErrPush, err)
		}
	}
	res.Pushed = true
	return res, nil
}

func (c Clone) openPR(ctx context.Context, pol Policy, branch, title string) (string, error) {
	run := c.Proc
	if run == nil {
		run = proc.Run
	}
	r, err := run(ctx, proc.Cmd{Name: "gh", Dir: c.Dir, Args: []string{
		"pr", "create", "--base", pol.Branch, "--head", branch, "--title", title,
		"--body", "Opened by `rota knowledge` from the external knowledge base policy (write: pr).",
	}})
	if err != nil {
		return "", fmt.Errorf("%w: gh pr create: %v (branch %s is pushed)", ErrPush, err, branch)
	}
	if r.ExitCode != 0 {
		return "", fmt.Errorf("%w: gh pr create: %s (branch %s is pushed)", ErrPush, firstLine(r.Stderr, r.Stdout), branch)
	}
	return strings.TrimSpace(r.Stdout), nil
}
