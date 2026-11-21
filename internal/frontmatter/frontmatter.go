// Package frontmatter is rota's one frontmatter implementation, ported from
// the earlier hvlib_frontmatter helper: the flat subset that design, plan, spike,
// milestone, map and qa files use. Between `---` lines it understands only
// `key: value` and inline lists `key: [a, b]`; no nesting, no quoting.
// LF and CRLF files both parse. Every phase reuses this package.
package frontmatter

import (
	"regexp"
	"strings"
	"unicode"
)

// fenceSpace reports the characters Python's str.isspace() accepts: Go's
// unicode.IsSpace plus the ASCII separators U+001C to U+001F.
func fenceSpace(r rune) bool { return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f) }

func isFence(line string) bool { return strings.TrimRightFunc(line, fenceSpace) == "---" }

// Fence locates the front matter in text, the one fence rule every Go reader
// shares and scripts/fmlite.py's: the first line is `---` and the next `---`
// line closes it, each ignoring trailing whitespace as Python's str.rstrip()
// does, so CRLF files, an NBSP after a fence and a closing fence at end of file
// are all front matter. The fixtures in test/fixtures/frontmatter pin it.
// text[blockStart:blockEnd] is the lines between the fences (each with its
// newline), blockEnd is where the closing fence line starts, and
// text[bodyStart:] follows it. ok is false when there is no closed front matter.
func Fence(text string) (blockStart, blockEnd, bodyStart int, ok bool) {
	nl := strings.IndexByte(text, '\n')
	if nl < 0 || !isFence(text[:nl]) {
		return 0, 0, 0, false
	}
	for pos := nl + 1; pos <= len(text); {
		lineEnd, next := len(text), len(text)
		if i := strings.IndexByte(text[pos:], '\n'); i >= 0 {
			lineEnd, next = pos+i, pos+i+1
		}
		if isFence(text[pos:lineEnd]) {
			return nl + 1, pos, next, true
		}
		if next == pos || lineEnd == len(text) {
			break
		}
		pos = next
	}
	return 0, 0, 0, false
}

// Parse reads a flat
// `key: value` / `key: [a, b]` subset between `---` lines. Values stay
// strings; an inline list comes back as []string. It returns (nil, text)
// when there is no frontmatter, and never fails. Keys keep file order.
func Parse(text string) (map[string]any, []string, string) {
	bs, be, body0, ok := Fence(text)
	if !ok {
		return nil, nil, text
	}
	block := text[bs:be]
	body := strings.TrimLeft(text[body0:], "\n")
	fm := map[string]any{}
	var order []string
	for _, raw := range strings.Split(strings.ReplaceAll(block, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if _, seen := fm[key]; !seen {
			order = append(order, key)
		}
		if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
			list := []string{}
			for _, v := range strings.Split(strings.TrimSpace(value[1:len(value)-1]), ",") {
				if v = strings.TrimSpace(v); v != "" {
					list = append(list, v)
				}
			}
			fm[key] = list
		} else {
			fm[key] = value
		}
	}
	if len(fm) == 0 {
		return nil, nil, text
	}
	return fm, order, body
}

// Str returns a frontmatter value as a string ("" when absent or a list).
func Str(fm map[string]any, key string) string {
	s, _ := fm[key].(string)
	return s
}

// UpdateField replaces the first `<field>: <value>` in the frontmatter slice and
// reports whether it found one.
func UpdateField(content, field, value string) (string, bool) {
	start, end, _, ok := Fence(content)
	if !ok {
		return content, false
	}
	fm, after := content[start:end], content[end:]
	pat := regexp.MustCompile(`(?m)^(` + regexp.QuoteMeta(field) + `:[ \t\r\f\v]*)\S+`)
	done := false
	newFM := pat.ReplaceAllStringFunc(fm, func(m string) string {
		if done {
			return m
		}
		done = true
		return pat.FindStringSubmatch(m)[1] + value
	})
	if !done {
		return content, false
	}
	return content[:start] + newFM + after, true
}

// SetField sets `<field>: <value>` in the frontmatter: it replaces the first
// such line, or inserts one after the `<after>:` line (at the end of the
// frontmatter when after is empty or absent). It reports false, leaving
// content untouched, when content has no frontmatter.
func SetField(content, field, value, after string) (string, bool) {
	if updated, ok := UpdateField(content, field, value); ok {
		return updated, true
	}
	start, end, _, ok := Fence(content)
	if !ok {
		return content, false
	}
	fmText := content[start:end]
	eol := "\n"
	if strings.Contains(fmText, "\r\n") {
		eol = "\r\n"
	}
	line := field + ": " + value
	if after != "" {
		pat := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(after) + `:[^\r\n]*`)
		if m := pat.FindStringIndex(fmText); m != nil {
			at := start + m[1]
			return content[:at] + eol + line + content[at:], true
		}
	}
	at := end // before the closing fence
	return content[:at] + line + eol + content[at:], true
}
