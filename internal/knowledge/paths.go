package knowledge

import (
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/l4ci/rota/internal/section"
)

var pathsField = regexp.MustCompile(`<!-- paths: (.*?) -->`)

// PathField encodes optional path scope without changing the dated bullet
// format. Patterns use slash-separated path.Match syntax; ** spans directories.
func PathField(patterns []string) (string, error) {
	if len(patterns) == 0 {
		return "", nil
	}
	for _, p := range patterns {
		if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") || strings.Contains(p, "-->") {
			return "", fmt.Errorf("invalid relative path glob %q", p)
		}
		for _, part := range strings.Split(p, "/") {
			if part == ".." {
				return "", fmt.Errorf("path glob cannot leave its root: %q", p)
			}
		}
		if _, err := path.Match(p, ""); err != nil {
			return "", fmt.Errorf("invalid path glob %q: %w", p, err)
		}
	}
	b, _ := json.Marshal(patterns)
	return "<!-- paths: " + string(b) + " -->", nil
}

// PathEntry is a complete knowledge bullet or active decision with its source.
type PathEntry struct{ ID, Text string }

func scopedMatch(block, file string) bool {
	m := pathsField.FindStringSubmatch(block)
	if m == nil {
		return false
	}
	var patterns []string
	if json.Unmarshal([]byte(m[1]), &patterns) != nil {
		return false
	}
	if _, err := PathField(patterns); err != nil {
		return false
	}
	for _, pattern := range patterns {
		if matchSegments(strings.Split(pattern, "/"), strings.Split(file, "/")) {
			return true
		}
	}
	return false
}

func matchSegments(pattern, file []string) bool {
	if len(pattern) == 0 {
		return len(file) == 0
	}
	if pattern[0] == "**" {
		return matchSegments(pattern[1:], file) || (len(file) > 0 && matchSegments(pattern, file[1:]))
	}
	if len(file) == 0 {
		return false
	}
	ok, _ := path.Match(pattern[0], file[0])
	return ok && matchSegments(pattern[1:], file[1:])
}

func relativeFile(root, file string) (string, bool) {
	rel, err := filepath.Rel(root, file)
	return filepath.ToSlash(rel), err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// PathEntries reads umbrella and applicable repo-local knowledge, plus the
// umbrella's active decisions. Umbrella patterns are relative to Root; repo
// patterns are relative to the registered repo. No tier or hit files are written.
func (s Store) PathEntries(file string) ([]PathEntry, error) {
	rel, inside := relativeFile(s.Root, file)
	if !inside {
		return nil, nil
	}
	var entries []PathEntry
	readKnowledge := func(scope, relative string) error {
		p, err := s.KnowledgePath(scope)
		if err != nil {
			return err
		}
		content, err := ReadFile(p)
		if err != nil {
			return err
		}
		tiers, err := s.tierMap(scope)
		if err != nil {
			return err
		}
		for _, topic := range section.Topics(content) {
			if topic.Name == GlossaryTopic {
				continue
			}
			for _, loc := range bulletStart.FindAllStringSubmatchIndex(topic.Body, -1) {
				title := topic.Body[loc[2]:loc[3]]
				block := strings.TrimSpace(topic.Body[loc[0]:bulletEnd(topic.Body, loc[0])])
				if tiers[[2]string{topic.Name, title}] != Deprecated && scopedMatch(block, relative) {
					entries = append(entries, PathEntry{p + "::" + topic.Name + "::" + title, "Knowledge: " + topic.Name + "\n" + block})
				}
			}
		}
		return nil
	}
	if err := readKnowledge(Umbrella, rel); err != nil {
		return nil, err
	}
	var names []string
	for name := range s.Repos {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if local, ok := relativeFile(s.Repos[name], file); ok {
			if err := readKnowledge(name, local); err != nil {
				return nil, err
			}
		}
	}
	content, err := ReadFile(s.decisionsPath())
	if err != nil {
		return nil, err
	}
	for _, topic := range section.Topics(ActiveDecisions(content)) {
		// Ignore entry-like headings inside fenced examples.
		var block, title string
		flush := func() {
			if title != "" && scopedMatch(block, rel) {
				entries = append(entries, PathEntry{s.decisionsPath() + "::" + topic.Name + "::" + title, "Decision (hard boundary): " + topic.Name + "\n" + strings.TrimSpace(block)})
			}
		}
		fenced := false
		for _, line := range strings.SplitAfter(topic.Body, "\n") {
			if strings.HasPrefix(line, "```") {
				fenced = !fenced
			}
			if !fenced && strings.HasPrefix(line, "### ") {
				flush()
				title = strings.TrimSpace(strings.TrimPrefix(line, "### "))
				block = ""
			}
			block += line
		}
		flush()
	}
	return entries, nil
}
