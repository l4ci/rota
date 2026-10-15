package tracker

import (
	"bytes"
	"encoding/json"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

var (
	adfListRe    = regexp.MustCompile(`^( *)([-*+]|\d+[.)]) +(.*)$`)
	adfHeadingRe = regexp.MustCompile(`^(#{1,6})[ \t]+(.*)$`)
	adfRuleRe    = regexp.MustCompile(`^(-{3,}|\*{3,}|_{3,})$`)
)

// markdownToADF converts rota's Markdown to an ADF document, because Jira
// Cloud REST v3 only accepts comment bodies and descriptions as ADF. It covers
// a small line-based subset (paragraphs, headings, lists, fences, quotes,
// rules, inline marks); anything else stays literal text.
func markdownToADF(md string) map[string]any {
	md = strings.ReplaceAll(md, "\r\n", "\n")
	md = strings.ReplaceAll(md, "\r", "\n")
	return map[string]any{
		"type":    "doc",
		"version": 1,
		"content": adfBlocks(strings.Split(md, "\n"), false),
	}
}

// adfBlocks parses lines into block nodes. Inside a quote only fences, lists
// and paragraphs are produced, since ADF blockquotes do not hold headings.
func adfBlocks(lines []string, quote bool) []any {
	out := []any{}
	for i := 0; i < len(lines); {
		line := strings.TrimRight(lines[i], " \t")
		trim := strings.TrimLeft(line, " \t")
		switch {
		case trim == "":
			i++
		case strings.HasPrefix(trim, "```"):
			var node map[string]any
			node, i = adfFence(lines, i)
			out = append(out, node)
		case !quote && adfHeadingRe.MatchString(trim):
			m := adfHeadingRe.FindStringSubmatch(trim)
			out = append(out, map[string]any{
				"type":    "heading",
				"attrs":   map[string]any{"level": len(m[1])},
				"content": adfInline(m[2]),
			})
			i++
		case !quote && adfRuleRe.MatchString(trim):
			out = append(out, map[string]any{"type": "rule"})
			i++
		case !quote && strings.HasPrefix(trim, ">"):
			var inner []string
			for ; i < len(lines); i++ {
				s := strings.TrimLeft(lines[i], " \t")
				if !strings.HasPrefix(s, ">") {
					break
				}
				inner = append(inner, strings.TrimPrefix(strings.TrimPrefix(s, ">"), " "))
			}
			out = append(out, map[string]any{"type": "blockquote", "content": adfBlocks(inner, true)})
		case adfListRe.MatchString(line):
			var node map[string]any
			node, i = adfList(lines, i)
			out = append(out, node)
		default:
			j := i + 1
			for j < len(lines) && strings.TrimSpace(lines[j]) != "" && !adfStartsBlock(lines[j], quote) {
				j++
			}
			out = append(out, map[string]any{"type": "paragraph", "content": adfInlineLines(lines[i:j])})
			i = j
		}
	}
	return out
}

// adfStartsBlock reports whether a line opens a block, which ends a paragraph
// without needing a blank line.
func adfStartsBlock(line string, quote bool) bool {
	line = strings.TrimRight(line, " \t")
	trim := strings.TrimLeft(line, " \t")
	if strings.HasPrefix(trim, "```") || adfListRe.MatchString(line) {
		return true
	}
	return !quote && (adfHeadingRe.MatchString(trim) || adfRuleRe.MatchString(trim) || strings.HasPrefix(trim, ">"))
}

// adfFence parses a fenced code block starting at lines[i]. Its body is
// verbatim; a fence of n backticks closes on a line of n or more, so code
// that holds ``` itself gets a longer fence. An unclosed fence runs to the end.
func adfFence(lines []string, i int) (map[string]any, int) {
	open := strings.TrimSpace(lines[i])
	n := len(open) - len(strings.TrimLeft(open, "`"))
	lang := strings.TrimSpace(open[n:])
	var body []string
	j := i + 1
	for ; j < len(lines); j++ {
		if c := strings.TrimSpace(lines[j]); len(c) >= n && strings.Trim(c, "`") == "" {
			j++
			break
		}
		body = append(body, lines[j])
	}
	node := map[string]any{"type": "codeBlock"}
	if lang != "" {
		node["attrs"] = map[string]any{"language": lang}
	}
	if code := strings.Join(body, "\n"); code != "" {
		node["content"] = []any{map[string]any{"type": "text", "text": code}}
	}
	return node, j
}

// adfList parses the list whose first item is lines[i]. Items two or more
// spaces deeper become nested lists inside the item; an item of the other
// kind (bullet vs ordered) at the same depth ends the list.
func adfList(lines []string, i int) (map[string]any, int) {
	first := adfListRe.FindStringSubmatch(lines[i])
	indent := len(first[1])
	ordered := adfOrderedMarker(first[2])
	order := 1
	if ordered {
		order, _ = strconv.Atoi(strings.TrimRight(first[2], ".)"))
	}
	items := []any{}
	for i < len(lines) {
		m := adfListRe.FindStringSubmatch(lines[i])
		if m == nil || len(m[1]) != indent || adfOrderedMarker(m[2]) != ordered {
			break
		}
		text := []string{m[3]}
		i++
		for i < len(lines) && strings.TrimSpace(lines[i]) != "" && !adfListRe.MatchString(lines[i]) &&
			len(lines[i])-len(strings.TrimLeft(lines[i], " ")) >= indent+2 {
			text = append(text, lines[i])
			i++
		}
		content := []any{map[string]any{"type": "paragraph", "content": adfInlineLines(text)}}
		for i < len(lines) {
			nm := adfListRe.FindStringSubmatch(lines[i])
			if nm == nil || len(nm[1]) < indent+2 {
				break
			}
			var nested map[string]any
			nested, i = adfList(lines, i)
			content = append(content, nested)
		}
		items = append(items, map[string]any{"type": "listItem", "content": content})
	}
	if !ordered {
		return map[string]any{"type": "bulletList", "content": items}, i
	}
	node := map[string]any{"type": "orderedList", "content": items}
	if order != 1 {
		node["attrs"] = map[string]any{"order": order}
	}
	return node, i
}

// adfOrderedMarker reports whether a list marker is numeric.
func adfOrderedMarker(marker string) bool {
	return marker[0] >= '0' && marker[0] <= '9'
}

// adfInlineLines parses lines as one run of inline content, joining them with
// hardBreak nodes so single newlines survive.
func adfInlineLines(lines []string) []any {
	out := []any{}
	for k, l := range lines {
		if k > 0 {
			out = append(out, map[string]any{"type": "hardBreak"})
		}
		out = append(out, adfInline(strings.TrimSpace(l))...)
	}
	return out
}

// adfInline parses one line of inline Markdown into text nodes.
func adfInline(s string) []any {
	out := []any{}
	adfInlineInto(&out, s, nil)
	return out
}

// adfInlineInto scans s for links, code, bold and emphasis, recursing into
// the span with the mark added. A delimiter without a valid partner stays
// literal, and text nodes are never empty because Jira rejects them.
func adfInlineInto(out *[]any, s string, marks []any) {
	var plain strings.Builder
	flush := func() {
		adfEmitText(out, plain.String(), marks)
		plain.Reset()
	}
	with := func(m map[string]any) []any {
		return append(append([]any{}, marks...), m)
	}
	for i := 0; i < len(s); {
		switch c := s[i]; {
		case c == '\\' && i+1 < len(s) && adfPunct(s[i+1]):
			plain.WriteByte(s[i+1])
			i += 2
			continue
		case c == '`':
			if end := strings.IndexByte(s[i+1:], '`'); end > 0 {
				flush()
				// The code mark cannot combine with strong or em in ADF.
				var keep []any
				for _, m := range marks {
					if m.(map[string]any)["type"] == "link" {
						keep = append(keep, m)
					}
				}
				adfEmitText(out, s[i+1:i+1+end], append(keep, map[string]any{"type": "code"}))
				i += end + 2
				continue
			}
		case c == '[':
			if mid := strings.Index(s[i:], "]("); mid > 1 {
				if end := strings.IndexByte(s[i+mid+2:], ')'); end > 0 {
					href := strings.TrimSpace(s[i+mid+2 : i+mid+2+end])
					if href != "" {
						flush()
						link := map[string]any{"type": "link", "attrs": map[string]any{"href": href}}
						adfInlineInto(out, s[i+1:i+mid], with(link))
						i += mid + 2 + end + 1
						continue
					}
				}
			}
		case c == '*' || c == '_':
			delim, mark := string(c), "em"
			if c == '*' && strings.HasPrefix(s[i:], "**") {
				delim, mark = "**", "strong"
			}
			if end, ok := adfFindClose(s, i, delim); ok {
				flush()
				adfInlineInto(out, s[i+len(delim):end], with(map[string]any{"type": mark}))
				i = end + len(delim)
				continue
			}
			plain.WriteString(delim)
			i += len(delim)
			continue
		}
		plain.WriteByte(s[i])
		i++
	}
	flush()
}

// adfFindClose finds the delimiter closing the span opened at s[open]. The
// span must be non-empty with no whitespace against either delimiter, and an
// underscore must sit at word edges so snake_case stays literal.
func adfFindClose(s string, open int, delim string) (int, bool) {
	if delim == "_" && open > 0 && adfWordByte(s[open-1]) {
		return 0, false
	}
	start := open + len(delim)
	if start >= len(s) || s[start] == ' ' || s[start] == '\t' {
		return 0, false
	}
	for from := start; ; {
		k := strings.Index(s[from:], delim)
		if k < 0 {
			return 0, false
		}
		end := from + k
		from = end + 1
		if end == start || s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\\' {
			continue
		}
		if delim == "_" && end+1 < len(s) && adfWordByte(s[end+1]) {
			continue
		}
		return end, true
	}
}

