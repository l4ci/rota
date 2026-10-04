package knowledge

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
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

// AutoLog appends an [Auto:Loop] entry under "## topic" of DECISIONS.md with
// placeholder Forbids/Permits and a provenance footer, creating the topic when
// it is missing. A second call with the same topic and title changes nothing.
func (s Store) AutoLog(topic, title, why, planKey, date string) (changed bool, err error) {
	if date == "" {
		date = Today()
	}
	path := s.decisionsPath()
	content, err := ReadFile(path)
	if err != nil {
		return false, err
	}
	for _, t := range section.Topics(content) {
		if t.Name != topic {
			continue
		}
		for _, line := range strings.Split(t.Body, "\n") {
			if strings.TrimSpace(line) == "### "+title {
				return false, nil
			}
		}
		break
	}
	footer := "[Auto:Loop]"
	if planKey != "" {
		footer += " " + planKey
	}
	footer += " " + date + " — review and articulate Forbids/Permits"
	entry := "### " + title + "\n\n" +
		"*Why.* " + why + "\n\n" +
		"**Forbids.**\n- _(Unresolved — user must articulate)_\n\n" +
		"**Permits.**\n- _(Unresolved — user must articulate)_\n\n" +
		"<!-- " + footer + " -->\n"

	var next string
	if st, en, ok := section.Find(content, topic); !ok {
		next = strings.TrimRight(content, "\n") + "\n\n## " + topic + "\n\n" + entry
	} else {
		next = appendToSection(content, st, en, "\n"+entry)
	}
	if !strings.HasSuffix(next, "\n") {
		next += "\n"
	}
	return true, writeText(path, next, fsio.WriteFileAtomic)
}

// appendToSection splices addition just before the next heading (or EOF) of a
// section whose body spans [start, end).
func appendToSection(content string, start, end int, addition string) string {
	body := content[start:end]
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return content[:start] + body + addition + content[end:]
}

// Decision is one auto-logged entry reported by AutoSince.
type Decision struct{ Topic, Title, Date, Status string }

// Decision statuses.
const (
	StatusUnresolved  = "unresolved"
	StatusPartial     = "partial"
	StatusArticulated = "articulated"
)

var (
	entrySplit   = regexp.MustCompile(`(?m)^### (.+)$`)
	autoFooter   = regexp.MustCompile(`<!--\s*\[Auto:Loop\]\s+\S+\s+(\d{4}-\d{2}-\d{2})\b[^>]*-->`)
	sectionStops = regexp.MustCompile(`(?m)^(?:\*\*\w+\.\*\*|<!--|### )`)
)

const unresolvedMarker = "_(Unresolved — user must articulate)_"

// decisionSection is the body of "**<name>.**" up to the next bold header,
// comment or heading; ok is false when the header is missing.
func decisionSection(entry, name string) (string, bool) {
	hdr := regexp.MustCompile(`(?m)^\*\*` + regexp.QuoteMeta(name) + `\.\*\*\s*\n`)
	loc := hdr.FindStringIndex(entry)
	if loc == nil {
		return "", false
	}
	rest := entry[loc[1]:]
	if stop := sectionStops.FindStringIndex(rest); stop != nil {
		return rest[:stop[0]], true
	}
	return rest, true
}

func decisionStatus(entry string) string {
	f, fok := decisionSection(entry, "Forbids")
	p, pok := decisionSection(entry, "Permits")
	fu := fok && strings.Contains(f, unresolvedMarker)
	pu := pok && strings.Contains(p, unresolvedMarker)
	switch {
	case fu && pu:
		return StatusUnresolved
	case fu || pu:
		return StatusPartial
	}
	return StatusArticulated
}

// AutoSince lists the [Auto:Loop] entries of DECISIONS.md dated on or after
// the day the current loop started (status.json loopStartedAt). since is that
// timestamp, empty when no loop is active (and then there are no decisions).
func (s Store) AutoSince() (since string, out []Decision, err error) {
	out = []Decision{}
	if st, ok := fsio.LoadJSON(filepath.Join(s.Root, ".rota", "status.json"), nil).(*jsonx.Object); ok {
		if v, has := st.Get("loopStartedAt"); has {
			since, _ = v.(string)
		}
	}
	if since == "" {
		return "", out, nil
	}
	sinceDate, _, _ := strings.Cut(since, "T")
	raw, err := readTextBytes(s.decisionsPath())
	if os.IsNotExist(err) {
		return since, out, nil
	}
	if err != nil {
		return "", nil, err
	}
	for _, t := range section.Topics(string(raw)) {
		locs := entrySplit.FindAllStringSubmatchIndex(t.Body, -1)
		for i, l := range locs {
			end := len(t.Body)
			if i+1 < len(locs) {
				end = locs[i+1][0]
			}
			title := strings.TrimSpace(t.Body[l[2]:l[3]])
			body := t.Body[l[1]:end]
			m := autoFooter.FindStringSubmatch(body)
			if m == nil || m[1] < sinceDate {
				continue
			}
			out = append(out, Decision{t.Name, title, m[1], decisionStatus(body)})
		}
	}
	return since, out, nil
}

// Text renders decisions as the markdown block the old helper printed.
func DecisionsText(ds []Decision) string {
	if len(ds) == 0 {
		return ""
	}
	tags := map[string]string{
		StatusUnresolved:  "[Forbids/Permits unresolved]",
		StatusPartial:     "[Partially articulated]",
		StatusArticulated: "[Articulated]",
	}
	lines := []string{"### Auto:Loop decisions this session", ""}
	for _, d := range ds {
		lines = append(lines, fmt.Sprintf("- **%s · %s** — %s · %s", d.Topic, d.Title, d.Date, tags[d.Status]))
	}
	return strings.Join(lines, "\n") + "\n"
}
