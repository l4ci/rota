// Package proof holds the verification proof rows on items (rota proof add,
// show): rows in the "## Proof" section of an item's text, which a Store
// keeps: the item's gitignored proof sidecar (Files) or its `proof` note (NewNotes).
// Rows are facts, not acceptance.
package proof

import (
	"strings"
	"time"

	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/section"
	"github.com/l4ci/rota/internal/stamp"
	"github.com/l4ci/rota/internal/strutil"
)

const sep = " · "

// Row is one proof row.
type Row struct{ Date, Check, Result, Sha, Evidence string }

// Store keeps the text an item's proof rows live in. Judging whether id
// names an item is the store's job, and the caller builds the store with the
// means to judge: file mode (Files) takes a Sidecar from the caller; issue
// mode asks the tracker.
type Store interface {
	// Update hands fn the item's text (a starter text when it has none yet)
	// and stores what fn returns when changed is true, under whatever lock
	// the store needs for a read-modify-write.
	Update(id string, fn func(content string) (updated string, changed bool, err error)) error
	// Read is the item's text; ok is false when it has none.
	Read(id string) (content string, ok bool, err error)
}

// formatRow is the one writer of the row grammar: the "- " line for r.
func formatRow(r Row) string {
	return "- " + strings.Join([]string{r.Date, r.Check, r.Result, r.Sha, r.Evidence}, sep)
}

// parseRow is the one reader of the row grammar: the Row on a "- " line. A short
// row is padded with empty fields; fields is how many the line really had.
func parseRow(line string) (r Row, fields int) {
	p := strings.SplitN(strings.TrimPrefix(line, "- "), sep, 5)
	fields = len(p)
	for len(p) < 5 {
		p = append(p, "")
	}
	return Row{p[0], p[1], p[2], p[3], p[4]}, fields
}

// sameRow is row identity: everything but the date.
func sameRow(a, b Row) bool { a.Date, b.Date = "", ""; return a == b }

func one(s string) string { return strings.Join(strings.Fields(s), " ") }

// AddOpts are Add's arguments. Sha is the commit the check ran against, which
// the caller resolves; "" stores "-" (no commit).
type AddOpts struct{ Check, Result, Evidence, Sha string }

// Add appends a proof row for id. changed is false when an identical row (check,
// result, sha, evidence; date ignored) already exists. The returned Row
// carries the whitespace-collapsed values that were stored.
func Add(s Store, id string, today time.Time, o AddOpts) (row Row, changed bool, err error) {
	check, evidence := one(o.Check), one(o.Evidence)
	if check == "" || evidence == "" {
		return row, false, exitcode.Errf(exitcode.ExitUsage, "--check and --evidence must not be empty")
	}
	if o.Result != "PASS" && o.Result != "FAIL" {
		return row, false, exitcode.Errf(exitcode.ExitUsage, "--result must be PASS or FAIL")
	}
	sha := one(o.Sha)
	if sha == "" {
		sha = "-"
	}
	for _, v := range []string{check, sha} {
		if strings.Contains(v, sep) {
			return row, false, exitcode.Errf(exitcode.ExitUsage, "--check and --sha must not contain %q: it separates the row's fields", sep)
		}
	}
	row = Row{stamp.Date(today), check, o.Result, sha, evidence}

	err = s.Update(id, func(content string) (string, bool, error) {
		updated, ch := withRow(content, row)
		changed = ch
		return updated, ch, nil
	})
	if err != nil {
		return Row{}, false, err
	}
	return row, changed, nil
}

// withRow adds row to content's "## Proof" section, or starts the section;
// changed is false when an identical row (date aside) is already there.
func withRow(content string, row Row) (updated string, changed bool) {
	line := formatRow(row)
	if s, e, ok := section.Find(content, "Proof"); ok {
		for _, l := range strings.Split(content[s:e], "\n") {
			if !strings.HasPrefix(l, "- ") || !strings.Contains(l, sep) {
				continue
			}
			if have, _ := parseRow(l); sameRow(have, row) {
				return content, false
			}
		}
		updated = section.Append(strings.TrimRight(content, "\n")+"\n", "Proof", line+"\n")
		return updated, true
	}
	body := strings.TrimRight(content, "\n")
	if body != "" {
		body += "\n\n"
	}
	return body + "## Proof\n\n" + line + "\n", true
}

