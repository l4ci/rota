package tracker

import (
	"encoding/json"
	"strings"
	"testing"
)

// adfJSON marshals a document the way it goes on the wire.
func adfJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMarkdownADFRoundTrip(t *testing.T) {
	cases := map[string]string{
		"paragraphs and hard break": "line one\nline two\n\nsecond paragraph",
		"heading":                   "## Title\n\ntext below",
		"fenced code":               "```go\nfmt.Println(\"**not bold**\") // <!-- x -->\n\nreturn\n```",
		"fenced code no language":   "```\nplain\n```",
		"bullets with nested":       "- a\n  - b\n  - c\n- d",
		"ordered from three":        "3. three\n4. four",
		"link":                      "see [docs](https://x.io/a?b=1) now",
		"inline code":               "run `go test ./...` first",
		"bold":                      "this is **bold** text",
		"em":                        "this is *em* text",
		"blockquote":                "> quoted\n> more",
		"rule":                      "above\n\n---\n\nbelow",
		"list item hard break":      "- first\n  second\n- third",
		"bold link":                 "[**bold**](https://x.io)",
		"code in link":              "[`code`](https://x.io)",
	}
	for name, md := range cases {
		t.Run(name, func(t *testing.T) {
			got := adfToMarkdown(adfJSON(t, markdownToADF(md)))
			if got != md {
				t.Errorf("round trip:\n got %q\nwant %q", got, md)
			}
		})
	}
}

func TestMarkdownToADFExactJSON(t *testing.T) {
	got := string(adfJSON(t, markdownToADF("See [docs](https://x.io/a) and `go test`.")))
	want := `{"content":[{"content":[` +
		`{"text":"See ","type":"text"},` +
		`{"marks":[{"attrs":{"href":"https://x.io/a"},"type":"link"}],"text":"docs","type":"text"},` +
		`{"text":" and ","type":"text"},` +
		`{"marks":[{"type":"code"}],"text":"go test","type":"text"},` +
		`{"text":".","type":"text"}],"type":"paragraph"}],"type":"doc","version":1}`
	if got != want {
		t.Errorf("json:\n got %s\nwant %s", got, want)
	}
}

