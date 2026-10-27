package knowledge

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
)

// ErrDecisionInvalid means the supplied body would not pass decisions check.
var ErrDecisionInvalid = errors.New("invalid decision")

func decisionName(name string) bool {
	return strings.TrimSpace(name) != "" && !strings.ContainsAny(name, "\r\n")
}
func decisionInvalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrDecisionInvalid, fmt.Sprintf(format, args...))
}

// Keep the original refusal and exit code, but give malformed stored entries a
// recovery path when a writer cannot make the complete file valid.
func decisionRepairError(content string, err error) error {
	if report := CheckDecisions(content); len(report.Errors) > 0 {
		return fmt.Errorf("%w; stored DECISIONS.md: %s; manually repair the reported entries in .rota/DECISIONS.md, then run rota decisions check and rota block decisions", err, strings.Join(report.Errors, "; "))
	}
	return err
}

func decisionBody(title, body, date string) (string, error) {
	if !decisionName(title) {
		return "", decisionInvalid("title must be one nonempty line")
	}
	raw := "### " + title + "\n\n" + strings.TrimSpace(body) + "\n"
	d := ParseDecision(raw)
	if len(d.errors) > 0 {
		return "", decisionInvalid("%s", strings.Join(d.errors, "; "))
	}
	if d.Status != "" {
		return "", decisionInvalid("new decision must be active")
	}
	render := func(name, label, value string) string {
		separator := " "
		span := d.parts[name]
		original := strings.TrimLeft(raw[span.start:span.end], " \t")
		if strings.HasPrefix(original, "\n") || strings.HasPrefix(original, "\r\n") {
			separator = "\n"
		}
		return label + separator + value
	}
	out := fmt.Sprintf("### %s\n\n%s\n\n%s\n\n%s\n\n%s\n", title, d.Rule, render("why", "*Why.*", d.Why), render("forbids", "**Forbids.**", d.Forbids), render("permits", "**Permits.**", d.Permits))
	for _, path := range d.paths {
		out += "\n" + path + "\n"
	}
	return out + "\n<!-- " + date + " -->\n", nil
}

func decisionTopicMatch(topics []decisionTopic, name string) (int, error) {
	found := -1
	for i, t := range topics {
		if strings.EqualFold(t.name, name) {
			if found >= 0 {
				return -1, fmt.Errorf("%w: topic %q is ambiguous", ErrExists, name)
			}
			found = i
		}
	}
	return found, nil
}

func decisionInsert(content, topic, entry string) (string, error) {
	topics := decisionTopics(content)
	i, err := decisionTopicMatch(topics, topic)
	if err != nil {
		return "", err
	}
	if i >= 0 {
		pos := topics[i].body
		return content[:pos] + "\n" + entry + "\n" + content[pos:], nil
	}
	key := func(name string) string {
		switch name {
		case "Architecture":
			return "0"
		case "Build & Tooling":
			return "1"
		}
		return "2" + strings.ToLower(name)
	}
	pos := len(content)
	for _, t := range topics {
		if key(t.name) > key(topic) {
			pos = t.start
			break
		}
	}
	prefix := content[:pos]
	if prefix == "" {
		prefix = "# Decisions\n\n"
	} else if !strings.HasSuffix(prefix, "\n\n") {
		prefix = strings.TrimRight(prefix, "\r\n") + "\n\n"
	}
	return prefix + "## " + topic + "\n\n" + entry + "\n" + content[pos:], nil
}

// changeDecisions serializes file read/modify/write and its index refresh.
func (s Store) changeDecisions(change func(string) (string, error)) (bool, error) {
	changed := false
	err := fsio.Locked(s.decisionsPath(), fsio.LockTimeout, func() error {
		raw, err := os.ReadFile(s.decisionsPath())
		if os.IsNotExist(err) {
			err = nil
		}
		if err != nil {
			return err
		}
		next, err := change(string(raw))
		if err != nil {
			return decisionRepairError(string(raw), err)
		}
		// Refresh legacy skill names in the preamble only. Decision entries
		// retain the names that were true when their boundaries were recorded.
		end := len(next)
		if topics := decisionTopics(next); len(topics) > 0 {
			end = topics[0].start
		}
		next = strings.ReplaceAll(next[:end], "`/hv-", "`/rota-") + next[end:]
		if report := CheckDecisions(next); len(report.Errors) > 0 {
			return decisionRepairError(string(raw), decisionInvalid("resulting DECISIONS.md: %s", strings.Join(report.Errors, "; ")))
		}
		changed = next != string(raw)
		if changed {
			if err = writeText(s.decisionsPath(), next, fsio.WriteFileAtomic); err != nil {
				return err
			}
		}
		_, err = s.RegenerateBlock("decisions", Umbrella)
		return err
	})
	return changed, err
}

