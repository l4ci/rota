// Package reap is the logic behind `rota reap`: it finds what a round left
// behind that nothing live owns (worktrees, branches, tabs, processes) and,
// on request, removes the part that is provably safe to remove.
//
// The rules are fixed by the verb contract and are all fail-closed:
//
//   - The live set is the round's status rows plus the host's agents matched
//     by cwd, never the registry alone: workers started by hand are host
//     agents that were never registered.
//   - A running agent is never killed. Tab and process candidates need proof
//     that no agent is under them; what a host cannot prove it does not list.
//   - A candidate holding work is listed with Held and never deleted.
//   - Registered slots, park/* worktrees and branches, the base branch and
//     any branch checked out in a worktree are never candidates.
//
// Nothing here reaches os/exec: git and the host are injected, so tests need
// no real git, herdr or tmux.
package reap

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/worker"
)

// Candidate kinds.
const (
	KindWorktree = "worktree"
	KindBranch   = "branch"
	KindTab      = "tab"
	KindProcess  = "process"
	KindLease    = "lease"
)

// Kinds lists every kind, in the order candidates are found and removed.
var Kinds = []string{KindWorktree, KindBranch, KindTab, KindProcess, KindLease}

// Candidate is something to reap. Held, when set, says why it must not be.
type Candidate struct {
	ID     string // "<kind>:<name>"
	Kind   string
	Name   string
	Path   string
	Reason string
	Held   string
}

// Failure is one deletion that did not happen.
type Failure struct{ ID, Error string }

// Process is a process a host reports under a tab.
type Process struct {
	PID  int
	Name string
	Tab  string
	Cwd  string
}

// HostOps is what reap asks of the host beyond the agent snapshot. A real
// herdr offers tabs only; a host with no tab support is a nil HostOps.
type HostOps interface {
	Tabs(ctx context.Context) ([]host.Tab, error)
	// Processes lists processes that sit under a tab. A host that cannot
	// prove a process tree has no agent in it returns none.
	Processes(ctx context.Context) ([]Process, error)
	CloseTab(ctx context.Context, id string) error
	StopProcess(ctx context.Context, pid int) error
}

// LeaseOps is the round lease as internal/round exposes it: round.Env
// satisfies it. A nil LeaseOps finds no lease.
type LeaseOps interface {
	ReadLease(ctx context.Context, root string) (round.Lease, round.LeaseState, error)
	ClearStaleLease(ctx context.Context, root string) (round.Lease, bool, error)
}

// Input is everything Find and Apply need from the outside.
type Input struct {
	Root string // project root
	Base string // the branch work merges into
	Git  worker.GitFunc
	// Report is the round's status (internal/round), the source of the live
	// set: its rows say which checkouts have a slot or a matched agent, and
	// its Unavailable list says whether the host could be read. Agents is the
	// snapshot the report was built from.
	Report *round.Report
	Agents []host.Agent
	Host   HostOps
	Lease  LeaseOps
}

// Result is what Find and Apply report. Warnings are for the caller to print.
type Result struct {
	Candidates []Candidate
	Reaped     []string
	Failed     []Failure
	Warnings   []string
}

var reIssueBranch = regexp.MustCompile(`^[^/]+/\d+-`)

// checkout is one entry of `git worktree list`.
type checkout struct {
	path, branch         string
	prunable             string // git's reason, when isPrunable
	isPrunable, isLocked bool
}

type state struct {
	in         Input
	root       string
	hostOK     bool
	checkouts  []checkout
	checkedOut map[string]bool // branches checked out in any worktree
	registered map[string]bool // row names with a slot
	slotBranch map[string]bool // branches registered rows name
	slotTab    map[string]bool // tab handles registered rows name
	warnings   []string
}

// A finder returns the candidates of one source. Adding a source is adding a
// function here.
type finder func(ctx context.Context, s *state) ([]Candidate, error)

var finders = []finder{findWorktrees, findBranches, findTabs, findProcesses, findLeases}

// findLeases lists the round lease when its holder is gone. A live, foreign
// or absent lease is never a candidate, and a lease is never Held: stale means
// nothing owns it.
func findLeases(ctx context.Context, s *state) ([]Candidate, error) {
	if s.in.Lease == nil {
		return nil, nil
	}
	l, st, err := s.in.Lease.ReadLease(ctx, s.in.Root)
	if err != nil {
		s.warn("round lease unavailable: %v; lease candidate skipped", err)
		return nil, nil
	}
	if st != roundlease.Stale {
		return nil, nil
	}
	who := "an unreadable lease file"
	if l.PID > 0 {
		who = fmt.Sprintf("pid %d (round %d, started %s)", l.PID, l.Round, l.StartedAt)
	}
	return []Candidate{{ID: KindLease + ":round", Kind: KindLease, Name: "round", Reason: "the round lease is held by " + who + ", which is gone"}}, nil
}

