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

// indexRe is one index entry: "- [Name](topics/slug.md) — hook", or for a
// split topic "- [Name](topics/slug/README.md) — hook".
var indexRe = regexp.MustCompile(`^- \[([^\]]+)\]\(topics/([a-z0-9-]+)(\.md|/README\.md)\)(?:\s+—\s+(.*))?\s*$`)

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
					if sb, err := c.readRegular(filepath.Join(c.Dir, filepath.FromSlash(st.File))); err == nil {
						st.Bullets = len(bulletLines(string(sb)))
					}
				}
			} else {
				t.Bullets = len(bulletLines(string(body)))
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
	res, err := c.QueryWith(want, QueryOpts{Project: project})
	if err != nil {
		return "", nil, nil, err
	}
	return res.Text, res.Missing, res.Recheck, nil
}

// QueryWith is Query with options. A split topic prints its README (sub-index
// and any bullets not yet given a subtopic); All adds a section per subtopic.
// A subtopic named on its own prints only its file. A stray flat file beside a
// split topic prints under "## Name (stray: file)" and is listed in Strays.
func (c Clone) QueryWith(want []string, opts QueryOpts) (QueryResult, error) {
	topics, err := c.Topics()
	if err != nil {
		return QueryResult{}, err
	}
	var res QueryResult
	var parts []string
	printed := map[string]bool{}
	section := func(heading, rel, name string) error {
		if printed[rel] {
			return nil
		}
		printed[rel] = true
		body, err := c.readRegular(filepath.Join(c.Dir, filepath.FromSlash(rel)))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil // indexed but not written yet: nothing to print
			}
			return err
		}
		file, rows := markRecheck(name, string(body), opts.Project)
		res.Recheck = append(res.Recheck, rows...)
		parts = append(parts, "## "+heading+"\n"+sectionBody(file))
		return nil
	}
	for _, w := range want {
		hit := resolve(w, topics)
		if len(hit) == 0 {
			res.Missing = append(res.Missing, w)
			continue
		}
		for _, t := range hit {
			if err := section(t.Name, t.File, t.Name); err != nil {
				return QueryResult{}, err
			}
			if t.Stray != "" && !printed[t.Stray] {
				if err := section(t.Name+" (stray: "+t.Stray+")", t.Stray, t.Name); err != nil {
					return QueryResult{}, err
				}
				res.Strays = append(res.Strays, StrayRef{Topic: t.Name, File: t.Stray})
			}
			if opts.All {
				for _, st := range t.Subtopics {
					if err := section(st.Name, st.File, st.Name); err != nil {
						return QueryResult{}, err
					}
				}
			}
		}
	}
	if len(parts) > 0 {
		res.Text = strings.Join(parts, "\n")
	}
	return res, nil
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

// QueryOpts tunes Query.
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
}

// Match prints "## Name" sections holding only the bullets (with their
// continuation lines) whose title or body contain every word,
// case-insensitively. Words are literal substrings; the stamp never matches.
// With no topics it searches every topic and subtopic in index order; with
// topics it searches those (a split parent includes its subtopics, and a
// stray flat file beside a split topic is searched too). A matched bullet
// carries the recheck marker as Query prints it. n counts the bullets.
func (c Clone) Match(words []string, topics []string, project map[string]string) (text string, n int, err error) {
	res, err := c.MatchWith(words, topics, project)
	return res.Text, res.N, err
}

// MatchWith is Match that also lists the requested topics that named no index entry.
func (c Clone) MatchWith(words []string, want []string, project map[string]string) (MatchResult, error) {
	var ws []string
	for _, w := range words {
		ws = append(ws, strings.Fields(strings.ToLower(w))...)
	}
	if len(ws) == 0 {
		return MatchResult{}, fmt.Errorf("%w: --match needs at least one word", ErrUsage)
	}
	all, err := c.Topics()
	if err != nil {
		return MatchResult{}, err
	}
	var res MatchResult
	var parts []string
	seen := map[string]bool{}
	section := func(heading, rel, name string, split bool) error {
		if seen[rel] {
			return nil
		}
		seen[rel] = true
		raw, err := c.readRegular(filepath.Join(c.Dir, filepath.FromSlash(rel)))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		file := string(raw)
		marked, _ := markRecheck(name, file, project)
		orig, out := strings.Split(file, "\n"), strings.Split(marked, "\n")
		var lines []string
		for _, b := range parseBullets(file) {
			if split && isSubIndexLine(orig[b.Line]) {
				continue
			}
			end := b.Line + 1
			for end < len(orig) && orig[end] != "" && (orig[end][0] == ' ' || orig[end][0] == '\t') {
				end++
			}
			if !hasAll(bulletText(b, orig[b.Line:end]), ws) {
				continue
			}
			lines = append(lines, out[b.Line:end]...)
			res.N++
		}
		if len(lines) > 0 {
			parts = append(parts, "## "+heading+"\n"+strings.Join(lines, "\n")+"\n")
		}
		return nil
	}
	withStray := func(t Topic) error {
		if err := section(t.Name, t.File, t.Name, t.Split); err != nil {
			return err
		}
		if t.Stray != "" {
			return section(t.Name+" (stray: "+t.Stray+")", t.Stray, t.Name, false)
		}
		return nil
	}
	if len(want) == 0 {
		for _, t := range all {
			if err := withStray(t); err != nil {
				return MatchResult{}, err
			}
		}
	}
	for _, w := range want {
		hit := resolve(w, all)
		if len(hit) == 0 {
			res.Missing = append(res.Missing, w)
			continue
		}
		for _, t := range hit {
			if err := withStray(t); err != nil {
				return MatchResult{}, err
			}
			for _, st := range t.Subtopics {
				if err := withStray(st); err != nil {
					return MatchResult{}, err
				}
			}
		}
	}
	res.Text = strings.Join(parts, "\n")
	return res, nil
}

// bulletText is what --match searches: the title and body of the bullet,
// continuation lines included, without the final stamp, lowercased.
func bulletText(b bullet, unit []string) string {
	s := strings.Join(unit, "\n")
	s = strings.TrimSuffix(s, stampTail.FindString(s))
	if b.Title != "" {
		s = strings.Replace(s, "**"+b.Title+"**", b.Title, 1)
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
