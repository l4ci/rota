package tui

import "strings"

// Detail is a scrolling text pane: a title over a wrapped body.
type Detail struct {
	Title string
	Body  string // wrapped at the render width on spaces; its own newlines kept
	Top   int    // first body line shown
}

// Update scrolls on Up/k, Down/j, PgUp and PgDn. Render clamps Top above,
// since only it knows the width.
func (d Detail) Update(k Key) (Detail, bool) {
	switch {
	case k.Kind == KeyUp || k.Is('k'):
		d.Top--
	case k.Kind == KeyDown || k.Is('j'):
		d.Top++
	case k.Kind == KeyPgUp:
		d.Top -= 10
	case k.Kind == KeyPgDn:
		d.Top += 10
	default:
		return d, false
	}
	d.Top = max(d.Top, 0)
	return d, true
}

// Render draws the title, then the body lines from Top, h lines in all.
func (d Detail) Render(w, h int, st Style) string {
	var out []string
	if d.Title != "" {
		out = append(out, st.Bold(Fit(d.Title, w)))
	}
	lines := Wrap(d.Body, w)
	room := len(lines)
	if h > 0 {
		room = max(h-len(out), 0)
	}
	top := min(max(d.Top, 0), max(len(lines)-room, 0))
	for i := top; i < len(lines) && i < top+room; i++ {
		out = append(out, lines[i])
	}
	return strings.Join(out, "\n")
}

// Wrap breaks s into lines of at most w runes on spaces, keeping its own
// newlines. A word longer than w is cut; w <= 0 splits on newlines only.
func Wrap(s string, w int) []string {
	out := []string{}
	if s == "" {
		return out
	}
	for _, para := range strings.Split(strings.TrimSuffix(s, "\n"), "\n") {
		if w <= 0 {
			out = append(out, para)
			continue
		}
		cur := ""
		n := 0 // runes in cur
		for _, word := range strings.Fields(para) {
			r := []rune(word)
			for len(r) > w {
				if cur != "" {
					out = append(out, cur)
					cur, n = "", 0
				}
				out = append(out, string(r[:w]))
				r = r[w:]
			}
			switch {
			case n == 0:
				cur, n = string(r), len(r)
			case n+1+len(r) <= w:
				cur, n = cur+" "+string(r), n+1+len(r)
			default:
				out = append(out, cur)
				cur, n = string(r), len(r)
			}
		}
		out = append(out, cur)
	}
	return out
}
