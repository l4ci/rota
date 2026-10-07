package tui

import "strings"

// Hint is one entry of the key-hint bar.
type Hint struct{ Key, Desc string }

// Hints is the bar "key desc · key desc", cut to w and dimmed.
func Hints(hs []Hint, w int, st Style) string {
	parts := make([]string, len(hs))
	for i, h := range hs {
		parts[i] = h.Key + " " + h.Desc
	}
	return st.Dim(Fit(strings.Join(parts, " · "), w))
}

// Frame stacks body over bar in exactly h lines: the body is cut or padded
// with empty lines, the bar is the last line. With h <= 0 the body is whole.
// Lines are not cut to the width; callers fit them.
func Frame(body, bar string, h int) string {
	if h <= 0 {
		return body + "\n" + bar
	}
	lines := strings.Split(body, "\n")
	for len(lines) < h-1 {
		lines = append(lines, "")
	}
	return strings.Join(append(lines[:h-1], bar), "\n")
}

// Columns sets two blocks side by side, the left padded to lw, a " │ " between.
func Columns(left, right string, lw int) string {
	l, r := strings.Split(left, "\n"), strings.Split(right, "\n")
	n := max(len(l), len(r))
	rows := make([]string, n)
	for i := range rows {
		var a, b string
		if i < len(l) {
			a = l[i]
		}
		if i < len(r) {
			b = r[i]
		}
		rows[i] = Pad(a, lw) + " │"
		if b != "" {
			rows[i] += " " + b
		}
	}
	return strings.Join(rows, "\n")
}
