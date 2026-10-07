package tui

import "strings"

// List is a selectable, filterable list of rows.
type List struct {
	Items     []string // rows as shown; the filter matches them, case-insensitive substring
	Sel       int      // index into Matches()
	Filter    string
	Filtering bool // '/' was pressed: runes go to the filter
	Typeahead bool // runes type into the filter directly (the palette's mode)
	// Mark, when set, styles an unselected row: item is its Items index and
	// text the row cut to the width, still plain.
	Mark func(item int, text string, st Style) string
}

// Matches is the Items indexes that contain Filter, in order.
func (l List) Matches() []int {
	f := strings.ToLower(l.Filter)
	var out []int
	for i, it := range l.Items {
		if f == "" || strings.Contains(strings.ToLower(it), f) {
			out = append(out, i)
		}
	}
	return out
}

// Selected is the Items index of the selection; false when nothing matches.
func (l List) Selected() (int, bool) {
	m := l.Matches()
	if len(m) == 0 {
		return 0, false
	}
	if l.Sel < 0 || l.Sel >= len(m) {
		return m[0], true
	}
	return m[l.Sel], true
}

func (l List) move(d int) List {
	n := len(l.Matches())
	if n == 0 {
		l.Sel = 0
		return l
	}
	l.Sel = ((l.Sel+d)%n + n) % n
	return l
}

func (l List) typed(r rune) List {
	l.Filter += string(r)
	l.Sel = 0
	return l
}

func (l List) backspace() List {
	if r := []rune(l.Filter); len(r) > 0 {
		l.Filter = string(r[:len(r)-1])
		l.Sel = 0
	}
	return l
}

// Update handles the keys the list understands; the bool says it did. Keys it
// leaves alone (q, Enter outside filtering, Esc with no filter) are the
// screen's.
func (l List) Update(k Key) (List, bool) {
	switch k.Kind {
	case KeyUp:
		return l.move(-1), true
	case KeyDown:
		return l.move(1), true
	case KeyEnter:
		if l.Filtering {
			l.Filtering = false
			return l, true
		}
	case KeyEsc:
		if l.Filtering || l.Filter != "" {
			l.Filter, l.Filtering, l.Sel = "", false, 0
			return l, true
		}
	case KeyBackspace:
		switch {
		case l.Filtering && l.Filter == "":
			l.Filtering = false
			return l, true
		case l.Filtering || (l.Typeahead && l.Filter != ""):
			return l.backspace(), true
		}
	case KeyRune:
		if k.R < ' ' {
			break
		}
		switch {
		case l.Filtering:
			return l.typed(k.R), true
		case l.Typeahead && l.Filter != "":
			return l.typed(k.R), true
		case l.Typeahead && k.R == ' ':
			return l, true
		case k.Is('j'):
			return l.move(1), true
		case k.Is('k'):
			return l.move(-1), true
		case l.Typeahead && !k.Is('q'):
			return l.typed(k.R), true
		case !l.Typeahead && k.Is('/'):
			l.Filtering = true
			return l, true
		}
	}
	return l, false
}

// FilterLine is the filter as a status line, or "" when there is none.
func (l List) FilterLine() string {
	if l.Filtering || l.Filter != "" {
		return "filter: " + l.Filter + "_"
	}
	return ""
}

// Render draws the visible page of matches, one row each, h rows at most.
// The page is the one holding the selection.
func (l List) Render(w, h int, st Style) string {
	m := l.Matches()
	if len(m) == 0 {
		return st.Dim("  no match")
	}
	sel := l.Sel
	if sel < 0 || sel >= len(m) {
		sel = 0
	}
	start, end := 0, len(m)
	if h > 0 && len(m) > h {
		start = sel - sel%h
		end = min(start+h, len(m))
	}
	rows := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		text := Fit(l.Items[m[i]], w-2)
		if i == sel {
			rows = append(rows, st.Cyan("› ")+st.Bold(text))
		} else if l.Mark != nil {
			rows = append(rows, "  "+l.Mark(m[i], text, st))
		} else {
			rows = append(rows, "  "+text)
		}
	}
	return strings.Join(rows, "\n")
}
