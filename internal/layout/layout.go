// Package layout arranges a round's herdr panes for the screen in front of
// you: `split` folds the live workers into the orchestrator's tab, one grid,
// for a wide screen; `tabs` gives every worker its own tab again, for a narrow
// one. It only moves panes. No process restarts and nothing is typed, so the
// agents never notice.
//
// The grid: the left column takes 40% of the width and holds the orchestrator
// (O), under the pane that launched the round (C) when it is known, a clean
// CLI at a quarter of the column's height. The workers fill the right 60%:
// columns two panes high in slot order, column-major, equal widths. An odd
// count leaves the last column as one full-height pane. The tab is called
// "rota" and the orchestrator pane "orchestrator".
//
//	n=3                  n=4
//	+-----+---+---+      +-----+---+---+
//	|  C  | 1 |   |      |  C  | 1 | 3 |
//	+-----+---+ 3 |      +-----+---+---+
//	|  O  | 2 |   |      |  O  | 2 | 4 |
//	+-----+---+---+      +-----+---+---+
package layout

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/repos"
)

// Label names the orchestrator's tab or window (`rota orchestrate` opens it),
// and the orchestrator's pane in a split.
const Label = "orchestrator"

// SplitLabel names the tab that holds the whole split: the CLI, the
// orchestrator and the workers.
const SplitLabel = "rota"

// Shares of the split: the left column's width, and the CLI's height in it.
const (
	leftShare = 0.4
	cliShare  = 0.25
)

// Layout names how a project's panes are arranged.
const (
	Split = "split" // every live worker is in the orchestrator's tab
	Tabs  = "tabs"  // no worker is (also: no live worker at all)
	Mixed = "mixed" // some are
)

// Worker is a live slot: one whose agent has a pane. Slot order is the order
// of the registry.
type Worker struct{ Slot, Pane string }

// Placed is a pane and the tab it is in.
type Placed struct{ Slot, Pane, Tab string }

// State is where a project's panes are right now.
type State struct {
	Orch host.LayoutPane
	// CLI is the pane that launched the round, nil when unknown or gone.
	CLI     *host.LayoutPane
	Workers []Placed
	// Foreign are the other panes of the orchestrator's tab: a shell the
	// person opened. They are never moved.
	Foreign []host.LayoutPane
}

// Tab is the tab the split lives in: the CLI's when there is one, else the
// orchestrator's.
func (s State) Tab() string {
	if s.CLI != nil {
		return s.CLI.Tab
	}
	return s.Orch.Tab
}

// base is the pane the grid grows from.
func (s State) base() string {
	if s.CLI != nil {
		return s.CLI.ID
	}
	return s.Orch.ID
}

// Layout classifies the state.
func (s State) Layout() string {
	in, total := 0, len(s.Workers)
	for _, w := range s.Workers {
		if w.Tab == s.Tab() {
			in++
		}
	}
	if s.CLI != nil {
		total++
		if s.Orch.Tab == s.Tab() {
			in++
		}
	}
	switch {
	case in == 0:
		return Tabs
	case in == total:
		return Split
	}
	return Mixed
}

// Find locates the project's orchestrator among the panes and places its
// workers. The orchestrator is a pane working in root that no worker owns,
// preferring one in a tab or workspace named label (what `rota orchestrate`
// opens) and one running an agent. ok is false when there is none. cli is the
// pane that launched the round ("" when unknown): it is kept only while it
// still exists and is neither the orchestrator nor a worker.
func Find(panes []host.LayoutPane, root, label, cli string, workers []Worker) (st State, ok bool) {
	byPane := map[string]host.LayoutPane{}
	owned := map[string]bool{}
	for _, p := range panes {
		byPane[p.ID] = p
	}
	for _, w := range workers {
		owned[w.Pane] = true
	}
	root = repos.Realpath(root)
	best, top := -1, -1
	for i, p := range panes {
		if owned[p.ID] || !(sameDir(p.Cwd, root) || sameDir(p.FgCwd, root)) {
			continue
		}
		if score := scoreOf(p, label); score > top {
			best, top = i, score
		}
	}
	if best < 0 {
		return State{}, false
	}
	st.Orch = panes[best]
	for _, w := range workers {
		if p, here := byPane[w.Pane]; here {
			st.Workers = append(st.Workers, Placed{Slot: w.Slot, Pane: w.Pane, Tab: p.Tab})
		}
	}
	if p, here := byPane[cli]; here && cli != "" && cli != st.Orch.ID && !owned[cli] {
		st.CLI = &p
	}
	for _, p := range panes {
		if p.Tab == st.Tab() && p.ID != st.Orch.ID && !owned[p.ID] && (st.CLI == nil || p.ID != st.CLI.ID) {
			st.Foreign = append(st.Foreign, p)
		}
	}
	return st, true
}

