// Package marker is the one place that writes and recognises the hidden
// `<!-- rota:... -->` line rota puts on every comment it posts, and parses each kind back. It depends only on the leaf
// pystr so any package can use it.
package marker

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/pystr"
)

// Prefix opens every marker rota writes. LegacyPrefix opens the ones hv wrote
// before the rename (#236): comments already on issues still carry it, so
// readers accept both.
const (
	Prefix       = "<!-- rota:"
	LegacyPrefix = "<!-- hv:"
)

// Line renders `<!-- rota:<kind>[ <arg>...] -->`.
func Line(kind string, args ...string) string {
	s := Prefix + kind
	for _, a := range args {
		s += " " + a
	}
	return s + " -->"
}

// Has reports whether body carries a rota marker, or a legacy hv one, so rota
// (or hv before it) posted it.
func Has(body string) bool {
	return strings.Contains(body, Prefix) || strings.Contains(body, LegacyPrefix)
}

// The marker kinds with a grammar beyond Line: durable notes, context
// comments, claims and releases, handoffs and the issue-body fields block.
// Each has a writer here and a parser here, so the two cannot drift, and every
// parser accepts the legacy hv prefix.
const (
	KindComment = "comment"
	KindClaim   = "claim"
	KindRelease = "release"
	KindHandoff = "handoff"
	KindFields  = "fields"
)

// opener matches `<!-- ` plus either prefix's name, up to the kind.
const opener = `\A<!-- (?:rota|hv):`

var (
	noteRe    = regexp.MustCompile(opener + `(proof|design|plan(?::S\p{Nd}+)?)(?: (\p{Nd}+)/(\p{Nd}+))? -->(?:\n|\z)`)
	commentRe = regexp.MustCompile(opener + `comment ([\p{L}\p{N}_]+) -->(?:\n|\z)`)
	claimRe   = regexp.MustCompile(opener + `(claim|release) ([^` + pystr.SpaceClass + `]+) -->`)
	fieldsRe  = regexp.MustCompile(`(?s)\n*<!-- (?:rota|hv):fields\n(.*?)\n?-->[ \t]*\n*\z`)
)

// NoteHeader is the first line of a durable-note comment: `<!-- rota:kind -->`
// for a note in one comment, `<!-- rota:kind i/n -->` for part i of n. It ends
// in a newline, ahead of the text.
func NoteHeader(kind string, i, n int) string {
	if n <= 1 {
		return Line(kind) + "\n"
	}
	return Line(kind, strconv.Itoa(i)+"/"+strconv.Itoa(n)) + "\n"
}

// Note is a parsed note comment. Kind is proof, design, plan or plan:S<n>.
// Part is the digits before the slash of an `i/n` header, "" for a note in one
// comment (the caller owns the integer conversion and its overflow). Rest is
// the text after the header line.
type Note struct{ Kind, Part, Rest string }

// ParseNote reads a note comment, which must start with its marker.
func ParseNote(body string) (Note, bool) {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	m := noteRe.FindStringSubmatchIndex(body)
	if m == nil {
		return Note{}, false
	}
	n := Note{Kind: body[m[2]:m[3]], Rest: body[m[1]:]}
	if m[4] >= 0 {
		n.Part = body[m[4]:m[5]]
	}
	return n, true
}

// CommentHeader is the first line of a context comment, ahead of the text.
func CommentHeader(kind string) string { return Line(KindComment, kind) + "\n" }

// ParseComment reads a context comment into its kind and the text after the
// header line.
func ParseComment(body string) (kind, rest string, ok bool) {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	m := commentRe.FindStringSubmatchIndex(body)
	if m == nil {
		return "", "", false
	}
	return body[m[2]:m[3]], body[m[1]:], true
}

// Claim and Release render the comments that take and give back a claim.
func Claim(id string) string   { return Line(KindClaim, id) }
func Release(id string) string { return Line(KindRelease, id) }

// ParseClaim reads a claim or release comment: verb is KindClaim or
// KindRelease.
func ParseClaim(body string) (verb, id string, ok bool) {
	m := claimRe.FindStringSubmatch(strings.ReplaceAll(body, "\r\n", "\n"))
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// Handoff renders the marker that ends a handoff comment: who handed over, in
// which round.
func Handoff(from string, round int) string {
	return Line(KindHandoff, from+"@"+strconv.Itoa(round))
}

// HasHandoff reports whether body carries a handoff marker.
func HasHandoff(body string) bool {
	return strings.Contains(body, Prefix+KindHandoff+" ") || strings.Contains(body, LegacyPrefix+KindHandoff+" ")
}

// FieldsOpen opens the fields block that ends an issue body, which carries one
// `Name: value` per line and closes with `-->`.
const FieldsOpen = Prefix + KindFields

// FieldsBlock renders the block around the already-joined lines.
func FieldsBlock(lines []string) string {
	return FieldsOpen + "\n" + strings.Join(lines, "\n") + "\n-->"
}

// SplitFields cuts the trailing fields block off a body (LF line endings): text
// is what precedes it, lines the block's inner text. Without a block ok is
// false.
func SplitFields(body string) (text, lines string, ok bool) {
	loc := fieldsRe.FindStringSubmatchIndex(body)
	if loc == nil {
		return body, "", false
	}
	return body[:loc[0]], body[loc[2]:loc[3]], true
}
