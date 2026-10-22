package milestone

import (
	"regexp"
	"strings"
)

// titleSafe keeps a title from carrying an HTML comment delimiter, which could
// close the generated section early or open a marker of its own.
var titleSafe = strings.NewReplacer("<!--", "&lt;!--", "-->", "--&gt;")

// The generated child list of a tracker-backed milestone's tracking issue: the
// issues in its native milestone, as links. It sits between these markers in
// the body text, after the plan, so the plan and the list can each be rewritten
// without touching the other.
const (
	ChildrenOpen  = "<!-- rota:children -->"
	ChildrenClose = "<!-- rota:/children -->"
)

var (
	childrenRe = regexp.MustCompile(`(?s)\n*` + regexp.QuoteMeta(ChildrenOpen) + `.*?` + regexp.QuoteMeta(ChildrenClose) + `[ \t]*\n*`)
	childRefRe = regexp.MustCompile(`(?m)^- (\S+)`)
)

// ChildRef is one issue in a tracking issue's generated child list.
type ChildRef struct{ Ref, Title string }

// RenderChildren is the marked section for kids, one `- #N title` line each.
func RenderChildren(kids []ChildRef) string {
	lines := []string{ChildrenOpen, "## Child issues", "", "_Generated from the native milestone by rota; edits here are overwritten._", ""}
	if len(kids) == 0 {
		lines = append(lines, "_none_")
	}
	for _, k := range kids {
		lines = append(lines, "- "+k.Ref+" "+titleSafe.Replace(strings.Join(strings.Fields(k.Title), " ")))
	}
	return strings.Join(append(lines, ChildrenClose), "\n")
}

// SplitChildren cuts the generated section out of a body text: plan is the
// text without it, section the marked block. Without one ok is false.
func SplitChildren(text string) (plan, section string, ok bool) {
	loc := childrenRe.FindStringIndex(text)
	if loc == nil {
		return text, "", false
	}
	return text[:loc[0]] + text[loc[1]:], strings.Trim(text[loc[0]:loc[1]], "\n \t"), true
}

// JoinChildren puts section after plan; an empty section leaves plan as is.
func JoinChildren(plan, section string) string {
	if section == "" {
		return plan
	}
	return strings.TrimRight(plan, "\n") + "\n\n" + section
}

// ChildRefs lists the issue refs a section links, in order.
func ChildRefs(section string) []string {
	var refs []string
	for _, m := range childRefRe.FindAllStringSubmatch(section, -1) {
		refs = append(refs, m[1])
	}
	return refs
}
