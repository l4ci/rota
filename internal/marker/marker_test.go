package marker

import (
	"strings"
	"testing"
)

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

func TestNoteRoundTrip(t *testing.T) {
	for _, c := range []struct {
		kind   string
		i, n   int
		part   string
		legacy bool
	}{
		{"proof", 1, 1, "", false},
		{"design", 2, 3, "2", false},
		{"plan", 1, 2, "1", false},
		{"plan:S12", 3, 3, "3", false},
		{"plan:S01", 1, 1, "", true},
		{"proof", 2, 2, "2", true},
	} {
		h := NoteHeader(c.kind, c.i, c.n)
		if c.legacy {
			h = strings.Replace(h, Prefix, LegacyPrefix, 1)
		}
		n, ok := ParseNote(h + "body\r\nmore")
		if !ok || n.Kind != c.kind || n.Part != c.part || n.Rest != "body\nmore" {
			t.Errorf("ParseNote(%q) = %+v, %v", h, n, ok)
		}
	}
	for _, bad := range []string{"text <!-- rota:proof -->\n", "<!-- rota:other -->\n", "<!-- rota:proof x/y -->\n", "<!-- rota:proof -->x"} {
		if _, ok := ParseNote(bad); ok {
			t.Errorf("ParseNote(%q) accepted", bad)
		}
	}
}

func TestCommentRoundTrip(t *testing.T) {
	for _, h := range []string{CommentHeader("feedback"), strings.Replace(CommentHeader("feedback"), Prefix, LegacyPrefix, 1)} {
		kind, rest, ok := ParseComment(h + "hello")
		if !ok || kind != "feedback" || rest != "hello" {
			t.Errorf("ParseComment(%q) = %q %q %v", h, kind, rest, ok)
		}
	}
	if _, _, ok := ParseComment("<!-- rota:comment bad kind -->\n"); ok {
		t.Error("kind with a space accepted")
	}
}

func TestClaimRoundTrip(t *testing.T) {
	for _, c := range []struct{ line, verb string }{
		{Claim("nia@4"), KindClaim},
		{Release("nia@4"), KindRelease},
		{strings.Replace(Claim("nia@4"), Prefix, LegacyPrefix, 1), KindClaim},
		{Claim("nia@4") + "\nClaimed by nia@4", KindClaim},
	} {
		verb, id, ok := ParseClaim(c.line)
		if !ok || verb != c.verb || id != "nia@4" {
			t.Errorf("ParseClaim(%q) = %q %q %v", c.line, verb, id, ok)
		}
	}
	if _, _, ok := ParseClaim("<!-- rota:done -->"); ok {
		t.Error("done parsed as a claim")
	}
}

func TestHandoffRoundTrip(t *testing.T) {
	m := Handoff("ben", 4)
	if m != "<!-- rota:handoff ben@4 -->" {
		t.Errorf("Handoff = %q", m)
	}
	if !HasHandoff("text\n\n"+m) || !HasHandoff("<!-- hv:handoff ben@4 -->") {
		t.Error("handoff marker not detected")
	}
	if HasHandoff("<!-- rota:handoffs x -->") || HasHandoff(Line("done")) {
		t.Error("other marker detected as handoff")
	}
}

func TestFieldsRoundTrip(t *testing.T) {
	block := FieldsBlock([]string{"Size: S", "Milestone: M01"})
	for _, b := range []string{block, strings.Replace(block, Prefix, LegacyPrefix, 1)} {
		text, lines, ok := SplitFields("Body text\n\n" + b + "\n")
		if !ok || text != "Body text" || lines != "Size: S\nMilestone: M01" {
			t.Errorf("SplitFields(%q) = %q %q %v", b, text, lines, ok)
		}
	}
	if text, _, ok := SplitFields("no block"); ok || text != "no block" {
		t.Errorf("SplitFields without a block = %q %v", text, ok)
	}
}

func TestParseBounce(t *testing.T) {
	for _, c := range []struct {
		name, body string
		kind, head string
		reset, ok  bool
	}{
		{"bounce", "<!-- rota:bounce head=abc123 -->\ntext", KindBounce, "abc123", false, true},
		{"review-bounce", "<!-- rota:review-bounce head=ABC123 -->\ntext", KindReviewBounce, "ABC123", false, true},
		{"reset", "<!-- rota:bounce reset -->\ntext", KindBounce, "", true, true},
		{"empty head", "<!-- rota:bounce head= -->", KindBounce, "", false, true},
		{"no argument", "<!-- rota:bounce -->", KindBounce, "", false, true},
		{"legacy prefix", "<!-- hv:bounce head=abc -->", KindBounce, "abc", false, true},
		{"CRLF", "<!-- rota:bounce head=abc -->\r\nline\r\n", KindBounce, "abc", false, true},
		{"quoted mid-body", "> quoting\n<!-- rota:bounce head=abc -->", "", "", false, false},
		{"after text", "see <!-- rota:bounce reset -->", "", "", false, false},
		{"malformed head", "<!-- rota:bounce head=xyz -->", "", "", false, false},
		{"unterminated", "<!-- rota:bounce head=abc", "", "", false, false},
		{"other kind", "<!-- rota:claim ben@3 -->", "", "", false, false},
		{"plain text", "bounce head=abc", "", "", false, false},
		{"empty", "", "", "", false, false},
	} {
		kind, head, reset, ok := ParseBounce(c.body)
		if kind != c.kind || head != c.head || reset != c.reset || ok != c.ok {
			t.Errorf("%s: ParseBounce(%q) = %q %q %v %v", c.name, c.body, kind, head, reset, ok)
		}
	}
}

func TestSideOf(t *testing.T) {
	cases := map[string]string{
		"<!-- rota:comment feedback -->\n<!-- rota:support-offer -->\nOffered.":    "main",
		"<!-- rota:comment feedback -->\n<!-- rota:support-return -->\nnot ready":  "support",
		"<!-- rota:comment relay -->\n<!-- rota:relay round=2 -->\ncontinue":       "support",
		"<!-- rota:comment note -->\n<!-- rota:from main -->\nuse the other API":   "main",
		"<!-- rota:pr-pointer bounce -->\n<!-- rota:from support -->\nhttps://x/9": "support",
		"just a human":                    "",
		"quoting <!-- rota:from main -->": "",
	}
	for body, want := range cases {
		if got := SideOf(body); got != want {
			t.Errorf("SideOf(%q) = %q, want %q", body, got, want)
		}
	}
}
