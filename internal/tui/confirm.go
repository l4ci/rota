package tui

// Confirm is a yes/no question. It starts on No.
type Confirm struct {
	Prompt string
	Yes    bool
}

// Update answers on y, n, Enter and Esc; the arrows and h/l switch the answer.
func (c Confirm) Update(k Key) (Confirm, FieldState) {
	switch {
	case k.Is('y'):
		c.Yes = true
		return c, Submitted
	case k.Is('n'):
		c.Yes = false
		return c, Submitted
	case k.Kind == KeyEnter:
		return c, Submitted
	case k.Kind == KeyEsc:
		c.Yes = false
		return c, Cancelled
	case k.Kind == KeyLeft || k.Kind == KeyRight || k.Is('h') || k.Is('l'):
		c.Yes = !c.Yes
	}
	return c, Editing
}

// Render is the prompt with the chosen answer in brackets.
func (c Confirm) Render(w int, st Style) string {
	yes, no := "yes", "no"
	if c.Yes {
		yes = "[yes]"
	} else {
		no = "[no]"
	}
	plain := c.Prompt + "  " + yes + "  " + no
	if w > 0 && Width(plain) > w {
		return Fit(plain, w)
	}
	if c.Yes {
		yes = st.Bold(yes)
	} else {
		no = st.Bold(no)
	}
	return c.Prompt + "  " + yes + "  " + no
}
