package tui

import (
	"os"
	"strings"
	"testing"
	"time"
)

// pause is the script chunk that lets an armed read deadline run out.
const pause = "<pause>"

// byteIn is a deadline-capable scripted terminal that keeps what a Read did
// not take. A pause chunk times the read out if a deadline is armed and is
// skipped otherwise, so no test depends on the clock.
type byteIn struct {
	*fakeTerm
	armed bool
}

func (b *byteIn) SetReadDeadline(at time.Time) error {
	b.armed = !at.IsZero()
	return nil
}

func (b *byteIn) Read(p []byte) (int, error) {
	for len(b.chunks) > 0 && string(b.chunks[0]) == pause {
		b.chunks = b.chunks[1:]
		if b.armed {
			return 0, os.ErrDeadlineExceeded
		}
	}
	return b.fakeTerm.Read(p)
}

// runBytes runs a screen on the chunks and returns what Update saw.
func runBytes(t *testing.T, on func(Msg) Cmd, chunks ...string) string {
	t.Helper()
	ft := script(chunks...)
	tc := ft.term()
	tc.In = &byteIn{fakeTerm: ft}
	r := &rec{on: on}
	if err := Run(tc, tm{r: r}); err != nil {
		t.Fatal(err)
	}
	return r.trace()
}

func keyTrace(keys ...Key) string {
	r := &rec{}
	for _, k := range keys {
		r.add(k)
	}
	return r.trace()
}

func TestRunDecodesKeys(t *testing.T) {
	k := func(kind KeyKind) Key { return Key{Kind: kind} }
	cases := []struct {
		name, in string
		want     []Key
	}{
		{"up CSI", "\x1b[A", []Key{k(KeyUp)}},
		{"down CSI", "\x1b[B", []Key{k(KeyDown)}},
		{"right CSI", "\x1b[C", []Key{k(KeyRight)}},
		{"left CSI", "\x1b[D", []Key{k(KeyLeft)}},
		{"up SS3", "\x1bOA", []Key{k(KeyUp)}},
		{"down SS3", "\x1bOB", []Key{k(KeyDown)}},
		{"right SS3", "\x1bOC", []Key{k(KeyRight)}},
		{"left SS3", "\x1bOD", []Key{k(KeyLeft)}},
		{"page up", "\x1b[5~", []Key{k(KeyPgUp)}},
		{"page down", "\x1b[6~", []Key{k(KeyPgDn)}},
		{"enter CR", "\r", []Key{k(KeyEnter)}},
		{"enter LF", "\n", []Key{k(KeyEnter)}},
		{"backspace DEL", "\x7f", []Key{k(KeyBackspace)}},
		{"backspace BS", "\x08", []Key{k(KeyBackspace)}},
		{"ctrl-c", "\x03", []Key{k(KeyCtrlC)}},
		{"lone esc", "\x1b", []Key{k(KeyEsc)}},
		{"esc at input end", "a\x1b", []Key{Rune('a'), k(KeyEsc)}},
		{"esc before a plain key", "\x1bx", []Key{k(KeyEsc), Rune('x')}},
		{"esc before an arrow", "\x1b\x1b[A", []Key{k(KeyEsc), k(KeyUp)}},
		{"delete dropped", "\x1b[3~", nil},
		{"F5 dropped, no digits leak", "\x1b[15~", nil},
		{"F1 SS3 dropped, no P leaks", "\x1bOP", nil},
		{"modified arrow dropped", "\x1b[1;5A", nil},
		{"truncated CSI dropped", "\x1b[", nil},
		{"rune", "q", []Key{Rune('q')}},
		{"utf8 runes", "é日🙂", []Key{Rune('é'), Rune('日'), Rune('🙂')}},
		{"control bytes dropped", "\x00\x01\x02\x1f", nil},
		{"invalid utf8 dropped", "\xffa", []Key{Rune('a')}},
		{"cut utf8 does not eat the next key", "\xc3a\xe6\x97\x1b[A", []Key{Rune('a'), k(KeyUp)}},
		{"broken CSI does not eat the next key", "\x1b[\r\x1b[1\x1b[A\x1b[é", []Key{k(KeyEnter), k(KeyUp), Rune('é')}},
		{"mixed", "a\x1b[Ab\r", []Key{Rune('a'), k(KeyUp), Rune('b'), k(KeyEnter)}},
	}
	for _, tc := range cases {
		want := keyTrace(tc.want...)
		// Whole, cut in two at every byte, and one byte per read.
		splits := [][]string{{tc.in}}
		var each []string
		for i := range len(tc.in) {
			each = append(each, tc.in[i:i+1])
			if i > 0 {
				splits = append(splits, []string{tc.in[:i], tc.in[i:]})
			}
		}
		for _, chunks := range append(splits, each) {
			if got := runBytes(t, nil, chunks...); got != want {
				t.Errorf("%s: chunks %q: trace %q, want %q", tc.name, chunks, got, want)
			}
		}
	}
}