// Show returns the item's proof rows and their raw lines. An item with no
// text or no "## Proof" section has zero rows.
func Show(s Store, id string) (rows []Row, lines []string, err error) {
	content, ok, err := s.Read(id)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return []Row{}, []string{}, nil
	}
	rows, lines = parseRows(content)
	return rows, lines, nil
}

// rowLines is the "- " lines of the "## Proof" section of content: the one
// definition of what a proof row is, shared by parseRows and CountRows.
func rowLines(content string) (lines []string) {
	s, e, ok := section.Find(content, "Proof")
	if !ok {
		return nil
	}
	for _, l := range pystr.Splitlines(content[s:e]) {
		if strings.HasPrefix(l, "- ") {
			lines = append(lines, l)
		}
	}
	return lines
}

// CountRows is the number of proof rows in an item's text (hv-proof-show --count).
func CountRows(content string) int { return len(rowLines(content)) }

// parseRows reads the "- " rows of the "## Proof" section of content.
func parseRows(content string) (rows []Row, lines []string) {
	rows, lines = []Row{}, []string{}
	for _, l := range rowLines(content) {
		r, _ := parseRow(l)
		rows = append(rows, r)
		lines = append(lines, l)
	}
	return
}

// Latest is the latest row for check at sha: the row's sha and sha agree as
// prefixes either way, both at least 7 characters, and checks compare with
// whitespace collapsed. It is the one rule for which row speaks for a commit;
// the caller reads the row's Result.
func Latest(rows []Row, sha, check string) (Row, bool) {
	for i := len(rows) - 1; i >= 0; i-- {
		if speaksFor(rows[i].Sha, rows[i].Check, sha, check) {
			return rows[i], true
		}
	}
	return Row{}, false
}

// speaksFor is the match rule behind Latest and Mark: a row's sha and the
// queried sha agree as prefixes either way, both at least strutil.MinSHA characters,
// and the checks agree with whitespace collapsed.
func speaksFor(rowSha, rowCheck, sha, check string) bool {
	sha = one(sha)
	return one(rowCheck) == one(check) && strutil.SameSHA(sha, rowSha)
}

// Lookup reads the latest result for a mark from stored proof text.
func Lookup(content, sha, check string) (string, bool) {
	rows, _ := parseRows(content)
	row, ok := Latest(rows, sha, check)
	return row.Result, ok
}

// Proves reports whether the latest row for check at head is a PASS: a FAIL
// recorded after a PASS at the same sha withdraws the proof.
func Proves(rows []Row, head, check string) bool {
	r, ok := Latest(rows, head, check)
	return ok && r.Result == "PASS"
}

// Mark appends mark to the evidence of the latest PASS row for check at sha, so
// the row records how it was taken (targeted, say). It reports whether a row
// was found; a row that already carries the mark is left as it is.
func Mark(s Store, id, sha, check, mark string) (found bool, err error) {
	err = s.Update(id, func(content string) (string, bool, error) {
		rows := rowLines(content)
		for i := len(rows) - 1; i >= 0; i-- {
			l := rows[i]
			r, fields := parseRow(l)
			if r.Result != "PASS" || !speaksFor(r.Sha, r.Check, sha, check) {
				continue
			}
			found = true
			if strings.Contains(" "+r.Evidence+" ", " "+mark+" ") {
				return content, false, nil
			}
			add := " " + mark
			if fields < 5 { // a short row has no evidence yet: the mark becomes it
				add = sep + mark
			}
			s, e, _ := section.Find(content, "Proof")
			at := s + strings.LastIndex(content[s:e], l) + len(l)
			return content[:at] + add + content[at:], true, nil
		}
		return content, false, nil
	})
	return found, err
}
