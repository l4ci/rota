package extknowledge

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/l4ci/rota/internal/toolver"
)

// indexRe is one index entry: "- [Name](topics/slug.md) — hook".
var indexRe = regexp.MustCompile(`^- \[([^\]]+)\]\(topics/([^)/]+)\.md\)(?:\s+—\s+(.*))?\s*$`)

// Topics lists the index entries, with each topic file's bullet count.
func (c Clone) Topics() ([]Topic, error) {
	raw, err := c.readRegular(filepath.Join(c.Dir, c.indexName()))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s has no %s", ErrNotFound, c.Dir, c.indexName())
		}
		return nil, err
	}
	var out []Topic
	for _, line := range strings.Split(string(raw), "\n") {
		m := indexRe.FindStringSubmatch(strings.TrimRight(line, " \t\r"))
		if m == nil {
			continue
		}
		t := Topic{Name: strings.TrimSpace(m[1]), Slug: m[2], Hook: strings.TrimSpace(m[3])}
		if body, err := c.readRegular(c.topicPath(t.Slug)); err == nil {
			t.Bullets = len(bulletLines(string(body)))
		}
		out = append(out, t)
	}
	return out, nil
}

func (c Clone) topicPath(slug string) string {
	return filepath.Join(c.Dir, TopicsDir, slug+".md")
}

// bulletLines are the "- " lines of a topic file.
func bulletLines(body string) []string {
	var out []string
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(l, "- ") {
			out = append(out, l)
		}
	}
	return out
}

// resolve maps a requested topic to the index entries it names: the entry
// whose name or slug equals want (case-insensitive) alone when there is one,
// else every entry whose name contains want.
func resolve(want string, topics []Topic) []Topic {
	w := strings.ToLower(strings.TrimSpace(want))
	if w == "" {
		return nil
	}
	var partial []Topic
	for _, t := range topics {
		if strings.ToLower(t.Name) == w || t.Slug == w {
			return []Topic{t}
		}
		if strings.Contains(strings.ToLower(t.Name), w) {
			partial = append(partial, t)
		}
	}
	return partial
}

// Recheck is one tool a bullet was seen on that the project has since outgrown.
type Recheck struct{ Topic, Title, Tool, Seen, Project string }

// Query prints "## Name" sections for the requested topics, in argument
// order. missing lists the requests that matched no index entry. A bullet seen
// on an older major.minor of a tool than project (tool name to version) gets a
// recheck marker before its stamp and a row in recheck; bullets are never
// hidden and a bullet with nothing outgrown prints as stored.
func (c Clone) Query(want []string, project map[string]string) (text string, missing []string, recheck []Recheck, err error) {
	topics, err := c.Topics()
	if err != nil {
		return "", nil, nil, err
	}
	var parts []string
	printed := map[string]bool{}
	for _, w := range want {
		hit := resolve(w, topics)
		if len(hit) == 0 {
			missing = append(missing, w)
			continue
		}
		for _, t := range hit {
			if printed[t.Slug] {
				continue
			}
			printed[t.Slug] = true
			body, err := c.readRegular(c.topicPath(t.Slug))
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue // indexed but not written yet: nothing to print
				}
				return "", nil, nil, err
			}
			file, rows := markRecheck(t.Name, string(body), project)
			recheck = append(recheck, rows...)
			parts = append(parts, "## "+t.Name+"\n"+sectionBody(file))
		}
	}
	if len(parts) > 0 {
		text = strings.Join(parts, "\n")
	}
	return text, missing, recheck, nil
}

// markRecheck inserts the recheck marker before the stamp of every bullet of
// file that was seen on an older version than project's, and lists them.
func markRecheck(topic, file string, project map[string]string) (string, []Recheck) {
	if len(project) == 0 {
		return file, nil
	}
	lines := strings.Split(file, "\n")
	var rows []Recheck
	for _, b := range parseBullets(file) {
		var notes []string
		for _, tv := range b.Seen {
			if pv, ok := project[tv.Tool]; ok && toolver.Newer(tv.Version, pv) {
				notes = append(notes, fmt.Sprintf("seen on %s %s, project on %s", tv.Tool, tv.Version, pv))
				rows = append(rows, Recheck{Topic: topic, Title: b.Title, Tool: tv.Tool, Seen: tv.Version, Project: pv})
			}
		}
		if len(notes) == 0 {
			continue
		}
		l := lines[b.Line]
		lines[b.Line] = strings.TrimSuffix(l, b.Stamp) + " (" + strings.Join(notes, "; ") + ": recheck)" + strings.TrimRight(b.Stamp, " \t\r")
	}
	return strings.Join(lines, "\n"), rows
}

// sectionBody is a topic file without its "# Name" title line, trimmed, with
// one trailing newline.
func sectionBody(file string) string {
	lines := strings.Split(file, "\n")
	for len(lines) > 0 && (strings.HasPrefix(lines[0], "# ") || strings.TrimSpace(lines[0]) == "") {
		lines = lines[1:]
	}
	return strings.TrimRight(strings.Join(lines, "\n"), " \t\r\n") + "\n"
}

// branchRe admits plain branch names only: no leading dash (a git option), no
// spaces or shell-ish characters.
var branchRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

// Policy reads rota-knowledge.json. A missing file is the default, push, with
// Defaulted set so the caller can say so. Branch stays empty when the policy
// omits it: a write then targets the clone's own branch.
func (c Clone) Policy() (Policy, error) {
	raw, err := c.readRegular(filepath.Join(c.Dir, PolicyFile))
	if errors.Is(err, os.ErrNotExist) {
		return Policy{Write: "push", Defaulted: true}, nil
	}
	if err != nil {
		return Policy{}, err
	}
	var p struct {
		Write  string `json:"write"`
		Branch string `json:"branch"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return Policy{}, fmt.Errorf("%w: %s: %v", ErrPolicy, PolicyFile, err)
	}
	if p.Write == "" {
		p.Write = "push"
	}
	if p.Write != "push" && p.Write != "pr" {
		return Policy{}, fmt.Errorf("%w: %s: write must be push or pr, got %q", ErrPolicy, PolicyFile, p.Write)
	}
	if p.Branch != "" && (!branchRe.MatchString(p.Branch) || strings.Contains(p.Branch, "..")) {
		return Policy{}, fmt.Errorf("%w: %s: branch %q is not a plain branch name", ErrPolicy, PolicyFile, p.Branch)
	}
	return Policy{Write: p.Write, Branch: p.Branch}, nil
}

// readRegular reads a clone file, refusing a non-regular file and any path
// whose symlinks resolve outside the clone, so a hostile knowledge repo cannot
// point a topic (or the topics directory) at a file elsewhere.
func (c Clone) readRegular(p string) ([]byte, error) {
	fi, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", p)
	}
	root, err := filepath.EvalSymlinks(c.Dir)
	if err != nil {
		return nil, err
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return nil, err
	}
	if rel, err := filepath.Rel(root, real); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("%s resolves outside the knowledge clone", p)
	}
	return os.ReadFile(p)
}

// insideClone refuses a directory whose symlinks resolve outside the clone.
func (c Clone) insideClone(dir string) error {
	root, err := filepath.EvalSymlinks(c.Dir)
	if err != nil {
		return err
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	if rel, err := filepath.Rel(root, real); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s resolves outside the knowledge clone", dir)
	}
	return nil
}
