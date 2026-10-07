package tui

import (
	"unicode"
	"unicode/utf8"
)

// KeyKind is a decoded key.
type KeyKind int

const (
	KeyRune KeyKind = iota
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyPgUp
	KeyPgDn
	KeyEnter
	KeyEsc
	KeyBackspace
	KeyCtrlC
)

// Key is one decoded key press; R is set for KeyRune.
type Key struct {
	Kind KeyKind
	R    rune
}

// Rune is the KeyRune for r.
func Rune(r rune) Key { return Key{Kind: KeyRune, R: r} }

// Is reports whether k is the rune r.
func (k Key) Is(r rune) bool { return k.Kind == KeyRune && k.R == r }

// csiKeys are the sequences a screen uses: arrows (ESC [ A, or ESC O A in
// application mode) and Page Up/Down (ESC [ 5 ~, ESC [ 6 ~).
var csiKeys = map[string]KeyKind{
	"A": KeyUp, "B": KeyDown, "C": KeyRight, "D": KeyLeft,
	"5~": KeyPgUp, "6~": KeyPgDn,
}

// DecodeKeys turns one terminal read into keys. A read holds a whole escape
// sequence (ESC [ A) or a lone ESC, so an ESC at the end of the chunk, or
// followed by something that is not [ or O, is the Esc key. Sequences no
// screen uses (Delete, Home, function keys) are dropped.
func DecodeKeys(b []byte) []Key {
	var out []Key
	for i := 0; i < len(b); {
		c := b[i]
		switch {
		case c == 0x1b:
			if i+1 < len(b) && (b[i+1] == '[' || b[i+1] == 'O') {
				j := i + 2
				for j < len(b) && !(b[j] >= 0x40 && b[j] <= 0x7e) {
					j++
				}
				if j < len(b) {
					if k, ok := csiKeys[string(b[i+2:j+1])]; ok {
						out = append(out, Key{Kind: k})
					}
					j++
				}
				i = j
				continue
			}
			out = append(out, Key{Kind: KeyEsc})
			i++
		case c == '\r' || c == '\n':
			out = append(out, Key{Kind: KeyEnter})
			i++
		case c == 0x7f || c == 0x08:
			out = append(out, Key{Kind: KeyBackspace})
			i++
		case c == 0x03:
			out = append(out, Key{Kind: KeyCtrlC})
			i++
		default:
			r, n := utf8.DecodeRune(b[i:])
			if r != utf8.RuneError && !unicode.IsControl(r) {
				out = append(out, Key{Kind: KeyRune, R: r})
			}
			i += n
		}
	}
	return out
}
