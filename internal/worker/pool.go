package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/jsonx"
)

// WorktreeRoot is where slot worktrees live: one root for every mode (rota-work
// slots and herdr rounds), inside the project and gitignored (rota init writes
// `.worktrees/` to .gitignore), so herdr groups the workspaces under the
// project. Slots created before this lived in .claude/worktrees/rota-worker/<slot>;
// init leaves those where they are.
const WorktreeRoot = ".worktrees"

// InitOpts are the flags of `rota worker pool init`.
type InitOpts struct {
	Slots   int
	Base    string // "" means the current branch
	Session string // "" means "rota"
	// Names provisions these slots instead of w1..wSlots, each on the branch
	// BranchPrefix+name (a round roster parks on "park/<agent>", #79).
	Names        []string
	BranchPrefix string
}

// slotNames are the slots PoolInit provisions and the branch each starts on.
func (o InitOpts) slotNames() (names []string, branchOf func(string) string) {
	if len(o.Names) > 0 {
		return o.Names, func(n string) string { return o.BranchPrefix + n }
	}
	for i := 1; i <= o.Slots; i++ {
		names = append(names, fmt.Sprintf("w%d", i))
	}
	return names, func(n string) string { return "rota-worker/" + n }
}

// InitResult is what `pool init` did.
type InitResult struct {
	Session  string
	Base     string
	Slots    []*jsonx.Object
	Changed  bool
	Warnings []string
}

