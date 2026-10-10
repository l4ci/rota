package worker

import (
	"context"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/host"
)

// Pane labels are display-only (herdr's sidebar, via host.Labeler): the slot,
// its task and the state rota last recorded. Best effort and never read back.

// stateWord is how a recorded state reads in a label.
func stateWord(state, pr string) string {
	switch strings.ToUpper(state) {
	case StateBusy:
		return "working"
	case StateNeedsPermission:
		return "needs permission"
	case StateDone:
		if pr != "" {
			return "done, PR"
		}
		return "done"
	}
	return strings.ToLower(state)
}

// slotTitle is `<slot> · <task> · <state>`, without the task when it has none.
func slotTitle(slot, task, state, pr string) string {
	parts := []string{slot}
	if task != "" {
		parts = append(parts, task)
	}
	return strings.Join(append(parts, stateWord(state, pr)), " · ")
}

// labelSlot asks a host that can label to set the slot pane's title, "" clears.
func labelSlot(ctx context.Context, h host.Host, name, handle, title string) {
	if l, ok := h.(host.Labeler); ok && handle != "" {
		l.Label(ctx, name, handle, title)
	}
}

// The label helpers read the registry with LoadRegistryTolerant: a pane title
// is cosmetic, so a corrupt registry just skips it (the verbs that matter refuse).

// syncLabel labels a slot's pane from what the registry holds for it now.
func syncLabel(ctx context.Context, h host.Host, root, name string) {
	if _, ok := h.(host.Labeler); !ok {
		return
	}
	if s := LoadRegistryTolerant(root).Slot(name); s != nil && s.State() != "" {
		labelSlot(ctx, h, name, s.PaneHandle(), slotTitle(name, s.Task(), s.State(), s.PR()))
	}
}

// syncChanged relabels the slots of rows whose state differs from the one the
// poll started from, then the workspace token.
func syncChanged(ctx context.Context, h host.Host, root string, rows []PollRow, targets []pollTarget) {
	if _, ok := h.(host.Labeler); !ok {
		return
	}
	prev := map[string]string{}
	for _, t := range targets {
		prev[t.name] = t.prev
	}
	changed := false
	for _, r := range rows {
		if !strings.EqualFold(r.State, prev[r.Name]) {
			syncLabel(ctx, h, root, r.Name)
			changed = true
		}
	}
	if changed {
		labelWorkspace(ctx, h, root)
	}
}

// workspaceToken is the round token: `r10 3 slots: 2 working, 1 blocked, 0 done`.
func workspaceToken(root string) string {
	var n, working, blocked, done int
	for _, s := range LoadRegistryTolerant(root).Slots() {
		n++
		switch strings.ToUpper(s.State()) {
		case StateBusy:
			working++
		case StateBlocked, StateNeedsPermission:
			blocked++
		case StateDone:
			done++
		}
	}
	return fmt.Sprintf("r%d %d slots: %d working, %d blocked, %d done", roundOf(root), n, working, blocked, done)
}

func labelWorkspace(ctx context.Context, h host.Host, root string) {
	if l, ok := h.(host.Labeler); ok {
		l.LabelWorkspace(ctx, workspaceToken(root))
	}
}

// ClearLabel removes a slot pane's label, for a slot that was parked or is
// about to be reaped. No-op for solo, a slot without a session or a host that
// cannot label.
func (e Env) ClearLabel(ctx context.Context, root, slot string) {
	if e.NewHost == nil || RegistryHost(root) == host.Solo {
		return
	}
	s := LoadRegistryTolerant(root).Slot(slot)
	if s == nil || s.PaneHandle() == "" {
		return
	}
	e = e.withDefaults()
	labelSlot(ctx, e.NewHost(e.hostKind(root)), slot, s.PaneHandle(), "")
}
