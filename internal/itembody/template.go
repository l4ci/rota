package itembody

import (
	"regexp"
	"strings"
)

// Template levels describe presence checks only, never content quality.
const (
	Required    = "required"
	Recommended = "recommended"
	Optional    = "optional"
)

// AcceptanceHeadRe is also the heading grammar of the criteria readiness check.
var AcceptanceHeadRe = regexp.MustCompile(`(?mi)^#{1,6}[ \t]+.*acceptance`)

// TemplateSection is one section in the per-kind item template.
type TemplateSection struct {
	Name, Level string
	head        *regexp.Regexp
	prose       *regexp.Regexp
}

func templateSection(name, level string) TemplateSection {
	return TemplateSection{Name: name, Level: level,
		head: regexp.MustCompile(`(?mi)^#{1,6}[ \t]+` + strings.ReplaceAll(regexp.QuoteMeta(name), " ", `[ \t]+`) + `[ \t]*$`)}
}

var templateSections = map[string][]TemplateSection{
	"features": {
		templateSection("Summary", Optional),
		{Name: "Acceptance", Level: Required, head: AcceptanceHeadRe},
		templateSection("Out of scope", Required),
		templateSection("Touches", Optional),
		templateSection("Pointers", Recommended),
		templateSection("Detail", Optional),
	},
	"bugs": {
		templateSection("Summary", Optional),
		{Name: "Repro", Level: Recommended, head: regexp.MustCompile(`(?mi)^#{1,6}[ \t]+repro[ \t]*$`), prose: regexp.MustCompile(`(?i)\brepro(?:duce|duction)?\b`)},
		{Name: "Expected", Level: Recommended, head: regexp.MustCompile(`(?mi)^#{1,6}[ \t]+expected[ \t]*$`), prose: regexp.MustCompile(`(?i)\bexpected\b`)},
		{Name: "Actual", Level: Recommended, head: regexp.MustCompile(`(?mi)^#{1,6}[ \t]+actual[ \t]*$`), prose: regexp.MustCompile(`(?i)\bactual\b`)},
		templateSection("Acceptance", Optional),
		templateSection("Out of scope", Optional),
		templateSection("Touches", Optional),
		templateSection("Pointers", Recommended),
		templateSection("Detail", Optional),
	},
	"tasks": {
		templateSection("Summary", Optional),
		templateSection("Acceptance", Optional),
		templateSection("Out of scope", Optional),
		templateSection("Touches", Optional),
		templateSection("Pointers", Recommended),
		templateSection("Detail", Optional),
	},
}

// Template returns an independent copy of the kind's section table.
// Pointers is recommended only when file paths are named in the body.
func Template(kind, tag string) []TemplateSection {
	sections := append([]TemplateSection(nil), templateSections[kind]...)
	if kind == "features" && tag == "Major" {
		for i := range sections {
			if sections[i].Name == "Touches" {
				sections[i].Level = Recommended
			}
		}
	}
	return sections
}

var (
	urlMentionRe  = regexp.MustCompile(`(?i)https?://\S+`)
	fileMentionRe = regexp.MustCompile(`(?:[A-Za-z0-9_.-]+/)+[A-Za-z0-9_.*-]+\.[A-Za-z0-9_*.-]+|\b[A-Za-z0-9_-]+\.(?:[A-Za-z]{2,10}|[chrs])\b|\b(?:Makefile|Dockerfile|README|LICENSE|VERSION)\b`)
)

// CheckTemplate lists each missing required or recommended section. Bugs may
// describe repro/expected/actual in the description before the first heading.
func CheckTemplate(kind, tag, body string) []TemplateSection {
	description := body
	if loc := anyHeadRe.FindStringIndex(body); loc != nil {
		description = body[:loc[0]]
	}
	var missing []TemplateSection
	for _, section := range Template(kind, tag) {
		if section.Level == Optional || (section.Name == "Pointers" && !fileMentionRe.MatchString(urlMentionRe.ReplaceAllString(body, ""))) {
			continue
		}
		if _, ok := Section(body, section.head); ok {
			continue
		}
		if section.prose != nil && section.prose.MatchString(description) {
			continue
		}
		missing = append(missing, section)
	}
	return missing
}
