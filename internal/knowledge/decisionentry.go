package knowledge

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Decision is one parsed entry. Part spans are retained so amendments can
// replace one part without reformatting its neighbours.
type Decision struct {
	Title, Rule, Why, Forbids, Permits, Status, Date string
	parts                                            map[string]decisionSpan
	paths                                            []string
	errors                                           []string
}

type decisionSpan struct{ start, end int }
type decisionLine struct {
	start, end int
	text       string
	structural bool
}

// decisionLines marks fenced code as literal, including longer and tilde
// fences. Positions always address the original bytes, including CRLF files.
func decisionLines(text string) []decisionLine {
	var out []decisionLine
	var fence byte
	width := 0
	for pos := 0; pos < len(text); {
		end := len(text)
		if n := strings.IndexByte(text[pos:], '\n'); n >= 0 {
			end = pos + n + 1
		}
		line := strings.TrimRight(text[pos:end], "\r\n")
		t := strings.TrimLeft(line, " ")
		structural := fence == 0
		if len(line)-len(t) <= 3 && len(t) >= 3 && (t[0] == '`' || t[0] == '~') {
			n := 0
			for n < len(t) && t[n] == t[0] {
				n++
			}
			if n >= 3 {
				if fence == 0 {
					fence, width, structural = t[0], n, false
				} else if t[0] == fence && n >= width && strings.TrimSpace(t[n:]) == "" {
					fence, width = 0, 0
				}
			}
		}
		out = append(out, decisionLine{pos, end, line, structural})
		pos = end
	}
	return out
}

var decisionMarkers = []struct{ name, prefix string }{
	{"why", "*Why.*"}, {"forbids", "**Forbids.**"}, {"permits", "**Permits.**"}, {"status", "*Status.*"},
}
var decisionDate = regexp.MustCompile(`^<!-- (\d{4}-\d{2}-\d{2}) -->$`)
var decisionStatus = regexp.MustCompile(`^(?:Retired (\d{4}-\d{2}-\d{2})\.(?: .+)?|Superseded (\d{4}-\d{2}-\d{2}) by "[^"\r\n]+"\.)$`)

func decisionValidDate(date string) bool { _, err := time.Parse("2006-01-02", date); return err == nil }

// ParseDecision reads a complete ### entry using the canonical four parts.
// CheckDecisions and writers use this same definition of a valid entry.
func ParseDecision(raw string) Decision {
	d := Decision{parts: map[string]decisionSpan{}}
	type mark struct {
		name         string
		start, value int
	}
	var marks []mark
	bodyStart := 0
	lines := decisionLines(raw)
	if len(lines) > 0 && strings.HasPrefix(lines[0].text, "### ") {
		d.Title = strings.TrimSpace(strings.TrimPrefix(lines[0].text, "### "))
		bodyStart = lines[0].end
	} else {
		d.errors = append(d.errors, "missing title")
	}
	marks = append(marks, mark{"rule", bodyStart, bodyStart})
	rank := map[string]int{"rule": 0, "why": 1, "forbids": 2, "permits": 3, "status": 4, "paths": 5, "date": 6}
	last := 0
	seen := map[string]bool{"rule": true}
	for _, l := range lines {
		if l.start < bodyStart || !l.structural {
			continue
		}
		name, value := "", l.start
		for _, m := range decisionMarkers {
			if strings.HasPrefix(l.text, m.prefix) {
				name, value = m.name, l.start+len(m.prefix)
				break
			}
		}
		if m := decisionDate.FindStringSubmatch(strings.TrimSpace(l.text)); m != nil {
			name, value, d.Date = "date", l.start, m[1]
		}
		if strings.HasPrefix(strings.TrimSpace(l.text), "<!-- paths:") {
			name = "paths"
			d.paths = append(d.paths, l.text)
		}
		if name == "" {
			if strings.HasPrefix(l.text, "## ") || strings.HasPrefix(l.text, "### ") {
				d.errors = append(d.errors, "unexpected heading")
			}
			continue
		}
		if seen[name] && name != "paths" {
			d.errors = append(d.errors, "duplicate "+name)
		}
		if rank[name] < last {
			d.errors = append(d.errors, "out-of-order "+name)
		}
		last = rank[name]
		seen[name] = true
		marks = append(marks, mark{name, l.start, value})
	}
	values := map[string]string{}
	for i, m := range marks {
		end := len(raw)
		if i+1 < len(marks) {
			end = marks[i+1].start
		}
		if _, ok := d.parts[m.name]; !ok {
			d.parts[m.name] = decisionSpan{m.value, end}
			values[m.name] = strings.TrimSpace(raw[m.value:end])
		}
	}
	d.Rule, d.Why, d.Forbids, d.Permits, d.Status = values["rule"], values["why"], values["forbids"], values["permits"], values["status"]
	for _, name := range []string{"rule", "why", "forbids", "permits"} {
		if values[name] == "" {
			d.errors = append(d.errors, "missing "+name)
		}
	}
	if seen["status"] {
		m := decisionStatus.FindStringSubmatch(d.Status)
		if m == nil || !decisionValidDate(m[1]+m[2]) {
			d.errors = append(d.errors, "malformed status")
		}
	}
	if d.Date != "" && !decisionValidDate(d.Date) {
		d.errors = append(d.errors, "malformed date")
	}
	return d
}

type decisionTopic struct {
	name             string
	start, body, end int
	entries          []decisionEntry
}
type decisionEntry struct {
	title      string
	start, end int
}

func decisionTopics(content string) []decisionTopic {
	var topics []decisionTopic
	for _, l := range decisionLines(content) {
		if !l.structural {
			continue
		}
		if strings.HasPrefix(l.text, "## ") {
			if len(topics) > 0 {
				topics[len(topics)-1].end = l.start
			}
			topics = append(topics, decisionTopic{name: strings.TrimSpace(l.text[3:]), start: l.start, body: l.end, end: len(content)})
		} else if strings.HasPrefix(l.text, "### ") && len(topics) > 0 {
			t := &topics[len(topics)-1]
			if len(t.entries) > 0 {
				t.entries[len(t.entries)-1].end = l.start
			}
			t.entries = append(t.entries, decisionEntry{strings.TrimSpace(l.text[4:]), l.start, len(content)})
		}
	}
	for i := range topics {
		if n := len(topics[i].entries); n > 0 {
			topics[i].entries[n-1].end = topics[i].end
		}
	}
	return topics
}

// DecisionCheck lists malformed entries separately from missing-date notices.
// Empty and missing files contain no entries and therefore pass.
type DecisionCheck struct{ Errors, Warnings []string }

func CheckDecisions(content string) DecisionCheck {
	r := DecisionCheck{Errors: []string{}, Warnings: []string{}}
	for _, t := range decisionTopics(content) {
		for _, e := range t.entries {
			d := ParseDecision(content[e.start:e.end])
			prefix := fmt.Sprintf("%s :: %s: ", t.name, e.title)
			for _, err := range d.errors {
				r.Errors = append(r.Errors, prefix+err)
			}
			if d.Date == "" {
				r.Warnings = append(r.Warnings, prefix+"missing date")
			}
		}
	}
	return r
}
