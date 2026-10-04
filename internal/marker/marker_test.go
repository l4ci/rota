package marker

import "testing"

func TestLine(t *testing.T) {
	for _, c := range []struct {
		kind string
		args []string
		want string
	}{
		{"blocked", nil, "<!-- rota:blocked -->"},
		{"escalation", []string{"e1"}, "<!-- rota:escalation e1 -->"},
		{"x", []string{"a", "b"}, "<!-- rota:x a b -->"},
	} {
		if got := Line(c.kind, c.args...); got != c.want {
			t.Errorf("Line(%q,%v) = %q, want %q", c.kind, c.args, got, c.want)
		}
	}
}

func TestHas(t *testing.T) {
	if !Has("text\n\n" + Line("done")) {
		t.Error("marked body not detected")
	}
	if Has("plain <!-- html comment --> text") {
		t.Error("plain comment detected as marker")
	}
}

func TestHasReadsLegacyHvMarker(t *testing.T) {
	if !Has("Done in `abc`\n\n<!-- hv:done -->") {
		t.Error("a marker hv wrote before the rename is not detected")
	}
}
