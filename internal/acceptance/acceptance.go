// Package acceptance owns the grammar of an item's acceptance criteria: the
// checkbox lines under "## Acceptance", their stable AC-n ids, the marks that
// say a criterion is met (the `acceptance` item note) and the coverage a
// reader computes from body, marks and proof rows. Pure: no I/O.
package acceptance

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/section"
)

// Criterion is one checkbox line under "## Acceptance". ID is "" until the
// line is numbered; Text excludes the id prefix; Line is the 1-based line.
type Criterion struct {
	ID, Text string
	Line     int
}

// Mark records one criterion as met: the proof row it rests on (sha and
// check) and the criterion text at the time of marking.
type Mark struct{ ID, Date, Sha, Check, Text string }

// Row is the part of a proof row coverage reads.
type Row struct{ Check, Result, Sha string }

// Status is one criterion's coverage. Flag is "", "changed" (text edited
// since the mark), "unproven" (the proof row is gone or not PASS) or
// "missing" (a mark whose id left the body).
type Status struct {
	ID, Text, Proof, Flag string
	Met                   bool
}

var (
	checkboxRe = regexp.MustCompile(`^(\s*[-*]\s+\[[ xX]\]\s+)(.*)$`)
	idRe       = regexp.MustCompile(`^AC-(\d+):\s*`)
	markSep    = " · "
)

func one(s string) string { return strings.Join(strings.Fields(s), " ") }

// scan walks the criteria lines of body: fn gets the 0-based line index, the
// checkbox prefix, the text after it and the parsed id number (-1 if none).
func scan(body string, fn func(i int, prefix, rest string, n int)) {
	s, e, ok := section.Find(body, "Acceptance")
	if !ok {
		return
	}
	// Line index of the section start.
	first := strings.Count(body[:s], "\n")
	lines := strings.Split(body[s:e], "\n")
	for j, l := range lines {
		m := checkboxRe.FindStringSubmatch(strings.TrimRight(l, "\r"))
		if m == nil || one(m[2]) == "" {
			continue
		}
		n := -1
		if im := idRe.FindStringSubmatch(m[2]); im != nil {
			n, _ = strconv.Atoi(im[1])
		}
		// lines[0] continues the heading line, so j is an offset from first.
		fn(first+j, m[1], m[2], n)
	}
}

func maxID(body string) int {
	max := 0
	scan(body, func(_ int, _, _ string, n int) {
		if n > max {
			max = n
		}
	})
	return max
}

func text(rest string) string {
	if loc := idRe.FindStringIndex(rest); loc != nil {
		rest = rest[loc[1]:]
	}
	return one(rest)
}

func label(n int) string { return "AC-" + strconv.Itoa(n) }

// Parse lists the criteria under "## Acceptance" in document order, and the
// ids that occur more than once.
func Parse(body string) (crits []Criterion, dups []string) {
	seen := map[string]int{}
	scan(body, func(i int, _, rest string, n int) {
		c := Criterion{Text: text(rest), Line: i + 1}
		if n >= 0 {
			c.ID = label(n)
			if seen[c.ID]++; seen[c.ID] == 2 {
				dups = append(dups, c.ID)
			}
		}
		crits = append(crits, c)
	})
	return crits, dups
}

// Number gives unlabeled criteria the next free AC-n ids in document order.
// Existing ids, checkbox state and everything outside the section stay as
// they are; changed is false when nothing needed a number.
func Number(body string) (string, bool) {
	next := maxID(body) + 1
	lines := strings.Split(body, "\n")
	changed := false
	scan(body, func(i int, prefix, rest string, n int) {
		if n >= 0 {
			return
		}
		l := lines[i]
		lines[i] = l[:len(prefix)] + label(next) + ": " + l[len(prefix):]
		next++
		changed = true
	})
	if !changed {
		return body, false
	}
	return strings.Join(lines, "\n"), true
}

// IDs is the criteria as Number would label them.
func IDs(body string) []Criterion {
	labelled, _ := Number(body)
	crits, _ := Parse(labelled)
	return crits
}

// ParseMarks reads the acceptance note: one "- AC-n · date · sha · check ·
// text" line per mark. Other lines are ignored.
func ParseMarks(note string) []Mark {
	var out []Mark
	for _, l := range strings.Split(note, "\n") {
		l = strings.TrimRight(l, "\r")
		if !strings.HasPrefix(l, "- AC-") {
			continue
		}
		f := strings.SplitN(l[2:], markSep, 5)
		if len(f) != 5 {
			continue
		}
		out = append(out, Mark{f[0], f[1], f[2], f[3], f[4]})
	}
	return out
}

func idNum(id string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(id, "AC-"))
	return n
}

// RenderMarks writes marks as the acceptance note, sorted by AC number.
func RenderMarks(marks []Mark) string {
	sorted := append([]Mark(nil), marks...)
	sort.SliceStable(sorted, func(i, j int) bool { return idNum(sorted[i].ID) < idNum(sorted[j].ID) })
	var b strings.Builder
	for _, m := range sorted {
		b.WriteString("- " + strings.Join([]string{m.ID, m.Date, m.Sha, m.Check, m.Text}, markSep) + "\n")
	}
	return b.String()
}

// Upsert returns marks with m replacing any mark for the same id.
func Upsert(marks []Mark, m Mark) []Mark {
	out := make([]Mark, 0, len(marks)+1)
	for _, x := range marks {
		if x.ID != m.ID {
			out = append(out, x)
		}
	}
	return append(out, m)
}

// FindProof is the latest row for check whose sha matches sha (a prefix
// either way, at least 4 characters). The caller decides whether the row's
// result is good enough.
func FindProof(rows []Row, sha, check string) (Row, bool) {
	sha, check = one(sha), one(check)
	if len(sha) < 4 {
		return Row{}, false
	}
	for i := len(rows) - 1; i >= 0; i-- {
		r := rows[i]
		if one(r.Check) != check || len(r.Sha) < 4 {
			continue
		}
		if strings.HasPrefix(sha, r.Sha) || strings.HasPrefix(r.Sha, sha) {
			return r, true
		}
	}
	return Row{}, false
}

// Coverage reports every criterion of body (unlabeled ones under the id
// Number would give them), then marks whose criterion is gone.
func Coverage(body string, marks []Mark, rows []Row) []Status {
	byID := map[string]Mark{}
	for _, m := range marks {
		byID[m.ID] = m
	}
	var out []Status
	seen := map[string]bool{}
	for _, c := range IDs(body) {
		st := Status{ID: c.ID, Text: c.Text}
		seen[c.ID] = true
		if m, ok := byID[c.ID]; ok {
			st.Proof = m.Sha + ":" + m.Check
			switch r, found := FindProof(rows, m.Sha, m.Check); {
			case m.Text != c.Text:
				st.Flag = "changed"
			case !found || r.Result != "PASS":
				st.Flag = "unproven"
			default:
				st.Met = true
			}
		}
		out = append(out, st)
	}
	for _, m := range marks {
		if !seen[m.ID] {
			out = append(out, Status{ID: m.ID, Text: m.Text, Proof: m.Sha + ":" + m.Check, Flag: "missing"})
		}
	}
	return out
}
