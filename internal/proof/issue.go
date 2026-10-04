package proof

import (
	"strings"
	"time"

	"github.com/l4ci/rota/internal/artifact"
	"github.com/l4ci/rota/internal/section"
)

// Issue mode: the rows live in the item's `proof` note, in a "## Proof"
// section, with the same row format and the same idempotence as the file.

func validate(o AddOpts) (check, evidence, sha string, err error) {
	check, evidence = one(o.Check), one(o.Evidence)
	if check == "" || evidence == "" {
		return "", "", "", artifact.Errf(artifact.ExitUsage, "--check and --evidence must not be empty")
	}
	if o.Result != "PASS" && o.Result != "FAIL" {
		return "", "", "", artifact.Errf(artifact.ExitUsage, "--result must be PASS or FAIL")
	}
	return check, evidence, one(o.Sha), nil
}

// AddNote appends a proof row to the item's proof note. ref is the item as
// the tracker resolves it. sha "" falls back to the working repository's HEAD
// in dir. changed is false when an identical row already exists.
func AddNote(n artifact.Notes, ref, dir string, o AddOpts) (row Row, changed bool, err error) {
	check, evidence, sha, err := validate(o)
	if err != nil {
		return
	}
	if sha == "" {
		sha = headSha(dir)
	}
	row = Row{time.Now().Format("2006-01-02"), check, o.Result, sha, evidence}
	line := "- " + strings.Join([]string{row.Date, row.Check, row.Result, row.Sha, row.Evidence}, sep)
	key := strings.SplitN(line, sep, 2)[1]

	content, _, err := n.NoteGet(ref, "proof")
	if err != nil {
		return row, false, err
	}
	var updated string
	if s, e, ok := section.Find(content, "Proof"); ok {
		for _, l := range strings.Split(content[s:e], "\n") {
			if strings.HasPrefix(l, "- ") && strings.Contains(l, sep) && strings.SplitN(l, sep, 2)[1] == key {
				return row, false, nil
			}
		}
		updated, _ = artifact.AppendSection(strings.TrimRight(content, "\n")+"\n", "Proof", line+"\n")
	} else {
		body := strings.TrimRight(content, "\n")
		if body != "" {
			body += "\n\n"
		}
		updated = body + "## Proof\n\n" + line + "\n"
	}
	if _, err = n.NotePut(ref, "proof", updated); err != nil {
		return row, false, err
	}
	return row, true, nil
}

// ShowNote returns the proof rows of the item's proof note (none when the
// item has no note).
func ShowNote(n artifact.Notes, ref string) (rows []Row, lines []string, err error) {
	rows, lines = []Row{}, []string{}
	content, ok, err := n.NoteGet(ref, "proof")
	if err != nil || !ok {
		return rows, lines, err
	}
	return parseRows(content)
}
