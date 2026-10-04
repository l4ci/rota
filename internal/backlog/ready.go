package backlog

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/section"
)

var (
	acceptHeadingRe = regexp.MustCompile(`(?mi)^#{1,6}[ \t]+.*acceptance`)
	checkboxRe      = regexp.MustCompile(`(?m)^[ \t]*[-*][ \t]+\[[ xX]\]`)
)

// hasCriteria is _has_criteria: a heading that mentions acceptance, or a
// markdown checkbox line.
func hasCriteria(text string) bool {
	return acceptHeadingRe.MatchString(text) || checkboxRe.MatchString(text)
}

// readyReasons is _ready_reasons: ready when there are criteria or a note.
// The wording says "issue body" in file mode too, where the criteria live in
// the detail file: the old helper printed exactly this in both backends, and
// item ready's data.reasons carries it verbatim (the shim parses those lines),
// so changing it is a contract change, not a port detail.
func readyReasons(criteria, note bool) []string {
	if criteria || note {
		return []string{}
	}
	return []string{"no acceptance criteria in the issue body", "no design or plan note"}
}

// Ready lists what the item lacks to be startable (FileBackend.ready_reasons):
// acceptance criteria in the detail file, or a design (.rota/designs/<ID>.md) or
// plan (.rota/plans/*-<ID>.md) note. An empty list means ready.
func (f *File) Ready(ref string) ([]string, error) {
	if _, _, ok := FindOrigin(f.Corpus(), ref); !ok {
		return nil, errf(ErrNotFound, "[%s] not found in BACKLOG.md or ARCHIVE.md", ref)
	}
	note := false
	if _, err := os.Stat(f.rota("designs", ref+".md")); err == nil {
		note = true
	}
	if !note {
		if entries, err := os.ReadDir(f.rota("plans")); err == nil {
			for _, e := range entries {
				note = note || strings.HasSuffix(e.Name(), "-"+ref+".md")
			}
		}
	}
	text, _, _ := f.Detail(ref)
	return readyReasons(hasCriteria(text), note), nil
}

func validCommentKind(kind string) bool {
	for _, k := range CommentKinds {
		if k == kind {
			return true
		}
	}
	return false
}

func commentKindErr() error {
	return errf(ErrInvalid, "comment kind must be one of %s", strings.Join(CommentKinds, "/"))
}

// parseLogRow is `- (\S+) · (\w+) · (.*)` over one line of the Log section.
func parseLogRow(line string) (who, kind, text string, ok bool) {
	rest, found := strings.CutPrefix(line, "- ")
	if !found {
		return
	}
	i := 0
	for i < len(rest) {
		r, n := decodeRune(rest[i:])
		if pystr.IsSpace(r) {
			break
		}
		i += n
	}
	if i == 0 {
		return
	}
	who = rest[:i]
	rest, found = strings.CutPrefix(rest[i:], " · ")
	if !found {
		return "", "", "", false
	}
	j := 0
	for j < len(rest) {
		r, n := decodeRune(rest[j:])
		if !pystr.IsWord(r) {
			break
		}
		j += n
	}
	if j == 0 {
		return "", "", "", false
	}
	kind = rest[:j]
	text, found = strings.CutPrefix(rest[j:], " · ")
	if !found {
		return "", "", "", false
	}
	return who, kind, text, true
}

// Comments lists the rows of the detail file's ## Log section, oldest first,
// with the date as Who (FileBackend.comments_list); empty when there is no
// log. kind "" lists all kinds.
func (f *File) Comments(ref, kind string) ([]Comment, error) {
	if kind != "" && !validCommentKind(kind) {
		return nil, commentKindErr()
	}
	if _, _, ok := FindOrigin(f.Corpus(), ref); !ok {
		return nil, errf(ErrNotFound, "[%s] not found in BACKLOG.md or ARCHIVE.md", ref)
	}
	content, _, _ := f.Detail(ref)
	out := []Comment{}
	s, e, ok := section.Find(content, "Log")
	if content == "" || !ok {
		return out, nil
	}
	var rows []Comment
	for _, line := range strings.Split(content[s:e], "\n") {
		if who, k, text, ok := parseLogRow(line); ok {
			rows = append(rows, Comment{Who: who, Kind: k, Text: text})
		} else if len(rows) > 0 && strings.HasPrefix(line, "  ") {
			rows[len(rows)-1].Text += "\n" + line[2:]
		} else if len(rows) > 0 && pystr.Strip(line) == "" {
			rows[len(rows)-1].Text += "\n"
		}
	}
	for _, r := range rows {
		r.Text = strings.TrimRight(r.Text, "\n")
		if kind == "" || kind == r.Kind {
			out = append(out, r)
		}
	}
	return out, nil
}

// AddComment appends `- <date> · <kind> · <first line>` (continuation lines
// indented two spaces) under ## Log in the item's detail file, creating the
// file when missing (FileBackend.comment_add). The returned id is always "".
func (f *File) AddComment(ref, kind, text string) (string, error) {
	if !validCommentKind(kind) {
		return "", commentKindErr()
	}
	dir := detailDir(ref)
	if dir == "" {
		return "", errf(ErrNotFound, "[%s] has no detail directory (expected B/F/T prefix)", ref)
	}
	_, title, ok := FindOrigin(f.Corpus(), ref)
	if !ok {
		return "", errf(ErrNotFound, "[%s] not found in BACKLOG.md or ARCHIVE.md", ref)
	}
	if title == "" {
		title = ref
	}
	lines := strings.Split(strings.Trim(strings.ReplaceAll(text, "\r\n", "\n"), "\n"), "\n")
	row := "- " + time.Now().Format("2006-01-02") + " · " + kind + " · " + pystr.Strip(lines[0]) + "\n"
	for _, l := range lines[1:] {
		if pystr.Strip(l) != "" {
			row += "  " + l + "\n"
		} else {
			row += "\n"
		}
	}
	path := f.rota(dir, ref+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		return "", err
	}
	err := fsio.Locked(path, fsio.LockTimeout, func() error {
		content, err := fsio.ReadText(path)
		if errors.Is(err, os.ErrNotExist) {
			content = "# " + ref + ": " + title + "\n\n> Related TODO entry: `[" + ref + "]` in `.rota/BACKLOG.md`\n"
		} else if err != nil {
			return err
		}
		var out string
		if _, _, has := section.Find(content, "Log"); has {
			out = section.Append(strings.TrimRight(content, "\n")+"\n", "Log", row)
		} else {
			out = strings.TrimRight(content, "\n") + "\n\n## Log\n\n" + row
		}
		return fsio.WriteFileAtomic(path, []byte(out))
	})
	return "", err
}
