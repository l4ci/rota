package extknowledge

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/l4ci/rota/internal/bulletfmt"
	"github.com/l4ci/rota/internal/toolver"
	"github.com/l4ci/rota/internal/topicmatch"
)

// indexNamePattern is the unescaped label the index format can represent.
// Writers validate against the same grammar the root and sub-index readers use.
const indexNamePattern = `[^\]]+`

var indexNameRe = regexp.MustCompile(`^` + indexNamePattern + `$`)

func validIndexName(name string) bool {
	return !strings.ContainsAny(name, "\r\n") && indexNameRe.MatchString(name)
}

// indexRe is one index entry: "- [Name](topics/slug.md) — hook", or for a
// split topic "- [Name](topics/slug/README.md) — hook".
var indexRe = regexp.MustCompile(`^- \[(` + indexNamePattern + `)\]\(topics/([a-z0-9-]+)(\.md|/README\.md)\)(?:\s+—\s+(.*))?\s*$`)

// looseIndexRe is any index line that points into topics/. One that indexRe
// rejects names a path outside the slugs rota writes (a glob, "..", capitals):
// it is ignored, never followed, and reported by Malformed.
var looseIndexRe = regexp.MustCompile(`^- \[[^\]]+\]\(topics/`)

// slugOK is a path stem Slugify could have produced.
var slugOK = regexp.MustCompile(`^[a-z0-9-]+$`)

// Topics lists the index entries, with each topic file's bullet count. A
// split topic is followed by its subtopics, named "Topic: Subtopic". A flat
// file an older rota left beside a split topic is its Stray, never a second
// topic.
func (c Clone) Topics() ([]Topic, error) {
	ts, _, err := c.scan()
	return ts, err
}

// Malformed lists the index and sub-index lines that name a path outside the
// slugs rota writes, each as "file: line". Topics ignores them and no write
// follows them.
func (c Clone) Malformed() []string {
	_, bad, _ := c.scan()
	return bad
}

func (c Clone) scan() ([]Topic, []string, error) {
	var bad []string
	raw, err := c.readRegular(filepath.Join(c.Dir, c.indexName()))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, fmt.Errorf("%w: %s has no %s", ErrNotFound, c.Dir, c.indexName())
		}
		return nil, nil, err
	}
	var roots []Topic
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimRight(line, " \t\r")
		m := indexRe.FindStringSubmatch(line)
		if m == nil {
			if looseIndexRe.MatchString(line) {
				bad = append(bad, c.indexName()+": "+line)
			}
			continue
		}
		t := Topic{Name: strings.TrimSpace(m[1]), Slug: m[2], Hook: strings.TrimSpace(m[4]), File: TopicsDir + "/" + m[2] + m[3], Split: m[3] != ".md"}
		merged := false
		for i := range roots {
			if roots[i].Slug != t.Slug || roots[i].Split == t.Split {
				continue
			}
			if t.Split {
				t.Stray, roots[i] = roots[i].File, t
			} else {
				roots[i].Stray = t.File
			}
			merged = true
			break
		}
		if !merged {
			roots = append(roots, t)
		}
	}
	var out []Topic
	for _, t := range roots {
		body, err := c.readRegular(filepath.Join(c.Dir, filepath.FromSlash(t.File)))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, nil, err
		}
		if err == nil {
			if t.Split {
				t.Bullets = len(t.bullets(string(body)))
				var sbad []string
				t.Subtopics, sbad = subtopics(t, string(body))
				for _, l := range sbad {
					bad = append(bad, t.File+": "+l)
				}
				for i := range t.Subtopics {
					st := &t.Subtopics[i]
					sb, err := c.readRegular(filepath.Join(c.Dir, filepath.FromSlash(st.File)))
					if err != nil && !errors.Is(err, os.ErrNotExist) {
						return nil, nil, err
					}
					if err == nil {
						st.Bullets = bulletCount(string(sb))
					}
				}
			} else {
				t.Bullets = bulletCount(string(body))
			}
		}
		if t.Split && t.Stray == "" {
			// An older rota's flat file beside the folder, with or without a root line.
			flat := TopicsDir + "/" + t.Slug + ".md"
			if fi, err := os.Lstat(filepath.Join(c.Dir, filepath.FromSlash(flat))); err == nil && fi.Mode().IsRegular() {
				t.Stray = flat
			}
		}
		out = append(out, t)
		out = append(out, t.Subtopics...)
	}
	return out, bad, nil
}

