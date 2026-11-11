package tracker

import "testing"

func TestFieldsBlockRoundTrip(t *testing.T) {
	const block = "<!-- rota:fields\nPriority: P1\nSize: M\n-->"
	cases := []struct {
		name, body, wantText, wantBlock string
	}{
		{"no block", "just text", "just text", ""},
		{"block", "text\n\n" + block, "text", block},
		{"crlf", "text\r\n\r\n<!-- rota:fields\r\nPriority: P1\r\nSize: M\r\n-->", "text", block},
		{"block only", block, "", block},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			text, got := splitBody(c.body)
			if text != c.wantText || got.Block != c.wantBlock {
				t.Fatalf("splitBody = %q, %q; want %q, %q", text, got.Block, c.wantText, c.wantBlock)
			}
			joined := joinBody(text, got.Block)
			if c.wantBlock == "" {
				if joined != c.body {
					t.Fatalf("joinBody = %q; want %q", joined, c.body)
				}
				return
			}
			// the join normalises the gap to one blank line
			want := block
			if c.wantText != "" {
				want = "text\n\n" + block
			}
			if joined != want {
				t.Fatalf("joinBody = %q; want %q", joined, want)
			}
		})
	}
}
