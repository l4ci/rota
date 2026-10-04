package release

import (
	"errors"
	"regexp"
	"strings"

	"github.com/l4ci/rota/internal/pystr"
)

// ErrSectionExists: the changelog already has a section for the version.
var ErrSectionExists = errors.New("changelog already has a section for this version")

var (
	h1Re    = regexp.MustCompile(`^#\s+\S`)
	h1Line  = regexp.MustCompile(`(?m)^#\s+\S`)
	blankRe = regexp.MustCompile(`(?m)^[ \t]*\n`)
)

// UpdateChangelog is the body of hv-release-update-changelog: it returns the
// changelog text with a `## v<version> — <today>` section for notes added.
// exists is false for a missing file, which gets a fresh `# Changelog`.
func UpdateChangelog(content string, exists bool, version, today, notes string) (string, error) {
	section := "## v" + version + " — " + today + "\n\n" + pystr.Rstrip(notes) + "\n\n"
	if !exists {
		return "# Changelog\n\n" + section, nil
	}
	if regexp.MustCompile(`(?m)^## v` + regexp.QuoteMeta(version) + `\b`).MatchString(content) {
		return "", ErrSectionExists
	}
	stripped := strings.TrimLeft(content, "\n")
	if !h1Re.MatchString(stripped) {
		return section + content, nil
	}
	start := h1Line.FindStringIndex(content)[0]
	nl := strings.IndexByte(content[start:], '\n')
	if nl < 0 { // an H1 on the last line, no newline after it
		return content + "\n\n" + section, nil
	}
	lineEnd := start + nl + 1
	if b := blankRe.FindStringIndex(content[lineEnd:]); b != nil {
		pos := lineEnd + b[1]
		return content[:pos] + section + content[pos:], nil
	}
	return content[:lineEnd] + "\n" + section + content[lineEnd:], nil
}