// adfPunct reports whether b is ASCII punctuation, which a backslash escapes.
func adfPunct(b byte) bool {
	return b > ' ' && b < 0x7f && !(b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z')
}

// adfWordByte reports whether b is part of a word for underscore emphasis.
func adfWordByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= 0x80
}

// adfEmitText appends a text node, merging into the previous node when the
// marks match and skipping empty text.
func adfEmitText(out *[]any, text string, marks []any) {
	if text == "" {
		return
	}
	if n := len(*out); n > 0 {
		if last, ok := (*out)[n-1].(map[string]any); ok && last["type"] == "text" {
			lastMarks, _ := last["marks"].([]any)
			if reflect.DeepEqual(lastMarks, marks) || len(lastMarks) == 0 && len(marks) == 0 {
				last["text"] = last["text"].(string) + text
				return
			}
		}
	}
	node := map[string]any{"type": "text", "text": text}
	if len(marks) > 0 {
		node["marks"] = marks
	}
	*out = append(*out, node)
}

type adfNode struct {
	Type    string         `json:"type"`
	Text    string         `json:"text"`
	Attrs   map[string]any `json:"attrs"`
	Content []adfNode      `json:"content"`
	Marks   []adfMark      `json:"marks"`
}

type adfMark struct {
	Type  string         `json:"type"`
	Attrs map[string]any `json:"attrs"`
}

