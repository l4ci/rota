package round

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// With round.support set, machine:<name> pins an issue to that machine.
// With support disabled the pins do not restrict a single-machine round.

// Blocked reasons of the support split and the support side's fresh base.
const (
	// BlockSite: the issue belongs to the other machine's side of the split.
	BlockSite = blockedby.Site
	// BlockFetch: the support side could not fetch origin/<base> to cut from.
	BlockFetch = blockedby.Fetch
	// BlockConflict: a resumed support branch does not merge origin/<base>.
	BlockConflict = blockedby.Conflict
	// BlockDirty: a resumed support branch has uncommitted changes, so it is
	// not merged with origin/<base> at all.
	BlockDirty = blockedby.Dirty
	// BlockConfig: the verb needs a config key that is not set.
	BlockConfig = blockedby.Config
)

// offSite is why it belongs to the other side of the split ("" when this
// machine may take it, and always with round.support empty).
func (e Env) offSite(support, role, legacy string, it backlog.Item) string {
	if support == "" {
		return ""
	}
	if why := legacySupportWarning(support, legacy, it); why != "" {
		return why
	}
	pins := roundcfg.PinsOf(it.Labels)
	if len(pins) > 0 && !slices.Contains(pins, roundcfg.MachineName(support, role)) {
		return fmt.Sprintf("%s is pinned %s elsewhere: remove the pin to move it; on main, rota round support offer --withdraw %s takes a %s issue back", it.ID, strings.Join(pinLabels(pins), ", "), it.ID, roundcfg.MachineLabel(support))
	}
	return ""
}

// supportPinStep makes pulled work visible to the existing pin-based queue,
// watch, provenance and return paths. Only this assignment's new pin is undone;
// a pre-existing pin belongs to the operator, including on a resumed assign.
func (e Env) supportPinStep(ctx context.Context, be Board, it *backlog.Item) step {
	label := roundcfg.MachineLabel(e.Round.Support)
	added := false
	return step{
		name: "pin to support " + it.ID,
		skip: func() bool {
			return e.Round.Role != roundcfg.RoleSupport || e.Round.Support == "" || slices.Contains(it.Labels, label)
		},
		do: func() error {
			if !e.forgeOn(be) {
				return blocked(BlockConfig, "pinning support work needs an issue tracker with labels")
			}
			if err := e.Forge.AddLabels(ctx, it.IssueKey(), []string{label}, true); err != nil {
				return err
			}
			added = true
			return nil
		},
		undo: func() error {
			if !added {
				return nil
			}
			return e.Forge.RemoveLabels(ctx, it.IssueKey(), []string{label})
		},
	}
}

// Block legacy offers on both machines until the explicit migration completes.
func legacySupportWarning(support, legacy string, it backlog.Item) string {
	if legacy == "" {
		legacy = "site:support"
	}
	if support != "" && slices.Contains(it.Labels, legacy) {
		return fmt.Sprintf("%s still carries retired label %s: run rota migrate labels --apply before assigning it", it.ID, legacy)
	}
	return ""
}

func pinLabels(names []string) []string {
	labels := make([]string, len(names))
	for i, name := range names {
		labels[i] = roundcfg.MachineLabel(name)
	}
	return labels
}