// AddDecision inserts a validated, dated entry at the top of its topic.
func (s Store) AddDecision(topic, title, body, date string) (bool, error) {
	if !decisionName(topic) {
		return false, decisionInvalid("topic must be one nonempty line")
	}
	if date == "" {
		date = s.today()
	}
	if !decisionValidDate(date) {
		return false, decisionInvalid("date must be a real YYYY-MM-DD")
	}
	entry, err := decisionBody(title, body, date)
	if err != nil {
		return false, err
	}
	return s.changeDecisions(func(content string) (string, error) {
		topics := decisionTopics(content)
		i, err := decisionTopicMatch(topics, topic)
		if err != nil {
			return "", err
		}
		if i >= 0 {
			for _, e := range topics[i].entries {
				if strings.EqualFold(e.title, title) {
					return "", fmt.Errorf("%w: decision %q already exists", ErrExists, title)
				}
			}
		}
		return decisionInsert(content, topic, entry)
	})
}

func decisionTarget(content, topic, title string) (decisionEntry, error) {
	topics := decisionTopics(content)
	i, err := decisionTopicMatch(topics, topic)
	if err != nil {
		return decisionEntry{}, err
	}
	if i < 0 {
		return decisionEntry{}, notFound("topic %q", topic)
	}
	var matches []decisionEntry
	for _, e := range topics[i].entries {
		if strings.EqualFold(e.title, title) {
			matches = append(matches, e)
		}
	}
	if len(matches) == 0 {
		return decisionEntry{}, notFound("decision %q in %q", title, topic)
	}
	if len(matches) > 1 {
		return decisionEntry{}, fmt.Errorf("%w: decision %q is ambiguous", ErrExists, title)
	}
	e := matches[0]
	if d := ParseDecision(content[e.start:e.end]); d.Status != "" {
		return decisionEntry{}, fmt.Errorf("%w: decision %q is inactive", ErrExists, title)
	}
	return e, nil
}

func decisionPartPrefix(part string) (string, bool) {
	if part == "rule" {
		return "", true
	}
	for _, m := range decisionMarkers {
		if m.name == part && part != "status" {
			return m.prefix, true
		}
	}
	return "", false
}

// AmendDecision changes only the selected part of one active entry. It can
// insert a missing part when that repair makes the entry well-formed.
func (s Store) AmendDecision(topic, title, part, body string) (bool, error) {
	prefix, ok := decisionPartPrefix(part)
	if !ok {
		return false, decisionInvalid("part must be rule, why, forbids or permits")
	}
	if strings.TrimSpace(body) == "" {
		return false, decisionInvalid("replacement part is empty")
	}
	return s.changeDecisions(func(content string) (string, error) {
		e, err := decisionTarget(content, topic, title)
		if err != nil {
			return "", err
		}
		raw := content[e.start:e.end]
		d := ParseDecision(raw)
		var next string
		if span, ok := d.parts[part]; ok {
			old := raw[span.start:span.end]
			lead := len(old) - len(strings.TrimLeft(old, " \t\r\n"))
			tail := len(strings.TrimRight(old, " \t\r\n"))
			if tail < lead {
				lead = 0
			}
			leading := old[:lead]
			if part != "rule" && decisionBlockStart(strings.TrimSpace(body)) {
				leading = "\n"
				if strings.Contains(raw, "\r\n") {
					leading = "\r\n"
				}
			}
			next = raw[:span.start] + leading + strings.TrimSpace(body) + old[tail:] + raw[span.end:]
		} else {
			order := []string{"rule", "why", "forbids", "permits", "status", "paths", "date"}
			pos, after := len(raw), false
			for _, name := range order {
				if name == part {
					after = true
					continue
				}
				if !after {
					continue
				}
				if span, ok := d.parts[name]; ok {
					pos = span.start
					for _, m := range decisionMarkers {
						if m.name == name {
							pos -= len(m.prefix)
						}
					}
					break
				}
			}
			next = raw[:pos] + prefix + " " + strings.TrimSpace(body) + "\n\n" + raw[pos:]
		}
		if problems := ParseDecision(next).errors; len(problems) > 0 {
			return "", decisionInvalid("%s", strings.Join(problems, "; "))
		}
		return content[:e.start] + next + content[e.end:], nil
	})
}

