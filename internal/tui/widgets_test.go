package tui

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/l4ci/rota/internal/golden"
)

// key turns a readable name into a Key.
func key(name string) Key {
	switch name {
	case "up":
		return Key{Kind: KeyUp}
	case "down":
		return Key{Kind: KeyDown}
	case "left":
		return Key{Kind: KeyLeft}
	case "right":
		return Key{Kind: KeyRight}
	case "pgup":
		return Key{Kind: KeyPgUp}
	case "pgdn":
		return Key{Kind: KeyPgDn}
	case "enter":
		return Key{Kind: KeyEnter}
	case "esc":
		return Key{Kind: KeyEsc}
	case "backspace":
		return Key{Kind: KeyBackspace}
	case "space":
		return Rune(' ')
	}
	r, n := utf8.DecodeRuneInString(name)
	if n != len(name) {
		panic("bad key name " + name)
	}
	return Rune(r)
}

func keys(names ...string) []Key {
	out := make([]Key, len(names))
	for i, n := range names {
		out[i] = key(n)
	}
	return out
}

// typed is the key names for the runes of s.
func typed(s string) []string {
	var out []string
	for _, r := range s {
		out = append(out, string(r))
	}
	return out
}

func repeat(name string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = name
	}
	return out
}

func cat(parts ...[]string) []string {
	var out []string
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

const gw, gh = 100, 30

type goldenIn struct {
	Keys []string `json:"keys"`
	W    int      `json:"w"`
	H    int      `json:"h"`
}

// frameLines strips the style from a frame and checks its size.
func frameLines(t *testing.T, frame string) []string {
	t.Helper()
	lines := strings.Split(Strip(frame), "\n")
	if len(lines) != gh {
		t.Errorf("frame has %d lines, want %d", len(lines), gh)
	}
	for i, l := range lines {
		if n := utf8.RuneCountInString(l); n > gw {
			t.Errorf("line %d is %d runes: %q", i, n, l)
		}
	}
	return lines
}

var st = Style{Color: true}

var listWords = []string{"release notes", "draft docs", "fix flaky test", "review pull request", "update changelog", "bump dependencies", "triage issues"}

func listItems() []string {
	var out []string
	for i := 0; i < 45; i++ {
		s := fmt.Sprintf("%02d %s", i+1, listWords[i%len(listWords)])
		if i == 6 {
			s += " and a very long tail that cannot fit the left column of the screen"
		}
		out = append(out, s)
	}
	return out
}

const listBody = "Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore et dolore magna aliqua.\nUt enim ad minim veniam, quis nostrud exercitation ullamco laboris."

// listScreen is the composed frame the list golden draws.
func listScreen(l List, d Detail) string {
	const lw = 40
	left := l.Render(lw, gh-2, st)
	if f := l.FilterLine(); f != "" {
		left += "\n" + Fit(f, lw)
	}
	if i, ok := l.Selected(); ok {
		d.Title, d.Body = l.Items[i], listBody
	} else {
		d.Title, d.Body = "", ""
	}
	right := d.Render(gw-lw-3, gh-1, st)
	bar := Hints([]Hint{{"↑/↓", "move"}, {"/", "filter"}, {"enter", "open"}, {"q", "quit"}}, gw, st)
	return Frame(Columns(left, right, lw), bar, gh)
}

func runList(l List, names []string) List {
	for _, k := range keys(names...) {
		l, _ = l.Update(k)
	}
	return l
}

func TestListGolden(t *testing.T) {
	l := List{Items: listItems()}
	mid := cat(repeat("down", 30))
	l = runList(l, mid)
	golden.Check(t, goldenIn{mid, gw, gh}, frameLines(t, listScreen(l, Detail{})))

	l = runList(List{Items: listItems()}, nil)
	script := cat(repeat("down", 3), []string{"/"}, typed("release"), []string{"enter"})
	l = runList(l, script)
	golden.Check(t, goldenIn{script, gw, gh}, frameLines(t, listScreen(l, Detail{})))
}

func TestDetailGolden(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&b, "Paragraph %d. %s\n", i, strings.Repeat("the quick brown fox jumps over the lazy dog ", 3))
	}
	d := Detail{Title: "A long note", Body: b.String()}
	script := []string{"pgdn", "down"}
	for _, k := range keys(script...) {
		d, _ = d.Update(k)
	}
	frame := Frame(d.Render(gw, gh-1, st), Hints([]Hint{{"↑/↓", "scroll"}, {"PgUp/PgDn", "page"}}, gw, st), gh)
	golden.Check(t, goldenIn{script, gw, gh}, frameLines(t, frame))
}