// subtopics reads the sub-index lines of a split topic's README; bad holds
// the lines whose path is not a plain slug.
func subtopics(parent Topic, readme string) (out []Topic, bad []string) {
	dir := strings.TrimSuffix(parent.File, "/README.md")
	for _, l := range strings.Split(readme, "\n") {
		m := subIndexRe.FindStringSubmatch(strings.TrimRight(l, " \t\r"))
		if m == nil || strings.EqualFold(m[2], "README") {
			continue
		}
		if !slugOK.MatchString(m[2]) {
			bad = append(bad, strings.TrimRight(l, " \t\r"))
			continue
		}
		sub := strings.TrimPrefix(strings.TrimSpace(m[1]), parent.Name+": ")
		name := parent.Name + ": " + sub
		out = append(out, Topic{Name: name, Slug: Slugify(name), Hook: strings.TrimSpace(m[3]), File: dir + "/" + m[2] + ".md", Parent: parent.Name})
	}
	return out, bad
}

// bulletCount is the number of bullets of a topic file.
func bulletCount(body string) int {
	return len(bulletfmt.Spans(strings.Split(body, "\n")))
}

// resolve maps a requested topic to the index entries it names for a read;
// the rule is topicmatch.ReadIndex.
func resolve(want string, topics []Topic) []Topic {
	var out []Topic
	for _, i := range topicmatch.ReadIndex(want, entries(topics)) {
		out = append(out, topics[i])
	}
	return out
}

// entries is the name/slug view of topics the matchers work on.
func entries(topics []Topic) []topicmatch.Entry {
	out := make([]topicmatch.Entry, len(topics))
	for i, t := range topics {
		out[i] = topicmatch.Entry{Name: t.Name, Slug: t.Slug}
	}
	return out
}

// Recheck is one tool a bullet was seen on that the project has since outgrown.
type Recheck struct{ Topic, Title, Tool, Seen, Project string }

// QueryWith prints "## Name" sections for the requested topics, in argument
// order. Missing lists the requests that matched no index entry. A bullet seen
// on an older major.minor of a tool than opts.Project gets a recheck marker
// before its stamp and a row in Recheck; bullets are never hidden and a bullet
// with nothing outgrown prints as stored.
// A split topic prints its README (sub-index and any bullets not yet given a
// subtopic); All adds a section per subtopic. A subtopic named on its own prints
// only its file. A stray flat file beside a split topic prints under
// "## Name (stray: file)" and is listed in Strays.
func (c Clone) QueryWith(want []string, opts QueryOpts) (QueryResult, error) {
	if len(want) == 0 {
		return QueryResult{}, nil // the walk reads that as every topic
	}
	w, err := c.walk(want, opts.All)
	if err != nil {
		return QueryResult{}, err
	}
	res := QueryResult{Missing: w.Missing, Strays: w.Strays}
	var parts []string
	for _, f := range w.Files {
		file, rows, err := markRecheck(f.Topic.Name, f.Content, opts.Project)
		if err != nil {
			return QueryResult{}, err
		}
		for _, r := range rows {
			res.Recheck = append(res.Recheck, r.Recheck)
		}
		parts = append(parts, "## "+f.heading()+"\n"+sectionBody(file))
	}
	res.Text = strings.Join(parts, "\n")
	return res, nil
}

// lineRecheck is a recheck row and the zero-based line of its bullet.
type lineRecheck struct {
	line int
	Recheck
}

