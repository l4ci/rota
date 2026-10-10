package hook

import (
	"errors"
	"strings"
)

// A small shell tokenizer for the guard (#702). It does not run or expand
// anything: it splits a command line into simple commands (argv word lists)
// and collects the scripts nested in $(...), backticks and process
// substitution, so the policy sees every command the line would run. Text in
// an argument is never a command; only argv[0] positions are.

var errUnterminated = errors.New("unterminated quote or substitution")

// parsed is one script: its simple commands in order and the scripts nested
// inside it (command substitutions).
type parsed struct {
	cmds   [][]string
	nested []string
}

type scanner struct {
	s       string
	i       int
	nested  []string
	pending []heredoc
}

type heredoc struct {
	delim string
	strip bool // <<- strips leading tabs from body lines
	raw   bool // a quoted delimiter: the body is literal, nothing expands
}

func parseScript(s string) (parsed, error) {
	sc := &scanner{s: s}
	var p parsed
	var cur []string
	lastEnd := -1 // index just past the last word, for `2>file` fd prefixes
	flush := func() {
		if len(cur) > 0 {
			p.cmds = append(p.cmds, cur)
		}
		cur = nil
	}
	for sc.i < len(sc.s) {
		c := sc.s[sc.i]
		switch {
		case c == ' ' || c == '\t' || c == '\r':
			sc.i++
		case c == '\n':
			sc.i++
			flush()
			if err := sc.skipHeredocs(); err != nil {
				return p, err
			}
		case c == ';' || c == '(' || c == ')':
			sc.i++
			flush()
		case c == '|' || (c == '&' && !sc.ampersandIsRedirect()):
			sc.i++
			if sc.i < len(sc.s) && sc.s[sc.i] == c {
				sc.i++
			}
			flush()
		case c == '#' && (sc.i == 0 || strings.ContainsRune(" \t\n;|&()", rune(sc.s[sc.i-1]))):
			for sc.i < len(sc.s) && sc.s[sc.i] != '\n' {
				sc.i++
			}
		case c == '<' || c == '>':
			if (sc.peek(1) == '(') && (c == '<' || c == '>') {
				sc.i++ // process substitution: the ( ends the command and starts a new one
				continue
			}
			if c == '<' && sc.peek(1) == '<' && sc.peek(2) != '<' {
				if err := sc.heredocHeader(); err != nil {
					return p, err
				}
				continue
			}
			cur = dropFdPrefix(cur, lastEnd, sc.i)
			if err := sc.redirect(); err != nil {
				return p, err
			}
		default:
			w, err := sc.word()
			if err != nil {
				return p, err
			}
			cur = append(cur, w)
			lastEnd = sc.i
		}
	}
	flush()
	p.nested = sc.nested
	return p, nil
}