type fieldsIn struct {
	Toggle []string `json:"toggle"`
	Choice []string `json:"choice"`
	Text   []string `json:"text"`
	W      int      `json:"w"`
	H      int      `json:"h"`
}

func drive(f Field, names []string) (Field, FieldState) {
	s := Editing
	for _, k := range keys(names...) {
		f, s = f.Update(k)
	}
	return f, s
}

func digits(s string) error {
	for _, r := range s {
		if r < '0' || r > '9' {
			return errors.New("digits only")
		}
	}
	return nil
}

func TestFieldsGolden(t *testing.T) {
	in := fieldsIn{
		Toggle: []string{"space"},
		Choice: []string{"right", "right"},
		Text:   cat(typed("12a"), []string{"enter"}),
		W:      gw, H: gh,
	}
	tg, _ := drive(Toggle{Label: "color"}, in.Toggle)
	ch, _ := drive(Choice{Label: "mode", Options: []string{"auto", "always", "never"}}, in.Choice)
	tx, s := drive(NewTextInput("port", "", digits), in.Text)
	if s != Editing {
		t.Fatalf("invalid text submitted")
	}
	long := NewTextInput("path", strings.Repeat("x", 120), nil)
	body := strings.Join([]string{
		tg.Render(gw, st), "", ch.Render(gw, st), "", tx.Render(gw, st), "", long.Render(gw, st),
	}, "\n")
	frame := Frame(body, Hints([]Hint{{"space", "flip"}, {"enter", "save"}, {"esc", "cancel"}}, gw, st), gh)
	golden.Check(t, in, frameLines(t, frame))
}

type confirmIn struct {
	Keys [][]string `json:"keys"`
	W    int        `json:"w"`
	H    int        `json:"h"`
}

func TestConfirmGolden(t *testing.T) {
	in := confirmIn{[][]string{{}, {"right"}, {"right", "left"}, {"y"}}, gw, gh}
	var rows []string
	for _, names := range in.Keys {
		c := Confirm{Prompt: "Remove the worktree?"}
		for _, k := range keys(names...) {
			c, _ = c.Update(k)
		}
		rows = append(rows, c.Render(gw, st), "")
	}
	frame := Frame(strings.Join(rows, "\n"), Hints([]Hint{{"y/n", "answer"}}, gw, st), gh)
	golden.Check(t, in, frameLines(t, frame))
}

func TestListWrapAndSelected(t *testing.T) {
	l := List{Items: []string{"a", "b", "c"}}
	l, _ = l.Update(key("up"))
	if l.Sel != 2 {
		t.Fatalf("up from 0: Sel=%d", l.Sel)
	}
	l, _ = l.Update(key("down"))
	if l.Sel != 0 {
		t.Fatalf("down from end: Sel=%d", l.Sel)
	}
	if i, ok := (List{Items: []string{"a"}, Filter: "z"}).Selected(); ok {
		t.Fatalf("Selected on no match = %d, true", i)
	}
	l = List{Items: []string{"a"}, Filter: "z"}
	if l, _ = l.Update(key("down")); l.Sel != 0 {
		t.Fatalf("down with no match: Sel=%d", l.Sel)
	}
	if got := l.Render(20, 5, Style{}); got != "  no match" {
		t.Fatalf("no match render: %q", got)
	}
}

