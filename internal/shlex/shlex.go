// Package shlex splits a command line the way Python's shlex.split does in
// POSIX mode: whitespace separates words, single quotes are literal, double
// quotes allow \" and \\ escapes, and a bare backslash escapes the next rune.
package shlex

import (
	"errors"
	"strings"
)

// ErrUnbalanced is returned for an unterminated quote or a trailing backslash.
var ErrUnbalanced = errors.New("shlex: no closing quotation")

// Split breaks s into words. A word that is quoted but empty ("") stays an
// empty word, as in Python.
func Split(s string) ([]string, error) {
	var (
		words []string
		cur   strings.Builder
		have  bool
	)
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			if have {
				words = append(words, cur.String())
				cur.Reset()
				have = false
			}
		case r == '\\':
			if i+1 >= len(rs) {
				return nil, errors.New("shlex: no escaped character")
			}
			i++
			cur.WriteRune(rs[i])
			have = true
		case r == '\'':
			have = true
			j := i + 1
			for ; j < len(rs) && rs[j] != '\''; j++ {
				cur.WriteRune(rs[j])
			}
			if j >= len(rs) {
				return nil, ErrUnbalanced
			}
			i = j
		case r == '"':
			have = true
			j := i + 1
			for ; j < len(rs) && rs[j] != '"'; j++ {
				if rs[j] == '\\' && j+1 < len(rs) && (rs[j+1] == '"' || rs[j+1] == '\\') {
					j++
				}
				cur.WriteRune(rs[j])
			}
			if j >= len(rs) {
				return nil, ErrUnbalanced
			}
			i = j
		default:
			cur.WriteRune(r)
			have = true
		}
	}
	if have {
		words = append(words, cur.String())
	}
	return words, nil
}