func scoreOf(p host.LayoutPane, label string) int {
	s := 0
	if p.TabLabel == label || p.WorkspaceName == label {
		s += 2
	}
	if p.Agent != "" {
		s++
	}
	return s
}

func sameDir(p, root string) bool {
	return p != "" && repos.Realpath(filepath.Clean(p)) == root
}

// Grid is the target arrangement: the columns left to right, each a list of
// pane ids top to bottom. The left column is the CLI (when cli is not "")
// over the orchestrator.
func Grid(cli, orch string, workers []Placed) [][]string {
	left := []string{orch}
	if cli != "" {
		left = []string{cli, orch}
	}
	cols := [][]string{left}
	for i := 0; i < len(workers); i += 2 {
		col := []string{workers[i].Pane}
		if i+1 < len(workers) {
			col = append(col, workers[i+1].Pane)
		}
		cols = append(cols, col)
	}
	return cols
}

// Result is what a run did.
type Result struct {
	Before, After string
	// Moves is how many panes were moved.
	Moves int
}

// ToSplit makes the base tab the grid. It does nothing when the panes already
// sit in it. Otherwise it first sends every pane that is in the way back out
// to a tab of its own (herdr cannot re-split inside a tab: a move into the
// pane's own tab is a no-op), then builds the grid from scratch: columns
// first, so the left column is still one full-height pane, then the splits
// inside each column.
func ToSplit(ctx context.Context, h host.Layouter, st State) (Result, error) {
	res := Result{Before: st.Layout()}
	res.After = res.Before
	if len(st.Workers) == 0 {
		return res, nil
	}
	cli := ""
	if st.CLI != nil {
		cli = st.CLI.ID
	}
	want := Grid(cli, st.Orch.ID, st.Workers)
	tab := st.Tab()
	if st.Layout() == Split {
		rects, err := h.PaneRects(ctx, st.base())
		if err != nil {
			return res, err
		}
		if sameGrid(want, rects) {
			if st.Orch.TabLabel != SplitLabel {
				if err := name(ctx, h, st, tab); err != nil {
					return res, err
				}
			}
			return res, nil
		}
	}
	if st.CLI != nil && st.Orch.Tab == tab {
		if err := h.ToNewTab(ctx, st.Orch.ID, Label); err != nil {
			return res, err
		}
		res.Moves++
	}
	for _, w := range st.Workers {
		if w.Tab != tab && w.Tab != st.Orch.Tab {
			continue
		}
		if err := h.ToNewTab(ctx, w.Pane, w.Slot); err != nil {
			return res, err
		}
		res.Moves++
	}
	// Worker columns first. herdr's ratio is the share the target pane keeps:
	// the first split leaves the left column its share, and each later pane
	// holds the columns still to place (itself included), so it keeps 1/c of
	// its room: equal columns.
	cols := len(want) - 1
	prev := st.base()
	for k := 1; k <= cols; k++ {
		head := want[k][0]
		keep := leftShare
		if k > 1 {
			keep = 1 / float64(cols-k+2)
		}
		if err := h.SplitInto(ctx, head, tab, prev, "right", keep); err != nil {
			return res, err
		}
		res.Moves++
		prev = head
	}
	// Then each two-pane worker column splits down the middle.
	for _, col := range want[1:] {
		if len(col) < 2 {
			continue
		}
		if err := h.SplitInto(ctx, col[1], tab, col[0], "down", 0.5); err != nil {
			return res, err
		}
		res.Moves++
	}
	// Last, the orchestrator under the CLI: the split only cuts the CLI's cell.
	if st.CLI != nil {
		if err := h.SplitInto(ctx, st.Orch.ID, tab, st.CLI.ID, "down", cliShare); err != nil {
			return res, err
		}
		res.Moves++
	}
	if err := name(ctx, h, st, tab); err != nil {
		return res, err
	}
	res.After = Split
	return res, nil
}