// adfToMarkdown reads an ADF document back as Markdown so rota can show Jira
// comments and descriptions. A JSON string (Jira v2 or legacy bodies) is
// returned as is; null, empty or unparseable input gives "".
func adfToMarkdown(raw json.RawMessage) string {
	s := bytes.TrimSpace(raw)
	if len(s) == 0 || string(s) == "null" {
		return ""
	}
	if s[0] == '"' {
		var str string
		if json.Unmarshal(s, &str) != nil {
			return ""
		}
		return str
	}
	var n adfNode
	if json.Unmarshal(s, &n) != nil {
		return ""
	}
	return strings.TrimRight(adfRender(n), " \t\r\n")
}

// adfInlineTypes are the node types that flow inside a line; their parent
// concatenates them instead of separating them with blank lines.
var adfInlineTypes = map[string]bool{
	"text": true, "hardBreak": true, "mention": true, "emoji": true,
	"inlineCard": true, "status": true, "date": true, "mediaInline": true,
}

// adfRender renders one node. Unknown nodes fall back to their children or
// their text, so new Jira node types degrade to readable text.
func adfRender(n adfNode) string {
	switch n.Type {
	case "text":
		return adfRenderText(n)
	case "hardBreak":
		return "\n"
	case "paragraph":
		lines := strings.Split(adfConcat(n.Content), "\n")
		for i, l := range lines {
			lines[i] = adfEscapeLineStart(l)
		}
		return strings.Join(lines, "\n")
	case "heading":
		level := 1
		if f, ok := n.Attrs["level"].(float64); ok {
			level = min(max(int(f), 1), 6)
		}
		return strings.TrimRight(strings.Repeat("#", level)+" "+adfConcat(n.Content), " ")
	case "codeBlock":
		var code strings.Builder
		for _, c := range n.Content {
			code.WriteString(c.Text)
		}
		lang, _ := n.Attrs["language"].(string)
		fence := adfFenceFor(code.String())
		if code.Len() == 0 {
			return fence + lang + "\n" + fence
		}
		return fence + lang + "\n" + code.String() + "\n" + fence
	case "bulletList", "orderedList":
		return adfRenderList(n)
	case "blockquote":
		lines := strings.Split(adfJoin(n.Content), "\n")
		for i, l := range lines {
			lines[i] = strings.TrimRight("> "+l, " ")
		}
		return strings.Join(lines, "\n")
	case "rule":
		return "---"
	case "mention":
		if t := adfAttr(n, "text"); t != "" {
			return t
		}
		return "@" + adfAttr(n, "id")
	case "emoji":
		if t := adfAttr(n, "text"); t != "" {
			return t
		}
		if name := strings.Trim(adfAttr(n, "shortName"), ":"); name != "" {
			return ":" + name + ":"
		}
		return ""
	case "inlineCard", "blockCard", "embedCard":
		return adfAttr(n, "url")
	case "media", "mediaSingle", "mediaGroup", "mediaInline":
		return ""
	case "table":
		return adfRenderTable(n)
	}
	if len(n.Content) > 0 {
		return adfJoin(n.Content)
	}
	if n.Text != "" {
		return n.Text
	}
	return adfAttr(n, "text")
}

