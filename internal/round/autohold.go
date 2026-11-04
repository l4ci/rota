package round

import (
	"regexp"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
)

// childListRe matches a task-list line whose subject is an issue, "- [ ] #19"
// or "* [x] #20": an epic's child list. A plain "- #12" is not one, since
// Depends on and Related lists use that shape.
var childListRe = regexp.MustCompile(`(?m)^[ \t]*[-*][ \t]+\[[ xX]\][ \t]+#\d+\b`)

// autoHold says why the autopilot must not assign an item that a person may
// still assign by hand (#2570), or "" when it may. An epic or umbrella issue
// is a container for child issues, not one worker's task. A Major item is
// held until a design or plan note settles its shape.
func autoHold(be backlog.Backend, it backlog.Item) string {
	if why := epicOf(be, it); why != "" {
		return why
	}
	// An issue's tag is its size label, read with the configured prefix.
	if it.Tag != "Major" {
		return ""
	}
	if backlog.HasNote(be, it.ID) {
		return ""
	}
	return "a Major item with no design or plan note: brainstorm or plan it, or assign it by hand"
}

// epicOf names why an item reads as an epic or umbrella issue: an Epic title
// prefix, or a body listing two or more child issues.
func epicOf(be backlog.Backend, it backlog.Item) string {
	title := strings.ToLower(strings.TrimSpace(it.Title))
	for _, p := range []string{"epic:", "epic -", "[epic]"} {
		if strings.HasPrefix(title, p) {
			return "an epic (its title says so): assign its child issues instead"
		}
	}
	if body, _, err := be.Detail(it.ID); err == nil && len(childListRe.FindAllString(body, 2)) >= 2 {
		return "an umbrella issue listing child issues: assign the children instead"
	}
	return ""
}
