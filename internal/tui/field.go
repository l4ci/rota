package tui

import "strings"

// FieldState is where an edit stands after a key.
type FieldState int

const (
	Editing FieldState = iota
	Submitted
	Cancelled
)

// Field is one editable value: the config screen picks the kind per key.
type Field interface {
	Update(Key) (Field, FieldState)
	Render(w int, st Style) string // one line
	Value() string
}

// labeled is "label  rest" cut to w. A cut line loses its styling; the
// label is bold otherwise.
func labeled(label, rest string, w int, st Style) string {
	plain := label + "  " + rest
	if w > 0 && Width(plain) > w {
		return Fit(plain, w)
	}
	return st.Bold(label) + "  " + rest
}

// Toggle is an on/off value.
type Toggle struct {
	Label string
	On    bool
}

func (t Toggle) Update(k Key) (Field, FieldState) {
	switch {
	case k.Kind == KeyEnter:
		return t, Submitted
	case k.Kind == KeyEsc:
		return t, Cancelled
	case k.Kind == KeyLeft || k.Kind == KeyRight || k.Is(' ') || k.Is('h') || k.Is('l'):
		t.On = !t.On
	}
	return t, Editing
}

func (t Toggle) Render(w int, st Style) string {
	v := "[ ] false"
	if t.On {
		v = "[x] true"
	}
	return labeled(t.Label, v, w, st)
}

func (t Toggle) Value() string {
	if t.On {
		return "true"
	}
	return "false"
}

// Choice is one of a fixed set of options.
type Choice struct {
	Label   string
	Options []string
	Sel     int
}

func (c Choice) Update(k Key) (Field, FieldState) {
	n := len(c.Options)
	switch {
	case k.Kind == KeyEnter:
		return c, Submitted
	case k.Kind == KeyEsc:
		return c, Cancelled
	case n == 0:
	case k.Kind == KeyRight || k.Kind == KeyDown || k.Is('l') || k.Is('j') || k.Is(' '):
		c.Sel = (c.Sel + 1) % n
	case k.Kind == KeyLeft || k.Kind == KeyUp || k.Is('h') || k.Is('k'):
		c.Sel = (c.Sel + n - 1) % n
	}
	return c, Editing
}

func (c Choice) Render(w int, st Style) string {
	opts := make([]string, len(c.Options))
	for i, o := range c.Options {
		opts[i] = o
		if i == c.Sel {
			opts[i] = "[" + o + "]"
		}
	}
	return labeled(c.Label, strings.Join(opts, "  "), w, st)
}

func (c Choice) Value() string {
	if c.Sel < 0 || c.Sel >= len(c.Options) {
		return ""
	}
	return c.Options[c.Sel]
}

// TextInput is a line of text with a cursor.
type TextInput struct {
	Label    string
	Text     string
	Cur      int                // cursor, in runes
	Validate func(string) error // nil accepts anything
	Err      string             // the last Validate failure, shown after the text
}

// NewTextInput is a TextInput with the cursor at the end of text.
func NewTextInput(label, text string, validate func(string) error) TextInput {
	return TextInput{Label: label, Text: text, Cur: len([]rune(text)), Validate: validate}
}

func (t TextInput) Update(k Key) (Field, FieldState) {
	r := []rune(t.Text)
	t.Cur = min(max(t.Cur, 0), len(r))
	switch {
	case k.Kind == KeyEsc:
		return t, Cancelled
	case k.Kind == KeyEnter:
		if t.Validate != nil {
			if err := t.Validate(t.Text); err != nil {
				t.Err = err.Error()
				return t, Editing
			}
		}
		return t, Submitted
	case k.Kind == KeyLeft:
		t.Cur = max(t.Cur-1, 0)
	case k.Kind == KeyRight:
		t.Cur = min(t.Cur+1, len(r))
	case k.Kind == KeyBackspace:
		if t.Cur > 0 {
			t.Text = string(r[:t.Cur-1]) + string(r[t.Cur:])
			t.Cur--
		}
		t.Err = ""
	case k.Kind == KeyRune && k.R >= ' ':
		t.Text = string(r[:t.Cur]) + string(k.R) + string(r[t.Cur:])
		t.Cur++
		t.Err = ""
	}
	return t, Editing
}

func (t TextInput) Render(w int, st Style) string {
	r := []rune(t.Text)
	cur := min(max(t.Cur, 0), len(r))
	text := string(r[:cur]) + "_" + string(r[cur:])
	plain := t.Label + "  " + text
	if t.Err == "" {
		return labeled(t.Label, text, w, st)
	}
	if w > 0 && Width(plain+"  "+t.Err) > w {
		return Fit(plain+"  "+t.Err, w)
	}
	return st.Bold(t.Label) + "  " + text + "  " + st.Red(t.Err)
}

func (t TextInput) Value() string { return t.Text }