// PoolInit creates the slots' worktrees and registers them. It is
// idempotent: an existing, healthy slot is left alone and only missing slots
// are created. Re-running with a larger Slots grows the pool and never shrinks
// one (use reap).
func (e Env) PoolInit(ctx context.Context, root string, o InitOpts, acc *Accounts) (InitResult, error) {
	e = e.withDefaults()
	res := InitResult{}
	base := o.Base
	if base == "" {
		cur, _ := e.git(root, "rev-parse", "--abbrev-ref", "HEAD")
		if cur == "" || cur == "HEAD" {
			return res, fail(ExitResolution, "cannot resolve base branch — pass --base <branch>")
		}
		base = cur
	}
	if _, code := e.git(root, "rev-parse", "--verify", "--quiet", base); code != 0 {
		return res, fail(ExitResolution, fmt.Sprintf("base branch '%s' does not exist", base))
	}
	session := o.Session
	if session == "" {
		session = "rota"
	}
	res.Base, res.Session = base, session
	before, _ := os.ReadFile(RegistryPath(root))

	if err := os.MkdirAll(filepath.Join(root, WorktreeRoot), 0o777); err != nil {
		return res, fail(ExitUnavailable, err.Error())
	}
	// A tracked-looking .worktrees/ makes every `git status` in the project
	// noisy. Warn rather than edit .gitignore: rota init owns that line.
	if _, code := e.git(root, "check-ignore", "-q", WorktreeRoot+"/"); code != 0 {
		res.Warnings = append(res.Warnings, WorktreeRoot+"/ is not gitignored — re-run rota init to add it")
	}

	// tmux handles are known now; herdr tab ids exist only after a dispatch.
	cfg := config.Load(filepath.Join(root, ".rota", "config.json"))
	dispatchV, _ := config.Lookup(cfg, "work.dispatch")
	dispatch, _ := dispatchV.(string)

	names, branchOf := o.slotNames()
	for _, name := range names {
		branch := branchOf(name)
		rel := filepath.Join(root, WorktreeRoot, name)

		// A slot registered at another path (the pre-.worktrees root) keeps it
		// while that worktree is healthy. Re-adding it here would fail on the
		// branch it already has checked out; moving it is `git worktree move`,
		// done by hand while the slot is idle.
		reg := LoadRegistry(root)
		regWT := ""
		if s := reg.Slot(name); s != nil {
			regWT = Str(s, "worktree")
		}
		if regWT != "" && regWT != realPath(rel) {
			if _, code := e.git(regWT, "rev-parse", "--git-dir"); code == 0 {
				// Only a worktree of THIS repository may be adopted: a registry
				// path that is a checkout of some other repo would be handed to
				// dispatch, reset and the gate as if it were a slot (#79).
				ours, theirs := e.commonDir(root), e.commonDir(regWT)
				if ours != theirs {
					return res, fail(ExitResolution, fmt.Sprintf("slot %s is registered at %s, a worktree of another repository (%s, not %s); fix or remove the registry entry", name, regWT, theirs, ours))
				}
				rel = regWT
				res.Warnings = append(res.Warnings, fmt.Sprintf("note: slot %s stays at %s (outside %s/); git worktree move it to relocate", name, regWT, WorktreeRoot))
			}
		}

		if _, code := e.git(rel, "rev-parse", "--git-dir"); isDir(rel) && code == 0 {
			// healthy slot — leave it alone
		} else {
			// A leftover directory with no working git dir is debris from an
			// interrupted reap; clear it so `worktree add` doesn't refuse.
			if _, err := os.Lstat(rel); err == nil {
				os.RemoveAll(rel)
			}
			e.git(root, "worktree", "prune")
			if _, code := e.git(root, "rev-parse", "--verify", "--quiet", branch); code == 0 {
				if _, code := e.git(root, "worktree", "add", rel, branch); code != 0 {
					return res, fail(ExitUnavailable, fmt.Sprintf("could not add worktree for %s on existing branch %s", name, branch))
				}
			} else if _, code := e.git(root, "worktree", "add", "-b", branch, rel, base); code != 0 {
				return res, fail(ExitUnavailable, fmt.Sprintf("could not create worktree/branch for %s from %s", name, base))
			}
		}

		abs := realPath(rel)
		// A healthy slot may sit on a per-task branch (rota worker reset);
		// register what is actually checked out, not the init-time name. A
		// detached worktree has no branch to register: keep what the registry
		// already holds for the slot, else the init-time name.
		if cur, code := e.git(abs, "symbolic-ref", "--short", "-q", "HEAD"); code == 0 && cur != "" {
			branch = cur
		} else if s := LoadRegistry(root).Slot(name); s != nil && Str(s, "branch") != "" {
			branch = Str(s, "branch")
		}
		handle := session + ":" + name
		// A round slot has no session until its first dispatch, whatever the
		// host: a nominal handle would read as a dead tab to reconcile.
		if dispatch == "herdr" || len(o.Names) > 0 {
			handle = ""
		}
		if err := registerSlot(root, name, branch, abs, base, session, handle); err != nil {
			return res, err
		}
	}

	// Spread slots across configured accounts. Assignment is by headroom, not
	// round-robin, so a fleet does not pile onto an account that is nearly
	// spent. Silent no-op when work.accounts is empty: the single-account
	// default inherits the ambient CLAUDE_CONFIG_DIR.
	if acc != nil && len(Configured(root)) > 0 {
		var used []string
		for _, name := range names {
			// Exclude accounts already handed out this pass so slots spread
			// rather than all landing on the single healthiest account. When
			// accounts run out, the exclusion list resets and they share.
			pick, ok := acc.Pick(ctx, root, used)
			if !ok {
				used = nil
				pick, ok = acc.Pick(ctx, root, nil)
			}
			if ok {
				if _, _, err := acc.Assign(ctx, root, name, pick); err == nil {
					used = append(used, pick)
				}
			}
		}
	}

	after, _ := os.ReadFile(RegistryPath(root))
	res.Changed = string(before) != string(after)
	for _, s := range LoadRegistry(root).Slots() {
		res.Slots = append(res.Slots, SlotData(s))
	}
	return res, nil
}

// commonDir is the resolved git common dir of a worktree.
func (e Env) commonDir(dir string) string {
	out, code := e.git(dir, "rev-parse", "--git-common-dir")
	if code != 0 || out == "" {
		return ""
	}
	return realPath(git.AbsCommonDir(dir, out))
}

func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

