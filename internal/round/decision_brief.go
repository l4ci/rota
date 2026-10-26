package round

import (
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/knowledge"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/section"
)

// decisionBriefContext captures the project's boundaries once per assignment.
// Recovery reuses this text rather than silently changing the worker's brief.
func decisionBriefContext(root string, topics []string) (string, error) {
	content, err := knowledge.ReadFile(rotatree.Decisions(root))
	if err != nil {
		return "", err
	}
	var names []string
	for _, topic := range section.Topics(knowledge.ActiveDecisions(content)) {
		if strings.TrimSpace(topic.Body) != "" {
			names = append(names, topic.Name)
		}
	}
	if len(names) == 0 {
		return "", nil
	}
	if len(topics) > 0 {
		text, missing, err := (knowledge.Store{Root: root}).DecisionsQuery(topics)
		if err != nil {
			return "", err
		}
		if len(missing) > 0 {
			return "", usage("unknown decision topics: %s", strings.Join(missing, ", "))
		}
		return "Binding decisions from DECISIONS.md (consult before implementing; escalate any conflict with the issue):\n\n" + strings.TrimSpace(text), nil
	}
	return fmt.Sprintf("DECISIONS.md topics: %s. Before implementing, run `rota decisions query <topic>...` for the ones your issue touches; escalate if the issue conflicts with a decision.", strings.Join(names, ", ")), nil
}
