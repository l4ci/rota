package backlog

import (
	"github.com/l4ci/rota/internal/itemid"
	"regexp"

	"github.com/l4ci/rota/internal/pystr"
)

// The locator: the one place an item ref becomes a BACKLOG.md bullet match.
// Every file-backend site that finds a bullet by ref builds its pattern here,
// so the ref is quoted once and the anchors (open, struck-through, either) are
// named instead of retyped.

// boldTag is the quoted `**[ref]` opener shared by every bullet shape.
func boldTag(ref string) string { return `\*\*\[` + regexp.QuoteMeta(ref) + `\]` }

// activeHead matches the start of an open bullet of ref (`- **[ref]`).
func activeHead(ref string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^- ` + boldTag(ref))
}

// activeLine matches the whole open bullet line of ref.
func activeLine(ref string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^- ` + boldTag(ref) + `.*$`)
}

// doneLine matches the whole struck-through bullet line of ref (`- ~~**[ref]`).
func doneLine(ref string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^- ~~` + boldTag(ref) + `.*$`)
}

// originLine matches the whole line that introduces ref, open or struck.
func originLine(ref string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^- (?:~~)?` + boldTag(ref) + `.*$`)
}

// originStart matches a single stripped line that begins with ref's bullet,
// open or struck; unlike originLine it is anchored to the string, not a line.
func originStart(ref string) *regexp.Regexp {
	return regexp.MustCompile(`\A- (?:~~)?` + boldTag(ref))
}

// completedMark matches ref's completed bullet through its Done date, the
// proof that a Complete already ran.
func completedMark(ref string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^- ~~` + boldTag(ref) + `.*~~[` + pystr.SpaceClass + `]+Done[` +
		pystr.SpaceClass + `]+\p{Nd}{4}-\p{Nd}{2}-\p{Nd}{2}`)
}

// boldRef matches the bold `**[ref] ... **` span of ref's bullet anywhere in md.
func boldRef(ref string) *regexp.Regexp {
	return regexp.MustCompile(boldTag(ref) + `[^*]*\*\*`)
}

// titleAfterRef matches `[ref]`, an optional `[tag]`, then the title up to its
// first period, capturing the title.
func titleAfterRef(ref string) *regexp.Regexp {
	return regexp.MustCompile(`\[` + regexp.QuoteMeta(ref) + `\](?:` + sp + `+\[[^\]]+\])?` + sp + `+([^.\n]+)\.`)
}

// The line grammars: whole-line parsers for the open, done and archive bullet
// shapes. They are not keyed by ref; the per-ref matchers above are.

const sp = "[" + pystr.SpaceClass + "]"

// OpenRe is open_bullet_re(): `- **[B07] [P1] Title.** rest`, anchored at the
// start. \d is \p{Nd} to match Python's Unicode digits.
var OpenRe = regexp.MustCompile(`(?m)\A- \*\*\[(` + itemid.IDPattern(1) + `)\](?:` + sp + `+\[([^\]]+)\])?` + sp + `+([^*]+?)\*\*(.*)$`)

// doneRe is _DONE_LINE_RE.
var doneRe = regexp.MustCompile("(?m)\\A- ~~(.+?)~~ Done (\\p{Nd}{4}-\\p{Nd}{2}-\\p{Nd}{2}) \\[`([^`]+)`\\]" +
	`(?: \((handed-off|blocked|dropped)(?:: (.*))?\))?` + sp + `*$`)

var doneIDRe = regexp.MustCompile(`\A\*\*\[(` + itemid.AnyTypeIDPattern + `)\]`)

// archiveBulletRe matches the start of an open or struck bullet of any type,
// capturing its ID.
var archiveBulletRe = regexp.MustCompile(`\A- (?:~~)?\*\*\[(` + itemid.AnyTypeIDPattern + `)\]`)
