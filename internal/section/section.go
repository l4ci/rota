// Package section scans `## Topic` sections in the markdown state files
// (KNOWLEDGE.md, DECISIONS.md) and writes managed blocks into the project
// instructions file. It ports bin/hvlib_section.py, so rota and the old helpers
// produce byte-identical files.
package section

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/pystr"
)

// Find locates the body of "## name": start is the byte offset where the
// body begins (right after the heading text and its trailing whitespace) and
// end is the offset of the next "## " heading, or len(content). ok is false
// when the heading is missing.
//
// Python's `^## name\s*$` lets \s* eat newlines and then backs off until the
// match ends at a line end, so start is the end of the longest whitespace run
// after the heading that stops at a "\n" or at the end of the text. The body
// may therefore begin with "\n" or not, exactly as in Python.
func Find(content, name string) (start, end int, ok bool) {
	head := "## " + name
	for p := 0; p <= len(content); p++ {
		if p > 0 && content[p-1] != '\n' {
			continue
		}
		if !strings.HasPrefix(content[p:], head) {
			continue
		}
		s := p + len(head)
		runEnd := s
		for runEnd < len(content) {
			r, n := decode(content[runEnd:])
			if !pystr.IsSpace(r) {
				break
			}
			runEnd += n
		}
		for j := runEnd; j >= s; j-- {
			if j == len(content) || content[j] == '\n' {
				return j, nextHeading(content, j), true
			}
		}
	}
	return 0, 0, false
}

// nextHeading is the offset of the first "## " that starts a line at or after
// from+1 (Python searches content[from:] with ^ matching only after "\n").
func nextHeading(content string, from int) int {
	for i := from + 1; i+3 <= len(content); i++ {
		if content[i-1] == '\n' && content[i:i+3] == "## " {
			return i
		}
	}
	return len(content)
}

// Body returns the body of "## <name>", or "" when it is missing.
func Body(content, name string) string {
	if s, e, ok := Find(content, name); ok {
		return content[s:e]
	}
	return ""
}

// Replace swaps the body of "## <name>" for newBody. A missing section is
// appended at the end, after a blank line.
func Replace(content, name, newBody string) string {
	s, e, ok := Find(content, name)
	if !ok {
		return strings.TrimRight(content, "\n") + "\n\n## " + name + "\n" + newBody
	}
	return content[:s] + newBody + content[e:]
}

// Append splices addition at the end of the body of "## name", just before
// the next "## " heading. If the body does not end in a newline one is added
// first; addition controls the rest of the layout. A missing section is
// appended as "## name\n" followed by addition.
func Append(content, name, addition string) string {
	s, e, ok := Find(content, name)
	if !ok {
		return strings.TrimRight(content, "\n") + "\n\n## " + name + "\n" + addition
	}
	body := content[s:e]
	if strings.HasSuffix(body, "\n") {
		return content[:s] + body + addition + content[e:]
	}
	return content[:s] + body + "\n" + addition + content[e:]
}

// Topic is one "## Name" section: Body runs from after the heading line to the
// next "## " heading or EOF, whitespace untouched.
type Topic struct{ Name, Body string }

// Topics lists every "## Name" heading in document order, like
// re.split(r"^(## .+)$", MULTILINE): a heading is a line that starts with
// "## " and has at least one more character. Text before the first heading is
// dropped.
func Topics(content string) []Topic {
	type head struct{ start, end int } // heading line [start, end), without its "\n"
	var heads []head
	for pos := 0; pos < len(content); {
		lineEnd, next := len(content), len(content)
		if eol := strings.IndexByte(content[pos:], '\n'); eol >= 0 {
			lineEnd = pos + eol
			next = lineEnd + 1
		}
		if lineEnd-pos > 3 && content[pos:pos+3] == "## " {
			heads = append(heads, head{pos, lineEnd})
		}
		pos = next
	}
	out := make([]Topic, 0, len(heads))
	for i, h := range heads {
		bodyEnd := len(content)
		if i+1 < len(heads) {
			bodyEnd = heads[i+1].start
		}
		out = append(out, Topic{
			Name: pystr.Strip(content[h.start+3 : h.end]),
			Body: content[h.end:bodyEnd],
		})
	}
	return out
}

func decode(s string) (rune, int) { return utf8.DecodeRuneInString(s) }

