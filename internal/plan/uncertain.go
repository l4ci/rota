package plan

import (
	"errors"
	"os"
	"regexp"
	"strings"

	"github.com/l4ci/rota/internal/artifact"
	"github.com/l4ci/rota/internal/backlog"
)

var (
	markerRes = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bTBD\b`),
		regexp.MustCompile(`(?i)\bunclear\b`),
		regexp.MustCompile(`(?i)\bunsure\b`),
		regexp.MustCompile(`(?i)\bopen questions?\b`),
		regexp.MustCompile(`(?i)\bheuristic TBD\b`),
	}
	codeSpanRe = regexp.MustCompile("`[^`]+`")
)

// Uncertain ports hv-uncertain for the file backend: whether an open item
// warrants a peek before planning. Only Major items can be uncertain; the
// reasons are "no detail file", "multiple open-question signals" and "no
// concrete identifiers (unknown surface)".
func Uncertain(root, id string) (typ string, reasons []string, err error) {
	f := &backlog.File{Root: root}
	md, merr := f.Markdown(0)
	if merr != nil {
		if errors.Is(merr, backlog.ErrNotFound) {
			return "", nil, artifact.Errf(artifact.ExitResolution, ".rota/BACKLOG.md not found")
		}
		return "", nil, merr
	}
	// ROTA_OPEN_SECTIONS ("Bugs|Features|Tasks", as hv-types.sh exports it)
	// limits which open sections the item may live in.
	active := map[string]bool{}
	sections := os.Getenv("ROTA_OPEN_SECTIONS")
	if sections == "" {
		sections = "Bugs|Features|Tasks"
	}
	for _, s := range strings.Split(sections, "|") {
		if s = strings.TrimSpace(s); s != "" {
			active[s] = true
		}
	}
	var line string
	for _, e := range backlog.OpenBullets(md) {
		if e.ID == id && active[e.Section] {
			line = e.Line
			break
		}
	}
	if line == "" {
		return "", nil, artifact.Errf(artifact.ExitResolution, "item %s not found in BACKLOG.md", id)
	}
	typ = id[:1]
	reasons = []string{}
	if b, ok := backlog.ParseOpen(line); !ok || !strings.EqualFold(b.Tag, "major") {
		return typ, reasons, nil
	}
	detail, has, derr := f.Detail(id)
	if derr != nil {
		return "", nil, derr
	}
	return typ, uncertainReasons(line, detail, has), nil
}

// uncertainReasons applies the three gates to a Major item's bullet and its
// detail text (has is false when there is none).
func uncertainReasons(line, detail string, has bool) []string {
	reasons := []string{}
	body := line
	if has {
		body += "\n" + detail
	} else {
		reasons = append(reasons, "no detail file")
	}
	marker := false
	for _, re := range markerRes {
		if re.MatchString(body) {
			marker = true
			break
		}
	}
	if strings.Count(body, "?") >= 2 || marker {
		reasons = append(reasons, "multiple open-question signals")
	}
	if !codeSpanRe.MatchString(body) {
		reasons = append(reasons, "no concrete identifiers (unknown surface)")
	}
	return reasons
}
