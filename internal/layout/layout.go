// Package layout arranges a round's herdr panes for the screen in front of
// you: `split` folds the live workers into the orchestrator's tab, one grid,
// for a wide screen; `tabs` gives every worker its own tab again, for a narrow
// one. It only moves panes. No process restarts and nothing is typed, so the
// agents never notice.
//
// The grid: the orchestrator is the left column at full height, the workers
// fill columns two panes high in slot order, column-major. An odd count leaves
// the last column as one full-height pane. Every column gets an equal width.
//
//	n=3            n=4
//	+---+---+---+  +---+---+---+
//	|   | 1 |   |  |   | 1 | 3 |
//	| O +---+ 3 |  | O +---+---+
//	|   | 2 |   |  |   | 2 | 4 |
//	+---+---+---+  +---+---+---+
package layout

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/repos"
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
	Orch    host.LayoutPane
	Workers []Placed
	// Foreign are the other panes of the orchestrator's tab: a shell the
	// person opened. They are never moved.
	Foreign []host.LayoutPane
}

// Layout classifies the state.
func (s State) Layout() string {
	in := 0
	for _, w := range s.Workers {
		if w.Tab == s.Orch.Tab {
			in++
		}
	}
	switch {
	case in == 0:
		return Tabs
	case in == len(s.Workers):
		return Split
	}
	return Mixed
}

// Find locates the project's orchestrator among the panes and places its
// workers. The orchestrator is a pane working in root that no worker owns,
// preferring one in a tab or workspace named label (what `rota orchestrate`
// opens) and one running an agent. ok is false when there is none.
func Find(panes []host.LayoutPane, root, label string, workers []Worker) (st State, ok bool) {
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
	for _, p := range panes {
		if p.Tab == st.Orch.Tab && p.ID != st.Orch.ID && !owned[p.ID] {
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
// pane ids top to bottom. The orchestrator is column one.
func Grid(orch string, workers []Placed) [][]string {
	cols := [][]string{{orch}}
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

// ToSplit makes the orchestrator's tab the grid. It does nothing when the
// panes already sit in it. Otherwise it first sends every worker already in
// the tab back out to its own tab (herdr cannot re-split inside a tab: a move
// into the pane's own tab is a no-op), then builds the grid from scratch.
func ToSplit(ctx context.Context, h host.Layouter, st State) (Result, error) {
	res := Result{Before: st.Layout()}
	res.After = res.Before
	if len(st.Workers) == 0 {
		return res, nil
	}
	want := Grid(st.Orch.ID, st.Workers)
	if st.Layout() == Split {
		rects, err := h.PaneRects(ctx, st.Orch.ID)
		if err != nil {
			return res, err
		}
		if sameGrid(want, rects) {
			return res, nil
		}
	}
	for _, w := range st.Workers {
		if w.Tab != st.Orch.Tab {
			continue
		}
		if err := h.ToNewTab(ctx, w.Pane, w.Slot); err != nil {
			return res, err
		}
		res.Moves++
	}
	// Columns first. herdr's ratio is the share the target pane keeps, so a
	// pane that leaves c columns of room (itself included) keeps 1/c: that
	// halves the room 3, 2, 1 columns ahead into equal thirds, and so on.
	cols := len(want)
	prev := st.Orch.ID
	for k := 1; k < cols; k++ {
		head := want[k][0]
		if err := h.SplitInto(ctx, head, st.Orch.Tab, prev, "right", 1/float64(cols-k+1)); err != nil {
			return res, err
		}
		res.Moves++
		prev = head
	}
	// Then each two-pane column splits down the middle.
	for _, col := range want[1:] {
		if len(col) < 2 {
			continue
		}
		if err := h.SplitInto(ctx, col[1], st.Orch.Tab, col[0], "down", 0.5); err != nil {
			return res, err
		}
		res.Moves++
	}
	res.After = Split
	return res, nil
}

// ToTabs gives every worker in the orchestrator's tab a tab of its own, in
// slot order so they land in the tab bar in that order.
func ToTabs(ctx context.Context, h host.Layouter, st State) (Result, error) {
	res := Result{Before: st.Layout()}
	res.After = res.Before
	for _, w := range st.Workers {
		if w.Tab != st.Orch.Tab {
			continue
		}
		if err := h.ToNewTab(ctx, w.Pane, w.Slot); err != nil {
			return res, err
		}
		res.Moves++
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
