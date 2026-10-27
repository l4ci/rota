package labels

import (
	"fmt"
	"strings"
)

//go:generate go run ./genref ../../docs/reference/labels.md

// ReferencePage renders effective registry entries. The checked-in page uses
// defaults; genref --config renders a project's overrides and declarations.
func ReferencePage(entries []Entry) string {
	var b strings.Builder
	b.WriteString(`# Tracker labels

Generated from ` + "`internal/labels`" + `; do not edit this page by hand. Regenerate the defaults with ` + "`go generate ./internal/labels`" + `.

` + "`rota labels sync`" + ` compares the effective registry and project declarations with the tracker. It previews missing definitions, wrong descriptions or colors, overlapping tracker defaults, unused claims, and unknown labels. ` + "`--apply`" + ` creates missing definitions and repairs metadata on GitHub, GitLab and Linear. It never deletes or renames a label. Jira and It's a Plan report names only; their sync makes no writes.

Open prefixes below describe existing definitions only. Sync does not create a literal prefix label. A claim used by no open issue is reported and left unchanged. Retired labels (including ` + "`site:support`" + `) are unknown and left unchanged, as are overlapping tracker defaults (` + "`bug`, `enhancement`, `documentation`" + `).

` + "`issues.labels.*`" + ` overrides the configured names and prefixes. Declare project labels in ` + "`issues.labels.extra`" + ` as a JSON object of name to description; exact registry entries retain their own metadata. Labels read through ` + "`review.crossLabels`, `review.lenses`, or `ship.review.labels`" + ` also count as known. The registry documents who applies and reads each definition and when it leaves an issue; sync never removes definitions.

For a project-specific page, run ` + "`go run ./internal/labels/genref --config .rota/config.json <output.md>`" + `. This uses the same resolved entries as sync, including custom names and project declarations.

Colors are six-digit RGB: blue for milestones and sizes, purple for routing and review, red for defects and blocked work, orange for priorities and review changes, yellow for round decisions, green for released work, and pale blue for project labels and tasks. Existing colors change only with ` + "`--apply`" + `.

| Name or prefix | Description | Color | Group | Config | Sets | Reads | Removed from issue |
|---|---|---|---|---|---|---|---|
`)
	cell := func(s string) string { return strings.ReplaceAll(s, "|", `\|`) }
	for _, e := range entries {
		name := e.Name
		if e.Prefix {
			name += "*"
		}
		key := e.ConfigKey
		if key != "" {
			key = "`" + key + "`"
		}
		fmt.Fprintf(&b, "| `%s` | %s | `%s` | %s | %s | %s | %s | %s |\n", cell(name), cell(e.Description), e.Color, e.Group, key, cell(e.Sets), cell(e.Reads), cell(e.Removed))
	}
	return b.String()
}