// registerSlot adds or updates one slot in .rota/workers.json, idempotent on
// the slot name.
func registerSlot(root, name, branch, worktree, base, session, handle string) error {
	def := jsonx.NewObject()
	def.Set("session", session)
	def.Set("slots", []any{})
	return Update(root, def, func(doc *jsonx.Object) {
		doc.Set("session", session)
		if _, ok := doc.Get("slots"); !ok {
			doc.Set("slots", []any{})
		}
		reg := Registry{Doc: doc}
		var rota any
		if handle != "" {
			rota = handle
		}
		if existing := reg.Slot(name); existing == nil {
			s := NewSlot(name, branch, worktree, base, rota)
			list, _ := doc.Get("slots")
			l, _ := list.([]any)
			doc.Set("slots", append(l, s))
		} else {
			// Migrate the pre-herdr field name. An empty handle (herdr) never
			// clobbers a live tab id that dispatch recorded.
			legacy, _ := existing.Get("window")
			existing.Delete("window")
			existing.Set("branch", branch)
			existing.Set("worktree", worktree)
			existing.Set("base", base)
			cur, _ := existing.Get("handle")
			switch {
			case handle != "":
			case cur != nil && cur != "":
				rota = cur
			default:
				rota = legacy
				if legacy == "" {
					rota = nil
				}
			}
			existing.Set("handle", rota)
		}
		list, _ := doc.Get("slots")
		l, _ := list.([]any)
		sort.SliceStable(l, func(i, j int) bool {
			a, _ := l[i].(*jsonx.Object)
			b, _ := l[j].(*jsonx.Object)
			return a != nil && b != nil && Str(a, "name") < Str(b, "name")
		})
	})
}

// PoolList returns the registry for `pool list`; ok is false when there is none.
func PoolList(root string) (session any, round any, slots []*jsonx.Object) {
	reg := LoadRegistry(root)
	session, _ = reg.Doc.Get("session")
	round, _ = reg.Doc.Get("round")
	for _, s := range reg.Slots() {
		slots = append(slots, SlotData(s))
	}
	return
}

// Reap removes the worktree and force-deletes the branch of the named slots
// (all of them for names == nil), then drops them from the registry. Worker
// branches are throwaway by construction: they are merged into the cycle
// branch through the gate, never shipped directly. An unknown slot is a no-op.
func (e Env) Reap(root string, names []string, all bool) (reaped []string, err error) {
	e = e.withDefaults()
	reg := LoadRegistry(root)
	if !reg.Exists {
		return nil, nil
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	for _, s := range reg.Slots() {
		name := Str(s, "name")
		if !all && !want[name] {
			continue
		}
		reaped = append(reaped, name)
		if wt := Str(s, "worktree"); wt != "" {
			e.git(root, "worktree", "remove", "--force", wt)
		}
		if br := Str(s, "branch"); br != "" {
			e.git(root, "branch", "-D", br)
		}
	}
	if len(reaped) > 0 {
		if _, code := e.git(root, "worktree", "prune"); code != 0 {
			return reaped, fail(ExitUnavailable, "git worktree prune failed")
		}
	}
	def := jsonx.NewObject()
	def.Set("slots", []any{})
	gone := map[string]bool{}
	for _, n := range reaped {
		gone[n] = true
	}
	err = Update(root, def, func(doc *jsonx.Object) {
		var keep []any
		for _, s := range (Registry{Doc: doc}).Slots() {
			if !gone[Str(s, "name")] {
				keep = append(keep, s)
			}
		}
		if keep == nil {
			keep = []any{}
		}
		doc.Set("slots", keep)
	})
	return reaped, err
}

// NewSlot is a fresh registry entry. Slots are seeded as already-reported idle
// so a parked slot never fires a spurious "it finished" on the first poll.
// Only a slot that has gone BUSY re-arms that report.
func NewSlot(name, branch, worktree, base string, handle any) *jsonx.Object {
	s := jsonx.NewObject()
	s.Set("name", name)
	s.Set("branch", branch)
	s.Set("worktree", worktree)
	s.Set("base", base)
	s.Set("handle", handle)
	s.Set("state", "idle")
	s.Set("task", nil)
	s.Set("pr", nil)
	// Orchestrator relays sent to this slot, for the gate's
	// approval-provenance check; dispatch --relay appends.
	s.Set("relays", []any{})
	s.Set("configDir", nil)
	return s
}