func decisionSetStatus(raw, status string) (string, error) {
	d := ParseDecision(raw)
	pos := len(raw)
	for _, name := range []string{"paths", "date"} {
		if span, ok := d.parts[name]; ok && span.start < pos {
			pos = span.start
		}
	}
	nl := "\n"
	if strings.Contains(raw, "\r\n") {
		nl = "\r\n"
	}
	before := raw[:pos]
	if !strings.HasSuffix(before, nl+nl) {
		before = strings.TrimRight(before, "\r\n") + nl + nl
	}
	next := before + "*Status.* " + status + nl + nl + raw[pos:]
	if problems := ParseDecision(next).errors; len(problems) > 0 {
		return "", decisionInvalid("%s", strings.Join(problems, "; "))
	}
	return next, nil
}

// RetireDecision lifts a boundary while retaining its dated history.
func (s Store) RetireDecision(topic, title, reason, date string) (bool, error) {
	reason = strings.TrimSpace(reason)
	if !decisionName(reason) {
		return false, decisionInvalid("reason must be one nonempty line")
	}
	if date == "" {
		date = s.today()
	}
	if !decisionValidDate(date) {
		return false, decisionInvalid("date must be a real YYYY-MM-DD")
	}
	return s.changeDecisions(func(content string) (string, error) {
		e, err := decisionTarget(content, topic, title)
		if err != nil {
			return "", err
		}
		next, err := decisionSetStatus(content[e.start:e.end], "Retired "+date+". "+reason)
		if err != nil {
			return "", err
		}
		return content[:e.start] + next + content[e.end:], nil
	})
}

// SupersedeDecision validates the replacement before making one atomic file
// update that both inserts it and marks the old entry as superseded.
func (s Store) SupersedeDecision(topic, title, newTitle, body, date string) (bool, error) {
	if date == "" {
		date = s.today()
	}
	if !decisionValidDate(date) {
		return false, decisionInvalid("date must be a real YYYY-MM-DD")
	}
	entry, err := decisionBody(newTitle, body, date)
	if err != nil {
		return false, err
	}
	return s.changeDecisions(func(content string) (string, error) {
		e, err := decisionTarget(content, topic, title)
		if err != nil {
			return "", err
		}
		topics := decisionTopics(content)
		i, err := decisionTopicMatch(topics, topic)
		if err != nil {
			return "", err
		}
		for _, other := range topics[i].entries {
			if strings.EqualFold(other.title, newTitle) {
				return "", fmt.Errorf("%w: decision %q already exists", ErrExists, newTitle)
			}
		}
		next, err := decisionSetStatus(content[e.start:e.end], "Superseded "+date+" by \""+newTitle+"\".")
		if err != nil {
			return "", err
		}
		return decisionInsert(content[:e.start]+next+content[e.end:], topic, entry)
	})
}

func decisionBlockStart(body string) bool {
	if strings.HasPrefix(body, "- ") || strings.HasPrefix(body, "* ") || strings.HasPrefix(body, "+ ") || strings.HasPrefix(body, "```") || strings.HasPrefix(body, "~~~") {
		return true
	}
	i := 0
	for i < len(body) && body[i] >= '0' && body[i] <= '9' {
		i++
	}
	return i > 0 && i+1 < len(body) && (body[i] == '.' || body[i] == ')') && body[i+1] == ' '
}
