package itembody

import "testing"

func TestHeadingRegexps(t *testing.T) {
	tests := []struct {
		name string
		text string
		re   func(string) bool
		want bool
	}{
		{"depends h2", "## Depends on\n", func(s string) bool { return DependsHeadRe.MatchString(s) }, true},
		{"depends any case and level", "#### DEPENDS   ON  \n", func(s string) bool { return DependsHeadRe.MatchString(s) }, true},
		{"depends not a heading", "Depends on\n", func(s string) bool { return DependsHeadRe.MatchString(s) }, false},
		{"depends trailing words", "## Depends on #4\n", func(s string) bool { return DependsHeadRe.MatchString(s) }, false},
		{"files", "## Files\n", func(s string) bool { return FilesHeadRe.MatchString(s) }, true},
		{"files touched", "### files touched\n", func(s string) bool { return FilesHeadRe.MatchString(s) }, true},
		{"files other", "## Files changed\n", func(s string) bool { return FilesHeadRe.MatchString(s) }, false},
		{"touches", "## Touches\n", func(s string) bool { return TouchesHeadRe.MatchString(s) }, true},
		{"touches other", "## Touched\n", func(s string) bool { return TouchesHeadRe.MatchString(s) }, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.re(tc.text); got != tc.want {
				t.Errorf("match(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

func TestSection(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		want   string
		wantOK bool
	}{
		{"missing", "## Other\n- x\n", "", false},
		{"up to next heading", "## Files\n- a\n- b\n## Next\n- c\n", "\n- a\n- b\n", true},
		{"deeper heading ends it", "## Files\n- a\n#### Sub\nz\n", "\n- a\n", true},
		{"to EOF", "intro\n## Files\n- a\n", "\n- a\n", true},
		{"first match wins", "## Files\n- a\n## Files\n- b\n", "\n- a\n", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Section(tc.text, FilesHeadRe)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("Section = (%q, %v), want (%q, %v)", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestHasDependsOn(t *testing.T) {
	tests := []struct {
		body string
		want bool
	}{
		{"", false},
		{"text\n## Depends on\n- #1\n", true},
		{"## Files\n- a\n", false},
	}
	for _, tc := range tests {
		if got := HasDependsOn([]byte(tc.body)); got != tc.want {
			t.Errorf("HasDependsOn(%q) = %v, want %v", tc.body, got, tc.want)
		}
	}
}

func TestAppend(t *testing.T) {
	tests := []struct {
		name string
		fn   func([]byte, []string) []byte
		body string
		refs []string
		want string
	}{
		{"depends on empty body", AppendDependsOn, "", []string{"#1", "#2"}, "## Depends on\n\n- #1\n- #2\n"},
		{"files after body", AppendFiles, "Body text\n\n\n", []string{"a.go", "b/*.go"}, "Body text\n\n## Files\n\n- a.go\n- b/*.go\n"},
		{"no items", AppendFiles, "x", nil, "x\n\n## Files\n\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(tc.fn([]byte(tc.body), tc.refs)); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// The writer and reader share one grammar: what Append writes, Section reads.
func TestAppendRoundTrip(t *testing.T) {
	out := AppendDependsOn([]byte("Body\n"), []string{"#7", "#9"})
	if !HasDependsOn(out) {
		t.Fatalf("HasDependsOn false on %q", out)
	}
	got, ok := Section(string(out), DependsHeadRe)
	if !ok || got != "\n\n- #7\n- #9\n" {
		t.Errorf("Section = (%q, %v)", got, ok)
	}
}
