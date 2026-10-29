// Package marker is the one place that writes and recognises the hidden
// `<!-- rota:... -->` line rota puts on every comment it posts, and parses each kind back. It depends only on the leaf
// pystr so any package can use it.
package marker

import (
	"regexp"
	"slices"
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
	// KindHandRelease rides on a release comment a person posted on purpose
	// (`rota item release`), so reconcile does not post the claim again.
	KindHandRelease = "hand-release"
	KindHandoff     = "handoff"
	KindFields      = "fields"
	// KindBounce is one bounce counted on an issue, or the reset that zeroes
	// the count (#1598). KindReviewBounce is a bounce posted on the PR for the
	// machine whose worker holds it.
	KindBounce       = "bounce"
	KindReviewBounce = "review-bounce"
	// KindRelay is an orchestrator relay carried to the issue thread (#1827);
	// KindSupportOffer is the comment `round support offer` leaves.
	KindRelay        = "relay"
	KindSupportOffer = "support-offer"
	// KindPRPointer is the issue comment that points at a PR post (#1827);
	// KindFrom stamps the posting machine's role on a comment.
	KindPRPointer     = "pr-pointer"
	KindFrom          = "from"
	KindSupportReturn = "support-return"
	// KindReviewMinted is the comment main leaves on an architecture-review
	// item it minted, so a support machine can tell it from a lookalike title.
	KindReviewMinted = "review-minted"
	// KindReport identifies a rendered verdict or gate report, keyed by kind and SHA.
	KindReport = "report"
)

// Report is the identity of one rendered report on a PR or item.
func Report(kind, sha string) string { return Line(KindReport, kind, "sha="+sha) }

// opener matches `<!-- ` plus either prefix's name, up to the kind.
const opener = `\A<!-- (?:rota|hv):`

