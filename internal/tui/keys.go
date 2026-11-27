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

// decoder turns terminal input into keys. A read may end anywhere, so it keeps
// the bytes of an unfinished escape sequence or UTF-8 character for the next.
type decoder struct {
	buf []byte
	// stale marks buf as the start of input that was dropped: whatever it
	// completes to is swallowed.
	stale bool
}

// feed adds the bytes of one read and returns the keys they complete.
func (d *decoder) feed(b []byte) []Key {
	d.buf = append(d.buf, b...)
	var out []Key
	for len(d.buf) > 0 {
		n, k, ok := nextKey(d.buf)
		if n == 0 {
			break
		}
		if ok && !d.stale {
			out = append(out, k)
		}
		d.buf, d.stale = d.buf[n:], false
	}
	return out
}

// escaped reports that the unfinished input starts with ESC, which only a
// pause can tell apart from the Esc key.
func (d *decoder) escaped() bool { return len(d.buf) > 0 && d.buf[0] == 0x1b }

// loneEsc reports that the unfinished input is an ESC and nothing else.
func (d *decoder) loneEsc() bool { return len(d.buf) == 1 && d.buf[0] == 0x1b }

// flush ends the unfinished input: a lone ESC is the Esc key, anything else
// is dropped.
func (d *decoder) flush() []Key {
	var out []Key
	if d.loneEsc() && !d.stale {
		out = []Key{{Kind: KeyEsc}}
	}
	d.buf, d.stale = nil, false
	return out
}

// discard drops the unfinished input, its tail included when that arrives.
func (d *decoder) discard() { d.stale = len(d.buf) > 0 }

// nextKey reads the token at the start of b. n is its length, 0 when b ends
// before it does; ok says it is a key. Sequences no screen uses (Delete, Home,
// function keys) and invalid bytes are tokens that are no key.
func nextKey(b []byte) (n int, k Key, ok bool) {
	switch c := b[0]; {
	case c == 0x1b:
		if len(b) < 2 {
			return 0, k, false
		}
		if b[1] != '[' && b[1] != 'O' {
			return 1, Key{Kind: KeyEsc}, true
		}
		j := 2
		for j < len(b) && b[j] >= 0x20 && b[j] <= 0x3f { // parameters
			j++
		}
		if j == len(b) {
			return 0, k, false
		}
		if b[j] < 0x40 || b[j] > 0x7e {
			return j, k, false // broken off: b[j] starts the next token
		}
		kind, ok := csiKeys[string(b[2:j+1])]
		return j + 1, Key{Kind: kind}, ok
	case c == '\r' || c == '\n':
		return 1, Key{Kind: KeyEnter}, true
	case c == 0x7f || c == 0x08:
		return 1, Key{Kind: KeyBackspace}, true
	case c == 0x03:
		return 1, Key{Kind: KeyCtrlC}, true
	}
	if !utf8.FullRune(b) {
		return 0, k, false
	}
	r, size := utf8.DecodeRune(b)
	return size, Rune(r), r != utf8.RuneError && !unicode.IsControl(r)
}
