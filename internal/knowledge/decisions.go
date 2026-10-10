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
	for _, t := range section.Topics(content) {
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
	return section.Matching(content, wanted), missing, nil
}

// DecisionsStats counts bullets and bytes per topic of DECISIONS.md, the list
// a reader screen offers before a topic is opened. A missing file has none.
func (s Store) DecisionsStats() ([]Stat, error) {
	content, err := ReadFile(s.decisionsPath())
	if err != nil {
		return nil, err
	}
	return topicStats(ActiveDecisions(content)), nil
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
	lines := strings.SplitAfter(content, "\n")
	var out []string
	var topic []string // lines of the current "## " topic, heading included
	var entry []string // lines of the current "### " entry
	entries, kept := 0, 0
	inFence := false
	flushEntry := func() {
		if entry == nil {
			return
		}
		entries++
		if !retiredStatus.MatchString(strings.Join(entry, "")) {
			kept++
			topic = append(topic, entry...)
		}
		entry = nil
	}
	flushTopic := func() {
		flushEntry()
		if topic != nil && (entries == 0 || kept > 0) {
			out = append(out, topic...)
		}
		topic, entries, kept = nil, 0, 0
	}
	for _, l := range lines {
		if strings.HasPrefix(l, "```") {
			inFence = !inFence
		}
		switch {
		case !inFence && strings.HasPrefix(l, "## "):
			flushTopic()
			topic = []string{l}
		case topic == nil:
			out = append(out, l)
		case !inFence && strings.HasPrefix(l, "### "):
			flushEntry()
			entry = []string{l}
		case entry != nil:
			entry = append(entry, l)
		default:
			topic = append(topic, l)
		}
	}
	flushTopic()
	return strings.Join(out, "")
}