// Find lists the candidates of the wanted kinds (all when kinds is empty).
// It fails only when git does.
func Find(ctx context.Context, in Input, kinds []string) (Result, error) {
	s, err := newState(ctx, in)
	if err != nil {
		return Result{}, err
	}
	want := map[string]bool{}
	for _, k := range kinds {
		want[k] = true
	}
	var res Result
	for _, f := range finders {
		cs, err := f(ctx, s)
		if err != nil {
			return Result{}, err
		}
		for _, c := range cs {
			if len(want) == 0 || want[c.Kind] {
				res.Candidates = append(res.Candidates, c)
			}
		}
	}
	res.Warnings = s.warnings
	return res, nil
}

func newState(ctx context.Context, in Input) (*state, error) {
	s := &state{in: in, root: in.Root, checkedOut: map[string]bool{}, registered: map[string]bool{}, slotBranch: map[string]bool{}, slotTab: map[string]bool{}}
	if real, err := filepath.EvalSymlinks(in.Root); err == nil {
		s.root = real
	}
	s.hostOK = true
	if in.Report != nil {
		for _, u := range in.Report.Unavailable {
			if u == round.SourceHost {
				s.hostOK = false
			}
		}
		for _, r := range in.Report.Rows {
			if r.Registered {
				s.registered[r.Name] = true
				if r.Branch != "" {
					s.slotBranch[r.Branch] = true
				}
				if r.Tab != "" {
					s.slotTab[r.Tab] = true
				}
			}
		}
	}
	out, errOut, code, err := in.Git(ctx, s.root, "worktree", "list", "--porcelain")
	if err != nil || code != 0 {
		return nil, &worker.Error{Exit: worker.ExitUnavailable, Message: "git worktree list failed: " + strings.TrimSpace(errOut)}
	}
	var cur *checkout
	flush := func() {
		if cur != nil {
			s.checkouts = append(s.checkouts, *cur)
			if cur.branch != "" {
				s.checkedOut[cur.branch] = true
			}
		}
		cur = nil
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur = &checkout{path: filepath.Clean(strings.TrimPrefix(line, "worktree "))}
		case cur == nil:
		case strings.HasPrefix(line, "branch "):
			cur.branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "locked" || strings.HasPrefix(line, "locked "):
			cur.isLocked = true
		case line == "prunable" || strings.HasPrefix(line, "prunable "):
			cur.isPrunable = true
			cur.prunable = strings.TrimSpace(strings.TrimPrefix(line, "prunable"))
		}
	}
	flush()
	return s, nil
}

func (s *state) warn(format string, a ...any) {
	s.warnings = append(s.warnings, fmt.Sprintf(format, a...))
}

func (s *state) wtDir() string { return filepath.Join(s.root, ".worktrees") }

// inside reports whether p is dir or below it.
func inside(p, dir string) bool {
	p, dir = filepath.Clean(p), filepath.Clean(dir)
	return p == dir || strings.HasPrefix(p, dir+string(os.PathSeparator))
}

// liveAgent: some host agent works inside dir (or is already matched to the
// round row for it).
func (s *state) liveAgent(dir string) bool {
	for _, a := range s.in.Agents {
		if a.Cwd != "" && inside(a.Cwd, dir) {
			return true
		}
	}
	return false
}

func (s *state) rowAgent(name string) bool {
	if s.in.Report == nil {
		return false
	}
	for _, r := range s.in.Report.Rows {
		if r.Name == name && r.Agent != "" {
			return true
		}
	}
	return false
}

func protectedBranch(b, base string) bool {
	return b == base || strings.HasPrefix(b, "park/")
}

func (s *state) git(ctx context.Context, dir string, args ...string) (string, int, error) {
	out, errOut, code, err := s.in.Git(ctx, dir, args...)
	if err != nil {
		return "", 0, err
	}
	if code != 0 && strings.TrimSpace(errOut) != "" && code > 1 {
		return out, code, fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(errOut))
	}
	return out, code, nil
}

// refs are what a branch must be reachable from to count as merged: the base
// and, when it exists, origin/base. Either keeps the commits.
func (s *state) refs(ctx context.Context) []string {
	refs := []string{s.in.Base}
	if _, code, err := s.git(ctx, s.root, "rev-parse", "--verify", "-q", "origin/"+s.in.Base); err == nil && code == 0 {
		refs = append(refs, "origin/"+s.in.Base)
	}
	return refs
}

