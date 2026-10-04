// Package proof ports hv-proof-add and hv-proof-show for file mode: proof
// rows in the "## Proof" section of an item's detail file
// (.rota/<bugs|features|tasks>/<ID>.md). Rows are facts, not acceptance.
package proof

import (
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/artifact"
	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/section"
)

const sep = " · "

var idRe = regexp.MustCompile(`^[BFT]\d+$`)

// Row is one proof row.
type Row struct{ Date, Check, Result, Sha, Evidence string }

// kindOf is the detail directory for a valid file-mode ID.
func kindOf(id string) (kind string, err error) {
	if !idRe.MatchString(id) {
		return "", exitcode.Errf(exitcode.ExitUsage, "ID must look like B07, F12 or T03, got %q", id)
	}
	t, _ := backlog.TypeByLetter(id[:1])
	return t.Kind, nil
}

func detailPath(root, kind, id string) string { return filepath.Join(root, ".rota", kind, id+".md") }

func one(s string) string { return strings.Join(strings.Fields(s), " ") }

// AddOpts are Add's arguments; Sha "" means the working repository's HEAD.
type AddOpts struct{ Check, Result, Evidence, Sha string }

// Add appends a proof row for id. changed is false when an identical row
// (check, result, sha, evidence; date ignored) already exists. The returned
// Row carries the whitespace-collapsed values that were stored.
func Add(root, id string, o AddOpts) (row Row, changed bool, err error) {
	kind, err := kindOf(id)
	if err != nil {
		return
	}
	check, evidence := one(o.Check), one(o.Evidence)
	if check == "" || evidence == "" {
		return row, false, exitcode.Errf(exitcode.ExitUsage, "--check and --evidence must not be empty")
	}
	if o.Result != "PASS" && o.Result != "FAIL" {
		return row, false, exitcode.Errf(exitcode.ExitUsage, "--result must be PASS or FAIL")
	}
	f := &backlog.File{Root: root}
	_, title, found := backlog.FindOrigin(f.Corpus(), id)
	if !found {
		return row, false, exitcode.Errf(exitcode.ExitResolution, "[%s] not found in BACKLOG.md or ARCHIVE.md", id)
	}
	if title == "" {
		title = id
	}
	sha := one(o.Sha)
	if sha == "" {
		sha = headSha(root)
	}
	row = Row{time.Now().Format("2006-01-02"), check, o.Result, sha, evidence}
	line := "- " + strings.Join([]string{row.Date, row.Check, row.Result, row.Sha, row.Evidence}, sep)
	key := strings.SplitN(line, sep, 2)[1]

	path := detailPath(root, kind, id)
	err = fsio.Locked(path, fsio.LockTimeout, func() error {
		content, rerr := fsio.ReadText(path)
		if rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
			return exitcode.Errf(exitcode.ExitInternal, "cannot read %s: %v", path, rerr)
		}
		if rerr != nil { // missing: start the detail file
			content = fmt.Sprintf("# %s: %s\n\n> Related TODO entry: `[%s]` in `.rota/BACKLOG.md`\n", id, title, id)
		}
		var updated string
		if s, e, ok := section.Find(content, "Proof"); ok {
			for _, l := range strings.Split(content[s:e], "\n") {
				if strings.HasPrefix(l, "- ") && strings.Contains(l, sep) && strings.SplitN(l, sep, 2)[1] == key {
					return nil
				}
			}
			updated, _ = artifact.AppendSection(strings.TrimRight(content, "\n")+"\n", "Proof", line+"\n")
		} else {
			updated = strings.TrimRight(content, "\n") + "\n\n## Proof\n\n" + line + "\n"
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
			return err
		}
		changed = true
		return fsio.WriteFileAtomic(path, []byte(updated))
	})
	return
}

// Show returns the item's proof rows and their raw lines. A missing detail
// file or "## Proof" section is zero rows, as in the old helper.
func Show(root, id string) (rows []Row, lines []string, err error) {
	kind, err := kindOf(id)
	if err != nil {
		return nil, nil, err
	}
	rows, lines = []Row{}, []string{}
	content, rerr := fsio.ReadText(detailPath(root, kind, id))
	if errors.Is(rerr, fs.ErrNotExist) {
		return
	}
	if rerr != nil {
		return nil, nil, exitcode.Errf(exitcode.ExitInternal, "cannot read %s: %v", detailPath(root, kind, id), rerr)
	}
	return parseRows(content)
}

// headSha is the abbreviated HEAD of the repository at dir, "-" when there is none.
func headSha(dir string) string {
	cmd := exec.Command("git", "log", "-1", "--format=%h")
	cmd.Dir = dir
	out, _ := cmd.Output()
	if sha := one(string(out)); sha != "" {
		return sha
	}
	return "-"
}

// parseRows reads the "- " rows of the "## Proof" section of content.
func parseRows(content string) (rows []Row, lines []string, err error) {
	rows, lines = []Row{}, []string{}
	s, e, ok := section.Find(content, "Proof")
	if !ok {
		return
	}
	for _, l := range strings.Split(content[s:e], "\n") {
		if !strings.HasPrefix(l, "- ") {
			continue
		}
		p := strings.SplitN(l[2:], sep, 5)
		for len(p) < 5 {
			p = append(p, "")
		}
		rows = append(rows, Row{p[0], p[1], p[2], p[3], p[4]})
		lines = append(lines, l)
	}
	return
}