func TestListSlashMode(t *testing.T) {
	l := List{Items: []string{"Alpha", "beta", "alpine", "delta"}}
	for _, name := range []string{"q", "l", "r", "d", "enter", "esc"} {
		if _, ok := l.Update(key(name)); ok {
			t.Errorf("%q handled outside filtering", name)
		}
	}
	for _, c := range []struct {
		name string
		want int
	}{{"j", 1}, {"k", 3}} {
		if n, ok := l.Update(key(c.name)); !ok || n.Sel != c.want {
			t.Errorf("%q: Sel=%d handled=%v", c.name, n.Sel, ok)
		}
	}
	l, ok := l.Update(key("/"))
	if !ok || !l.Filtering {
		t.Fatal("/ did not start filtering")
	}
	l.Sel = 2
	l = runList(l, typed("al"))
	if l.Filter != "al" || l.Sel != 0 {
		t.Fatalf("filter %q Sel %d", l.Filter, l.Sel)
	}
	if got := l.Matches(); !reflect.DeepEqual(got, []int{0, 2}) {
		t.Fatalf("matches %v (case-insensitive substring)", got)
	}
	if l.FilterLine() != "filter: al_" {
		t.Fatalf("filter line %q", l.FilterLine())
	}
	l = runList(l, []string{"j", "q"}) // runes, not moves
	if l.Filter != "alj"+"q" {
		t.Fatalf("filter %q", l.Filter)
	}
	l = runList(l, []string{"backspace", "backspace", "enter"})
	if l.Filtering || l.Filter != "al" {
		t.Fatalf("enter kept filter: %+v", l)
	}
	if _, ok := l.Update(key("enter")); ok {
		t.Fatal("enter handled outside filtering")
	}
	if n, ok := l.Update(key("esc")); !ok || n.Filter != "" {
		t.Fatalf("esc with filter: %+v %v", n, ok)
	}
	l.Filter = ""
	l, _ = l.Update(key("/"))
	if n, ok := l.Update(key("esc")); !ok || n.Filtering || n.Filter != "" {
		t.Fatalf("esc while filtering: %+v %v", n, ok)
	}
	if n, ok := l.Update(key("backspace")); !ok || n.Filtering {
		t.Fatalf("backspace on empty filter: %+v %v", n, ok)
	}
}

func TestListTypeahead(t *testing.T) {
	l := List{Items: []string{"quit", "kill", "jump", "run"}, Typeahead: true}
	if _, ok := l.Update(key("q")); ok {
		t.Error("q handled on empty filter")
	}
	if n, ok := l.Update(key("space")); !ok || n.Filter != "" {
		t.Errorf("space: %+v %v", n, ok)
	}
	if n, ok := l.Update(key("j")); !ok || n.Sel != 1 || n.Filter != "" {
		t.Errorf("j: %+v %v", n, ok)
	}
	if n, ok := l.Update(key("k")); !ok || n.Sel != 3 || n.Filter != "" {
		t.Errorf("k: %+v %v", n, ok)
	}
	if _, ok := l.Update(key("backspace")); ok {
		t.Error("backspace handled on empty filter")
	}
	if _, ok := l.Update(key("esc")); ok {
		t.Error("esc handled on empty filter")
	}
	if _, ok := l.Update(key("enter")); ok {
		t.Error("enter handled")
	}
	l = runList(l, []string{"r"})
	if l.Filter != "r" || l.FilterLine() != "filter: r_" {
		t.Fatalf("r: %+v", l)
	}
	l = runList(l, []string{"u", "n", "space", "j", "q"}) // all text now
	if l.Filter != "run jq" {
		t.Fatalf("filter %q", l.Filter)
	}
	l, ok := l.Update(key("backspace"))
	if !ok || l.Filter != "run j" {
		t.Fatalf("backspace: %+v", l)
	}
	if n, ok := l.Update(key("esc")); !ok || n.Filter != "" {
		t.Fatalf("esc: %+v", n)
	}
	l = List{Items: []string{"aa", "ab", "ac"}, Typeahead: true, Sel: 2}
	if n, _ := l.Update(key("a")); n.Sel != 0 {
		t.Fatalf("typing keeps Sel=%d", n.Sel)
	}
}

func TestListPages(t *testing.T) {
	items := make([]string, 10)
	for i := range items {
		items[i] = fmt.Sprintf("row%d", i)
	}
	st := Style{}
	rows := func(sel int) []string {
		return strings.Split((List{Items: items, Sel: sel}).Render(20, 4, st), "\n")
	}
	if got := rows(2); !reflect.DeepEqual(got, []string{"  row0", "  row1", "› row2", "  row3"}) {
		t.Fatalf("page 1: %q", got)
	}
	if got := rows(5); !reflect.DeepEqual(got, []string{"  row4", "› row5", "  row6", "  row7"}) {
		t.Fatalf("page 2: %q", got)
	}
	if got := rows(9); !reflect.DeepEqual(got, []string{"  row8", "› row9"}) {
		t.Fatalf("last page: %q", got)
	}
	if got := strings.Count((List{Items: items}).Render(20, 0, st), "\n"); got != 9 {
		t.Fatalf("h=0 draws all rows, got %d breaks", got)
	}
	if got := (List{Items: []string{"abcdefgh"}}).Render(6, 0, st); got != "› abc…" {
		t.Fatalf("cut row %q", got)
	}
}

