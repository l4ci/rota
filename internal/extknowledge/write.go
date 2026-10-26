package extknowledge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

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

// checkLine refuses text that would break the one-line bullet format: a line
// break splits the bullet, "-->" closes its date stamp early.
func checkLine(what, s string) error {
	if strings.ContainsAny(s, "\r\n") {
		return fmt.Errorf("%w: %s must be a single line", ErrContent, what)
	}
	if strings.Contains(s, "-->") {
		return fmt.Errorf("%w: %s must not contain %q", ErrContent, what, "-->")
	}
	return nil
}

// topicFile resolves a topic name to its index entry and the topic file's
// content, the one path every write that touches an existing bullet goes
// through. An unknown topic (or a base without an index) is ErrNotFound; an
// indexed topic whose file is not written yet returns nil content.
func (c Clone) topicFile(name string) (Topic, []byte, error) {
	topics, err := c.Topics()
	if err != nil {
		return Topic{}, nil, err
	}
	t, ok := exact(name, topics)
	if !ok {
		return Topic{}, nil, fmt.Errorf("%w: topic %q is not in %s", ErrNotFound, name, c.indexName())
	}
	raw, err := c.readRegular(filepath.Join(c.Dir, filepath.FromSlash(t.File)))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return t, nil, nil
		}
		return Topic{}, nil, err
	}
	return t, raw, nil
}

// Add writes one bullet under the topic, creating the topic file and its
// index line when the topic is new, then lands it by the repo's policy. A
// bullet whose title already exists (case-insensitive) is a no-op.
func (c Clone) Add(ctx context.Context, in AddIn) (WriteResult, error) {
	for _, f := range []struct{ what, s string }{{"title", in.Title}, {"body", strings.TrimRight(in.Body, "\r\n")}, {"topic", in.Topic}, {"hook", in.Hook}, {"source", in.Source}} {
		if err := checkLine(f.what, f.s); err != nil {
			return WriteResult{}, err
		}
	}
	if strings.Contains(in.Title, "*") {
		return WriteResult{}, fmt.Errorf("%w: title must not contain %q", ErrContent, "*")
	}
	if err := c.syncForWrite(ctx); err != nil {
		return WriteResult{}, err
	}
	pol, err := c.Policy()
	if err != nil {
		return WriteResult{}, err
	}
	t, raw, err := c.topicFile(in.Topic)
	known := err == nil
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
	if len(in.Seen) > 0 {
		stamp += " seen:" + in.Seen.String()
	}
	bullet := fmt.Sprintf("- **%s** — %s <!-- %s -->", in.Title, strings.TrimRight(in.Body, "\n"), stamp)

	p := &plan{}
	if known {
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
		if t.Split {
			p.set(t.File, insertBulletBelowSubIndex(body, bullet))
		} else {
			p.set(t.File, insertBullet(body, bullet))
		}
		p.slug = t.Slug
	} else if parent, sub, ok, err := c.newSubtopic(in.Topic); err != nil {
		return WriteResult{}, err
	} else if ok {
		hook := in.Hook
		if hook == "" {
			hook = in.Title
		}
		subSlug := Slugify(sub)
		if subSlug == "" || subSlug == "readme" {
			return WriteResult{}, fmt.Errorf("%w: subtopic %q has no usable file name", ErrNotFound, sub)
		}
		dir := strings.TrimSuffix(parent.File, "/README.md")
		rel := dir + "/" + subSlug + ".md"
		if _, err := os.Lstat(filepath.Join(c.Dir, filepath.FromSlash(rel))); err == nil {
			return WriteResult{}, fmt.Errorf("%w: %s exists but %s does not list it", ErrContent, rel, parent.File)
		}
		readme, err := c.readRegular(filepath.Join(c.Dir, filepath.FromSlash(parent.File)))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return WriteResult{}, err
		}
		body := string(readme)
		if body == "" {
			body = "# " + parent.Name + "\n\n"
		}
		p.set(parent.File, insertSubIndex(body, fmt.Sprintf("- [%s](%s.md) — %s", sub, subSlug, hook)))
		p.set(rel, "# "+parent.Name+": "+sub+"\n\n"+bullet+"\n")
		p.slug = Slugify(parent.Name + " " + sub)
	} else {
		slug := Slugify(in.Topic)
		if slug == "" {
			return WriteResult{}, fmt.Errorf("%w: topic %q has no usable file name", ErrNotFound, in.Topic)
		}
		hook := in.Hook
		if hook == "" {
			hook = in.Title
		}
		idx, err := c.readRegular(filepath.Join(c.Dir, c.indexName()))
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
		p.set(c.indexName(), text)
		p.set(filepath.ToSlash(filepath.Join(TopicsDir, slug+".md")), "# "+in.Topic+"\n\n"+bullet+"\n")
		p.slug = slug
	}
	p.message = fmt.Sprintf("knowledge: add %q to %s", in.Title, in.Topic)
	return c.land(ctx, pol, p)
}

