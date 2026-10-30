package reap

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/worker"
)

// ownership answers "does anything live own this?" for every kind reap
// removes. It is built once per Find or Apply from the round report and the
// checkouts, so a rule change lands here and not in each finder.
type ownership struct {
	base, wtDir string
	checkouts   []checkout
	agents      []host.Agent    // the host snapshot the report was built from
	registered  map[string]bool // row names with a slot
	rowAgent    map[string]bool // row names the report matched an agent to
	slotBranch  map[string]bool // branches registered rows name
	slotTab     map[string]bool // tab handles registered rows name
	agentTab    map[string]bool // tabs a host agent runs in
}

// newOwnership reads rep once. A nil report owns nothing by itself; the
// caller's host-availability check keeps that from failing open.
func newOwnership(rep *round.Report, base, wtDir string, checkouts []checkout) *ownership {
	o := &ownership{base: base, wtDir: wtDir, checkouts: checkouts,
		registered: map[string]bool{}, rowAgent: map[string]bool{}, slotBranch: map[string]bool{}, slotTab: map[string]bool{}, agentTab: map[string]bool{}}
	if rep == nil {
		return o
	}
	o.agents = rep.Agents
	for _, a := range rep.Agents {
		o.agentTab[a.Tab] = true
	}
	for _, r := range rep.Rows {
		if r.Agent != "" {
			o.rowAgent[r.Name] = true
		}
		if !r.Registered {
			continue
		}
		o.registered[r.Name] = true
		if r.Branch != "" {
			o.slotBranch[r.Branch] = true
		}
		if r.Tab != "" {
			o.slotTab[r.Tab] = true
		}
	}
	return o
}

func protectedBranch(b, base string) bool {
	return b == base || worker.IsPark(b)
}

// inside reports whether p is dir or below it.
func inside(p, dir string) bool {
	p, dir = filepath.Clean(p), filepath.Clean(dir)
	return p == dir || strings.HasPrefix(p, dir+string(os.PathSeparator))
}

// agentIn: some host agent works inside dir.
func (o *ownership) agentIn(dir string) bool {
	for _, a := range o.agents {
		if a.Cwd != "" && inside(a.Cwd, dir) {
			return true
		}
	}
	return false
}

// tabLive: an agent runs in the tab, or a registered slot names it.
func (o *ownership) tabLive(id string) bool { return o.agentTab[id] || o.slotTab[id] }

// Worktree: a checkout reap must never remove.
func (o *ownership) Worktree(c *checkout) bool {
	name := filepath.Base(c.path)
	return o.registered[name] || c.isLocked || protectedBranch(c.branch, o.base) || o.agentIn(c.path) || o.rowAgent[name]
}

// Branch: a branch reap must never delete for liveness reasons.
func (o *ownership) Branch(b string) bool {
	return protectedBranch(b, o.base) || o.slotBranch[b]
}

// cwd: whether something working in cwd belongs to something reap must leave
// alone: a registered, parked, base-branch or live checkout, or anything
// outside .worktrees/ (another project's, or the main checkout's).
func (o *ownership) cwd(cwd string) bool {
	if !inside(cwd, o.wtDir) {
		return true
	}
	rel, err := filepath.Rel(o.wtDir, filepath.Clean(cwd))
	if err != nil {
		return true
	}
	name := strings.Split(rel, string(os.PathSeparator))[0]
	if name == "." || name == "" {
		return true
	}
	for i := range o.checkouts {
		if c := &o.checkouts[i]; filepath.Base(c.path) == name && filepath.Dir(c.path) == o.wtDir {
			return o.Worktree(c)
		}
	}
	return o.registered[name] || o.agentIn(filepath.Join(o.wtDir, name))
}

// Tab: a tab reap must not close, including one it cannot prove agentless.
func (o *ownership) Tab(t host.Tab) bool {
	if !t.Agentless || o.tabLive(t.ID) || len(t.Cwds) == 0 {
		return true
	}
	for _, c := range t.Cwds {
		if o.cwd(c) {
			return true
		}
	}
	return false
}

// Process: a process reap must not stop.
func (o *ownership) Process(p Process) bool {
	return p.PID <= 0 || o.cwd(p.Cwd) || o.tabLive(p.Tab)
}