// dropFdPrefix removes a bare-digit word glued to a redirection (`2>x`).
func dropFdPrefix(cur []string, lastEnd, at int) []string {
	if n := len(cur); n > 0 && lastEnd == at && isDigits(cur[n-1]) {
		return cur[:n-1]
	}
	return cur
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (sc *scanner) peek(n int) byte {
	if sc.i+n < len(sc.s) {
		return sc.s[sc.i+n]
	}
	return 0
}

// ampersandIsRedirect: `2>&1`, `>&2` and `&>file` are redirections, not
// command separators.
func (sc *scanner) ampersandIsRedirect() bool {
	if sc.i > 0 && (sc.s[sc.i-1] == '>' || sc.s[sc.i-1] == '<') {
		return true
	}
	return sc.peek(1) == '>'
}

// redirect consumes a redirection operator and its target word.
func (sc *scanner) redirect() error {
	for sc.i < len(sc.s) && strings.IndexByte("<>&|", sc.s[sc.i]) >= 0 {
		sc.i++
	}
	for sc.i < len(sc.s) && (sc.s[sc.i] == ' ' || sc.s[sc.i] == '\t') {
		sc.i++
	}
	if sc.i >= len(sc.s) || sc.isBreak(sc.s[sc.i]) {
		return nil
	}
	_, err := sc.word()
	return err
}

func (sc *scanner) isBreak(c byte) bool {
	return strings.IndexByte(" \t\r\n;|&()<>", c) >= 0
}

// heredocHeader reads `<<[-] [quote]DELIM[quote]` and queues the body skip.
func (sc *scanner) heredocHeader() error {
	sc.i += 2
	h := heredoc{}
	if sc.peek(0) == '-' {
		h.strip = true
		sc.i++
	}
	for sc.i < len(sc.s) && (sc.s[sc.i] == ' ' || sc.s[sc.i] == '\t') {
		sc.i++
	}
	start := sc.i
	w, err := sc.word()
	if err != nil {
		return err
	}
	h.delim = w
	h.raw = strings.ContainsAny(sc.s[start:sc.i], `'"\\`)
	sc.pending = append(sc.pending, h)
	return nil
}

// skipHeredocs drops the body lines of every queued here-document; text in a
// body (a commit message) is data, not commands. An unquoted body still
// expands, so its $(...) and backtick substitutions are queued as nested
// scripts.
func (sc *scanner) skipHeredocs() error {
	pending := sc.pending
	sc.pending = nil
	for _, h := range pending {
		bodyStart := sc.i
		bodyEnd := len(sc.s)
		for sc.i < len(sc.s) {
			lineStart := sc.i
			end := strings.IndexByte(sc.s[sc.i:], '\n')
			var line string
			if end < 0 {
				line, sc.i = sc.s[sc.i:], len(sc.s)
			} else {
				line, sc.i = sc.s[sc.i:sc.i+end], sc.i+end+1
			}
			if h.strip {
				line = strings.TrimLeft(line, "\t")
			}
			if line == h.delim {
				bodyEnd = lineStart
				break
			}
		}
		if h.raw {
			continue
		}
		if err := sc.expandBody(sc.s[bodyStart:bodyEnd]); err != nil {
			return err
		}
	}
	return nil
}

// expandBody collects the command substitutions of an unquoted here-document
// body, which bash expands like a double-quoted string.
func (sc *scanner) expandBody(body string) error {
	sub := &scanner{s: body}
	for sub.i < len(sub.s) {
		switch {
		case sub.s[sub.i] == '\\':
			sub.i += 2
		case sub.s[sub.i] == '`':
			if err := sub.backtick(); err != nil {
				return err
			}
		case sub.s[sub.i] == '$' && sub.peek(1) == '(':
			if err := sub.substitution(); err != nil {
				return err
			}
		default:
			sub.i++
		}
	}
	sc.nested = append(sc.nested, sub.nested...)
	return nil
}

// word reads one shell word, resolving quotes and escapes and collecting
// command substitutions into sc.nested.
func (sc *scanner) word() (string, error) {
	var b strings.Builder
	for sc.i < len(sc.s) {
		c := sc.s[sc.i]
		switch {
		case c == '\\':
			if sc.i+1 < len(sc.s) {
				if sc.s[sc.i+1] != '\n' {
					b.WriteByte(sc.s[sc.i+1])
				}
				sc.i += 2
			} else {
				sc.i++
			}
		case c == '\'':
			end := strings.IndexByte(sc.s[sc.i+1:], '\'')
			if end < 0 {
				return "", errUnterminated
			}
			b.WriteString(sc.s[sc.i+1 : sc.i+1+end])
			sc.i += end + 2
		case c == '"':
			if err := sc.doubleQuoted(&b); err != nil {
				return "", err
			}
		case c == '`':
			if err := sc.backtick(); err != nil {
				return "", err
			}
		case c == '$' && sc.peek(1) == '(':
			if err := sc.substitution(); err != nil {
				return "", err
			}
		case sc.isBreak(c):
			return b.String(), nil
		default:
			b.WriteByte(c)
			sc.i++
		}
	}
	return b.String(), nil
}

func (sc *scanner) doubleQuoted(b *strings.Builder) error {
	sc.i++ // opening quote
	for sc.i < len(sc.s) {
		c := sc.s[sc.i]
		switch {
		case c == '"':
			sc.i++
			return nil
		case c == '\\' && sc.i+1 < len(sc.s):
			n := sc.s[sc.i+1]
			if strings.IndexByte("$`\"\\\n", n) >= 0 {
				if n != '\n' {
					b.WriteByte(n)
				}
			} else {
				b.WriteByte('\\')
				b.WriteByte(n)
			}
			sc.i += 2
		case c == '`':
			if err := sc.backtick(); err != nil {
				return err
			}
		case c == '$' && sc.peek(1) == '(':
			if err := sc.substitution(); err != nil {
				return err
			}
		default:
			b.WriteByte(c)
			sc.i++
		}
	}
	return errUnterminated
}

func (sc *scanner) backtick() error {
	j := sc.i + 1
	for j < len(sc.s) && sc.s[j] != '`' {
		if sc.s[j] == '\\' {
			j++
		}
		j++
	}
	if j >= len(sc.s) {
		return errUnterminated
	}
	sc.nested = append(sc.nested, sc.s[sc.i+1:j])
	sc.i = j + 1
	return nil
}

// substitution consumes `$(...)`, or `$((...))` arithmetic, which is not a
// script. Parentheses inside quotes do not count.
func (sc *scanner) substitution() error {
	arith := sc.peek(2) == '('
	start := sc.i + 2
	depth, j := 1, start
	for j < len(sc.s) && depth > 0 {
		switch sc.s[j] {
		case '\\':
			j++
		case '\'':
			if end := strings.IndexByte(sc.s[j+1:], '\''); end >= 0 {
				j += end + 1
			}
		case '"':
			for j++; j < len(sc.s) && sc.s[j] != '"'; j++ {
				if sc.s[j] == '\\' {
					j++
				}
			}
		case '(':
			depth++
		case ')':
			depth--
		}
		j++
	}
	if depth > 0 {
		return errUnterminated
	}
	if !arith {
		sc.nested = append(sc.nested, sc.s[start:j-1])
	}
	sc.i = j
	return nil
}