// OfferSupport pins ids to the support side, or with withdraw removes the
// pin, and returns the ids it changed. Support can also pull unpinned work. Every id is checked before any label
// moves: an issue a main slot or claim holds is not offered, and one a support
// claim holds is not withdrawn (BlockClaimed). It needs round.support
// (BlockConfig) and a tracker.
func (e Env) OfferSupport(ctx context.Context, root string, be Board, ids []string, withdraw bool) ([]string, error) {
	support := e.Round.Support
	if support == "" {
		return nil, blocked(BlockConfig, "round.support is empty: set it to the support machine's name first (rota config set round.support <name>)")
	}
	if !e.forgeOn(be) {
		return nil, blocked(BlockConfig, "offering issues to the support machine needs an issue tracker with labels")
	}
	held := heldIDs(root)
	prefix := support + "-"
	var items []*backlog.Item
	for _, id := range ids {
		it, err := be.Get(id)
		if err != nil {
			return nil, err
		}
		if it.Closed {
			return nil, fmt.Errorf("%w: %s is closed", backlog.ErrNotFound, id)
		}
		if pins := roundcfg.PinsOf(it.Labels); !withdraw && len(pins) > 0 && !slices.Contains(pins, support) {
			return nil, blocked(BlockSite, "%s is pinned %s: remove that label before offering it to %s", it.ID, strings.Join(pinLabels(pins), ", "), roundcfg.MachineLabel(support))
		}
		st, err := be.Status(it.ID)
		if err != nil {
			return nil, err
		}
		var claims []string
		if st != nil {
			claims = st.Claims
		}
		switch {
		case !withdraw && held[worker.NormID(it.ID)]:
			return nil, blocked(BlockClaimed, "%s is held by a slot of this round: it stays with main", it.ID)
		case !withdraw && slices.ContainsFunc(claims, func(c string) bool { return !strings.HasPrefix(c, prefix) }):
			return nil, blocked(BlockClaimed, "%s is claimed on main (%s): it stays with main", it.ID, strings.Join(claims, ", "))
		case withdraw && slices.ContainsFunc(claims, func(c string) bool { return strings.HasPrefix(c, prefix) }):
			return nil, blocked(BlockClaimed, "%s is claimed by the support machine (%s): it stays there until that claim is released", it.ID, strings.Join(claims, ", "))
		}
		items = append(items, it)
	}
	var changed []string
	for _, it := range items {
		has := slices.Contains(it.Labels, e.SupportLabel)
		if has != withdraw {
			continue
		}
		var err error
		if withdraw {
			err = e.Forge.RemoveLabels(ctx, it.IssueKey(), []string{e.SupportLabel})
		} else {
			err = e.Forge.AddLabels(ctx, it.IssueKey(), []string{e.SupportLabel}, true)
		}
		if err != nil {
			return changed, fmt.Errorf("%s: %w", tracker.RefOf(it.ID), err)
		}
		if !withdraw {
			// The comment is the assignment approval the support PR cites.
			if _, err := be.AddComment(it.ID, "feedback", marker.SupportOfferLine()+"\n"+fmt.Sprintf("Offered to the support machine %s by main.", support)); err != nil {
				return changed, fmt.Errorf("%s: %w", tracker.RefOf(it.ID), err)
			}
		}
		changed = append(changed, it.ID)
	}
	return changed, nil
}

// freshBase is the support side's start point (#1598 AC-9): it fetches
// origin/<base> and returns it, so a support branch is cut from the main the
// main machine last pushed, never from this machine's stale local base. A slot
// resuming its issue on its branch merges it first; a merge that fails is
// aborted and refused (BlockConflict) before the brief goes out, and a dirty
// worktree is refused (BlockDirty) before any merge runs.
func (e Env) freshBase(ctx context.Context, root string, slot *worker.Slot, branch string, resuming bool, target string) (string, error) {
	base := strutil.FirstNonEmpty(target, e.Base)
	res, err := e.Git(ctx, root, "fetch", "-q", "origin", base)
	if err != nil || res.ExitCode != 0 {
		return "", blocked(BlockFetch, "could not fetch origin/%s to cut the support branch from (%s): a support machine never cuts from its local %s", base, gitWhy(res.Stderr, err), base)
	}
	from := "origin/" + base
	if !resuming {
		return from, nil
	}
	wt := slot.Worktree()
	if cur, err := e.Git(ctx, wt, "symbolic-ref", "--short", "-q", "HEAD"); err != nil || strings.TrimSpace(cur.Stdout) != branch {
		return from, nil // not on the branch: the reset cuts it fresh from origin
	}
	// Uncommitted work is the slot's to keep: no merge touches it, and the
	// refusal says dirty, never conflict.
	if st, err := e.Git(ctx, wt, "status", "--porcelain"); err != nil || st.ExitCode != 0 || strings.TrimSpace(st.Stdout) != "" {
		return "", blocked(BlockDirty, "%s has uncommitted changes in %s: commit or discard them before %s is merged in and the slot resumes", branch, wt, from)
	}
	m, err := e.Git(ctx, wt, "merge", "--no-edit", "-q", from)
	if err == nil && m.ExitCode == 0 {
		return from, nil
	}
	e.Git(ctx, wt, "merge", "--abort")
	return "", blocked(BlockConflict, "%s does not merge %s cleanly (%s): resolve it on the branch, then assign again", branch, from, gitWhy(m.Stdout+m.Stderr, err))
}

