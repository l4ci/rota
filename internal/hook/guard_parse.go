package hook

import (
	"strings"

	"github.com/l4ci/rota/internal/shlex"
)

// A small shell tokenizer for the guard (#702). It does not run or expand
// anything: it splits a command line into simple commands (argv word lists)
// and collects the scripts nested in $(...), backticks and process
// substitution, so the policy sees every command the line would run. Text in
// an argument is never a command; only argv[0] positions are.

// parsed is one script: its simple commands in order and the scripts nested
// inside it (command substitutions).
type parsed struct {
	cmds   [][]string
	nested []string
}

type scanner struct {
	shlex.Scanner
	pending []heredoc
	opEnd   int // index just past the last redirect operator the scanner consumed
}

type heredoc struct {
	delim string
	strip bool // <<- strips leading tabs from body lines
	raw   bool // a quoted delimiter: the body is literal, nothing expands
}

func parseScript(s string) (parsed, error) {
	sc := &scanner{Scanner: shlex.Scanner{S: s}}
	var p parsed
	var cur []string
	lastEnd := -1 // index just past the last word, for `2>file` fd prefixes
	flush := func() {
		if len(cur) > 0 {
			p.cmds = append(p.cmds, cur)
		}
		cur = nil
	}
	for sc.I < len(sc.S) {
		c := sc.S[sc.I]
		switch {
		case c == ' ' || c == '\t' || c == '\r':
			sc.I++
		case c == '\n':
			sc.I++
			flush()
			if err := sc.skipHeredocs(); err != nil {
				return p, err
			}
		case c == ';' || c == '(' || c == ')':
			sc.I++
			flush()
		case c == '|' || (c == '&' && !sc.ampersandIsRedirect()):
			sc.I++
			if sc.I < len(sc.S) && sc.S[sc.I] == c {
				sc.I++
			}
			flush()
		case c == '#' && (sc.I == 0 || strings.ContainsRune(" \t\n;|&()", rune(sc.S[sc.I-1]))):
			for sc.I < len(sc.S) && sc.S[sc.I] != '\n' {
				sc.I++
			}
		case c == '<' || c == '>':
			if (sc.Peek(1) == '(') && (c == '<' || c == '>') {
				sc.I++ // process substitution: the ( ends the command and starts a new one
				sc.opEnd = sc.I
				continue
			}
			if c == '<' && sc.Peek(1) == '<' && sc.Peek(2) != '<' {
				if err := sc.heredocHeader(); err != nil {
					return p, err
				}
				continue
			}
			cur = dropFdPrefix(cur, lastEnd, sc.I)
			if err := sc.redirect(); err != nil {
				return p, err
			}
		case c == '&':
			// Only reachable when ampersandIsRedirect: `&>file`, `<&3`.
			// word() stops on a break byte without advancing, so consume it here.
			if err := sc.redirect(); err != nil {
				return p, err
			}
		default:
			w, err := sc.word()
			if err != nil {
				return p, err
			}
			cur = append(cur, w)
			lastEnd = sc.I
		}
	}
	flush()
	p.nested = sc.Nested
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
	if sc.I+n < len(sc.S) {
		return sc.S[sc.I+n]
	}
	return 0
}

// ampersandIsRedirect: `2>&1`, `>&2` and `&>file` are redirections, not
// command separators.
func (sc *scanner) ampersandIsRedirect() bool {
	// The previous byte only counts when the scanner consumed it as an
	// operator: an escaped or quoted `>` (`echo \>&& cmd`) is a word byte.
	if sc.I > 0 && sc.opEnd == sc.I && (sc.S[sc.I-1] == '>' || sc.S[sc.I-1] == '<') {
		return true
	}
	return sc.Peek(1) == '>'
}

// redirect consumes a redirection operator and its target word.
func (sc *scanner) redirect() error {
	for sc.I < len(sc.S) && strings.IndexByte("<>&|", sc.S[sc.I]) >= 0 {
		sc.I++
	}
	sc.opEnd = sc.I
	for sc.I < len(sc.S) && (sc.S[sc.I] == ' ' || sc.S[sc.I] == '\t') {
		sc.I++
	}
	if sc.I >= len(sc.S) || sc.isBreak(sc.S[sc.I]) {
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
	sc.I += 2
	sc.opEnd = sc.I
	h := heredoc{}
	if sc.Peek(0) == '-' {
		h.strip = true
		sc.I++
	}
	for sc.I < len(sc.S) && (sc.S[sc.I] == ' ' || sc.S[sc.I] == '\t') {
		sc.I++
	}
	start := sc.I
	w, err := sc.word()
	if err != nil {
		return err
	}
	h.delim = w
	h.raw = strings.ContainsAny(sc.S[start:sc.I], `'"\\`)
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
		bodyStart := sc.I
		bodyEnd := len(sc.S)
		for sc.I < len(sc.S) {
			lineStart := sc.I
			end := strings.IndexByte(sc.S[sc.I:], '\n')
			var line string
			if end < 0 {
				line, sc.I = sc.S[sc.I:], len(sc.S)
			} else {
				line, sc.I = sc.S[sc.I:sc.I+end], sc.I+end+1
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
		if err := sc.expandBody(sc.S[bodyStart:bodyEnd]); err != nil {
			return err
		}
	}
	return nil
}

// expandBody collects the command substitutions of an unquoted here-document
// body, which bash expands like a double-quoted string.
func (sc *scanner) expandBody(body string) error {
	sub := &scanner{Scanner: shlex.Scanner{S: body}}
	for sub.I < len(sub.S) {
		switch {
		case sub.S[sub.I] == '\\':
			sub.I += 2
		case sub.S[sub.I] == '`':
			if err := sub.Backtick(); err != nil {
				return err
			}
		case sub.S[sub.I] == '$' && sub.Peek(1) == '(':
			if err := sub.Substitution(); err != nil {
				return err
			}
		default:
			sub.I++
		}
	}
	sc.Nested = append(sc.Nested, sub.Nested...)
	return nil
}

// word reads one shell word through the shared quote core.
func (sc *scanner) word() (string, error) { return sc.Word(sc.isBreak) }
