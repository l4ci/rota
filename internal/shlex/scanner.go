package shlex

import "strings"

// Scanner reads shell words from S starting at I without running or expanding
// anything. Unlike Split it knows the shell's quoting well enough to record the
// scripts nested in $(...) and backticks, so a caller that splits on operators
// itself (the guard tokenizer) can reuse the quote rules instead of copying
// them. Quoting rules live here once.
type Scanner struct {
	S string
	I int
	// Nested collects the script text of every command substitution and
	// backtick the scan passed, in order. $((...)) arithmetic is not a script, but
	// the substitutions inside it are collected.
	Nested []string
}

// Peek returns the byte n past the cursor, or 0 past the end.
func (sc *Scanner) Peek(n int) byte {
	if sc.I+n < len(sc.S) {
		return sc.S[sc.I+n]
	}
	return 0
}

// Word reads one word, resolving quotes and escapes, until a byte for which
// isBreak is true (left unconsumed) or the end. It returns ErrUnbalanced for an
// unterminated quote or substitution.
func (sc *Scanner) Word(isBreak func(byte) bool) (string, error) {
	var b strings.Builder
	for sc.I < len(sc.S) {
		c := sc.S[sc.I]
		switch {
		case c == '\\':
			if sc.I+1 < len(sc.S) {
				if sc.S[sc.I+1] != '\n' {
					b.WriteByte(sc.S[sc.I+1])
				}
				sc.I += 2
			} else {
				sc.I++
			}
		case c == '\'':
			end := strings.IndexByte(sc.S[sc.I+1:], '\'')
			if end < 0 {
				return "", ErrUnbalanced
			}
			b.WriteString(sc.S[sc.I+1 : sc.I+1+end])
			sc.I += end + 2
		case c == '"':
			if err := sc.doubleQuoted(&b); err != nil {
				return "", err
			}
		case c == '`':
			if err := sc.Backtick(); err != nil {
				return "", err
			}
		case c == '$' && sc.Peek(1) == '(':
			if err := sc.Substitution(); err != nil {
				return "", err
			}
		case isBreak(c):
			return b.String(), nil
		default:
			b.WriteByte(c)
			sc.I++
		}
	}
	return b.String(), nil
}

func (sc *Scanner) doubleQuoted(b *strings.Builder) error {
	sc.I++ // opening quote
	for sc.I < len(sc.S) {
		c := sc.S[sc.I]
		switch {
		case c == '"':
			sc.I++
			return nil
		case c == '\\' && sc.I+1 < len(sc.S):
			n := sc.S[sc.I+1]
			if strings.IndexByte("$`\"\\\n", n) >= 0 {
				if n != '\n' {
					b.WriteByte(n)
				}
			} else {
				b.WriteByte('\\')
				b.WriteByte(n)
			}
			sc.I += 2
		case c == '`':
			if err := sc.Backtick(); err != nil {
				return err
			}
		case c == '$' && sc.Peek(1) == '(':
			if err := sc.Substitution(); err != nil {
				return err
			}
		default:
			b.WriteByte(c)
			sc.I++
		}
	}
	return ErrUnbalanced
}

// Backtick consumes a backtick substitution at the cursor and records its body.
func (sc *Scanner) Backtick() error {
	j := sc.I + 1
	for j < len(sc.S) && sc.S[j] != '`' {
		if sc.S[j] == '\\' {
			j++
		}
		j++
	}
	if j >= len(sc.S) {
		return ErrUnbalanced
	}
	sc.Nested = append(sc.Nested, unescapeBacktick(sc.S[sc.I+1:j]))
	sc.I = j + 1
	return nil
}

// unescapeBacktick undoes the escapes bash removes from a backtick body before
// running it: \` , \\ and \$ lose their backslash. Without this an escaped
// nested backtick stays literal and the command inside it is never collected.
func unescapeBacktick(body string) string {
	if !strings.Contains(body, "\\") {
		return body
	}
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		if body[i] == '\\' && i+1 < len(body) && strings.IndexByte("`\\$", body[i+1]) >= 0 {
			i++
		}
		b.WriteByte(body[i])
	}
	return b.String()
}

// collectInner records the substitutions inside text that is not itself a
// script, such as the body of $((...)) arithmetic.
func (sc *Scanner) collectInner(text string) error {
	sub := &Scanner{S: text}
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

// Substitution consumes `$(...)` at the cursor and records its body; `$((...))`
// arithmetic is consumed and only the substitutions inside it are recorded.
// Parentheses inside quotes do not count.
func (sc *Scanner) Substitution() error {
	arith := sc.Peek(2) == '('
	start := sc.I + 2
	depth, j := 1, start
	for j < len(sc.S) && depth > 0 {
		switch sc.S[j] {
		case '\\':
			j++
		case '\'':
			if end := strings.IndexByte(sc.S[j+1:], '\''); end >= 0 {
				j += end + 1
			}
		case '"':
			for j++; j < len(sc.S) && sc.S[j] != '"'; j++ {
				if sc.S[j] == '\\' {
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
		return ErrUnbalanced
	}
	if arith {
		if err := sc.collectInner(sc.S[start : j-1]); err != nil {
			return err
		}
	} else {
		sc.Nested = append(sc.Nested, sc.S[start:j-1])
	}
	sc.I = j
	return nil
}