// adfAttr returns a string attribute, or "" when absent or not a string.
func adfAttr(n adfNode, key string) string {
	s, _ := n.Attrs[key].(string)
	return s
}

// adfConcat renders children back to back, as inline content flows.
func adfConcat(nodes []adfNode) string {
	var b strings.Builder
	for _, c := range nodes {
		b.WriteString(adfRender(c))
	}
	return b.String()
}

// adfJoin renders children as inline when all are, else as blocks separated
// by a blank line, skipping blocks that render empty (media).
func adfJoin(nodes []adfNode) string {
	inline := true
	for _, c := range nodes {
		inline = inline && adfInlineTypes[c.Type]
	}
	if inline {
		return adfConcat(nodes)
	}
	var parts []string
	for _, c := range nodes {
		if s := adfRender(c); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n\n")
}

// adfRenderText applies marks to a text node: code innermost, then strong,
// em, and link outermost. Edge whitespace stays outside the marks so the
// result is still valid Markdown.
func adfRenderText(n adfNode) string {
	core := strings.TrimSpace(n.Text)
	if core == "" {
		return n.Text
	}
	lead := n.Text[:strings.Index(n.Text, core)]
	trail := n.Text[len(lead)+len(core):]
	has := map[string]adfMark{}
	for _, m := range n.Marks {
		has[m.Type] = m
	}
	if _, ok := has["code"]; ok {
		core = "`" + core + "`"
	} else {
		core = adfEscape(core)
	}
	if _, ok := has["strong"]; ok {
		core = "**" + core + "**"
	}
	if _, ok := has["em"]; ok {
		core = "*" + core + "*"
	}
	if m, ok := has["link"]; ok {
		if href, _ := m.Attrs["href"].(string); href != "" {
			core = "[" + core + "](" + href + ")"
		}
	}
	return lead + core + trail
}

// adfRenderList renders a list with two-space indentation for continuation
// lines and nested lists, which adfList accepts when writing back.
func adfRenderList(n adfNode) string {
	order := 1
	if f, ok := n.Attrs["order"].(float64); ok {
		order = int(f)
	}
	var out []string
	for k, item := range n.Content {
		marker := "-"
		if n.Type == "orderedList" {
			marker = strconv.Itoa(order+k) + "."
		}
		var parts []string
		for _, c := range item.Content {
			if s := adfRender(c); s != "" {
				parts = append(parts, s)
			}
		}
		lines := strings.Split(strings.Join(parts, "\n"), "\n")
		out = append(out, strings.TrimRight(marker+" "+lines[0], " "))
		for _, l := range lines[1:] {
			out = append(out, strings.TrimRight("  "+l, " "))
		}
	}
	return strings.Join(out, "\n")
}

// adfRenderTable renders rows as pipe lines, adding the separator row after a
// header row so the result is a valid Markdown table.
func adfRenderTable(n adfNode) string {
	var out []string
	for r, row := range n.Content {
		cells := make([]string, len(row.Content))
		for i, cell := range row.Content {
			s := strings.ReplaceAll(adfJoin(cell.Content), "\n", " ")
			cells[i] = strings.TrimSpace(strings.ReplaceAll(s, "|", `\|`))
		}
		out = append(out, "| "+strings.Join(cells, " | ")+" |")
		if r == 0 && len(row.Content) > 0 && row.Content[0].Type == "tableHeader" {
			out = append(out, "|"+strings.Repeat(" --- |", len(cells)))
		}
	}
	return strings.Join(out, "\n")
}

var adfOrderedStartRe = regexp.MustCompile(`^(\d+)([.)] .*)$`)

// adfEscapeLineStart escapes a paragraph line the writer would otherwise read
// as the start of a heading, list, quote, rule or fence.
func adfEscapeLineStart(line string) string {
	trim := strings.TrimLeft(line, " \t")
	lead := line[:len(line)-len(trim)]
	if m := adfOrderedStartRe.FindStringSubmatch(trim); m != nil {
		return lead + m[1] + "\\" + m[2]
	}
	if adfStartsBlock(trim, false) || adfRuleRe.MatchString(trim) {
		return lead + "\\" + trim
	}
	return line
}

// adfEscape backslash-escapes the Markdown-significant characters of plain
// text, but only when the writer would read the text as anything other than
// itself, so ordinary text (snake_case, a lone *) reads back unchanged.
func adfEscape(s string) string {
	nodes := adfInline(s)
	if len(nodes) == 1 {
		if n := nodes[0].(map[string]any); n["marks"] == nil && n["text"] == s {
			return s
		}
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if strings.IndexByte("\\`*_[]", s[i]) >= 0 {
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// adfFenceFor is a backtick fence longer than any backtick run in code.
func adfFenceFor(code string) string {
	longest, run := 0, 0
	for i := 0; i < len(code); i++ {
		if code[i] == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return strings.Repeat("`", max(3, longest+1))
}
