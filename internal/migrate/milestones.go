package migrate

import (
	"regexp"
	"strings"

	ms "github.com/l4ci/rota/internal/milestone"
)

var (
	oldAcceptanceHeading = regexp.MustCompile(`(?mi)^## Acceptance criteria[ \t]*$`)
	acceptanceHeading    = regexp.MustCompile(`^## Acceptance[ \t]*$`)
	bareBullet           = regexp.MustCompile(`^([-*] )(\[[ xX]\]|)`)
)

// MigrateMilestoneBody renames a "## Acceptance criteria" heading to
// "## Acceptance" and gives every bare top-level bullet inside that section a
// "[ ] " checkbox, so the acceptance package can number the criteria. An
// indented sub-bullet is detail under a criterion and stays as it is. Nothing else
// is touched, and the result is a fixed point: a second pass reports no
// change.
func MigrateMilestoneBody(text string) (string, bool) {
	out := oldAcceptanceHeading.ReplaceAllString(text, "## Acceptance")
	lines := strings.Split(out, "\n")
	in := false
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "## "):
			in = acceptanceHeading.MatchString(l)
		case in:
			if m := bareBullet.FindStringSubmatch(l); m != nil && m[2] == "" && !strings.HasPrefix(l[len(m[1]):], "[") {
				lines[i] = m[1] + "[ ] " + l[len(m[1]):]
			}
		}
	}
	out = strings.Join(lines, "\n")
	return out, out != text
}

// MilestoneChange is one milestone's outcome in RunMilestones.
type MilestoneChange struct {
	ID      string
	Changed bool
}

// RunMilestones rewrites every milestone body in st through
// MigrateMilestoneBody. Without apply it only reports what would change.
func RunMilestones(st ms.Store, apply bool) ([]MilestoneChange, error) {
	list, err := st.List()
	if err != nil {
		return nil, err
	}
	res := []MilestoneChange{}
	for _, e := range list {
		text, err := st.Show(e.ID)
		if err != nil {
			return nil, err
		}
		next, changed := MigrateMilestoneBody(text)
		if changed && apply {
			if _, err := st.Put(e.ID, next); err != nil {
				return nil, err
			}
		}
		res = append(res, MilestoneChange{ID: e.ID, Changed: changed})
	}
	return res, nil
}
