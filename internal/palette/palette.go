// Package palette is the interactive menu bare `rota` opens in a terminal.
// The core is pure: State.Update takes a decoded key and returns the next
// state plus what to do, and Render turns a State into a frame string. The
// terminal work (raw mode, key reads, signals) is internal/tui's driver, run
// from run.go behind a Config a test fills with fakes. Standard library only.
package palette

import (
	"github.com/l4ci/rota/internal/tui"
)

// Scope says where an entry shows: everywhere, only in an initialized
// project, or only outside one.
type Scope int

const (
	Always Scope = iota
	InProject
	NoProject
)

// Entry is one line of the palette. Adding an action is adding one Entry to
// the table the caller builds.
type Entry struct {
	Label string
	Hint  string
	Scope Scope
	// Default marks the entry preselected when it is in scope; the first one
	// in scope wins.
	Default bool
	// Quit leaves the palette without running anything.
	Quit bool
	// Ends leaves the palette after Run (the action may exec, or ends the
	// session); other entries return to the palette after "press any key".
	Ends bool
	Run  func() error
}

// Item is an entry in scope with its fixed number: the digit that runs it.
type Item struct {
	N int
	Entry
}

// State is everything a frame depends on.
type State struct {
	Items []Item   // entries in scope, in table order
	List  tui.List // their labels, typed into; its Filter and Sel are the palette's
}

// New scopes the entries and preselects the default one.
func New(entries []Entry, inProject bool) State {
	var items []Item
	for _, e := range entries {
		if (e.Scope == InProject && !inProject) || (e.Scope == NoProject && inProject) {
			continue
		}
		items = append(items, Item{N: len(items) + 1, Entry: e})
	}
	s := newState(items)
	s.List.Sel = s.defaultSel()
	return s
}

func newState(items []Item) State {
	labels := make([]string, len(items))
	for i, it := range items {
		labels[i] = it.Label
	}
	return State{Items: items, List: tui.List{Items: labels, Typeahead: true}}
}

func (s State) defaultSel() int {
	for i, it := range s.Items {
		if it.Default {
			return i
		}
	}
	return 0
}

// Matches are the items whose label contains the filter, ignoring case.
func (s State) Matches() []Item {
	var out []Item
	for _, i := range s.List.Matches() {
		out = append(out, s.Items[i])
	}
	return out
}

// ActionKind is what the driver does after an Update.
type ActionKind int

const (
	None ActionKind = iota
	Quit
	RunItem
)

// Action is Update's second result.
type Action struct {
	Kind ActionKind
	Item Item
}

func runOrQuit(it Item) Action {
	if it.Quit {
		return Action{Kind: Quit}
	}
	return Action{Kind: RunItem, Item: it}
}

// Update applies one key. A digit runs its entry, even when a filter hides
// it; the list moves and filters (j/k move and q quits only while the filter
// is empty); Esc clears the filter, then quits.
func (s State) Update(k tui.Key) (State, Action) {
	switch {
	case k.Kind == tui.KeyCtrlC:
		return s, Action{Kind: Quit}
	case k.Kind == tui.KeyEnter:
		if i, ok := s.List.Selected(); ok {
			return s, runOrQuit(s.Items[i])
		}
		return s, Action{}
	case k.Kind == tui.KeyRune && k.R >= '1' && k.R <= '9':
		for _, it := range s.Items {
			if it.N == int(k.R-'0') {
				return s, runOrQuit(it)
			}
		}
		return s, Action{}
	case s.List.Filter == "" && (k.Kind == tui.KeyEsc || k.Is('q')):
		return s, Action{Kind: Quit}
	}
	s.List, _ = s.List.Update(k)
	if k.Kind == tui.KeyEsc {
		s.List.Sel = s.defaultSel()
	}
	return s, Action{}
}
