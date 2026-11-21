package milestone

import "testing"

func TestParseTitle(t *testing.T) {
	for _, c := range []struct {
		in, id, rest string
		ok           bool
	}{
		{"M02 — Sharing", "M02", "Sharing", true},
		{"  M10 – Two words here ", "M10", "Two words here", true},
		{"M3 - dash", "M3", "dash", true},
		{"M04", "M04", "", true},
		{" M5 — x", "M5", "x", true},
		{"M7-x", "M7", "x", true},
		{" M٠٥ — Unicode ", "M٠٥", "Unicode", true},
		{"M05: title", "M05", "", true},
		{"M05x — nope", "", "", false},
		{"M05_", "", "", false},
		{"M05é — nope", "", "", false},
		{"M05中 — nope", "", "", false},
		{"M", "", "", false},
		{"m05 — lowercase", "", "", false},
		{"Milestone M02", "", "", false},
		{"", "", "", false},
	} {
		id, rest, ok := ParseTitle(c.in)
		if id != c.id || rest != c.rest || ok != c.ok {
			t.Errorf("ParseTitle(%q) = %q %q %v, want %q %q %v", c.in, id, rest, ok, c.id, c.rest, c.ok)
		}
	}
}

func TestTitleRoundTrip(t *testing.T) {
	for _, c := range []struct{ id, title string }{
		{"M02", "Sharing"},
		{"M10", "Two words — with a dash"},
		{"M7", "x"},
		{"M12", ""},
	} {
		id, rest, ok := ParseTitle(Title(c.id, c.title))
		// An empty title leaves trailing separator text that strips to "".
		if !ok || id != c.id || rest != c.title {
			t.Errorf("ParseTitle(Title(%q, %q)) = %q %q %v", c.id, c.title, id, rest, ok)
		}
	}
}
