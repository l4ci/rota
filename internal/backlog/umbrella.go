package backlog

import (
	"context"
	"fmt"
	gitx "github.com/l4ci/rota/internal/git"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/repos"
	"github.com/l4ci/rota/internal/tracker"
)

// Umbrella is the issue backlog of an umbrella project: every registered
// sub-repo keeps its items on its own tracker (UmbrellaIssueBackend in
// hvlib_backend.py). Reads merge the sub-repos' renderings, each bullet
// carrying `Repos: <name>`; an item's ID is "<repo>:<number>" (contract rule
// 11); writes go to the tracker of the sub-repo that owns the item.
//
// References: `<repo>#<n>` and `<repo>:<ID>` always resolve; a bare `F42`,
// `#42` or `42` resolves when exactly one sub-repo has it, and several is an
// ErrInvalid error that lists the qualified candidates.
type Umbrella struct {
	Cfg   any
	Repos []repos.Repo // registry order
	// NewTracker builds the tracker of one sub-repo, run in dir (that
	// sub-repo's checkout). It is called at most once per sub-repo, on first
	// use, so a sub-repo whose forge is down fails only the verbs that reach it.
	NewTracker func(dir string) (Tracker, error)
	Ctx        context.Context // for every tracker call; nil is context.Background()
	// Scope narrows reads and bare-reference resolution to one sub-repo (the
	// global --repo). Qualified references still resolve anywhere, and IDs
	// keep their "<repo>:" spelling.
	Scope string
	// CwdRepo names the sub-repo the caller works in; "" when none. With no
	// Scope it narrows reads like Scope does (contract: scope S) and is the
	// default target of Create.
	CwdRepo string

	subs map[string]*Issues
}

// NewUmbrella opens the umbrella backend over the sub-repos registered under root.
func NewUmbrella(root string, cfg any, newTracker func(dir string) (Tracker, error)) *Umbrella {
	return &Umbrella{Cfg: cfg, Repos: repos.Load(root), NewTracker: newTracker}
}

func (u *Umbrella) ctx() context.Context {
	if u.Ctx != nil {
		return u.Ctx
	}
	return context.Background()
}

func (u *Umbrella) names() string {
	names := make([]string, len(u.Repos))
	for i, r := range u.Repos {
		names[i] = r.Name
	}
	return strings.Join(names, ", ")
}

func (u *Umbrella) registered(name string) (repos.Repo, bool) {
	for _, r := range u.Repos {
		if r.Name == name {
			return r, true
		}
	}
	return repos.Repo{}, false
}

// sub is the issue backend of one registered sub-repo, built on first use.
func (u *Umbrella) sub(name string) (*Issues, error) {
	if s, ok := u.subs[name]; ok {
		return s, nil
	}
	r, ok := u.registered(name)
	if !ok {
		return nil, errf(ErrNotFound, "unknown sub-repo '%s' (registered: %s)", name, u.names())
	}
	tr, err := u.NewTracker(r.Path)
	if err != nil {
		return nil, err
	}
	s := &Issues{Cfg: u.Cfg, Tracker: tr, Ctx: u.Ctx, Repo: name}
	s.OnMissingMilestone = func(mid string) (string, bool, error) { return u.createSubMilestone(s, mid) }
	if u.subs == nil {
		u.subs = map[string]*Issues{}
	}
	u.subs[name] = s
	return s, nil
}

// readScope is the sub-repo reads cover: --repo, else the working
// directory's sub-repo; "" (all of them) at the umbrella root.
func (u *Umbrella) readScope() string {
	if u.Scope != "" {
		return u.Scope
	}
	return u.CwdRepo
}

// scoped lists the sub-repos reads cover: all of them, or just readScope.
func (u *Umbrella) scoped() []string {
	scope := u.readScope()
	var out []string
	for _, r := range u.Repos {
		if scope == "" || r.Name == scope {
			out = append(out, r.Name)
		}
	}
	return out
}

// Name is "issues".
func (u *Umbrella) Name() string { return "issues" }

// ---- reads -----------------------------------------------------------------

type closedEntry struct {
	closedAt string
	number   int
	item     Item
	line     string
}

// closed is the closed items of the scoped sub-repos, newest first: sorted on
// (closed_at, number) descending, ties keeping registry order.
func (u *Umbrella) closed() ([]closedEntry, error) {
	var out []closedEntry
	for _, name := range u.scoped() {
		s, err := u.sub(name)
		if err != nil {
			return nil, err
		}
		closed, err := s.closedIssues()
		if err != nil {
			return nil, err
		}
		for _, is := range closed {
			it := s.item(is)
			out = append(out, closedEntry{is.ClosedAt, is.Number, u.qualify(name, *it), it.Line})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].closedAt != out[j].closedAt {
			return out[i].closedAt > out[j].closedAt
		}
		return out[i].number > out[j].number
	})
	return out, nil
}