// heldUnmerged returns "" when rev is proven merged, else why it is held.
// Anything git cannot answer is held.
func (s *state) heldUnmerged(ctx context.Context, dir, rev string) string {
	if s.in.Base == "" {
		return "no base branch to prove merged against"
	}
	for _, ref := range s.refs(ctx) {
		_, code, err := s.git(ctx, dir, "merge-base", "--is-ancestor", rev, ref)
		if err != nil {
			return "cannot prove " + rev + " is merged: " + err.Error()
		}
		if code == 0 {
			return ""
		}
	}
	out, code, err := s.git(ctx, dir, "rev-list", "--count", s.in.Base+".."+rev)
	if n, convErr := strconv.Atoi(strings.TrimSpace(out)); err == nil && code == 0 && convErr == nil {
		return fmt.Sprintf("%d commit(s) not on %s", n, s.in.Base)
	}
	return "commits not on " + s.in.Base
}

func (s *state) owner(name string) *checkout {
	for i := range s.checkouts {
		if filepath.Base(s.checkouts[i].path) == name && filepath.Dir(s.checkouts[i].path) == s.wtDir() {
			return &s.checkouts[i]
		}
	}
	return nil
}

// protectedWorktree: a checkout reap must never touch, and why.
func (s *state) protectedWorktree(c *checkout) bool {
	name := filepath.Base(c.path)
	return s.registered[name] || c.isLocked || protectedBranch(c.branch, s.in.Base) || s.liveAgent(c.path) || s.rowAgent(name)
}

