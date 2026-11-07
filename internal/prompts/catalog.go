// Package prompts versions the configurable reporting sections of generated agents.
package prompts

// Section defines a builtin template and its fixed reporting contracts.
type Section struct{ Default, Status, Closing string }

// Catalog returns the supported reporting insertion points.
func Catalog() map[string]Section {
	return map[string]Section{
		"agents.explorer.report":    {"Report in at most 15 lines, one finding per line as `path:line: what`, then a {{status}}, {{closing}}", "`Not found:` line (`none` if everything was found)", "and end with one last-line token: `done.`, `too-big.` (the lookup is wider than one report) or `ambiguous.` (the brief admits several readings). A brief that names its own closing tokens overrides these."},
		"agents.implementer.report": {"Report in at most 15 lines: one `path: what changed` line per file, the check you ran and its result, a {{status}}, {{closing}}", "`Not done:` line (`none` if the brief is fully met)", "and end with one last-line token: `done.`, `too-big.` (the brief needs splitting) or `ambiguous.` (the brief admits several readings; nothing guessed)."},
		"agents.reasoner.report":    {"Report in at most 20 lines: assumptions, the alternatives in one line each, the recommendation, a {{status}}, {{closing}}", "`Not checked:` line for what you did not verify (`none` if nothing)", "and end with one last-line token: `done.`, `too-big.` (the question needs splitting) or `ambiguous.` (the question admits several readings)."},
	}
}

// Report expands a template using this section's fixed reporting contracts.
func (s Section) Report(template string) string { return expand(template, s) }
