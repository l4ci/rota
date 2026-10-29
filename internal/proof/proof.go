// Package proof holds the verification proof rows on items (rota proof add,
// show): rows in the "## Proof" section of an item's text, which a Store
// keeps: the item's gitignored proof sidecar (Files) or its `proof` note (NewNotes).
// Rows are facts, not acceptance.
package proof

import (
	"strings"
	"time"

	"context"
	"github.com/l4ci/rota/internal/artifact"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/section"
)

const sep = " · "

// Row is one proof row.
type Row struct{ Date, Check, Result, Sha, Evidence string }

// Store keeps the text an item's proof rows live in. Judging whether id
// names an item is the store's job: file mode needs a letter and digits and
// an entry in the backlog (exit 2, 3); issue mode asks the tracker.
type Store interface {
	// Update hands fn the item's text (a starter text when it has none yet)
	// and stores what fn returns when changed is true, under whatever lock
	// the store needs for a read-modify-write.
	Update(id string, fn func(content string) (updated string, changed bool, err error)) error
	// Read is the item's text; ok is false when it has none.
	Read(id string) (content string, ok bool, err error)
}

func one(s string) string { return strings.Join(strings.Fields(s), " ") }

// AddOpts are Add's arguments; Sha "" means the HEAD of the repository at dir.
type AddOpts struct{ Check, Result, Evidence, Sha string }

// Add appends a proof row for id. dir is the working repository, read for
// HEAD when o.Sha is empty. changed is false when an identical row (check,
// result, sha, evidence; date ignored) already exists. The returned Row
// carries the whitespace-collapsed values that were stored.
func Add(s Store, dir, id string, today time.Time, o AddOpts) (row Row, changed bool, err error) {
	check, evidence := one(o.Check), one(o.Evidence)
	if check == "" || evidence == "" {
		return row, false, exitcode.Errf(exitcode.ExitUsage, "--check and --evidence must not be empty")
	}
	if o.Result != "PASS" && o.Result != "FAIL" {
		return row, false, exitcode.Errf(exitcode.ExitUsage, "--result must be PASS or FAIL")
	}
	sha := one(o.Sha)
	if sha == "" {
		sha = headSha(dir)
	}
	row = Row{today.Format("2006-01-02"), check, o.Result, sha, evidence}
	line := "- " + strings.Join([]string{row.Date, row.Check, row.Result, row.Sha, row.Evidence}, sep)
	key := strings.SplitN(line, sep, 2)[1]

	err = s.Update(id, func(content string) (string, bool, error) {
		updated, ch := withRow(content, line, key)
		changed = ch
		return updated, ch, nil
	})
	if err != nil {
		return Row{}, false, err
	}
	return row, changed, nil
}

// withRow adds line to content's "## Proof" section, or starts the section;
// changed is false when a row with the same key is already there.
func withRow(content, line, key string) (updated string, changed bool) {
	if s, e, ok := section.Find(content, "Proof"); ok {
		for _, l := range strings.Split(content[s:e], "\n") {
			if strings.HasPrefix(l, "- ") && strings.Contains(l, sep) && strings.SplitN(l, sep, 2)[1] == key {
				return content, false
			}
		}
		updated, _ = artifact.AppendSection(strings.TrimRight(content, "\n")+"\n", "Proof", line+"\n")
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
	return parseRows(content)
}

// headSha is the abbreviated HEAD of the repository at dir, "-" when there is none.
func headSha(dir string) string {
	res, _ := git.Repo{Dir: dir}.Run(context.Background(), "log", "-1", "--format=%h")
	if sha := one(res.Stdout); sha != "" {
		return sha
	}
	return "-"
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
func parseRows(content string) (rows []Row, lines []string, err error) {
	rows, lines = []Row{}, []string{}
	for _, l := range rowLines(content) {
		p := strings.SplitN(l[2:], sep, 5)
		for len(p) < 5 {
			p = append(p, "")
		}
		rows = append(rows, Row{p[0], p[1], p[2], p[3], p[4]})
		lines = append(lines, l)
	}
	return
}

// minSha is the shortest sha a row or a query may carry and still name a commit.
const minSha = 7

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
// queried sha agree as prefixes either way, both at least minSha characters,
// and the checks agree with whitespace collapsed.
func speaksFor(rowSha, rowCheck, sha, check string) bool {
	sha = one(sha)
	if len(sha) < minSha || len(rowSha) < minSha || one(rowCheck) != one(check) {
		return false
	}
	return strings.HasPrefix(sha, rowSha) || strings.HasPrefix(rowSha, sha)
}

// Lookup reads the latest result for a mark from stored proof text.
func Lookup(content, sha, check string) (string, bool) {
	rows, _, _ := parseRows(content)
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
		lines := strings.Split(content, "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			l := lines[i]
			if !strings.HasPrefix(l, "- ") {
				continue
			}
			p := strings.SplitN(l[2:], sep, 5)
			if len(p) < 5 || p[2] != "PASS" || !speaksFor(p[3], p[1], sha, check) {
				continue
			}
			found = true
			if strings.Contains(" "+p[4]+" ", " "+mark+" ") {
				return content, false, nil
			}
			lines[i] = l + " " + mark
			return strings.Join(lines, "\n"), true, nil
		}
		return content, false, nil
	})
	return found, err
}
