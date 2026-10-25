package extknowledge

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// indexRe is one INDEX.md entry: "- [Name](topics/slug.md) — hook".
var indexRe = regexp.MustCompile(`^- \[([^\]]+)\]\(topics/([^)/]+)\.md\)(?:\s+—\s+(.*))?\s*$`)

// Topics lists the INDEX.md entries, with each topic file's bullet count.
func (c Clone) Topics() ([]Topic, error) {
	raw, err := os.ReadFile(filepath.Join(c.Dir, IndexFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s has no %s", ErrNotFound, c.Dir, IndexFile)
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
		if body, err := os.ReadFile(c.topicPath(t.Slug)); err == nil {
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

// Query prints "## Name" sections for the requested topics, in argument
// order. missing lists the requests that matched no index entry.
func (c Clone) Query(want []string) (text string, missing []string, err error) {
	topics, err := c.Topics()
	if err != nil {
		return "", nil, err
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
			body, err := os.ReadFile(c.topicPath(t.Slug))
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue // indexed but not written yet: nothing to print
				}
				return "", nil, err
			}
			parts = append(parts, "## "+t.Name+"\n"+sectionBody(string(body)))
		}
	}
	if len(parts) > 0 {
		text = strings.Join(parts, "\n") + ""
	}
	return text, missing, nil
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

// Policy reads rota-knowledge.json. A missing file is the default, push to
// main, with Defaulted set so the caller can say so.
func (c Clone) Policy() (Policy, error) {
	raw, err := os.ReadFile(filepath.Join(c.Dir, PolicyFile))
	if errors.Is(err, os.ErrNotExist) {
		return Policy{Write: "push", Branch: "main", Defaulted: true}, nil
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
	if p.Branch == "" {
		p.Branch = "main"
	}
	return Policy{Write: p.Write, Branch: p.Branch}, nil
}