func TestMarkdownToADFShapes(t *testing.T) {
	cases := []struct{ name, md, want string }{
		{"empty", "", `{"content":[],"type":"doc","version":1}`},
		{"whitespace", " \n\t\n", `{"content":[],"type":"doc","version":1}`},
		{"crlf", "a\r\nb", `{"content":[{"content":[{"text":"a","type":"text"},{"type":"hardBreak"},{"text":"b","type":"text"}],"type":"paragraph"}],"type":"doc","version":1}`},
		{"empty code", "```\n```", `{"content":[{"type":"codeBlock"}],"type":"doc","version":1}`},
		{"unclosed fence", "```py\nx = 1", `{"content":[{"attrs":{"language":"py"},"content":[{"text":"x = 1","type":"text"}],"type":"codeBlock"}],"type":"doc","version":1}`},
		{"heading level", "### h", `{"content":[{"attrs":{"level":3},"content":[{"text":"h","type":"text"}],"type":"heading"}],"type":"doc","version":1}`},
		{"snake case literal", "a_b_c", `{"content":[{"content":[{"text":"a_b_c","type":"text"}],"type":"paragraph"}],"type":"doc","version":1}`},
		{"unmatched delimiters", "2 * 3 and **x", `{"content":[{"content":[{"text":"2 * 3 and **x","type":"text"}],"type":"paragraph"}],"type":"doc","version":1}`},
		{"ordered paren", "2) a", `{"content":[{"attrs":{"order":2},"content":[{"content":[{"content":[{"text":"a","type":"text"}],"type":"paragraph"}],"type":"listItem"}],"type":"orderedList"}],"type":"doc","version":1}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := string(adfJSON(t, markdownToADF(c.md))); got != c.want {
				t.Errorf("got %s\nwant %s", got, c.want)
			}
		})
	}
}

func TestMarkdownToADFNoEmptyText(t *testing.T) {
	inputs := []string{
		"**", "****", "[]()", "[](x)", "[a]()", "`", "``", "a  \nb  ", "  \n", "*", "_", "__",
		"- \n", "# ", "text   ", "**a** **", "*a*b*", "[**](x)", "> ", "- a\n  - ",
	}
	var walk func(t *testing.T, in string, v any)
	walk = func(t *testing.T, in string, v any) {
		switch x := v.(type) {
		case map[string]any:
			if x["type"] == "text" && x["text"] == "" {
				t.Errorf("%q: empty text node", in)
			}
			for _, c := range x {
				walk(t, in, c)
			}
		case []any:
			for _, c := range x {
				walk(t, in, c)
			}
		}
	}
	for _, in := range inputs {
		var doc any
		if err := json.Unmarshal(adfJSON(t, markdownToADF(in)), &doc); err != nil {
			t.Fatal(err)
		}
		walk(t, in, doc)
	}
}

func TestADFToMarkdownNodes(t *testing.T) {
	doc := `{"type":"doc","version":1,"content":[
	 {"type":"paragraph","content":[
	  {"type":"text","text":"Ping "},
	  {"type":"mention","attrs":{"id":"1","text":"@Ann"}},
	  {"type":"text","text":" and "},
	  {"type":"mention","attrs":{"id":"42"}},
	  {"type":"text","text":" "},
	  {"type":"emoji","attrs":{"shortName":":smile:"}},
	  {"type":"text","text":" "},
	  {"type":"weird","text":"raw"}]},
	 {"type":"paragraph","content":[{"type":"inlineCard","attrs":{"url":"https://x.io/b"}}]},
	 {"type":"mediaSingle","content":[{"type":"media","attrs":{"id":"m"}}]},
	 {"type":"table","content":[
	  {"type":"tableRow","content":[
	   {"type":"tableHeader","content":[{"type":"paragraph","content":[{"type":"text","text":"A"}]}]},
	   {"type":"tableHeader","content":[{"type":"paragraph","content":[{"type":"text","text":"B"}]}]}]},
	  {"type":"tableRow","content":[
	   {"type":"tableCell","content":[{"type":"paragraph","content":[{"type":"text","text":"1"}]}]},
	   {"type":"tableCell","content":[{"type":"paragraph","content":[{"type":"text","text":"2"}]}]}]}]},
	 {"type":"panel","attrs":{"panelType":"info"},"content":[{"type":"paragraph","content":[{"type":"text","text":"Careful"}]}]},
	 {"type":"fancyThing","content":[{"type":"paragraph","content":[
	  {"type":"text","text":"bold ","marks":[{"type":"strong"}]},
	  {"type":"text","text":"x","marks":[{"type":"code"},{"type":"link","attrs":{"href":"https://u"}}]}]}]}
	]}`
	want := "Ping @Ann and @42 :smile: raw\n\n" +
		"https://x.io/b\n\n" +
		"| A | B |\n| --- | --- |\n| 1 | 2 |\n\n" +
		"Careful\n\n" +
		"**bold** [`x`](https://u)"
	if got := adfToMarkdown(json.RawMessage(doc)); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestADFToMarkdownNonDocument(t *testing.T) {
	cases := []struct{ name, raw, want string }{
		{"string", `"plain string"`, "plain string"},
		{"escaped string", `"a\nb"`, "a\nb"},
		{"null", `null`, ""},
		{"empty", ``, ""},
		{"garbage", `{nope`, ""},
		{"empty doc", `{"type":"doc","version":1,"content":[]}`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := adfToMarkdown(json.RawMessage(c.raw)); got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
	if strings.Contains(adfToMarkdown(json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"x"}]}]}`)), "\n") {
		t.Error("trailing newline not trimmed")
	}
}
