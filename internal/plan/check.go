package plan

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/itembody"
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

	UncoveredDecisions []string  // decisions no task honors
	UnknownDecisions   []Unknown // tasks honoring a decision the item lacks
}

// Ok is true when nothing is wrong.
func (r Report) Ok() bool {
	return len(r.Uncovered)+len(r.Orphans)+len(r.Unknown)+len(r.NoVerify)+len(r.UncoveredDecisions)+len(r.UnknownDecisions) == 0
}

var (
	taskBullet  = regexp.MustCompile(`^(\s*)[-*]\s+\*{0,2}(T\d+)\b`)
	verifyBul   = regexp.MustCompile(`(?i)^(\s*)[-*]\s+\*{0,2}Verify\*{0,2}:\*{0,2}\s*(.*)$`)
	acNumberRe  = regexp.MustCompile(`\d+`)
	nonBlankSub = regexp.MustCompile(`^(\s*)[-*]\s+(.+)$`)
)

// taskBlock is one top-level task bullet of the Tasks section: its id and the
// lines under it, up to the next task at the same or a shallower indent.
type taskBlock struct {
	name  string
	lines []string
}

func taskBlocks(body string) []taskBlock {
	var out []taskBlock
	lines := strings.Split(strings.ReplaceAll(tasksSection(body), "\r", ""), "\n")
	for i := 0; i < len(lines); {
		m := taskBullet.FindStringSubmatch(lines[i])
		if m == nil {
			i++
			continue
		}
		base := len(m[1])
		j := i + 1
		for j < len(lines) {
			if tm := taskBullet.FindStringSubmatch(lines[j]); tm != nil && len(tm[1]) <= base {
				break
			}
			j++
		}
		out = append(out, taskBlock{m[2], lines[i+1 : j]})
		i = j
	}
	return out
}

// Check reads the Tasks section of a plan body against the item's criterion
// ids (as acceptance.IDs numbers them). A task is a top-level `- **T<n>**`
// bullet; its `Serves:` sub-bullet names the criteria it delivers and its
// `Verify:` sub-bullet (inline or nested) the check that proves it. A
// `Honors:` sub-bullet cites the item's decisions (D1, D2, ... as the caller
// numbers them). Pure.
func Check(body string, ids, decisions []string) Report {
	known := map[string]bool{}
	for _, id := range ids {
		known[id] = true
	}
	served := map[string]bool{}
	knownDec := map[string]bool{}
	for _, id := range decisions {
		knownDec[id] = true
	}
	honored := map[string]bool{}
	var r Report

	for _, tb := range taskBlocks(body) {
		task, block := tb.name, tb.lines
		var serves []string
		for _, l := range block {
			if sm := itembody.ServesRe.FindStringSubmatch(l); sm != nil && !placeholder.MatchString(strings.TrimSpace(sm[1])) {
				serves = append(serves, itembody.ACIDRe.FindAllString(sm[1], -1)...)
			}
		}
		var unknownDec []string
		for _, l := range block {
			if hm := itembody.HonorsRe.FindStringSubmatch(l); hm != nil && !placeholder.MatchString(strings.TrimSpace(hm[1])) {
				for _, id := range itembody.DecisionIDRe.FindAllString(hm[1], -1) {
					if knownDec[id] {
						honored[id] = true
					} else {
						unknownDec = append(unknownDec, id)
					}
				}
			}
		}
		if len(unknownDec) > 0 {
			r.UnknownDecisions = append(r.UnknownDecisions, Unknown{Task: task, IDs: unknownDec})
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
	for _, id := range decisions {
		if !honored[id] {
			r.UncoveredDecisions = append(r.UncoveredDecisions, id)
		}
	}
	sort.SliceStable(r.Uncovered, func(a, b int) bool { return acNum(r.Uncovered[a]) < acNum(r.Uncovered[b]) })
	sort.SliceStable(r.UncoveredDecisions, func(a, b int) bool { return acNum(r.UncoveredDecisions[a]) < acNum(r.UncoveredDecisions[b]) })
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
