package plan

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Unknown is a task whose Serves line names criteria the item does not have.
type Unknown struct {
	Task string
	IDs  []string
}

// Report is what Check finds wrong with a plan against its item's criteria.
// Every list is empty when the plan is sound.
type Report struct {
	Uncovered []string  // criteria no task serves
	Orphans   []string  // tasks whose Serves line names no criterion (or is missing)
	Unknown   []Unknown // tasks serving an id the item lacks
	NoVerify  []string  // tasks without a Verify step
}

// Ok is true when nothing is wrong.
func (r Report) Ok() bool {
	return len(r.Uncovered)+len(r.Orphans)+len(r.Unknown)+len(r.NoVerify) == 0
}

var (
	taskBullet  = regexp.MustCompile(`^(\s*)[-*]\s+\*{0,2}(T\d+)\b`)
	servesBul   = regexp.MustCompile(`(?i)^\s*[-*]\s+\*{0,2}Serves\*{0,2}:\*{0,2}\s*(.*)$`)
	verifyBul   = regexp.MustCompile(`(?i)^(\s*)[-*]\s+\*{0,2}Verify\*{0,2}:\*{0,2}\s*(.*)$`)
	acToken     = regexp.MustCompile(`\bAC-\d+\b`)
	acNumberRe  = regexp.MustCompile(`\d+`)
	nonBlankSub = regexp.MustCompile(`^(\s*)[-*]\s+(.+)$`)
)

// Check reads the Tasks section of a plan body against the item's criterion
// ids (as acceptance.IDs numbers them). A task is a top-level `- **T<n>**`
// bullet; its `Serves:` sub-bullet names the criteria it delivers and its
// `Verify:` sub-bullet (inline or nested) the check that proves it. Pure.
func Check(body string, ids []string) Report {
	known := map[string]bool{}
	for _, id := range ids {
		known[id] = true
	}
	served := map[string]bool{}
	var r Report

	lines := strings.Split(strings.ReplaceAll(tasksSection(body), "\r", ""), "\n")
	for i := 0; i < len(lines); {
		m := taskBullet.FindStringSubmatch(lines[i])
		if m == nil {
			i++
			continue
		}
		task, base := m[2], len(m[1])
		j := i + 1
		for j < len(lines) {
			if tm := taskBullet.FindStringSubmatch(lines[j]); tm != nil && len(tm[1]) <= base {
				break
			}
			j++
		}
		block := lines[i+1 : j]
		i = j

		var serves []string
		for _, l := range block {
			if sm := servesBul.FindStringSubmatch(l); sm != nil && !placeholder.MatchString(strings.TrimSpace(sm[1])) {
				serves = append(serves, acToken.FindAllString(sm[1], -1)...)
			}
		}
		var unknown []string
		for _, id := range serves {
			if known[id] {
				served[id] = true
			} else {
				unknown = append(unknown, id)
			}
		}
		switch {
		case len(serves) == 0:
			r.Orphans = append(r.Orphans, task)
		case len(unknown) > 0:
			r.Unknown = append(r.Unknown, Unknown{Task: task, IDs: unknown})
		}
		if !hasVerify(block) {
			r.NoVerify = append(r.NoVerify, task)
		}
	}
	for _, id := range ids {
		if !served[id] {
			r.Uncovered = append(r.Uncovered, id)
		}
	}
	sort.SliceStable(r.Uncovered, func(a, b int) bool { return acNum(r.Uncovered[a]) < acNum(r.Uncovered[b]) })
	return r
}

func acNum(id string) int {
	n, _ := strconv.Atoi(acNumberRe.FindString(id))
	return n
}

// hasVerify: a Verify bullet with text after the colon, or with real sub-bullets
// under it. A `_(placeholder)_` is no check.
func hasVerify(block []string) bool {
	for i, l := range block {
		m := verifyBul.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		if tail := strings.TrimSpace(m[2]); tail != "" && !placeholder.MatchString(tail) {
			return true
		}
		base := len(m[1])
		for _, nxt := range block[i+1:] {
			if strings.TrimSpace(nxt) == "" {
				continue
			}
			sm := nonBlankSub.FindStringSubmatch(nxt)
			if sm == nil || len(sm[1]) <= base {
				break
			}
			if !placeholder.MatchString(strings.TrimSpace(sm[2])) {
				return true
			}
		}
	}
	return false
}