func TestDetailUpdateRender(t *testing.T) {
	d := Detail{Body: "a\nb\nc\nd\ne"}
	if n, ok := d.Update(key("up")); !ok || n.Top != 0 {
		t.Fatalf("up at top: %+v", n)
	}
	if _, ok := d.Update(key("x")); ok {
		t.Fatal("x handled")
	}
	if n, _ := d.Update(key("pgdn")); n.Top != 10 {
		t.Fatalf("pgdn: %d", n.Top)
	}
	d.Top = 99
	if got := d.Render(10, 2, Style{}); got != "d\ne" {
		t.Fatalf("clamped last page: %q", got)
	}
	d = Detail{Title: "T", Body: "a\nb\nc", Top: 1}
	if got := d.Render(10, 3, Style{}); got != "T\nb\nc" {
		t.Fatalf("title and page: %q", got)
	}
	if got := d.Render(10, 1, Style{}); got != "T" {
		t.Fatalf("title only: %q", got)
	}
	if got := d.Render(0, 0, Style{}); got != "T\na\nb\nc" {
		t.Fatalf("unknown size: %q", got)
	}
}

func TestWrap(t *testing.T) {
	for _, c := range []struct {
		s    string
		w    int
		want []string
	}{
		{"", 10, []string{}},
		{"hello world", 20, []string{"hello world"}},
		{"hello world", 5, []string{"hello", "world"}},
		{"a bb ccc dddd", 6, []string{"a bb", "ccc", "dddd"}},
		{"abcdefghij", 4, []string{"abcd", "efgh", "ij"}},
		{"x abcdefghij", 4, []string{"x", "abcd", "efgh", "ij"}},
		{"one\n\ntwo three", 20, []string{"one", "", "two three"}},
		{"a b\nc d", 0, []string{"a b", "c d"}},
		{"line\n", 10, []string{"line"}},
		{"héllo wörld", 5, []string{"héllo", "wörld"}},
	} {
		if got := Wrap(c.s, c.w); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Wrap(%q, %d) = %q, want %q", c.s, c.w, got, c.want)
		}
	}
}

func TestFieldStates(t *testing.T) {
	tg := Toggle{Label: "x"}
	for _, name := range []string{"space", "left", "right", "h", "l"} {
		f, s := tg.Update(key(name))
		if s != Editing || f.(Toggle).On == tg.On {
			t.Errorf("%s: %+v %v", name, f, s)
		}
	}
	f, s := tg.Update(key("enter"))
	if s != Submitted || f.Value() != "false" {
		t.Errorf("enter: %v %q", s, f.Value())
	}
	if _, s := tg.Update(key("esc")); s != Cancelled {
		t.Errorf("esc: %v", s)
	}
	if got := (Toggle{Label: "x", On: true}).Render(0, Style{}); got != "x  [x] true" {
		t.Errorf("toggle render %q", got)
	}

	ch := Choice{Label: "m", Options: []string{"a", "b", "c"}}
	f, _ = ch.Update(key("left"))
	if f.Value() != "c" {
		t.Errorf("left wraps to %q", f.Value())
	}
	f, _ = drive(ch, []string{"j", "j", "j", "space"})
	if f.Value() != "b" {
		t.Errorf("cycle: %q", f.Value())
	}
	if got := ch.Render(0, Style{}); got != "m  [a]  b  c" {
		t.Errorf("choice render %q", got)
	}
	if got := ch.Render(8, Style{}); utf8.RuneCountInString(got) != 8 {
		t.Errorf("choice cut %q", got)
	}
	if got := (Choice{}).Value(); got != "" {
		t.Errorf("empty choice value %q", got)
	}
	if f, s := (Choice{}).Update(key("down")); s != Editing || f.Value() != "" {
		t.Errorf("empty choice update")
	}
	if _, s := ch.Update(key("enter")); s != Submitted {
		t.Errorf("choice enter %v", s)
	}
	if _, s := ch.Update(key("esc")); s != Cancelled {
		t.Errorf("choice esc %v", s)
	}
}

