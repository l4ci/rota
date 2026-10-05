// Package proof holds the verification proof rows on items (rota proof add,
// show): rows in the "## Proof" section of an item's text, which a Store
// keeps: the item's detail file (Files) or its `proof` note (NewNotes).
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
func Add(s Store, dir, id string, o AddOpts) (row Row, changed bool, err error) {
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
	row = Row{time.Now().Format("2006-01-02"), check, o.Result, sha, evidence}
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
