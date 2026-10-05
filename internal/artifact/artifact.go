// Package artifact holds what the A6 file-mode verbs (milestone, plan,
// design, spike, proof, debug counter) share: the exit-coded error their
// packages return. It does not import internal/cli; internal/cli's A6 glue
// maps Error to the exit table.
package artifact

import (
	"errors"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/repos"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/l4ci/rota/internal/frontmatter"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/section"
)

// ReadBody reads a --body-file ("-" is stdin) the way the old put helpers
// did: raw bytes, each invalid UTF-8 byte replaced by U+FFFD, no newline
// normalisation. An unreadable file is a usage error.
func ReadBody(stdin io.Reader, path string) (string, error) {
	var b []byte
	var err error
	if path == "-" {
		b, err = io.ReadAll(stdin)
	} else {
		b, err = os.ReadFile(path)
	}
	if err != nil {
		return "", exitcode.Errf(exitcode.ExitUsage, "cannot read --body-file %s: %v", path, unwrap(err))
	}
	return string([]rune(string(b))), nil
}

func unwrap(err error) error {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

// Doc is one markdown file with parseable frontmatter.
type Doc struct {
	Stem string // file name without .md
	FM   map[string]any
}

// ListDocs globs dir/*.md in name order and returns the files that carry
// frontmatter, like hv-fm-list. A missing dir is an empty list.
func ListDocs(dir string) ([]Doc, error) {
	files, _ := filepath.Glob(filepath.Join(dir, "*.md"))
	sort.Strings(files)
	docs := []Doc{}
	for _, f := range files {
		text, err := fsio.ReadText(f)
		if err != nil {
			return nil, err
		}
		if fm, _, _ := frontmatter.Parse(text); fm != nil {
			docs = append(docs, Doc{Stem: strings.TrimSuffix(filepath.Base(f), ".md"), FM: fm})
		}
	}
	return docs, nil
}

// Repos is the sub-repo registry, name to absolute path, from
// .rota/repos.json (internal/repos).
func Repos(root string) map[string]string { return repos.Paths(root) }

// SplitCSV splits a comma list, trimming blanks and dropping empties
// (hvlib_repos.parse_repos_csv).
func SplitCSV(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// AppendSection splices addition into the body of "## <name>" just before
// the next "## " heading (or at EOF), like hvlib_section.append_to_section:
// the body gains a newline first if it lacks one. A missing section is
// appended at the end as "## <name>\n<addition>". ok reports whether the
// section existed.
func AppendSection(content, name, addition string) (updated string, ok bool) {
	start, end, ok := section.Find(content, name)
	if !ok {
		return strings.TrimRight(content, "\n") + "\n\n## " + name + "\n" + addition, false
	}
	body := content[start:end]
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return content[:start] + body + addition + content[end:], true
}

// Notes is the durable-note store of an issue-mode backlog: a note is kept
// as comments on an item's issue (kind proof, design, plan, or plan:S<NN> on
// a milestone's tracking issue). backlog.Issues implements it; the issue-mode
// halves of design, plan and proof take this narrow view of it.
type Notes interface {
	NoteGet(ref, kind string) (text string, ok bool, err error)
	NotePut(ref, kind, text string) (changed bool, err error)
	NoteRm(ref, kind string) (removed bool, err error)
}
