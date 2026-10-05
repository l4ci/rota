// Package palette is the interactive menu bare `rota` opens in a terminal.
// The core is pure: State.Update takes a decoded key and returns the next
// state plus what to do, and Render turns a State into a frame string. The
// terminal work (raw mode, key reads, signals) is in run.go and tty.go, behind
// a Config a test fills with fakes. Standard library only.
package palette

import (
	"strings"
	"unicode/utf8"
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
	Items  []Item // entries in scope, in table order
	Filter string
	Sel    int // index into Matches()
}

// New scopes the entries and preselects the default one.
func New(entries []Entry, inProject bool) State {
	var s State
	for _, e := range entries {
		if (e.Scope == InProject && !inProject) || (e.Scope == NoProject && inProject) {
			continue
		}
		s.Items = append(s.Items, Item{N: len(s.Items) + 1, Entry: e})
	}
	s.Sel = s.defaultSel()
	return s
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
	if s.Filter == "" {
		return s.Items
	}
	f := strings.ToLower(s.Filter)
	var out []Item
	for _, it := range s.Items {
		if strings.Contains(strings.ToLower(it.Label), f) {
			out = append(out, it)
		}
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

// Update applies one key. With an empty filter j/k move and q quits; once a
// filter is typing, every letter extends it.
func (s State) Update(k Key) (State, Action) {
	m := s.Matches()
	switch k.Kind {
	case KeyCtrlC:
		return s, Action{Kind: Quit}
	case KeyUp:
		s.Sel = wrap(s.Sel-1, len(m))
	case KeyDown:
		s.Sel = wrap(s.Sel+1, len(m))
	case KeyEnter:
		if s.Sel >= 0 && s.Sel < len(m) {
			return s, runOrQuit(m[s.Sel])
		}
	case KeyBackspace:
		if s.Filter != "" {
			_, n := utf8.DecodeLastRuneInString(s.Filter)
			s.Filter = s.Filter[:len(s.Filter)-n]
			s.Sel = 0
		}
	case KeyEsc:
		if s.Filter == "" {
			return s, Action{Kind: Quit}
		}
		s.Filter, s.Sel = "", s.defaultSel()
	case KeyRune:
		switch {
		case k.R >= '1' && k.R <= '9':
			for _, it := range s.Items {
				if it.N == int(k.R-'0') {
					return s, runOrQuit(it)
				}
			}
		case s.Filter == "" && k.R == 'q':
			return s, Action{Kind: Quit}
		case s.Filter == "" && k.R == 'k':
			s.Sel = wrap(s.Sel-1, len(m))
		case s.Filter == "" && k.R == 'j':
			s.Sel = wrap(s.Sel+1, len(m))
		case k.R == ' ' && s.Filter == "":
		case k.R >= ' ':
			s.Filter += string(k.R)
			s.Sel = 0
		}
	}
	return s, Action{}
}

func wrap(i, n int) int {
	if n == 0 {
		return 0
	}
	return (i%n + n) % n
}
