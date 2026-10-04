package knowledge

import (
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/section"
)

func (s Store) decisionsPath() string { return filepath.Join(s.Root, ".rota", "DECISIONS.md") }

// DecisionsQuery prints the requested "## Topic" sections of DECISIONS.md in
// document order. Topics matching no heading are returned in missing.
func (s Store) DecisionsQuery(topics []string) (text string, missing []string, err error) {
	content, err := ReadFile(s.decisionsPath())
	if err != nil {
		return "", nil, err
	}
	return queryTopics(content, topics)
}

func queryTopics(content string, topics []string) (string, []string, error) {
	wanted := map[string]bool{}
	for _, t := range topics {
		wanted[strings.ToLower(strings.TrimSpace(t))] = true
	}
	have := map[string]bool{}
	for _, t := range section.Topics(content) {
		have[strings.ToLower(t.Name)] = true
	}
	var missing []string
	seen := map[string]bool{}
	for _, t := range topics {
		k := strings.ToLower(strings.TrimSpace(t))
		if k != "" && !have[k] && !seen[k] {
			seen[k] = true
			missing = append(missing, t)
		}
	}
	return section.Matching(content, wanted), missing, nil
}