// markRecheck inserts the recheck marker before the stamp of every bullet of
// file that was seen on an older version than project's, and lists them.
func markRecheck(topic, file string, project map[string]string) (string, []lineRecheck, error) {
	if len(project) == 0 {
		return file, nil, nil
	}
	lines := strings.Split(file, "\n")
	var rows []lineRecheck
	for _, b := range parseBullets(file) {
		if b.SeenErr != nil {
			return "", nil, fmt.Errorf("topic %s bullet %q: bad seen stamp: %w", topic, b.Title, b.SeenErr)
		}
		var notes []string
		for _, tv := range b.Seen {
			if pv, ok := project[tv.Tool]; ok && toolver.Newer(tv.Version, pv) {
				notes = append(notes, fmt.Sprintf("seen on %s %s, project on %s", tv.Tool, tv.Version, pv))
				rows = append(rows, lineRecheck{b.Line, Recheck{Topic: topic, Title: b.Title, Tool: tv.Tool, Seen: tv.Version, Project: pv}})
			}
		}
		if len(notes) == 0 {
			continue
		}
		l := lines[b.Line]
		lines[b.Line] = strings.TrimSuffix(l, b.Stamp) + " (" + strings.Join(notes, "; ") + ": recheck)" + strings.TrimRight(b.Stamp, " \t\r")
	}
	return strings.Join(lines, "\n"), rows, nil
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

// QueryOpts tunes QueryWith.
type QueryOpts struct {
	All     bool              // a split topic also prints every subtopic
	Project map[string]string // tool versions the project runs
}

// StrayRef is a flat file left beside a split topic.
type StrayRef struct{ Topic, File string }

// QueryResult is what QueryWith found.
type QueryResult struct {
	Text    string
	Missing []string
	Recheck []Recheck
	Strays  []StrayRef
}

// MatchResult is what MatchWith found.
type MatchResult struct {
	Text    string
	N       int
	Missing []string
	Recheck []Recheck // of the printed bullets only
	Strays  []StrayRef
}

// MatchWith prints "## Name" sections holding only the bullets (with their
// continuation lines) whose title or body contain every word,
// case-insensitively. Words are literal substrings; the stamp never matches.
// With no topics it searches every topic and subtopic in index order; with
// topics it searches those (a split parent includes its subtopics, and a
// stray flat file beside a split topic is searched too). A matched bullet
// carries the recheck marker as QueryWith prints it. N counts the bullets;
// Missing and Strays are as QueryWith reports them.
func (c Clone) MatchWith(words []string, want []string, project map[string]string) (MatchResult, error) {
	var ws []string
	for _, w := range words {
		ws = append(ws, strings.Fields(strings.ToLower(w))...)
	}
	if len(ws) == 0 {
		return MatchResult{}, fmt.Errorf("%w: --match needs at least one word", ErrUsage)
	}
	w, err := c.walk(want, true)
	if err != nil {
		return MatchResult{}, err
	}
	res := MatchResult{Missing: w.Missing, Strays: w.Strays}
	var parts []string
	for _, f := range w.Files {
		marked, rows, err := markRecheck(f.Topic.Name, f.Content, project)
		if err != nil {
			return MatchResult{}, err
		}
		orig, out := strings.Split(f.Content, "\n"), strings.Split(marked, "\n")
		var lines []string
		for _, b := range f.bullets() {
			end := bulletfmt.End(orig, b.Line)
			if !hasAll(bulletText(b, orig[b.Line:end]), ws) {
				continue
			}
			lines = append(lines, out[b.Line:end]...)
			res.N++
			for _, r := range rows {
				if r.line == b.Line {
					res.Recheck = append(res.Recheck, r.Recheck)
				}
			}
		}
		if len(lines) > 0 {
			parts = append(parts, "## "+f.heading()+"\n"+strings.Join(lines, "\n")+"\n")
		}
	}
	res.Text = strings.Join(parts, "\n")
	return res, nil
}

// bulletText is what --match searches: the title and body of the bullet,
// continuation lines included, without the final stamp, lowercased.
func bulletText(b bullet, unit []string) string {
	s := strings.Join(unit, "\n")
	s, _ = bulletfmt.SplitStamp(s)
	if b.Title != "" && strings.HasPrefix(s, "- **") {
		if end := strings.Index(s[4:], "**"); end >= 0 { // the padded title too
			s = s[:2] + b.Title + s[4+end+2:]
		}
	}
	return strings.ToLower(strings.TrimPrefix(s, "- "))
}

func hasAll(s string, words []string) bool {
	for _, w := range words {
		if !strings.Contains(s, w) {
			return false
		}
	}
	return true
}
