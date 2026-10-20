package milestone

import (
	"strings"
	"testing"
)

func TestAppendDecision(t *testing.T) {
	base := "---\nid: M01\n---\n\n## Goal\n\ng\n\n## Decisions\n\n_(none yet)_\n\n## Not yet specified\n\n- fog\n"
	t.Run("first line replaces the placeholder", func(t *testing.T) {
		got, changed := AppendDecision(base, "#12", "use X")
		if !changed || !strings.Contains(got, "## Decisions\n\n- #12: use X\n\n## Not yet specified") {
			t.Errorf("%v\n%s", changed, got)
		}
	})
	t.Run("lines accumulate in order", func(t *testing.T) {
		a, _ := AppendDecision(base, "#12", "use X")
		b, _ := AppendDecision(a, "#13", "no Y")
		if !strings.Contains(b, "- #12: use X\n- #13: no Y\n") {
			t.Errorf("%s", b)
		}
	})
	t.Run("a repeat adds nothing", func(t *testing.T) {
		a, _ := AppendDecision(base, "#12", "use X")
		b, changed := AppendDecision(a, "#12", "use X")
		if changed || a != b {
			t.Errorf("%v", changed)
		}
	})
	t.Run("id prefix is not a match", func(t *testing.T) {
		a, _ := AppendDecision(base, "#120", "other")
		b, changed := AppendDecision(a, "#12", "use X")
		if !changed || !strings.Contains(b, "- #12: use X") {
			t.Errorf("%s", b)
		}
	})
	t.Run("a missing section is created", func(t *testing.T) {
		got, changed := AppendDecision("---\nid: M01\n---\n\n## Goal\n\ng\n", "#12", "use X")
		if !changed || !strings.HasSuffix(got, "## Decisions\n\n- #12: use X\n") {
			t.Errorf("%q", got)
		}
	})
	t.Run("gist newlines collapse", func(t *testing.T) {
		got, _ := AppendDecision(base, "#12", "a\nb")
		if !strings.Contains(got, "- #12: a b\n") {
			t.Errorf("%s", got)
		}
	})
}

func TestKindOfAndWithKind(t *testing.T) {
	stub := StubOn("M01", "Fog", "Find out.", nil, "2026-01-01")
	if KindOf(stub) != KindDelivery {
		t.Errorf("a plan with no kind is delivery: %q", KindOf(stub))
	}
	if WithKind(stub, KindDelivery) != stub {
		t.Error("delivery leaves the text byte-identical")
	}
	d := WithKind(stub, KindDecision)
	if KindOf(d) != KindDecision || !strings.HasPrefix(d, "---\nid: M01\n") || !strings.Contains(d, "\nkind: decision\n---\n") {
		t.Errorf("frontmatter kind:\n%s", d)
	}
	for _, want := range []string{"\n## Decisions\n", "\n## Not yet specified\n"} {
		if !strings.Contains(d, want) {
			t.Errorf("decision plan lacks %q:\n%s", want, d)
		}
	}
	if got, _ := AppendDecision(d, "#12", "g"); !strings.Contains(got, "## Decisions\n\n- #12: g\n\n## Not yet specified") {
		t.Errorf("a decision plan takes the first line:\n%s", got)
	}
}