func (u *Umbrella) qualify(name string, it Item) Item {
	it.ID = name + ":" + it.ID
	return it
}

// Markdown renders the merged backlog: the open bullets of every sub-repo by
// type (registry order, then number), then the newest closedLimit Done lines
// across all of them (all when negative, none when 0).
func (u *Umbrella) Markdown(closedLimit int) (string, error) {
	sections := map[string][]string{}
	for _, name := range u.scoped() {
		s, err := u.sub(name)
		if err != nil {
			return "", err
		}
		open, err := s.openByLetter()
		if err != nil {
			return "", err
		}
		for _, l := range ItemLetters {
			sections[string(l)] = append(sections[string(l)], open[string(l)]...)
		}
	}
	var done []string
	if closedLimit != 0 {
		entries, err := u.closed()
		if err != nil {
			return "", err
		}
		for _, e := range entries {
			done = append(done, e.line)
		}
		if closedLimit > 0 && len(done) > closedLimit {
			done = done[:closedLimit]
		}
	}
	return renderBacklog(sections, done), nil
}

// List returns the open items by type (each type: sub-repo in registry order,
// then issue number), then, with includeClosed, the closed ones newest first.
// IDs are "<repo>:<number>".
func (u *Umbrella) List(includeClosed bool) ([]Item, error) {
	perRepo := map[string][]Item{}
	for _, name := range u.scoped() {
		s, err := u.sub(name)
		if err != nil {
			return nil, err
		}
		items, err := s.List(false)
		if err != nil {
			return nil, err
		}
		perRepo[name] = items
	}
	var out []Item
	for _, l := range ItemLetters {
		for _, name := range u.scoped() {
			for _, it := range perRepo[name] {
				if it.Type == string(l) {
					out = append(out, u.qualify(name, it))
				}
			}
		}
	}
	if includeClosed {
		entries, err := u.closed()
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			out = append(out, e.item)
		}
	}
	return out, nil
}

// ---- references ------------------------------------------------------------

// pick finds the sub-repo that owns ref and the reference as that sub-repo
// spells it. A nil sub means no sub-repo has it. A bare reference that several
// sub-repos hold is an ErrInvalid error naming the qualified candidates.
func (u *Umbrella) pick(ref string) (*Issues, string, error) {
	ref = pystr.Strip(ref)
	if m := qualHashRe.FindStringSubmatch(ref); m != nil {
		if _, ok := u.registered(m[1]); ok {
			s, err := u.sub(m[1])
			return s, "#" + m[2], err
		}
	}
	if m := qualColonRe.FindStringSubmatch(ref); m != nil {
		if _, ok := u.registered(m[1]); ok {
			s, err := u.sub(m[1])
			return s, m[2], err
		}
	}
	if _, _, err := resolveItemRef(ref); err != nil {
		return nil, ref, nil
	}
	var hits []*Issues
	var cands []string
	for _, name := range u.scoped() {
		s, err := u.sub(name)
		if err != nil {
			return nil, ref, err
		}
		is, ok, err := s.lookup(ref)
		if err != nil {
			return nil, ref, err
		}
		if ok {
			hits = append(hits, s)
			cands = append(cands, fmt.Sprintf("%s:%d", name, is.Number))
		}
	}
	if len(hits) > 1 {
		return nil, ref, errf(ErrInvalid, "[%s] is ambiguous across sub-repos — qualify it: %s", ref, strings.Join(cands, ", "))
	}
	if len(hits) == 1 {
		return hits[0], ref, nil
	}
	return nil, ref, nil
}

// owner is pick, with an unknown reference as ErrNotFound.
func (u *Umbrella) owner(ref string) (*Issues, string, error) {
	s, plain, err := u.pick(ref)
	if err != nil {
		return nil, "", err
	}
	if s == nil {
		return nil, "", errf(ErrNotFound, "[%s] not found in the issue tracker", ref)
	}
	return s, plain, nil
}

// Get returns the item behind ref with its ID as "<repo>:<number>".
func (u *Umbrella) Get(ref string) (*Item, error) {
	s, plain, err := u.owner(ref)
	if err != nil {
		return nil, err
	}
	it, err := s.Get(plain)
	if err != nil {
		return nil, err
	}
	q := u.qualify(s.Repo, *it)
	return &q, nil
}

