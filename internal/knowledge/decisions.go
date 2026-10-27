package knowledge

import (
	"regexp"
	"strings"

	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/section"
)

func (s Store) decisionsPath() string { return rotatree.Decisions(s.Root) }

// DecisionsQuery prints the requested "## Topic" sections of DECISIONS.md in
// document order. Topics matching no heading are returned in missing.
func (s Store) DecisionsQuery(topics []string) (text string, missing []string, err error) {
	content, err := ReadFile(s.decisionsPath())
	if err != nil {
		return "", nil, err
	}
	return queryTopics(ActiveDecisions(content), topics)
}

func queryTopics(content string, topics []string) (string, []string, error) {
	var names []string
	for _, t := range decisionSections(content) {
		names = append(names, t.Name)
	}
	wanted := map[string]bool{}
	var missing []string
	seen := map[string]bool{}
	for _, t := range topics {
		hit := resolveTopic(t, names)
		for _, n := range hit {
			wanted[n] = true
		}
		k := strings.ToLower(strings.TrimSpace(t))
		if k != "" && len(hit) == 0 && !seen[k] {
			seen[k] = true
			missing = append(missing, t)
		}
	}
	var matched []string
	for _, t := range decisionSections(content) {
		if wanted[strings.ToLower(t.Name)] {
			matched = append(matched, "## "+t.Name+"\n"+strings.TrimRight(t.Body, " \t\n\r\f\v")+"\n")
		}
	}
	return strings.Join(matched, "\n"), missing, nil
}

// DecisionsStats counts bullets and bytes per topic of DECISIONS.md, the list
// a reader screen offers before a topic is opened. A missing file has none.
func (s Store) DecisionsStats() ([]Stat, error) {
	content, err := ReadFile(s.decisionsPath())
	if err != nil {
		return nil, err
	}
	var stats = []Stat{}
	for _, t := range decisionSections(ActiveDecisions(content)) {
		stats = append(stats, Stat{t.Name, len(bulletLine.FindAllStringIndex(t.Body, -1)), len(t.Body)})
	}
	return stats, nil
}

// retiredStatus marks a decision entry lifted by /rota-decide --retire or
// --supersede: a line "*Status.* Retired ..." or "*Status.* Superseded ...".
var retiredStatus = regexp.MustCompile(`(?m)^\*Status\.\* (Retired|Superseded)\b`)

// ActiveDecisions drops retired and superseded "### " entries from DECISIONS.md
// content, and any topic left with no entry at all, so readers and the index
// block never see a lifted boundary. Content with no retired entry is returned
// untouched.
func ActiveDecisions(content string) string {
	if !retiredStatus.MatchString(content) {
		return content
	}
	topics := decisionTopics(content)
	if len(topics) == 0 {
		return content
	}
	var out strings.Builder
	out.WriteString(content[:topics[0].start])
	for _, topic := range topics {
		if len(topic.entries) == 0 {
			out.WriteString(content[topic.start:topic.end])
			continue
		}
		var kept strings.Builder
		for _, entry := range topic.entries {
			raw := content[entry.start:entry.end]
			status := ParseDecision(raw).Status
			if !retiredStatus.MatchString("*Status.* " + status) {
				kept.WriteString(raw)
			}
		}
		if kept.Len() > 0 {
			out.WriteString(content[topic.start:topic.entries[0].start])
			out.WriteString(kept.String())
		}
	}
	return out.String()
}

// CheckDecisions validates the umbrella file; a missing file has no entries.
func (s Store) CheckDecisions() (DecisionCheck, error) {
	content, err := ReadFile(s.decisionsPath())
	if err != nil {
		return DecisionCheck{}, err
	}
	return CheckDecisions(content), nil
}

// decisionSections gives readers the same fence-aware topic boundaries as the
// validator. Body includes the heading's newline, matching section.Topics.
func decisionSections(content string) []section.Topic {
	var out []section.Topic
	for _, topic := range decisionTopics(content) {
		start := topic.body
		if start > topic.start && content[start-1] == '\n' {
			start--
			if start > topic.start && content[start-1] == '\r' {
				start--
			}
		}
		out = append(out, section.Topic{Name: topic.name, Body: content[start:topic.end]})
	}
	return out
}