func gitWhy(out string, err error) string {
	if err != nil {
		return err.Error()
	}
	if s := strings.TrimSpace(out); s != "" {
		return strings.ReplaceAll(s, "\n", "; ")
	}
	return "git failed"
}

// SupportReturnMarker is the first line of the comment `round support return`
// posts on an issue it hands back to main.
const SupportReturnMarker = "<!-- rota:support-return -->"

// BlockRole: the verb belongs to the other machine's role.
const BlockRole = blockedby.Role

// SupportReturned is what ReturnSupport did.
type SupportReturned struct {
	ID      string
	Comment string // id of the posted comment; "" when nothing was posted
	Changed bool
}

// ReturnSupport is the support machine's hand-back: it posts reason on the
// issue under SupportReturnMarker, releases support's claims and removes the
// support pin, so main's candidates can list the issue again. It refuses on main (BlockRole), with
// round.support empty or no tracker (BlockConfig) and while a slot still holds
// the issue (BlockClaimed). An issue that no longer carries the label is
// already main's: nothing is posted and Changed is false.
func (e Env) ReturnSupport(ctx context.Context, root string, be Board, id, reason string) (SupportReturned, error) {
	switch {
	case e.Round.Support == "":
		return SupportReturned{}, blocked(BlockConfig, "round.support is empty: set it to the support machine's name first (rota config set round.support <name>)")
	case !e.forgeOn(be):
		return SupportReturned{}, blocked(BlockConfig, "handing an issue back needs an issue tracker with labels")
	case e.Round.Role != roundcfg.RoleSupport:
		return SupportReturned{}, blocked(BlockRole, "round support return runs on the support machine (round.role support); main takes an offer back with: rota round support offer --withdraw")
	}
	it, err := be.Get(id)
	if err != nil {
		return SupportReturned{}, err
	}
	if it.Closed {
		return SupportReturned{}, fmt.Errorf("%w: %s is closed", backlog.ErrNotFound, id)
	}
	if heldIDs(root)[worker.NormID(it.ID)] {
		return SupportReturned{}, blocked(BlockClaimed, "%s is held by a slot of this round: return or reclaim that slot first", it.ID)
	}
	out := SupportReturned{ID: it.ID}
	if !slices.Contains(it.Labels, e.SupportLabel) {
		return out, nil
	}
	st, err := be.Status(it.ID)
	if err != nil {
		return out, err
	}
	if out.Comment, err = be.AddComment(it.ID, "feedback", SupportReturnMarker+"\n"+strings.TrimSpace(reason)); err != nil {
		return out, err
	}
	if st != nil {
		for _, claim := range openClaimsOf(st) {
			if !strings.HasPrefix(claim, e.Round.Support+"-") {
				continue
			}
			if _, err := be.Release(it.ID, claim); err != nil {
				return out, fmt.Errorf("%s: release %s: %w", tracker.RefOf(it.ID), claim, err)
			}
		}
	}
	if err := e.Forge.RemoveLabels(ctx, it.IssueKey(), []string{e.SupportLabel}); err != nil {
		return out, fmt.Errorf("%s: %w", tracker.RefOf(it.ID), err)
	}
	out.Changed = true
	return out, nil
}