// Detail is the issue body without its fields block; ok is false when the
// reference is unknown or the body blank.
func (u *Umbrella) Detail(ref string) (string, bool, error) {
	s, plain, err := u.pick(ref)
	if err != nil || s == nil {
		return "", false, err
	}
	return s.Detail(plain)
}

// ---- writes ----------------------------------------------------------------

// CwdSubRepo is _cwd_repo: the registered sub-repo that cwd is inside (a
// Layout B worktree counts, through git's common dir), "" when none.
func CwdSubRepo(cwd string, list []repos.Repo) string {
	common, ok, err := gitx.Repo{Dir: cwd}.CommonDir(context.Background())
	if err != nil || !ok {
		return ""
	}
	root := repos.Realpath(filepath.Dir(common))
	for _, r := range list {
		if r.Path == root {
			return r.Name
		}
	}
	return ""
}

// Create captures one item in one sub-repo: the Repos field, else Scope, else
// the sub-repo the caller works in. The ID is "<repo>:<number>".
func (u *Umbrella) Create(in CreateInput) (CreateResult, error) {
	var names []string
	var rest []Field
	for _, f := range in.Fields {
		if f.Name != "Repos" {
			rest = append(rest, f)
			continue
		}
		names = nil // a name given twice keeps its last value
		for _, n := range strings.Split(f.Value, ",") {
			if n = pystr.Strip(n); n != "" {
				names = append(names, n)
			}
		}
	}
	if len(names) > 1 {
		return CreateResult{}, errf(ErrNotFound, "an item lives in exactly one sub-repo in issue mode — capture one item "+
			"per repo and link them with Related:")
	}
	name := ""
	switch {
	case len(names) == 1:
		name = names[0]
		if u.Scope != "" && u.Scope != name {
			return CreateResult{}, errf(ErrInvalid, "--repo %s and the Repos field %s name different sub-repos", u.Scope, name)
		}
	case u.Scope != "":
		name = u.Scope
	default:
		name = u.CwdRepo
	}
	if name == "" {
		return CreateResult{}, errf(ErrNotFound, "umbrella issue mode needs a target sub-repo — pass --repo <name> or --repos <name> "+
			"(registered: %s) or run from inside one", u.names())
	}
	if _, ok := u.registered(name); !ok {
		return CreateResult{}, errf(ErrNotFound, "unknown sub-repo '%s' (registered: %s)", name, u.names())
	}
	s, err := u.sub(name)
	if err != nil {
		return CreateResult{}, err
	}
	in.Fields = rest
	res, err := s.Create(in)
	if err != nil {
		return CreateResult{}, err
	}
	res.ID = name + ":" + res.ID
	return res, nil
}

// SetField sets a field of an open item on its sub-repo's tracker. The Repos
// field is the owner and cannot change.
func (u *Umbrella) SetField(ref, field, value string) (bool, error) {
	if strings.ToLower(field) == "repos" {
		return false, errf(ErrInvalid, "repos is the owning sub-repo; an issue cannot move between trackers")
	}
	s, plain, err := u.owner(ref)
	if err != nil {
		return false, err
	}
	return s.SetField(plain, field, value)
}

// Complete closes the item on its sub-repo's tracker.
func (u *Umbrella) Complete(ref string, in CompleteInput) (bool, error) {
	s, plain, err := u.owner(ref)
	if err != nil {
		return false, err
	}
	return s.Complete(plain, in)
}

// Reopen reopens the item on its sub-repo's tracker.
func (u *Umbrella) Reopen(ref string) (bool, error) {
	s, plain, err := u.owner(ref)
	if err != nil {
		return false, err
	}
	return s.Reopen(plain)
}

// Ready lists what the item lacks to be startable.
func (u *Umbrella) Ready(ref string) ([]string, error) {
	s, plain, err := u.owner(ref)
	if err != nil {
		return nil, err
	}
	return s.Ready(plain)
}

// Comments lists the item's comments, oldest first.
func (u *Umbrella) Comments(ref, kind string) ([]Comment, error) {
	s, plain, err := u.owner(ref)
	if err != nil {
		return nil, err
	}
	return s.Comments(plain, kind)
}

// AddComment appends a comment on the item's tracker.
func (u *Umbrella) AddComment(ref, kind, text string) (string, error) {
	s, plain, err := u.owner(ref)
	if err != nil {
		return "", err
	}
	return s.AddComment(plain, kind, text)
}

// Claim takes the item for claimID.
func (u *Umbrella) Claim(ref, claimID string) (bool, string, error) {
	s, plain, err := u.owner(ref)
	if err != nil {
		return false, "", err
	}
	return s.Claim(plain, claimID)
}