// newSubtopic reads a topic name of the form "Topic: Subtopic" that names no
// existing topic. ok is true when Topic is a split topic, so the subtopic can
// be created under it. A name with a second ": " level, or a subtopic under a
// flat topic, is refused (exit 3) with the way out: split the topic first. A
// name whose first part is no topic at all is an ordinary new flat topic.
func (c Clone) newSubtopic(name string) (parent Topic, sub string, ok bool, err error) {
	parts := strings.Split(name, ": ")
	if len(parts) < 2 {
		return Topic{}, "", false, nil
	}
	topics, err := c.Topics()
	if errors.Is(err, ErrNotFound) {
		return Topic{}, "", false, nil // no index yet: the first topic creates it
	}
	if err != nil {
		return Topic{}, "", false, err
	}
	head := strings.TrimSpace(parts[0])
	for _, t := range topics {
		if t.Parent != "" || !(strings.EqualFold(t.Name, head) || t.Slug == Slugify(head)) {
			continue
		}
		switch {
		case len(parts) > 2:
			return Topic{}, "", false, fmt.Errorf("%w: %q is two levels deep; subtopics nest one level (%s: <Subtopic>)", ErrNotFound, name, t.Name)
		case !t.Split:
			return Topic{}, "", false, fmt.Errorf("%w: %q is a flat topic, so it has no subtopics; split it first: rota knowledge split --external --topic %q", ErrNotFound, t.Name, t.Name)
		}
		sub = strings.TrimSpace(strings.Join(parts[1:], ": "))
		if sub == "" {
			return Topic{}, "", false, fmt.Errorf("%w: %q has an empty subtopic name", ErrNotFound, name)
		}
		return t, sub, true, nil
	}
	return Topic{}, "", false, nil
}

// insertBulletBelowSubIndex puts bullet above the first bullet of a split
// topic's README that is not a sub-index line, or at its end.
func insertBulletBelowSubIndex(body, bullet string) string {
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "- ") && !isSubIndexLine(l) {
			return strings.Join(append(lines[:i:i], append([]string{bullet}, lines[i:]...)...), "\n")
		}
	}
	return strings.TrimRight(body, "\n") + "\n\n" + bullet + "\n"
}

// insertSubIndex adds a sub-index line after the last one of a split topic's
// README, or below its title when it has none.
func insertSubIndex(body, line string) string {
	lines := strings.Split(body, "\n")
	last := -1
	for i, l := range lines {
		if isSubIndexLine(l) {
			last = i
		}
	}
	if last >= 0 {
		return strings.Join(append(lines[:last+1:last+1], append([]string{line}, lines[last+1:]...)...), "\n")
	}
	for i, l := range lines {
		if strings.HasPrefix(l, "- ") {
			return strings.Join(append(lines[:i:i], append([]string{line, ""}, lines[i:]...)...), "\n")
		}
	}
	return strings.TrimRight(body, "\n") + "\n\n" + line + "\n"
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

// Amend edits the one bullet under the topic that contains the fragment
// (case-sensitive): it appends text before the date stamp and rewrites the
// stamp's date and seen: list, then lands it by policy.
func (c Clone) Amend(ctx context.Context, in AmendIn) (WriteResult, error) {
	if err := checkLine("text", strings.TrimRight(in.Text, "\r\n")); err != nil {
		return WriteResult{}, err
	}
	if in.Text == "" && in.Date == "" && in.Seen == nil {
		return WriteResult{}, fmt.Errorf("%w: amend needs text, a date or a seen list", ErrUsage)
	}
	if in.Date != "" {
		if _, err := time.Parse("2006-01-02", in.Date); err != nil || !dateRe.MatchString(in.Date) {
			return WriteResult{}, fmt.Errorf("%w: date %q is not a real YYYY-MM-DD", ErrUsage, in.Date)
		}
	}
	e, err := c.beginEdit(ctx, in.Topic)
	if err != nil {
		return WriteResult{}, err
	}
	hit := -1
	for i, l := range e.lines {
		if strings.HasPrefix(l, "- ") && strings.Contains(l, in.Fragment) && !(e.topic.Split && isSubIndexLine(l)) {
			if hit >= 0 {
				return WriteResult{}, fmt.Errorf("%w: fragment %q under %q", ErrMultiMatch, in.Fragment, in.Topic)
			}
			hit = i
		}
	}
	if hit < 0 {
		return WriteResult{}, fmt.Errorf("%w: no bullet in %q contains %q", ErrNotFound, in.Topic, in.Fragment)
	}
	l := e.lines[hit]
	tail := stampTail.FindString(l)
	text := strings.TrimRight(strings.TrimSuffix(l, tail), " ")
	if in.Text != "" {
		text += " " + in.Text
	}
	stamp := strings.TrimSpace(tail)
	if in.Date != "" || in.Seen != nil {
		if stamp == "" && in.Date == "" {
			return WriteResult{}, fmt.Errorf("%w: the bullet has no stamp; give --date too so it gets one", ErrUsage)
		}
		stamp = editStamp(stamp, in.Date, in.Seen)
	}
	if stamp != "" {
		text += " " + stamp
	}
	e.lines[hit] = text
	return e.land(ctx, fmt.Sprintf("knowledge: amend %q in %s", strings.TrimSpace(in.Fragment), in.Topic))
}

// startBranch is the branch a write lands on when the policy names none: the
// upstream's branch name, else the current branch.
func (c Clone) startBranch(ctx context.Context, cur string) string {
	if up, err := c.out(ctx, "rev-parse", "--abbrev-ref", "@{u}"); err == nil {
		if _, name, ok := strings.Cut(up, "/"); ok && name != "" {
			return name
		}
	}
	return cur
}

// writable refuses a destination a write would follow out of the clone or
// clobber: anything that exists and is not a regular file, or a regular file
// git does not track.
func (c Clone) writable(ctx context.Context, rel, dst string) error {
	fi, err := os.Lstat(dst)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%w: %s is not a regular file; refusing to write through it", ErrDirty, rel)
	}
	if _, err := c.out(ctx, "ls-files", "--error-unmatch", "--", rel); err != nil {
		return fmt.Errorf("%w: untracked %s would be overwritten", ErrDirty, rel)
	}
	return nil
}