var (
	noteRe    = regexp.MustCompile(opener + `(proof|design|acceptance|plan(?::S\p{Nd}+)?)(?: (\p{Nd}+)/(\p{Nd}+))? -->(?:\n|\z)`)
	commentRe = regexp.MustCompile(opener + `comment ([\p{L}\p{N}_]+) -->(?:\n|\z)`)
	claimRe   = regexp.MustCompile(opener + `(claim|release) ([^` + pystr.SpaceClass + `]+) -->`)
	bounceRe  = regexp.MustCompile(opener + `(bounce|review-bounce)(?: (reset|head=[0-9a-fA-F]*))? -->`)
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

// Note is a parsed note comment. Kind is proof, design, acceptance, plan or plan:S<n>.
// Part is the digits before the slash of an `i/n` header, "" for a note in one
// comment (the caller owns the integer conversion and its overflow). Rest is
// the text after the header line.
type Note struct{ Kind, Part, Rest string }

// ParseNote reads a note comment, which must start with its marker.
func ParseNote(body string) (Note, bool) {
	body = pystr.UniversalNewlines(body)
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
	body = pystr.UniversalNewlines(body)
	m := commentRe.FindStringSubmatchIndex(body)
	if m == nil {
		return "", "", false
	}
	return body[m[2]:m[3]], body[m[1]:], true
}

// Claim and Release render the comments that take and give back a claim.
func Claim(id string) string   { return Line(KindClaim, id) }
func Release(id string) string { return Line(KindRelease, id) }

// HandRelease renders the release of claim id that a person chose to make.
func HandRelease(id string) string { return Release(id) + "\n" + Line(KindHandRelease, id) }

// IsHandRelease reports whether body is a release of claim id that carries the
// hand-release marker.
func IsHandRelease(body, id string) bool {
	return strings.Contains(pystr.UniversalNewlines(body), Line(KindHandRelease, id))
}

// ParseClaim reads a claim or release comment: verb is KindClaim or
// KindRelease.
func ParseClaim(body string) (verb, id string, ok bool) {
	m := claimRe.FindStringSubmatch(pystr.UniversalNewlines(body))
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

// Bounce renders the marker of one bounce at PR head (head "" when the head is
// not known); BounceReset the one that zeroes an issue's count; ReviewBounce
// the marker of a bounce posted on the PR itself.
func Bounce(head string) string       { return Line(KindBounce, "head="+head) }
func BounceReset() string             { return Line(KindBounce, "reset") }
func ReviewBounce(head string) string { return Line(KindReviewBounce, "head="+head) }

// ParseBounce reads a comment that opens with a bounce or review-bounce marker: kind is
// KindBounce or KindReviewBounce, reset reports a reset, head the sha after
// head= ("" for a reset or an unknown head).
func ParseBounce(body string) (kind, head string, reset, ok bool) {
	m := bounceRe.FindStringSubmatch(pystr.UniversalNewlines(body))
	if m == nil {
		return "", "", false, false
	}
	if m[2] == "reset" {
		return m[1], "", true, true
	}
	return m[1], strings.TrimPrefix(m[2], "head="), false, true
}

var (
	// relayRe finds the relay marker on the first or second line: the tracker
	// puts its own `<!-- rota:comment <kind> -->` line ahead of the text.
	relayRe        = regexp.MustCompile(`\A(?:<!-- (?:rota|hv):comment [\p{L}\p{N}_]+ -->\n)?<!-- (?:rota|hv):relay round=(\p{Nd}+) -->(?:\n|\z)`)
	reviewMintedRe = regexp.MustCompile(`\A(?:<!-- (?:rota|hv):comment [\p{L}\p{N}_]+ -->\n)?<!-- (?:rota|hv):review-minted round=\p{Nd}+ -->(?:\n|\z)`)
	offerRe        = regexp.MustCompile(`\A(?:<!-- (?:rota|hv):comment [\p{L}\p{N}_]+ -->\n)?<!-- (?:rota|hv):support-offer -->(?:\n|\z)`)
)

// RelayLine renders `<!-- rota:relay round=<n> -->`.
func RelayLine(round int) string { return Line(KindRelay, "round="+strconv.Itoa(round)) }

// SupportOfferLine renders `<!-- rota:support-offer -->`.
func SupportOfferLine() string { return Line(KindSupportOffer) }

// ParseRelay reads a relay comment: its round and the relayed text after the
// marker line.
func ParseRelay(body string) (round int, text string, ok bool) {
	body = pystr.UniversalNewlines(body)
	m := relayRe.FindStringSubmatchIndex(body)
	if m == nil {
		return 0, "", false
	}
	n, err := strconv.Atoi(body[m[2]:m[3]])
	if err != nil {
		return 0, "", false
	}
	return n, body[m[1]:], true
}

// IsSupportOffer reports whether body is a support-offer comment.
func IsSupportOffer(body string) bool {
	return offerRe.MatchString(pystr.UniversalNewlines(body))
}

// ReviewMintedLine renders `<!-- rota:review-minted round=<n> -->`.
func ReviewMintedLine(round int) string {
	return Line(KindReviewMinted, "round="+strconv.Itoa(round))
}

// IsReviewMinted reports whether body is a review-minted comment.
func IsReviewMinted(body string) bool {
	return reviewMintedRe.MatchString(pystr.UniversalNewlines(body))
}

// PointerLine renders `<!-- rota:pr-pointer <kind> -->`.
func PointerLine(kind string) string { return Line(KindPRPointer, kind) }

// FromLine renders `<!-- rota:from <role> -->`, the machine role (main or
// support) that posted the comment it sits in.
func FromLine(role string) string { return Line(KindFrom, role) }

var fromRe = regexp.MustCompile(`(?m)^<!-- (?:rota|hv):from (main|support) -->[ \t]*$`)

// SideOf says which machine posted a comment, "main", "support" or "" when
// the body does not say. The stamp is the `rota:from` line; the comments only
// one machine posts name it by kind: the support-offer comment is main's,
// the support-return and relay comments the support machine's.
func SideOf(body string) string {
	body = pystr.UniversalNewlines(body)
	if m := fromRe.FindStringSubmatch(body); m != nil {
		return m[1]
	}
	switch {
	case IsSupportOffer(body):
		return "main"
	case supportReturnRe.MatchString(body):
		return "support"
	}
	if _, _, ok := ParseRelay(body); ok {
		return "support"
	}
	return ""
}

var supportReturnRe = regexp.MustCompile(`\A(?:<!-- (?:rota|hv):comment [\p{L}\p{N}_]+ -->\n)?<!-- (?:rota|hv):support-return -->(?:\n|\z)`)

// OpenClaims returns claim ids with no later release, in claim order.
// Repeated claims keep their place; a released id can claim again at the end.
func OpenClaims(bodies []string) []string {
	var held []string
	for _, body := range bodies {
		verb, id, ok := ParseClaim(body)
		if !ok {
			continue
		}
		if verb == KindClaim {
			if !slices.Contains(held, id) {
				held = append(held, id)
			}
		} else if i := slices.Index(held, id); i >= 0 {
			held = append(held[:i], held[i+1:]...)
		}
	}
	return held
}
