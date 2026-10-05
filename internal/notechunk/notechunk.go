// Package notechunk splits a note body into the numbered marker comments an
// issue holds it in (_note_parts in bin/hvlib_backend.py). It is pure text: the
// per-comment limit is a parameter, so the environment is read at the call edge.
package notechunk

import (
	"strings"
	"unicode/utf8"

	"github.com/l4ci/rota/internal/marker"
)

// Norm is a note body with CRLF folded to LF and trailing newlines dropped.
func Norm(text string) string {
	return strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
}

func runes(s string) int { return utf8.RuneCountInString(s) }

// KeepLines is str.splitlines(keepends=True).
func KeepLines(s string) []string {
	var out []string
	start, i := 0, 0
	for i < len(s) {
		r, n := utf8.DecodeRuneInString(s[i:])
		i += n
		switch r {
		case '\n', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
		case '\r':
			if i < len(s) && s[i] == '\n' {
				i++
			}
		default:
			continue
		}
		out = append(out, s[start:i])
		start = i
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// Parts is the comment bodies for text: one `<!-- rota:kind -->` comment, or
// numbered `<!-- rota:kind i/n -->` parts of at most limit runes each, split on
// line boundaries (a line longer than a part is cut).
func Parts(kind, text string, limit int) []string {
	text = Norm(text)
	single := marker.NoteHeader(kind, 1, 1)
	if runes(single)+runes(text) <= limit {
		return []string{single + text}
	}
	budget := limit - runes(marker.NoteHeader(kind, 99, 99))
	var chunks []string
	cur := ""
	for _, line := range KeepLines(text) {
		for runes(line) > budget {
			if cur != "" {
				chunks = append(chunks, cur)
				cur = ""
			}
			rs := []rune(line)
			chunks = append(chunks, string(rs[:budget]))
			line = string(rs[budget:])
		}
		if runes(cur)+runes(line) > budget {
			chunks = append(chunks, cur)
			cur = ""
		}
		cur += line
	}
	chunks = append(chunks, cur)
	out := make([]string, len(chunks))
	for i, c := range chunks {
		out[i] = marker.NoteHeader(kind, i+1, len(chunks)) + c
	}
	return out
}
