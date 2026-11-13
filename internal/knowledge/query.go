package knowledge

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/section"
	"github.com/l4ci/rota/internal/topicmatch"
)

// QueryOpts are the filters of Query.
type QueryOpts struct {
	IncludeDeprecated bool
	Tier              string // only bullets on this tier; "" means all
}

// Query prints the requested "## Topic" sections of scope's KNOWLEDGE.md with
// tier-aware filtering: deprecated bullets are hidden unless asked for,
// provisional ones get a " (provisional)" suffix. A sub-repo scope reads the
// umbrella first and then the sub-repo, each under a "> from:" line, grouped
// per topic in argument order. missing lists topics that matched no heading
// in any file read.
func (s Store) Query(scope string, topics []string, o QueryOpts) (text string, missing []string, err error) {
	umbrellaFile, err := s.KnowledgePath(Umbrella)
	if err != nil {
		return "", nil, err
	}
	umbrellaTier, err := s.effectiveBackfilled(Umbrella)
	if err != nil {
		return "", nil, err
	}
	type pair struct {
		file  string
		tiers *Sidecar
		label string
	}
	pairs := []pair{{umbrellaFile, umbrellaTier, rotatree.Rel(rotatree.KnowledgeFile)}}
	hybrid := scope != "" && scope != Umbrella
	if hybrid {
		f, err := s.KnowledgePath(scope)
		if err != nil {
			return "", nil, err
		}
		t, err := s.effectiveBackfilled(scope)
		if err != nil {
			return "", nil, err
		}
		pairs = append(pairs, pair{f, t, rotatree.Rel(rotatree.KnowledgeDir, scope, rotatree.KnowledgeFile)})
	}

	filter := func(body, name string, tiers *Sidecar) ([]string, error) {
		var out []string
		for _, line := range pystr.Splitlines(body) {
			title, text, stamp, ok := parseBullet(strings.TrimRight(line, " \t\r\f\v"))
			if !ok {
				out = append(out, line)
				continue
			}
			tier := ""
			if tiered(name) {
				e, _, err := tiers.Get(name, title)
				if err != nil {
					return nil, err
				}
				tier = e.Tier
			}
			if o.Tier != "" && tier != o.Tier {
				continue
			}
			if tier == Deprecated && !o.IncludeDeprecated {
				continue
			}
			if tier == Provisional {
				line = fmt.Sprintf("- **%s** — %s (provisional)%s", title, text, stamp)
			}
			out = append(out, line)
		}
		return out, nil
	}

	var b strings.Builder
	matched := map[string]bool{}
	if !hybrid {
		content, err := ReadFile(pairs[0].file)
		if err != nil {
			return "", nil, err
		}
		if !fileExists(pairs[0].file) {
			// Like the old helper: no file, no output and no warnings.
			return "", nil, nil
		}
		all := section.Topics(content)
		names := headingNames(all)
		wanted := map[string]bool{}
		for _, t := range topics {
			hit := topicmatch.Headings(t, names)
			for _, n := range hit {
				wanted[n] = true
			}
			if len(hit) > 0 {
				matched[strings.ToLower(strings.TrimSpace(t))] = true
			}
		}
		first := true
		for _, t := range all {
			if !wanted[strings.ToLower(t.Name)] {
				continue
			}
			if !first {
				b.WriteString("\n")
			}
			b.WriteString("## " + t.Name + "\n")
			lines, err := filter(t.Body, t.Name, pairs[0].tiers)
			if err != nil {
				return "", nil, err
			}
			b.WriteString(strings.TrimRight(strings.Join(lines, "\n"), " \t\n\r\f\v") + "\n")
			first = false
		}
	} else {
		contents := make([]string, len(pairs))
		exists := make([]bool, len(pairs))
		for i, p := range pairs {
			exists[i] = fileExists(p.file)
			if exists[i] {
				if contents[i], err = ReadFile(p.file); err != nil {
					return "", nil, err
				}
			}
		}
		var names []string
		for i := range pairs {
			if exists[i] {
				names = append(names, headingNames(section.Topics(contents[i]))...)
			}
		}
		first := true
		for _, w := range topics {
			wl := strings.ToLower(strings.TrimSpace(w))
			for _, hl := range topicmatch.Headings(w, names) {
				matched[wl] = true
				printed := false
				for i, p := range pairs {
					if !exists[i] {
						continue
					}
					for _, t := range section.Topics(contents[i]) {
						if strings.ToLower(t.Name) != hl {
							continue
						}
						lines, err := filter(t.Body, t.Name, p.tiers)
						if err != nil {
							return "", nil, err
						}
						if len(lines) == 0 {
							break
						}
						if !printed {
							if !first {
								b.WriteString("\n")
							}
							b.WriteString("## " + t.Name + "\n")
							first = false
							printed = true
						}
						b.WriteString("> from: " + p.label + "\n")
						b.WriteString(strings.TrimRight(strings.Join(lines, "\n"), " \t\n\r\f\v") + "\n")
						break
					}
				}
			}
		}
	}

	seen := map[string]bool{}
	for _, orig := range topics {
		k := strings.ToLower(strings.TrimSpace(orig))
		if k != "" && !matched[k] && !seen[k] {
			seen[k] = true
			missing = append(missing, orig)
		}
	}
	return b.String(), missing, nil
}

// Stat is one topic's size.
type Stat struct {
	Name    string
	Bullets int
	Bytes   int
}

var bulletLine = regexp.MustCompile(`(?m)^- `)

// Stats counts bullets and bytes per topic of the umbrella KNOWLEDGE.md.
func (s Store) Stats() ([]Stat, error) {
	p, err := s.KnowledgePath(Umbrella)
	if err != nil {
		return nil, err
	}
	content, err := ReadFile(p)
	if err != nil {
		return nil, err
	}
	return topicStats(content), nil
}

func topicStats(content string) []Stat {
	out := []Stat{}
	for _, t := range section.Topics(content) {
		out = append(out, Stat{t.Name, len(bulletLine.FindAllStringIndex(t.Body, -1)), len(t.Body)})
	}
	return out
}

func headingNames(ts []section.Topic) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Name
	}
	return out
}
