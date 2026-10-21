package migrate

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	ms "github.com/l4ci/rota/internal/milestone"
	"github.com/l4ci/rota/internal/rotatree"
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

// StaleMilestoneFiles names the *.md files under .rota/milestones, sorted. The
// caller decides they are stale: no verb reads them while milestones live on
// the tracker.
func StaleMilestoneFiles(root string) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(rotatree.File(root, rotatree.MilestonesDir), "*.md"))
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, f := range files {
		names = append(names, filepath.Base(f))
	}
	sort.Strings(names)
	return names, nil
}

// ArchiveMilestoneFiles moves every stale milestone file to
// .rota/archive/milestones and returns the names. Without apply it only
// reports them. A second run finds nothing. A name already present in the
// archive is refused, preview included, before any file moves: a rename would
// silently replace the archived copy.
func ArchiveMilestoneFiles(root string, apply bool) ([]string, error) {
	names, err := StaleMilestoneFiles(root)
	if err != nil || len(names) == 0 {
		return names, err
	}
	dest := rotatree.File(root, "archive", rotatree.MilestonesDir)
	for _, n := range names {
		if _, err := os.Lstat(filepath.Join(dest, n)); err == nil {
			return nil, fmt.Errorf("archive already holds %s: move or remove one of the two copies, then rerun", filepath.Join(dest, n))
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	if !apply {
		return names, nil
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, err
	}
	for _, n := range names {
		if err := os.Rename(filepath.Join(rotatree.File(root, rotatree.MilestonesDir), n), filepath.Join(dest, n)); err != nil {
			return nil, err
		}
	}
	return names, nil
}
