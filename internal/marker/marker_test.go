package marker

import (
	"slices"
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
		"<!-- rota:comment feedback -->\n<!-- rota:support-offer -->\nOffered.":      "main",
		"<!-- rota:comment feedback -->\n<!-- rota:support-return -->\nnot ready":    "support",
		"<!-- rota:comment relay -->\n<!-- rota:relay round=2 -->\ncontinue":         "support",
		"<!-- rota:comment feedback -->\n<!-- rota:from main -->\nuse the other API": "main",
		"<!-- rota:pr-pointer bounce -->\n<!-- rota:from support -->\nhttps://x/9":   "support",
		"just a human":                    "",
		"quoting <!-- rota:from main -->": "",
	}
	for body, want := range cases {
		if got := SideOf(body); got != want {
			t.Errorf("SideOf(%q) = %q, want %q", body, got, want)
		}
	}
}

func TestOpenClaims(t *testing.T) {
	for _, c := range []struct {
		name         string
		bodies, want []string
	}{
		{name: "none", bodies: []string{"plain comment", "<!-- rota:release absent@1 -->"}},
		{name: "ordered unique", bodies: []string{"<!-- rota:claim ben@3 -->", "<!-- rota:claim dana@5 -->", "<!-- rota:claim ben@3 -->"}, want: []string{"ben@3", "dana@5"}},
		{name: "release and reclaim", bodies: []string{"<!-- hv:claim ben@3 -->", "<!-- rota:claim dana@5 -->", "<!-- hv:release ben@3 -->", "<!-- rota:claim ben@3 -->"}, want: []string{"dana@5", "ben@3"}},
		{name: "released", bodies: []string{"<!-- rota:claim ben@3 -->", "<!-- rota:release ben@3 -->"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := OpenClaims(c.bodies); !slices.Equal(got, c.want) {
				t.Fatalf("claims %v, want %v", got, c.want)
			}
		})
	}
}

func TestReportProse(t *testing.T) {
	cases := map[string]string{
		"plain":                     "plain",
		"## Heading":                "\\## Heading",
		"# Title":                   "\\# Title",
		"a\n## B\nc":                "a\n\\## B\nc",
		"<!-- rota:report gate -->": "&lt;!-- rota:report gate -->",
		"#nospace":                  "#nospace",
		"<!-- rota:note -->":        "<!-- rota:note -->",
	}
	for in, want := range cases {
		if got := ReportProse(in); got != want {
			t.Errorf("ReportProse(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReportRoundTrip(t *testing.T) {
	for _, c := range []struct{ kind, sha string }{
		{"review-spec", "abcdef1234567"},
		{"qa", "abcd"},
		{"gate", strings.Repeat("a", 64)},
	} {
		line := Report(c.kind, c.sha)
		kind, sha, ok := ParseReport(line)
		if !IsReportKey(line) || !ok || kind != c.kind || sha != c.sha {
			t.Errorf("Report/ParseReport(%q, %q) = %q, %q, %v; key=%v", c.kind, c.sha, kind, sha, ok, IsReportKey(line))
		}
	}
}

func TestReportKeyAndParseBoundaries(t *testing.T) {
	for _, c := range []struct {
		line string
		key  bool
	}{
		{"plain text", false},
		{"Copied report\n<!-- rota:report qa sha=abcd -->", false},
		{"```\n<!-- rota:report qa sha=abcd -->\n```", false},
		{"<!-- hv:report qa sha=abcd -->", false},
		{"<!-- rota:reporting qa sha=abcd -->", false},
		{"<!-- rota:report qa sha=abcd", false},
		{"<!-- rota:report qa sha=abcd --> trailing", false},
		// Posting only requires an envelope, as it did before the extraction.
		{"<!-- rota:report qa sha=not-a-sha -->", true},
		{"<!-- rota:report qa sha=abc -->", true},
		{"<!-- rota:report qa sha=" + strings.Repeat("a", 65) + " -->", true},
		{"<!-- rota:report qa sha=ABCD -->", true},
		{"<!-- rota:report qa -->", true},
		{"<!-- rota:report QA sha=abcd -->", true},
	} {
		t.Run(c.line, func(t *testing.T) {
			if got := IsReportKey(c.line); got != c.key {
				t.Errorf("IsReportKey(%q) = %v, want %v", c.line, got, c.key)
			}
			if kind, sha, ok := ParseReport(c.line); ok || kind != "" || sha != "" {
				t.Errorf("ParseReport(%q) = %q, %q, %v; want empty rejection", c.line, kind, sha, ok)
			}
		})
	}
}
