package palette

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Header is the banner's version and the one-line project context.
type Header struct {
	Version string // "0.10.1"
	Dir     string // project dir, with ~
	Round   string // "idle", "2 active slots"
	Host    string // herdr, tmux or solo
}

// artRows is "rota" in the figlet slant font, padded to a rectangle.
var artRows = []string{
	"               __  ",
	"   _________  / /_____ _",
	"  / ___/ __ \\/ __/ __ `/",
	" / /  / /_/ / /_/ /_/ / ",
	"/_/   \\____/\\__/\\__,_/  ",
}

// narrowCols: under this width the art gives way to a one-line banner.
const narrowCols = 40

// RenderOpts is the terminal a frame is drawn for. Width 0 means unknown.
type RenderOpts struct {
	Width int
	Color bool
}

func (o RenderOpts) sgr(code, text string) string {
	if !o.Color || text == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (o RenderOpts) dim(t string) string  { return o.sgr("2", t) }
func (o RenderOpts) bold(t string) string { return o.sgr("1", t) }
func (o RenderOpts) tint(t string) string { return o.sgr("36", t) }

func versionTag(v string) string {
	if v == "" {
		return ""
	}
	if v[0] >= '0' && v[0] <= '9' {
		return "v" + v
	}
	return v
}

// Banner is the art with the version beside its last row, or a one-line
// "rota <version>" when the terminal is narrow.
func Banner(version string, o RenderOpts) []string {
	art := 0
	for _, r := range artRows {
		art = max(art, utf8.RuneCountInString(strings.TrimRight(r, " ")))
	}
	tag := versionTag(version)
	last := utf8.RuneCountInString(strings.TrimRight(artRows[len(artRows)-1], " ")) + 3 + utf8.RuneCountInString(tag)
	if o.Width > 0 && (o.Width < narrowCols || max(art, last) > o.Width) {
		line := o.tint("rota")
		if tag != "" {
			line += " " + o.dim(strings.TrimPrefix(tag, "v"))
		}
		return []string{line}
	}
	var out []string
	for i, r := range artRows {
		r = strings.TrimRight(r, " ")
		if i == len(artRows)-1 && tag != "" {
			out = append(out, o.tint(r)+"   "+o.dim(tag))
			continue
		}
		out = append(out, o.tint(r))
	}
	return out
}

// fit cuts s to w runes, ending in an ellipsis; w <= 0 leaves it alone.
func fit(s string, w int) string {
	if w <= 0 || utf8.RuneCountInString(s) <= w {
		return s
	}
	r := []rune(s)
	if w == 1 {
		return string(r[:1])
	}
	return string(r[:w-1]) + "…"
}

// Context joins the non-empty parts of the context line.
func (h Header) Context() string {
	var p []string
	for _, s := range []string{h.Dir, h.Round, h.Host} {
		if s != "" {
			p = append(p, s)
		}
	}
	return strings.Join(p, "  ·  ")
}

// Render draws one frame, without the screen-clearing prefix.
func Render(s State, h Header, o RenderOpts) string {
	lines := Banner(h.Version, o)
	lines = append(lines, "")
	if c := h.Context(); c != "" {
		lines = append(lines, o.dim(fit(c, o.Width)))
		lines = append(lines, "")
	}
	m := s.Matches()
	lw := 0
	for _, it := range m {
		lw = max(lw, utf8.RuneCountInString(it.Label))
	}
	for i, it := range m {
		head := fmt.Sprintf("%d  ", it.N)
		label, hint := it.Label, it.Hint
		mark := "  "
		if i == s.Sel {
			mark = "› "
		}
		pad := strings.Repeat(" ", lw-utf8.RuneCountInString(label))
		if o.Width > 0 {
			room := o.Width - utf8.RuneCountInString(mark+head)
			if hint != "" && utf8.RuneCountInString(label)+len(pad)+2+utf8.RuneCountInString(hint) > room {
				hint = ""
			}
			if hint == "" {
				pad = ""
			}
			label = fit(label, room)
		}
		var row string
		if i == s.Sel {
			row = o.tint(mark) + o.bold(head+label)
		} else {
			row = mark + o.dim(head) + label
		}
		if hint != "" {
			row += pad + "  " + o.dim(hint)
		}
		lines = append(lines, row)
	}
	if len(m) == 0 {
		lines = append(lines, o.dim("  no match"))
	}
	lines = append(lines, "")
	if s.Filter != "" {
		lines = append(lines, fit("filter: "+s.Filter+"_", o.Width))
	} else {
		lines = append(lines, o.dim(fit("↑/↓ move · enter run · 1-9 jump · type to filter · q quit", o.Width)))
	}
	return strings.Join(lines, "\n") + "\n"
}
