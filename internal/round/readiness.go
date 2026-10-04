package round

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/gate"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/worker"
)

// Readiness check names.
const (
	CheckCriteria     = "criteria"
	CheckDependencies = "dependencies"
	CheckOverlap      = "overlap"
)

// Check is one readiness check of one item.
type Check struct {
	Name   string
	OK     bool
	Detail []string
}

// Overlap says which in-flight item an item's footprint collides with.
type Overlap struct {
	With  string
	Slot  string
	Paths []string
}

// Readiness is the verdict on one item: ready when every check holds.
type Readiness struct {
	ID       string
	Checks   []Check
	Overlaps []Overlap
}

// Ready is true when every check holds.
func (r Readiness) Ready() bool {
	for _, c := range r.Checks {
		if !c.OK {
			return false
		}
	}
	return true
}

// InFlight is an item a slot holds, with the paths its work touches.
type InFlight struct {
	Slot, Issue string
	Paths       []string
}

var (
	filesHeadingRe = regexp.MustCompile(`(?mi)^#{1,6}[ \t]+files(?:[ \t]+touched)?[ \t]*$`)
	dependsHeadRe  = regexp.MustCompile(`(?mi)^#{1,6}[ \t]+depends[ \t]+on[ \t]*$`)
	headingRe      = regexp.MustCompile(`(?m)^#{1,6}[ \t]`)
	pathTokenRe    = regexp.MustCompile("[A-Za-z0-9_./*@+-]+")
	foreignRefRe   = regexp.MustCompile(`https?://\S+|[\w.-]+/[\w.-]+#\d+`)
	issueRefRe     = regexp.MustCompile(`#(\d+)`)
	itemRefRe      = regexp.MustCompile(`\b([BFT]\d{2,})\b`)
)

// section is the text under the first heading matching head, up to the next
// heading. ok is false when there is none.
func section(text string, head *regexp.Regexp) (string, bool) {
	loc := head.FindStringIndex(text)
	if loc == nil {
		return "", false
	}
	rest := text[loc[1]:]
	if n := headingRe.FindStringIndex(rest); n != nil {
		rest = rest[:n[0]]
	}
	return rest, true
}

// Dependencies parses the `## Depends on` section into item references (in
// the item's own spelling) and references that cannot be looked up.
func Dependencies(text string) (refs, unverifiable []string) {
	sec, ok := section(text, dependsHeadRe)
	if !ok {
		return nil, nil
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(sec, "\n") {
		for _, f := range foreignRefRe.FindAllString(line, -1) {
			unverifiable = append(unverifiable, strings.TrimRight(f, ".,;:)"))
		}
		line = foreignRefRe.ReplaceAllString(line, " ")
		for _, m := range issueRefRe.FindAllStringSubmatch(line, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				refs = append(refs, m[1])
			}
		}
		for _, m := range itemRefRe.FindAllStringSubmatch(line, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				refs = append(refs, m[1])
			}
		}
	}
	return refs, unverifiable
}

// Footprint is the set of repo paths an item says it touches: its `## Files`
// section when present (one path or glob per bullet), otherwise every token of
// text that is a tracked file or the directory (two or more segments) of one.
// Directories end in "/". shared globs are dropped.
func Footprint(text string, tracked, shared []string) []string {
	var out []string
	if sec, ok := section(text, filesHeadingRe); ok {
		for _, line := range strings.Split(sec, "\n") {
			line = strings.TrimSpace(line)
			line = strings.TrimLeft(line, "-*+ \t")
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			tok := strings.Fields(line)[0]
			out = append(out, strings.Trim(tok, "`,;"))
		}
	} else {
		files := map[string]bool{}
		dirs := map[string]bool{}
		for _, f := range tracked {
			files[f] = true
			for d := path.Dir(f); d != "." && d != "/"; d = path.Dir(d) {
				if strings.Contains(d, "/") {
					dirs[d] = true
				}
			}
		}
		seen := map[string]bool{}
		for _, tok := range pathTokenRe.FindAllString(text, -1) {
			tok = strings.TrimLeft(strings.Trim(tok, ".,:;"), "./")
			if tok == "" || seen[tok] {
				continue
			}
			switch {
			case files[tok]:
			case dirs[strings.TrimSuffix(tok, "/")]:
				tok = strings.TrimSuffix(tok, "/") + "/"
			default:
				continue
			}
			seen[tok] = true
			out = append(out, tok)
		}
	}
	var kept []string
	for _, p := range out {
		skip := false
		for _, g := range shared {
			if gate.MatchPath(g, strings.TrimSuffix(p, "/")) {
				skip = true
			}
		}
		if !skip {
			kept = append(kept, p)
		}
	}
	sort.Strings(kept)
	return kept
}