// land writes the plan, commits it and follows the policy: push to the policy
// branch (one pull --rebase and retry when rejected), or push a branch and
// open a PR. The commit message carries no trailer.
func (c Clone) land(ctx context.Context, pol Policy, p *plan) (res WriteResult, err error) {
	res = WriteResult{Changed: true}
	res.File = p.order[len(p.order)-1]
	remote := c.hasRemote(ctx)
	cur, err := c.out(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return res, err
	}
	if pol.Branch == "" {
		if cur == "HEAD" {
			return res, fmt.Errorf("%w: %s is on a detached HEAD and the policy names no branch", ErrPolicy, c.Dir)
		}
		pol.Branch = c.startBranch(ctx, cur)
		if !branchRe.MatchString(pol.Branch) || strings.Contains(pol.Branch, "..") {
			return res, fmt.Errorf("%w: branch %q is not a plain branch name", ErrPolicy, pol.Branch)
		}
	} else if pol.Write == "push" && cur != pol.Branch {
		return res, fmt.Errorf("%w: %s is on %q but the policy branch is %q; switch it first", ErrPolicy, c.Dir, cur, pol.Branch)
	}
	res.Policy = pol
	for _, rel := range p.order {
		if err := c.writable(ctx, rel, filepath.Join(c.Dir, filepath.FromSlash(rel))); err != nil {
			return res, err
		}
	}
	if pol.Write == "pr" {
		res.Branch = fmt.Sprintf("rota-knowledge/%s-%s", p.slug, c.now().UTC().Format("20060102T150405"))
		if _, err := c.out(ctx, "switch", "-q", "-c", res.Branch); err != nil {
			return res, err
		}
		committed := false
		defer func() {
			if !committed {
				// Drop the half-written plan and the branch it started.
				_, _ = c.out(ctx, "reset", "-q", "--hard", "HEAD")
				_, _ = c.out(ctx, append([]string{"clean", "-fdq", "--"}, p.order...)...)
			}
			_, _ = c.out(ctx, "switch", "-q", cur)
			if !committed {
				_, _ = c.out(ctx, "branch", "-D", res.Branch)
			}
		}()
		if err := c.writeAndCommit(ctx, p); err != nil {
			return res, err
		}
		committed = true
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
	if err := c.writeAndCommit(ctx, p); err != nil {
		return res, err
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

// writeAndCommit writes the plan's files into the clone and commits them.
func (c Clone) writeAndCommit(ctx context.Context, p *plan) error {
	for _, rel := range p.order {
		dst := filepath.Join(c.Dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := c.insideClone(filepath.Dir(dst)); err != nil {
			return err
		}
		if err := c.writable(ctx, rel, dst); err != nil {
			return err
		}
		if err := os.WriteFile(dst, []byte(p.files[rel]), 0o644); err != nil {
			return err
		}
	}
	if _, err := c.out(ctx, append([]string{"add", "--"}, p.order...)...); err != nil {
		return err
	}
	_, err := c.out(ctx, "commit", "-q", "-m", p.message)
	return err
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