// The driver reads 64 bytes at a time; one long chunk crosses that limit.
func TestRunDecodesAcrossTheReadBuffer(t *testing.T) {
	for _, seq := range []string{"\x1b[A", "\x1b[5~", "\x1bOB", "é", "日", "🙂"} {
		for cut := 1; cut < len(seq); cut++ {
			pad := strings.Repeat("a", 64-cut)
			got := runBytes(t, nil, pad+seq+"z")
			if want := pad + runBytes(t, nil, seq) + "z"; got != want {
				t.Errorf("%q cut at %d: trace %q, want %q", seq, cut, got, want)
			}
		}
	}
}

// A pause after ESC ends the wait: Escape arrives with no further key, and
// what follows is typed input of its own.
func TestRunEscapeWait(t *testing.T) {
	for _, tc := range []struct {
		name   string
		chunks []string
		want   string
	}{
		{"lone esc", []string{"\x1b", pause, "[", "A"}, keyTrace(Key{Kind: KeyEsc}, Rune('['), Rune('A'))},
		{"unfinished sequence is dropped", []string{"\x1b[", pause, "q"}, "q"},
		{"utf8 has no wait", []string{"\xc3", pause, "\xa9"}, "é"},
	} {
		if got := runBytes(t, nil, tc.chunks...); got != tc.want {
			t.Errorf("%s: trace %q, want %q", tc.name, got, tc.want)
		}
	}
	// Esc is delivered before the next read has a key to give.
	if got := runBytes(t, func(Msg) Cmd { return quit }, "\x1b", pause, "never"); got != keyTrace(Key{Kind: KeyEsc}) {
		t.Errorf("esc alone: trace %q", got)
	}
}

// Without a read deadline nothing can end a wait, so a read that ends in ESC
// is the Esc key at once.
func TestRunEscapeWithoutDeadline(t *testing.T) {
	ft := script("\x1b", "q")
	r, err := run(t, ft, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := r.trace(), keyTrace(Key{Kind: KeyEsc}, Rune('q')); got != want {
		t.Errorf("trace %q, want %q", got, want)
	}
}

// The rest of a read that started a Cmd is dropped, including a sequence
// whose tail has not arrived yet.
func TestRunHandoffDropsPendingInput(t *testing.T) {
	cmds := map[string]Cmd{
		"exec":   {Exec: func() error { return nil }},
		"cooked": {Cooked: true, Exec: func() error { return nil }},
	}
	for name, cmd := range cmds {
		for _, tc := range [][2]string{{"x\x1b[", "A"}, {"x\x1b", "[A"}, {"x\xc3", "\xa9"}} {
			got := runBytes(t, byRune(map[rune]Cmd{'x': cmd}), tc[0], tc[1], "z")
			if got != "xDz" {
				t.Errorf("%s %q: trace %q, want xDz", name, tc, got)
			}
		}
		if got := runBytes(t, byRune(map[rune]Cmd{'x': cmd}), "x\x1b", pause, "z"); got != "xDz" {
			t.Errorf("%s: pending esc leaked: trace %q", name, got)
		}
	}
	// A split key answering "press any key" is swallowed whole.
	wait := byRune(map[rune]Cmd{'x': {Cooked: true, Wait: true, Exec: func() error { return nil }}})
	for _, tc := range [][2]string{{"\x1b[", "A"}, {"\xc3", "\xa9"}} {
		if got := runBytes(t, wait, "x", tc[0], tc[1], "z"); got != "xDz" {
			t.Errorf("press any key %q: trace %q, want xDz", tc, got)
		}
	}
}
