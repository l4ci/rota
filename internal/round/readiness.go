package round

import (
	"context"
	"fmt"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/strutil"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/exitmap"
	"github.com/l4ci/rota/internal/itembody"
	"github.com/l4ci/rota/internal/overlap"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// Readiness check names.
const (
	CheckCriteria     = "criteria"
	CheckDependencies = "dependencies"
	CheckOverlap      = "overlap"
	CheckTemplate     = "template"
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
	// Dirs are the directory pointers (trailing slash) behind Paths: the
	// clash comes from a directory, not from both issues naming the file.
	Dirs   []string
	Scopes []string
	// Inferred is true when a scope clash involves an inferred entry: it
	// only warns, whatever round.scopeOverlap says.
	Inferred bool
}

// String is the overlap as text: the holder, then the declared scopes the two
// share ("overlaps #12 (ben): scopes post /items").
func (o Overlap) String() string {
	w := "overlaps #" + strings.TrimPrefix(o.With, "#")
	if o.Slot != "" {
		w += " (" + o.Slot + ")"
	}
	if len(o.Scopes) > 0 {
		label := ": scopes "
		if o.Inferred {
			label = ": inferred scopes "
		}
		w += label + strings.Join(o.Scopes, ", ")
	}
	return w
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
	Scopes      []string
	// ScopeSource is where Scopes came from (the Source* constants).
	ScopeSource string
	// FromPR is true when Paths are the open PR's changed files rather than the
	// item's footprint; its Scopes then hold only declared `## Touches` entries.
	FromPR bool
	// PRFilesErr says why an open PR's files could not be read, so Paths and
	// Scopes fell back to the footprint and the inferred scope. "" otherwise.
	PRFilesErr string
}

var (
	pathTokenRe  = regexp.MustCompile("[A-Za-z0-9_./*@+-]+")
	foreignRefRe = regexp.MustCompile(`https?://\S+|[\w.-]+/[\w.-]+#\d+`)
	issueRefRe   = regexp.MustCompile(`#(\d+)`)
	keyRefRe     = regexp.MustCompile(`\b([A-Z][A-Z0-9_]*-\d+)\b`) // a key-based tracker's PROJ-123
	// listKeyRe is a key heading a list item (`- OTHER-7`): a dependency, even
	// of another project, where a key-shaped word inside a sentence is prose.
	listKeyRe = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s+([A-Za-z][A-Za-z0-9_]*-\d+)\b`)
	itemRefRe = regexp.MustCompile(`\b(` + backlog.IDPattern(backlog.FileIDDigits) + `)\b`)
)

// keyMatches is the PROJ-123 refs of project on line; none when project is "".
// Another prefix (UTF-8, ISO-8601, another project) is prose, not a ref.
func keyMatches(line, project string) [][]string {
	if project == "" {
		return nil
	}
	var out [][]string
	for _, m := range keyRefRe.FindAllStringSubmatch(line, -1) {
		if tracker.IssueKey(m[1]).Project() == project {
			out = append(out, m)
		}
	}
	return out
}

// dependencies parses the `## Depends on` section into item references (in
// the item's own spelling) and references that cannot be looked up. A project
// key also reads a key-based tracker's refs of that project (PROJ-123). A list item headed by another
// project's key is unverifiable; such a word inside a sentence (UTF-8) is prose.
func dependencies(text, project string) (refs, unverifiable []string) {
	sec, ok := itembody.Section(text, itembody.DependsHeadRe)
	if !ok {
		return nil, nil
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(sec, "\n") {
		for _, f := range foreignRefRe.FindAllString(line, -1) {
			unverifiable = append(unverifiable, strings.TrimRight(f, ".,;:)"))
		}
		line = foreignRefRe.ReplaceAllString(line, " ")
		if m := listKeyRe.FindStringSubmatch(line); project != "" && m != nil {
			if k := tracker.IssueKey(strings.ToUpper(m[1])); k.Project() != project {
				unverifiable = append(unverifiable, string(k))
			}
		}
		for _, m := range issueRefRe.FindAllStringSubmatch(line, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				refs = append(refs, m[1])
			}
		}
		for _, m := range keyMatches(line, project) {
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
	return footprint(text, "", tracked, shared)
}

// footprint is Footprint where extra (an item's comments) feeds only the
// inference fallback: the `## Files` section is read from body alone, so a
// comment cannot become part of the last section.
func footprint(body, extra string, tracked, shared []string) []string {
	var out []string
	if sec, ok := itembody.Section(body, itembody.FilesHeadRe); ok {
		for _, line := range strings.Split(sec, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "- ") && !strings.HasPrefix(line, "* ") {
				continue
			}
			line = strings.TrimSpace(line[2:])
			if line == "" {
				continue
			}
			tok := strings.Fields(line)[0]
			if tok = overlap.Clean(strings.Trim(tok, "`,;")); tok != "" {
				out = append(out, tok)
			}
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
		for _, tok := range pathTokenRe.FindAllString(body+"\n"+extra, -1) {
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
		if !overlap.Shared(shared, strings.TrimSuffix(p, "/")) {
			kept = append(kept, p)
		}
	}
	sort.Strings(kept)
	return kept
}

// itemParts is an item's body and, separately, the text of its comments, each
// comment on its own line.
func itemParts(be backlog.Backend, id string) (body, comments string) {
	body, _, _ = be.Detail(id)
	if cs, err := be.Comments(id, ""); err == nil {
		for _, c := range cs {
			comments += "\n" + c.Text
		}
	}
	return body, comments
}

// itemFootprint is the footprint of an item: `## Files` from its body, else
// path tokens from body and comments.
func itemFootprint(be backlog.Backend, id string, tracked, shared []string) []string {
	body, comments := itemParts(be, id)
	return footprint(body, comments, tracked, shared)
}

// Assess runs the readiness checks on one item. inFlight is what other
// slots hold; shared and tracked feed the footprint. acceptOverlap records the
// overlap but does not fail the check.
func Assess(be backlog.Backend, id string, tracked, shared []string, inFlight []InFlight, acceptOverlap bool) (Readiness, error) {
	return AssessBrief(be, id, tracked, shared, inFlight, acceptOverlap, "")
}

// AssessBrief is Assess where the acceptance criteria may come from brief, the
// text an orchestrator passes to assign (--settled-file): a brief that states
// criteria makes the item startable without a design or plan note, so a round
// needs no bookkeeping PR before its first assign.
func AssessBrief(be backlog.Backend, id string, tracked, shared []string, inFlight []InFlight, acceptOverlap bool, brief string) (Readiness, error) {
	return AssessScoped(be, id, tracked, shared, inFlight, acceptOverlap, brief, false)
}

// AssessScoped is AssessBrief where a clash on declared scopes (`## Touches`,
// else the Subsystem field) fails the overlap check when scopeBlock, as a
// shared path does, and only warns otherwise.
func AssessScoped(be backlog.Backend, id string, tracked, shared []string, inFlight []InFlight, acceptOverlap bool, brief string, scopeBlock bool) (Readiness, error) {
	r := Readiness{ID: id}
	self, err := be.Get(id)
	if err != nil {
		return r, err
	}

	reasons, err := be.Ready(id)
	if err != nil {
		return r, err
	}
	if len(reasons) > 0 && backlog.HasCriteria(brief) {
		reasons = nil
	}
	r.Checks = append(r.Checks, Check{CheckCriteria, len(reasons) == 0, nonNil(reasons)})

	// Dependencies come from the body alone: a comment cannot extend the last
	// section, and an item is never its own dependency.
	body, _ := itemParts(be, id)
	project := self.IssueKey().Project()
	refs, unverifiable := dependencies(body, project)
	refs = slices.DeleteFunc(refs, func(ref string) bool { return worker.NormID(ref) == worker.NormID(id) })
	dep := Check{Name: CheckDependencies, OK: true, Detail: []string{}}
	for _, ref := range unverifiable {
		dep.OK = false
		as := "#N"
		if project != "" {
			as = project + "-N"
		}
		dep.Detail = append(dep.Detail, fmt.Sprintf("cannot verify %s: edit the issue to name the dependency as %s, or drop it from Depends on", ref, as))
	}
	for _, ref := range refs {
		it, err := be.Get(ref)
		switch {
		case exitmap.IsNotFound(err):
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

	mine := itemFootprint(be, id, tracked, shared)
	myScopes, myScopeSrc := ItemScopeSource(be, id)
	ov := Check{Name: CheckOverlap, OK: true, Detail: []string{}}
	for _, f := range inFlight {
		if worker.NormID(f.Issue) == worker.NormID(id) {
			continue
		}
		// Finished work no longer collides: its issue is closed, its PR merged.
		if it, err := be.Get(f.Issue); err == nil && it.Closed {
			continue
		}
		paths, scopes := overlap.Paths(mine, f.Paths), ScopeOverlaps(myScopes, f.Scopes)
		if len(paths) == 0 && len(scopes) == 0 {
			continue
		}
		inferred := len(scopes) > 0 && (myScopeSrc == SourceInferred || f.ScopeSource == SourceInferred)
		dirs := overlap.Dirs(mine, f.Paths)
		r.Overlaps = append(r.Overlaps, Overlap{With: f.Issue, Slot: f.Slot, Paths: paths, Dirs: dirs, Scopes: scopes, Inferred: inferred})
		detail := overlapDetail(f.Issue, f.Slot, paths, scopes, inferred)
		if len(dirs) > 0 {
			detail += fmt.Sprintf(" [from directory %s: name exact files in the pointers]", strings.Join(dirs, ", "))
		}
		if f.PRFilesErr != "" {
			detail += fmt.Sprintf(" [open PR's files unavailable (%s): compared the item's footprint and scope]", f.PRFilesErr)
		}
		ov.Detail = append(ov.Detail, detail)
		if !acceptOverlap && (len(paths) > 0 || (scopeBlock && !inferred)) {
			ov.OK = false
		}
	}
	r.Checks = append(r.Checks, ov)

	template := Check{Name: CheckTemplate, OK: true, Detail: []string{}}
	decision, err := DecisionOf(be.Capabilities(), *self)
	if err != nil {
		return r, err
	}
	if decision == "" {
		if typ, ok := backlog.TypeByLetter(self.Type); ok {
			for _, missing := range itembody.CheckTemplate(typ.Kind, self.Tag, body+"\n\n"+brief) {
				if missing.Level == itembody.Required {
					template.OK = false
					template.Detail = append(template.Detail, "missing required section: ## "+missing.Name)
				}
			}
		}
	}
	r.Checks = append(r.Checks, template)
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
	reg := worker.LoadRegistryTolerant(root) // no error return; the verbs that call this read the registry strictly themselves
	prs := e.openPRFiles(ctx)
	for _, s := range reg.Slots() {
		name := s.Name()
		branch := s.Branch()
		id := worker.HeldID(s.Task(), branch, name)
		if id == "" {
			continue
		}
		paths := itemFootprint(be, id, tracked, shared)
		sc, src := ItemScopeSource(be, id)
		if wt := s.Worktree(); wt != "" {
			base := s.Base()
			if base == "" {
				base = e.Base
			}
			paths = append(paths, e.changed(ctx, wt, base, shared)...)
		}
		sort.Strings(paths)
		x := InFlight{Slot: name, Issue: id, Paths: paths, Scopes: sc, ScopeSource: src}
		out = append(out, prs.apply(be, x, branch, shared))
		out = append(out, memberInFlight(be, name, s.Bundle(), tracked, shared)...)
	}
	for _, q := range reg.PRs() {
		id := queuedIssue(q)
		if id == "" {
			continue
		}
		paths := itemFootprint(be, id, tracked, shared)
		sc, src := ItemScopeSource(be, id)
		paths = append(paths, e.queuedChanged(ctx, root, q, shared)...)
		sort.Strings(paths)
		x := InFlight{Slot: "queue:" + q.From, Issue: id, Paths: paths, Scopes: sc, ScopeSource: src}
		out = append(out, prs.apply(be, x, q.Branch, shared))
		out = append(out, memberInFlight(be, "queue:"+q.From, q.Bundle, tracked, shared)...)
	}
	return out
}

// memberInFlight is one entry per member of a bundle, labelled with its slot:
// the member's own footprint and scopes. The slot's changed files and open PR
// stay on the lead's entry, which holds the bundle's one branch.
func memberInFlight(be backlog.Backend, slot string, members, tracked, shared []string) []InFlight {
	var out []InFlight
	for _, id := range members {
		paths := itemFootprint(be, id, tracked, shared)
		sc, src := ItemScopeSource(be, id)
		out = append(out, InFlight{Slot: slot, Issue: id, Paths: paths, Scopes: sc, ScopeSource: src})
	}
	return out
}

// prFileLister is the forge side that reads a PR's changed files. It is
// optional on Forge: a forge without it keeps the footprint comparison.
type prFileLister interface {
	PRFiles(ctx context.Context, pr int) ([]string, error)
}

// openPRFiles resolves the open PR of a branch to its changed files, lazily and
// at most once per branch, so an in-flight item with an open PR is compared by
// what the PR touches, not by what its issue text names.
type openPRFiles struct {
	ctx    context.Context
	lister prFileLister
	byHead map[string]int // head branch -> open PR number
	err    string         // why the open PRs could not be listed
}

func (e Env) openPRFiles(ctx context.Context) *openPRFiles {
	o := &openPRFiles{ctx: ctx, byHead: map[string]int{}}
	if e.Forge == nil {
		return o // no forge to ask: every item keeps the footprint comparison
	}
	l, ok := e.Forge.(prFileLister)
	if !ok {
		o.err = "forge cannot list PR files"
		return o
	}
	o.lister = l
	prs, err := e.Forge.OpenPRs(ctx)
	if err != nil {
		o.err = "cannot list open PRs: " + err.Error()
		return o
	}
	for _, p := range prs {
		o.byHead[p.Branch] = p.Number
	}
	return o
}

// apply swaps x's footprint paths for its open PR's changed files and keeps only
// declared `## Touches` scopes (a subsystem or inferred scope is a guess the PR
// has outgrown). A branch with no open PR is returned unchanged; one whose files
// cannot be read is returned unchanged with PRFilesErr set.
func (o *openPRFiles) apply(be backlog.Backend, x InFlight, branch string, shared []string) InFlight {
	if branch == "" {
		return x
	}
	if o.err != "" {
		x.PRFilesErr = o.err
		return x
	}
	if o.lister == nil {
		return x
	}
	n, open := o.byHead[branch]
	if !open {
		return x
	}
	files, err := o.lister.PRFiles(o.ctx, n)
	if err != nil {
		x.PRFilesErr = err.Error()
		return x
	}
	x.Paths = overlap.Filter(files, shared)
	sort.Strings(x.Paths)
	x.FromPR = true
	x.Scopes, x.ScopeSource = nil, SourceNone
	body, _, _ := be.Detail(x.Issue)
	if s := Scopes(body); len(s) > 0 {
		x.Scopes, x.ScopeSource = s, SourceTouches
	}
	return x
}

// queuedChanged lists the paths a queued PR's branch changes against its base,
// read from the pushed branch in root (the local one when origin lacks it).
func (e Env) queuedChanged(ctx context.Context, root string, q worker.QueuedPR, shared []string) []string {
	branch := q.Branch
	base := strutil.FirstNonEmpty(q.Base, e.Base)
	if branch == "" || base == "" {
		return nil
	}
	head := branch
	if res, err := e.Git(ctx, root, "rev-parse", "--verify", "-q", "refs/remotes/origin/"+branch); err == nil && res.ExitCode == 0 {
		head = "origin/" + branch
	}
	paths, err := git.ChangedPaths(ctx, e.Git, root, git.RemoteBase(ctx, e.Git, root, base)+"..."+head)
	if err != nil {
		return nil
	}
	return overlap.Filter(paths, shared)
}

// changed lists the paths a worktree changed against the base: commits on its
// branch and uncommitted files.
func (e Env) changed(ctx context.Context, wt, base string, shared []string) []string {
	seen := map[string]bool{}
	add := func(p string) {
		if !overlap.Shared(shared, p) {
			seen[p] = true
		}
	}
	if paths, err := git.ChangedPaths(ctx, e.Git, wt, git.RemoteBase(ctx, e.Git, wt, base)+"...HEAD"); err == nil {
		for _, p := range paths {
			add(p)
		}
	}
	if res, err := e.Git(ctx, wt, "status", "--porcelain"); err == nil && res.ExitCode == 0 {
		out := res.Stdout
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