// Matching renders each section whose lowercased title is in wanted, in
// document order, separated by a blank line, bodies right-trimmed. Every line
// ends in a newline; the result is "" when nothing matches.
func Matching(content string, wanted map[string]bool) string {
	var b strings.Builder
	first := true
	for _, t := range Topics(content) {
		if !wanted[strings.ToLower(t.Name)] {
			continue
		}
		if !first {
			b.WriteString("\n")
		}
		b.WriteString("## " + t.Name + "\n")
		b.WriteString(strings.TrimRight(t.Body, " \t\n\r\f\v") + "\n")
		first = false
	}
	return b.String()
}

// InstructionsFile is the project-instructions file under root that holds the
// managed rota blocks: AGENTS.md when it exists, else CLAUDE.md (which may not
// exist yet; UpsertBlock creates it).
func InstructionsFile(root string) string {
	agents := filepath.Join(root, "AGENTS.md")
	if _, err := os.Stat(agents); err == nil {
		return agents
	}
	return filepath.Join(root, "CLAUDE.md")
}

// Managed-block markers. Every writer and matcher of a block goes through
// this file, so the format is spelled once. hv-<key> is the pre-rename (#236)
// spelling, still matched so old blocks upgrade in place.
const (
	blockStartFmt = "<!-- rota-%s-start -->"
	blockEndFmt   = "<!-- rota-%s-end -->"
)

var (
	// BlockKeyRe finds the start marker of any managed block; group 1 is the key.
	BlockKeyRe = regexp.MustCompile(`<!-- (?:rota|hv)-([\w-]+)-start -->`)
	// AnyBlockRe matches any canonical (rota-) managed block; groups 1 and 2
	// are the start and end keys, which the caller compares (RE2 has no
	// backreferences).
	AnyBlockRe = regexp.MustCompile(`(?s)<!-- rota-([\w-]+)-start -->.*?<!-- rota-([\w-]+)-end -->`)
)

// Wrap puts body between the key's start and end markers. body is used as
// given: it carries its own trailing blank line if the block wants one.
func Wrap(key, body string) string {
	return fmt.Sprintf(blockStartFmt+"\n%s\n"+blockEndFmt, key, body, key)
}

// BlockRegex matches a managed block: the canonical
// "<!-- rota-<key>-start -->…<!-- rota-<key>-end -->" and the same block hv
// wrote before the rename (#236) as "<!-- hv-<key>-start -->…".
// consumeNewline also eats one newline after the end marker.
func BlockRegex(key string, consumeNewline bool) *regexp.Regexp {
	tail := ""
	if consumeNewline {
		tail = `\n?`
	}
	k := regexp.QuoteMeta(key)
	return regexp.MustCompile(`(?s)<!-- (?:rota|hv)-` + k + `-start -->.*?<!-- (?:rota|hv)-` + k + `-end -->` + tail)
}

// Block statuses returned by UpsertBlock.
const (
	Created   = "created"
	Updated   = "updated"
	Appended  = "appended"
	Unchanged = "unchanged"
)

// UpsertBlock writes block into path: it replaces an existing block for key
// (a legacy hv-<key> block too, in place), appends one when none exists, or
// creates the file holding just the block.
// A second identical call is Unchanged and does not touch the file.
func UpsertBlock(path, key, block string) (string, error) {
	content, err := fsio.ReadText(path)
	if os.IsNotExist(err) {
		return Created, fsio.WriteFileAtomic(path, []byte(block+"\n"))
	}
	if err != nil {
		return "", err
	}
	re := BlockRegex(key, false)
	var next, status string
	if re.MatchString(content) {
		// ReplaceAllLiteralString: Python's re.sub would read backslash
		// escapes in block, which managed blocks never rely on.
		next = re.ReplaceAllLiteralString(content, block)
		status = Updated
	} else {
		next = strings.TrimRight(content, " \t\n\r\f\v") + "\n\n" + block + "\n"
		status = Appended
	}
	if next == content {
		return Unchanged, nil
	}
	return status, fsio.WriteFileAtomic(path, []byte(next))
}

// UpsertManaged wraps body in the key's markers and upserts it into the
// project's instructions file under root.
func UpsertManaged(root, key, body string) (string, error) {
	return UpsertBlock(InstructionsFile(root), key, Wrap(key, body))
}

// Lines splits s like Python's str.splitlines: at \n, \r, \r\n, \v, \f,
// \x1c-\x1e, \x85, \u2028 and \u2029, dropping the separators and any final
// empty piece.
func Lines(s string) []string {
	var out []string
	start := 0
	rs := []rune(s)
	// Work on byte offsets via a rune walk.
	pos := 0
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		w := len(string(r))
		switch r {
		case '\n', '\r', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
			out = append(out, s[start:pos])
			if r == '\r' && i+1 < len(rs) && rs[i+1] == '\n' {
				i++
				pos++
			}
			start = pos + w
		}
		pos += w
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