// Overlaps lists the paths two footprints share: equal paths, a path matching
// the other's glob, or a file under the other's directory.
func Overlaps(a, b []string) []string {
	hit := map[string]bool{}
	for _, x := range a {
		for _, y := range b {
			switch {
			case x == y:
				hit[x] = true
			case strings.HasSuffix(x, "/") && strings.HasPrefix(y, x):
				hit[y] = true
			case strings.HasSuffix(y, "/") && strings.HasPrefix(x, y):
				hit[x] = true
			default:
				if ok, _ := path.Match(x, y); ok {
					hit[y] = true
				} else if ok, _ := path.Match(y, x); ok {
					hit[x] = true
				}
			}
		}
	}
	var out []string
	for p := range hit {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// itemText is an item's body and the text of its comments: what a footprint
// is read from.
func itemText(be backlog.Backend, id string) string {
	text, _, _ := be.Detail(id)
	if cs, err := be.Comments(id, ""); err == nil {
		for _, c := range cs {
			text += "\n" + c.Text
		}
	}
	return text
}

// Assess runs the three readiness checks on one item. inFlight is what other
// slots hold; shared and tracked feed the footprint. acceptOverlap records the
// overlap but does not fail the check.
func Assess(be backlog.Backend, id string, tracked, shared []string, inFlight []InFlight, acceptOverlap bool) (Readiness, error) {
	r := Readiness{ID: id}
	if _, err := be.Get(id); err != nil {
		return r, err
	}

	reasons, err := be.Ready(id)
	if err != nil {
		return r, err
	}
	r.Checks = append(r.Checks, Check{CheckCriteria, len(reasons) == 0, nonNil(reasons)})

	text := itemText(be, id)
	refs, unverifiable := Dependencies(text)
	dep := Check{Name: CheckDependencies, OK: true, Detail: []string{}}
	for _, ref := range unverifiable {
		dep.OK = false
		dep.Detail = append(dep.Detail, fmt.Sprintf("cannot verify %s: edit the issue to name the dependency as #N, or drop it from Depends on", ref))
	}
	for _, ref := range refs {
		it, err := be.Get(ref)
		switch {
		case errors.Is(err, backlog.ErrNotFound):
			dep.OK = false
			dep.Detail = append(dep.Detail, fmt.Sprintf("cannot verify %s: not found; edit the issue to fix the reference", ref))
		case err != nil:
			return r, err
		case !it.Closed:
			dep.OK = false
			dep.Detail = append(dep.Detail, fmt.Sprintf("%s is not done", ref))
		}
	}
	r.Checks = append(r.Checks, dep)

	mine := Footprint(text, tracked, shared)
	ov := Check{Name: CheckOverlap, OK: true, Detail: []string{}}
	for _, f := range inFlight {
		if f.Issue == id {
			continue
		}
		if paths := Overlaps(mine, f.Paths); len(paths) > 0 {
			r.Overlaps = append(r.Overlaps, Overlap{With: f.Issue, Slot: f.Slot, Paths: paths})
			ov.Detail = append(ov.Detail, fmt.Sprintf("%s (held by %s): %s", f.Issue, f.Slot, strings.Join(paths, ", ")))
			if !acceptOverlap {
				ov.OK = false
			}
		}
	}
	r.Checks = append(r.Checks, ov)
	return r, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// InFlightItems reads what the registry's slots hold and what their work
// touches: the item's own footprint plus the slot's real changes.
func (e Env) InFlightItems(ctx context.Context, root string, be backlog.Backend, tracked, shared []string) []InFlight {
	var out []InFlight
	reg := worker.LoadRegistry(root)
	for _, s := range reg.Slots() {
		name := worker.Str(s, "name")
		branch := worker.Str(s, "branch")
		id := heldID(worker.Str(s, "task"), branch, name)
		if id == "" {
			continue
		}
		paths := Footprint(itemText(be, id), tracked, shared)
		if wt := worker.Str(s, "worktree"); wt != "" {
			base := worker.Str(s, "base")
			if base == "" {
				base = e.Base
			}
			paths = append(paths, e.changed(ctx, wt, base, shared)...)
		}
		sort.Strings(paths)
		out = append(out, InFlight{Slot: name, Issue: id, Paths: paths})
	}
	for _, q := range reg.PRs() {
		id := queuedIssue(q)
		if id == "" {
			continue
		}
		paths := Footprint(itemText(be, id), tracked, shared)
		paths = append(paths, e.queuedChanged(ctx, root, q, shared)...)
		sort.Strings(paths)
		out = append(out, InFlight{Slot: "queue:" + worker.Str(q, "from"), Issue: id, Paths: paths})
	}
	return out
}

// queuedChanged lists the paths a queued PR's branch changes against its base,
// read from the pushed branch in root (the local one when origin lacks it).
func (e Env) queuedChanged(ctx context.Context, root string, q *jsonx.Object, shared []string) []string {
	branch := worker.Str(q, "branch")
	base := firstNonEmpty(worker.Str(q, "base"), e.Base)
	if branch == "" || base == "" {
		return nil
	}
	head := branch
	if _, _, code, err := e.Git(ctx, root, "rev-parse", "--verify", "-q", "refs/remotes/origin/"+branch); err == nil && code == 0 {
		head = "origin/" + branch
	}
	ref := base
	if _, _, code, err := e.Git(ctx, root, "rev-parse", "--verify", "-q", "origin/"+base); err == nil && code == 0 {
		ref = "origin/" + base
	}
	out, _, code, err := e.Git(ctx, root, "diff", "--name-only", ref+"..."+head)
	if err != nil || code != 0 {
		return nil
	}
	var paths []string
next:
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			for _, g := range shared {
				if gate.MatchPath(g, l) {
					continue next
				}
			}
			paths = append(paths, l)
		}
	}
	return paths
}

// changed lists the paths a worktree changed against the base: commits on its
// branch and uncommitted files.
func (e Env) changed(ctx context.Context, wt, base string, shared []string) []string {
	seen := map[string]bool{}
	add := func(p string) {
		for _, g := range shared {
			if gate.MatchPath(g, p) {
				return
			}
		}
		seen[p] = true
	}
	ref := base
	if _, _, code, err := e.Git(ctx, wt, "rev-parse", "--verify", "-q", "origin/"+base); err == nil && code == 0 {
		ref = "origin/" + base
	}
	if out, _, code, err := e.Git(ctx, wt, "diff", "--name-only", ref+"...HEAD"); err == nil && code == 0 {
		for _, l := range strings.Split(out, "\n") {
			if l = strings.TrimSpace(l); l != "" {
				add(l)
			}
		}
	}
	if out, _, code, err := e.Git(ctx, wt, "status", "--porcelain"); err == nil && code == 0 {
		for _, l := range strings.Split(out, "\n") {
			if len(l) > 3 {
				p := strings.TrimSpace(l[3:])
				if i := strings.Index(p, " -> "); i >= 0 {
					p = p[i+4:]
				}
				add(p)
			}
		}
	}
	var out []string
	for p := range seen {
		out = append(out, p)
	}
	return out
}

// heldID is the item a slot holds, in the backend's spelling: the task when
// set (`#12`, `12`, `B07`), else the number leading `<agent>/<issue>-<slug>`.
func heldID(task, branch, name string) string {
	t := strings.TrimPrefix(strings.TrimSpace(task), "#")
	if t != "" {
		return strings.ToUpper(t)
	}
	return issueOf("", branch, name)
}