// Release gives the item back.
func (u *Umbrella) Release(ref, claimID string) (bool, error) {
	s, plain, err := u.owner(ref)
	if err != nil {
		return false, err
	}
	return s.Release(plain, claimID)
}

// SetState sets the item's state label.
func (u *Umbrella) SetState(ref, state string) (bool, error) {
	s, plain, err := u.owner(ref)
	if err != nil {
		return false, err
	}
	return s.SetState(plain, state)
}

// Status is the read-back of the item, its ID "<repo>:<number>".
func (u *Umbrella) Status(ref string) (*Status, error) {
	s, plain, err := u.owner(ref)
	if err != nil {
		return nil, err
	}
	st, err := s.Status(plain)
	if err != nil {
		return nil, err
	}
	st.ID = s.Repo + ":" + st.ID
	return st, nil
}

// NoteGet returns the item's durable note of a kind.
func (u *Umbrella) NoteGet(ref, kind string) (string, bool, error) {
	s, plain, err := u.owner(ref)
	if err != nil {
		return "", false, err
	}
	return s.NoteGet(plain, kind)
}

// NotePut writes the item's durable note.
func (u *Umbrella) NotePut(ref, kind, text string) (bool, error) {
	s, plain, err := u.owner(ref)
	if err != nil {
		return false, err
	}
	return s.NotePut(plain, kind, text)
}

// NoteRm deletes the item's durable note.
func (u *Umbrella) NoteRm(ref, kind string) (bool, error) {
	s, plain, err := u.owner(ref)
	if err != nil {
		return false, err
	}
	return s.NoteRm(plain, kind)
}

// ---- milestones ------------------------------------------------------------

// msTitleID is _MS_TITLE_RE's group 1 for a tracking issue title, "" when it
// does not start with a milestone ID.
func msTitleID(title string) string {
	title = pystr.Strip(title)
	m := msTitleRe.FindStringSubmatch(title)
	if m == nil {
		return ""
	}
	if r, n := decodeRune(title[len(m[1]):]); n > 0 && pystr.IsWord(r) {
		return ""
	}
	return m[1]
}

// homeSub is the sub-repo that holds the milestone tracking issues:
// issues.homeRepo, else the first registered one.
func (u *Umbrella) HomeSub() (*Issues, error) { return u.homeSub() }

func (u *Umbrella) homeSub() (*Issues, error) {
	name := ""
	if v, ok := config.Lookup(u.Cfg, "issues.homeRepo"); ok && v != nil {
		name = fmt.Sprint(v)
	}
	if name == "" && len(u.Repos) > 0 {
		name = u.Repos[0].Name
	}
	if _, ok := u.registered(name); !ok {
		return nil, errf(ErrNotFound, "issues.homeRepo '%s' is not a registered sub-repo (registered: %s)", name, u.names())
	}
	return u.sub(name)
}

// trackerBefore orders tracking issues of one milestone: open before closed,
// then the lower number.
func trackerBefore(a, b Issue) bool {
	if (a.State == "open") != (b.State == "open") {
		return a.State == "open"
	}
	return a.Number < b.Number
}

// createSubMilestone is _create_sub_milestone: the first use of mid in sub
// creates its native milestone "MNN — <title>", titled after the home
// tracking issue. ok is false when the milestone has no tracking issue.
func (u *Umbrella) createSubMilestone(sub *Issues, mid string) (string, bool, error) {
	home, err := u.homeSub()
	if err != nil {
		return "", false, err
	}
	tr, err := home.tracker()
	if err != nil {
		return "", false, err
	}
	all, err := tr.List(u.ctx(), tracker.ListFilter{State: "all", Labels: []string{config.Label(u.Cfg, "milestoneTracker")}})
	if err != nil {
		return "", false, err
	}
	var best *Issue
	for i := range all {
		is := all[i]
		if !slices.Contains(is.Labels, config.Label(u.Cfg, "milestoneTracker")) || msTitleID(is.Title) != mid {
			continue
		}
		// Lowest open number wins; the lowest closed one when none is open.
		if best == nil || trackerBefore(is, *best) {
			best = &all[i]
		}
	}
	if best == nil {
		return "", false, nil
	}
	native := pystr.Strip(best.Title)
	mk, ok := sub.Tracker.(interface {
		CreateMilestone(ctx context.Context, title, description string) (int, error)
	})
	if !ok {
		return "", false, fmt.Errorf("the tracker of %s cannot create milestones", sub.Repo)
	}
	if _, err := mk.CreateMilestone(u.ctx(), native, ""); err != nil {
		return "", false, err
	}
	return native, true, nil
}

var (
	_ Backend  = (*Umbrella)(nil)
	_ Workflow = (*Umbrella)(nil)
)