func findWorktrees(ctx context.Context, s *state) ([]Candidate, error) {
	if !s.hostOK {
		s.warn("host unavailable: no agent can be matched by cwd, so no .worktrees/ checkout is listed")
		return nil, nil
	}
	var out []Candidate
	for i := range s.checkouts {
		c := &s.checkouts[i]
		if filepath.Dir(c.path) != s.wtDir() || s.protectedWorktree(c) {
			continue
		}
		name := filepath.Base(c.path)
		cand := Candidate{ID: KindWorktree + ":" + name, Kind: KindWorktree, Name: name, Path: c.path}
		if c.isPrunable {
			cand.Reason = "git marks the checkout prunable: " + c.prunable
			out = append(out, cand)
			continue
		}
		cand.Reason = "no live owner"
		rev := "HEAD"
		if st, code, err := s.git(ctx, c.path, "status", "--porcelain"); err != nil || code != 0 {
			cand.Held = "git status failed"
		} else if strings.TrimSpace(st) != "" {
			cand.Held = "uncommitted changes"
		} else if h := s.heldUnmerged(ctx, c.path, rev); h != "" {
			cand.Held = h
		}
		out = append(out, cand)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func findBranches(ctx context.Context, s *state) ([]Candidate, error) {
	list, _, err := s.git(ctx, s.root, "for-each-ref", "--format=%(refname:short)", "refs/heads/")
	if err != nil {
		return nil, &worker.Error{Exit: worker.ExitUnavailable, Message: err.Error()}
	}
	var out []Candidate
	for _, b := range strings.Fields(list) {
		if protectedBranch(b, s.in.Base) || s.checkedOut[b] || s.slotBranch[b] || !reIssueBranch.MatchString(b) {
			continue
		}
		cand := Candidate{ID: KindBranch + ":" + b, Kind: KindBranch, Name: b, Reason: "checked out nowhere"}
		if h := s.heldUnmerged(ctx, s.root, b); h != "" {
			cand.Held = h
		} else {
			cand.Reason = "merged into " + s.in.Base + " and checked out nowhere"
		}
		out = append(out, cand)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// hostOwned: whether a tab or process working in cwd belongs to something
// reap must leave alone: a registered, parked, base-branch or live checkout.
func (s *state) hostOwned(cwd string) bool {
	if !inside(cwd, s.wtDir()) {
		return true // another project's, or the main checkout's
	}
	rel, err := filepath.Rel(s.wtDir(), filepath.Clean(cwd))
	if err != nil {
		return true
	}
	name := strings.Split(rel, string(os.PathSeparator))[0]
	if name == "." || name == "" {
		return true
	}
	if c := s.owner(name); c != nil {
		return s.protectedWorktree(c)
	}
	return s.registered[name] || s.liveAgent(filepath.Join(s.wtDir(), name))
}

func (s *state) agentOnTab(tab string) bool {
	for _, a := range s.in.Agents {
		if a.Tab == tab {
			return true
		}
	}
	return false
}

// hostCandidates guards the two host kinds: they need a readable host.
func (s *state) hostCandidates() bool {
	if !s.hostOK {
		s.warn("host unavailable: tab and process candidates skipped")
		return false
	}
	return s.in.Host != nil
}

func (s *state) tabOK(t host.Tab) bool {
	if !t.Agentless || s.agentOnTab(t.ID) || s.slotTab[t.ID] || len(t.Cwds) == 0 {
		return false
	}
	for _, c := range t.Cwds {
		if s.hostOwned(c) {
			return false
		}
	}
	return true
}

func findTabs(ctx context.Context, s *state) ([]Candidate, error) {
	if !s.hostCandidates() {
		return nil, nil
	}
	tabs, err := s.in.Host.Tabs(ctx)
	if err != nil {
		s.warn("tabs unavailable: %v; tab candidates skipped", err)
		return nil, nil
	}
	var out []Candidate
	for _, t := range tabs {
		if s.tabOK(t) {
			out = append(out, Candidate{ID: KindTab + ":" + t.ID, Kind: KindTab, Name: t.ID, Path: t.Cwds[0], Reason: "no agent runs in the tab"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func findProcesses(ctx context.Context, s *state) ([]Candidate, error) {
	if !s.hostCandidates() {
		return nil, nil
	}
	procs, err := s.in.Host.Processes(ctx)
	if err != nil {
		s.warn("processes unavailable: %v; process candidates skipped", err)
		return nil, nil
	}
	var out []Candidate
	for _, p := range procs {
		if p.PID <= 0 || s.hostOwned(p.Cwd) || s.agentOnTab(p.Tab) || s.slotTab[p.Tab] {
			continue
		}
		id := strconv.Itoa(p.PID)
		out = append(out, Candidate{ID: KindProcess + ":" + id, Kind: KindProcess, Name: strings.TrimSpace(id + " " + p.Name), Path: p.Cwd, Reason: "process under a tab with no agent"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Apply removes every candidate without Held. A removal that fails is a
// Failure and the rest still run. Each removal re-proves its own safety
// first: git refuses a dirty or locked worktree on its own, and a tab is
// re-read so an agent that appeared since Find is never closed.
func Apply(ctx context.Context, in Input, cands []Candidate) Result {
	var res Result
	s, err := newState(ctx, in)
	if err != nil {
		for _, c := range cands {
			if c.Held == "" {
				res.Failed = append(res.Failed, Failure{c.ID, err.Error()})
			}
		}
		return res
	}
	for _, c := range cands {
		if c.Held != "" {
			continue
		}
		if err := s.remove(ctx, c); err != nil {
			res.Failed = append(res.Failed, Failure{c.ID, err.Error()})
		} else {
			res.Reaped = append(res.Reaped, c.ID)
		}
	}
	res.Warnings = s.warnings
	return res
}

func (s *state) remove(ctx context.Context, c Candidate) error {
	switch c.Kind {
	case KindWorktree:
		// no --force: git refuses a dirty or locked checkout
		_, errOut, code, err := s.in.Git(ctx, s.root, "worktree", "remove", c.Path)
		return gitErr(errOut, code, err)
	case KindBranch:
		if s.checkedOut[c.Name] || protectedBranch(c.Name, s.in.Base) {
			return fmt.Errorf("%s is checked out or protected", c.Name)
		}
		if h := s.heldUnmerged(ctx, s.root, c.Name); h != "" {
			return fmt.Errorf("no longer provably merged: %s", h)
		}
		_, errOut, code, err := s.in.Git(ctx, s.root, "branch", "-D", c.Name)
		return gitErr(errOut, code, err)
	case KindTab:
		if s.in.Host == nil {
			return fmt.Errorf("no host")
		}
		tabs, err := s.in.Host.Tabs(ctx)
		if err != nil {
			return err
		}
		for _, t := range tabs {
			if t.ID == c.Name {
				if !s.tabOK(t) {
					return fmt.Errorf("an agent may be running in %s; left open", c.Name)
				}
				return s.in.Host.CloseTab(ctx, c.Name)
			}
		}
		return fmt.Errorf("tab %s is gone", c.Name)
	case KindLease:
		if s.in.Lease == nil {
			return fmt.Errorf("no lease access")
		}
		// ClearStaleLease re-proves staleness under the lease lock.
		_, cleared, err := s.in.Lease.ClearStaleLease(ctx, s.in.Root)
		if err == nil && !cleared {
			return fmt.Errorf("the lease is no longer stale; left alone")
		}
		return err
	case KindProcess:
		if s.in.Host == nil {
			return fmt.Errorf("no host")
		}
		pid, err := strconv.Atoi(strings.Fields(c.Name + " ")[0])
		if err != nil {
			return err
		}
		return s.in.Host.StopProcess(ctx, pid)
	}
	return fmt.Errorf("unknown kind %s", c.Kind)
}

func gitErr(errOut string, code int, err error) error {
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("%s", strings.TrimSpace(errOut))
	}
	return nil
}