// name labels the split's tab and the orchestrator's pane.
func name(ctx context.Context, h host.Layouter, st State, tab string) error {
	if err := h.RenameTab(ctx, tab, SplitLabel); err != nil {
		return err
	}
	return h.RenamePane(ctx, st.Orch.ID, Label)
}

// ToTabs gives every worker in the split's tab a tab of its own, in slot
// order so they land in the tab bar in that order. A CLI sharing the tab keeps
// it; the orchestrator goes out to a tab named Label, and when the
// orchestrator's tab was the split's it gets that name back.
func ToTabs(ctx context.Context, h host.Layouter, st State) (Result, error) {
	res := Result{Before: st.Layout()}
	res.After = res.Before
	tab := st.Tab()
	for _, w := range st.Workers {
		if w.Tab != tab {
			continue
		}
		if err := h.ToNewTab(ctx, w.Pane, w.Slot); err != nil {
			return res, err
		}
		res.Moves++
	}
	switch {
	case st.CLI != nil && st.Orch.Tab == tab:
		if err := h.ToNewTab(ctx, st.Orch.ID, Label); err != nil {
			return res, err
		}
		res.Moves++
	case st.CLI == nil && st.Orch.TabLabel == SplitLabel:
		if err := h.RenameTab(ctx, tab, Label); err != nil {
			return res, err
		}
	}
	res.After = Tabs
	return res, nil
}

// sameGrid reports whether the panes sit in the want columns: same column
// order by x and same top-to-bottom order inside each. Widths are not
// compared, so a column the person dragged stays where they put it.
func sameGrid(want [][]string, rects map[string]host.Rect) bool {
	type cell struct {
		id string
		r  host.Rect
	}
	byX := map[int][]cell{}
	for _, col := range want {
		for _, id := range col {
			r, ok := rects[id]
			if !ok {
				return false
			}
			byX[r.X] = append(byX[r.X], cell{id: id, r: r})
		}
	}
	xs := make([]int, 0, len(byX))
	for x := range byX {
		xs = append(xs, x)
	}
	sort.Ints(xs)
	if len(xs) != len(want) {
		return false
	}
	for i, x := range xs {
		cells := byX[x]
		sort.Slice(cells, func(a, b int) bool { return cells[a].r.Y < cells[b].r.Y })
		if len(cells) != len(want[i]) {
			return false
		}
		for j, c := range cells {
			if c.id != want[i][j] {
				return false
			}
		}
	}
	return true
}

// Describe is a one-line account of a result, for the text output.
func (r Result) Describe() string {
	if r.Moves == 0 {
		return fmt.Sprintf("%s (already)", r.After)
	}
	return fmt.Sprintf("%s (%d moved)", r.After, r.Moves)
}

// Arrange puts root's live workers into mode: the arrangement `rota layout`
// makes and a round keeps for the workers it spawns later. ok is false when
// the orchestrator's pane is not among the panes.
func Arrange(ctx context.Context, h host.Layouter, root, mode, cli string, workers []Worker) (st State, res Result, ok bool, err error) {
	panes, err := h.LayoutPanes(ctx)
	if err != nil {
		return State{}, Result{}, false, err
	}
	st, ok = Find(panes, root, Label, cli, workers)
	if !ok {
		return State{}, Result{}, false, nil
	}
	if mode == Split {
		res, err = ToSplit(ctx, h, st)
	} else {
		res, err = ToTabs(ctx, h, st)
	}
	return st, res, true, err
}