func TestTextInput(t *testing.T) {
	ti := NewTextInput("n", "ab", digits)
	if ti.Cur != 2 {
		t.Fatalf("cursor %d", ti.Cur)
	}
	f, s := drive(ti, []string{"left", "x", "backspace", "backspace"})
	got := f.(TextInput)
	if s != Editing || got.Text != "b" || got.Cur != 0 {
		t.Fatalf("edit: %+v", got)
	}
	if r := got.Render(0, Style{}); r != "n  _b" {
		t.Fatalf("render %q", r)
	}
	f, s = drive(ti, []string{"enter"})
	if s != Editing || f.(TextInput).Err != "digits only" {
		t.Fatalf("invalid enter: %+v %v", f, s)
	}
	if r := f.Render(0, Style{}); r != "n  ab_  digits only" {
		t.Fatalf("render with err %q", r)
	}
	if r := f.Render(12, Style{}); utf8.RuneCountInString(r) != 12 {
		t.Fatalf("cut err %q", r)
	}
	f, _ = f.Update(key("backspace"))
	if f.(TextInput).Err != "" {
		t.Fatal("typing keeps Err")
	}
	f, s = drive(NewTextInput("n", "12", digits), []string{"enter"})
	if s != Submitted || f.Value() != "12" {
		t.Fatalf("valid enter: %v %q", s, f.Value())
	}
	if _, s = drive(NewTextInput("n", "x", nil), []string{"enter"}); s != Submitted {
		t.Fatalf("nil Validate: %v", s)
	}
	if _, s = drive(ti, []string{"esc"}); s != Cancelled {
		t.Fatalf("esc: %v", s)
	}
	f, _ = drive(NewTextInput("n", "é", nil), []string{"left", "left", "right", "right", "right", "z"})
	if f.Value() != "éz" {
		t.Fatalf("clamped cursor, runes: %q", f.Value())
	}
}

func TestConfirm(t *testing.T) {
	c := Confirm{Prompt: "Sure?"}
	for _, x := range []struct {
		name string
		yes  bool
		s    FieldState
	}{{"y", true, Submitted}, {"n", false, Submitted}, {"enter", false, Submitted}, {"right", true, Editing}, {"h", true, Editing}, {"esc", false, Cancelled}} {
		got, s := c.Update(key(x.name))
		if got.Yes != x.yes || s != x.s {
			t.Errorf("%s: Yes=%v %v", x.name, got.Yes, s)
		}
	}
	c.Yes = true
	if got, s := c.Update(key("esc")); got.Yes || s != Cancelled {
		t.Errorf("esc keeps Yes: %+v", got)
	}
	if got := c.Render(0, Style{}); got != "Sure?  [yes]  no" {
		t.Errorf("render %q", got)
	}
	if got := (Confirm{Prompt: "Sure?"}).Render(0, Style{}); got != "Sure?  yes  [no]" {
		t.Errorf("render %q", got)
	}
	if got := c.Render(9, Style{}); utf8.RuneCountInString(got) != 9 {
		t.Errorf("cut %q", got)
	}
}

func TestFrameAndHints(t *testing.T) {
	for _, body := range []string{"", "a", strings.Repeat("x\n", 50) + "x"} {
		f := Frame(body, "bar", 30)
		if strings.HasSuffix(f, "\n") || strings.Count(f, "\n") != 29 || !strings.HasSuffix(f, "\nbar") {
			t.Errorf("frame of %q: %d breaks", body, strings.Count(f, "\n"))
		}
	}
	if got := Frame("a\nb", "bar", 0); got != "a\nb\nbar" {
		t.Errorf("h=0: %q", got)
	}
	if got := Frame("a\nb", "bar", 1); got != "bar" {
		t.Errorf("h=1: %q", got)
	}
	hs := []Hint{{"q", "quit"}, {"enter", "open"}}
	if got := Hints(hs, 0, Style{}); got != "q quit · enter open" {
		t.Errorf("hints %q", got)
	}
	if got := Hints(hs, 10, Style{}); got != "q quit · …" {
		t.Errorf("fit hints %q", got)
	}
	if got := Hints(hs, 100, Style{Color: true}); Strip(got) != "q quit · enter open" || got == Strip(got) {
		t.Errorf("styled hints %q", got)
	}
}

func TestColumns(t *testing.T) {
	got := Columns("aa\nb\nc", "x\ny", 3)
	want := "aa  │ x\nb   │ y\nc   │"
	if got != want {
		t.Errorf("columns:\n%q\nwant\n%q", got, want)
	}
	if got := Columns("a", "x\ny", 2); got != "a  │ x\n   │ y" {
		t.Errorf("taller right: %q", got)
	}
}
