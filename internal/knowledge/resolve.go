package knowledge

import "github.com/l4ci/rota/internal/topicmatch"

// resolveTopic maps one requested topic to the headings it names; the rule and
// why it differs from the external store's live in topicmatch.Headings.
func resolveTopic(want string, names []string) []string {
	return topicmatch.Headings(want, names)
}
