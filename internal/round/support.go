package round

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// The support split of #1598: with round.support set, an issue labelled
// issues.labels.support belongs to the support machine and every other issue
// to main. Main offers and withdraws the label; each side offers and assigns
// only its own issues. With round.support empty nothing here applies.

// DefaultSupportLabel is issues.labels.support when unset.
const DefaultSupportLabel = "site:support"

// Blocked reasons of the support split and the support side's fresh base.
const (
	// BlockSite: the issue belongs to the other machine's side of the split.
	BlockSite = "site"
	// BlockFetch: the support side could not fetch origin/<base> to cut from.
	BlockFetch = "fetch"
	// BlockConflict: a resumed support branch does not merge origin/<base>.
	BlockConflict = "conflict"
	// BlockConfig: the verb needs a config key that is not set.
	BlockConfig = "config"
)

func (e Env) supportLabel() string { return strutil.FirstNonEmpty(e.SupportLabel, DefaultSupportLabel) }

// offSite is why it belongs to the other side of the split ("" when this
// machine may take it, and always with round.support empty).
func (e Env) offSite(support, role string, it backlog.Item) string {
	if support == "" {
		return ""
	}
	labelled := slices.Contains(it.Labels, e.supportLabel())
	switch {
	case role == roundcfg.RoleSupport && !labelled:
		return fmt.Sprintf("%s is not labelled %s: the support machine (round.role support) takes only the issues main offers it", it.ID, e.supportLabel())
	case role != roundcfg.RoleSupport && labelled:
		return fmt.Sprintf("%s is labelled %s and belongs to the support machine %s: rota round support offer --withdraw %s takes it back", it.ID, e.supportLabel(), support, it.ID)
	}
	return ""
}

// OfferSupport labels ids for the support side, or with withdraw removes the
// label, and returns the ids it changed. Every id is checked before any label
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
		has := slices.Contains(it.Labels, e.supportLabel())
		if has != withdraw {
			continue
		}
		var err error
		if withdraw {
			err = e.Forge.RemoveLabels(ctx, it.IssueKey(), []string{e.supportLabel()})
		} else {
			err = e.Forge.AddLabels(ctx, it.IssueKey(), []string{e.supportLabel()}, true)
		}
		if err != nil {
			return changed, fmt.Errorf("%s: %w", tracker.RefOf(it.ID), err)
		}
		changed = append(changed, it.ID)
	}
	return changed, nil
}

// freshBase is the support side's start point (#1598 AC-9): it fetches
// origin/<base> and returns it, so a support branch is cut from the main the
// main machine last pushed, never from this machine's stale local base. A slot
// resuming its issue on its branch merges it first; a merge that fails is
// aborted and refused (BlockConflict) before the brief goes out.
func (e Env) freshBase(ctx context.Context, root string, slot *worker.Slot, branch string, resuming bool) (string, error) {
	base := strutil.FirstNonEmpty(slot.Base(), e.Base)
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
