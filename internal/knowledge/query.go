package knowledge

import (
	"fmt"
	"github.com/l4ci/rota/internal/bulletfmt"
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

	// Each requested topic is rendered once, whatever the scope: empty and
	// repeated requests (compared trimmed and case-folded) are dropped here.
	topics = uniqueTopics(topics)
	var ordered []queryTopic
	matched := map[string]bool{}
	if !hybrid {
		content, err := ReadFile(pairs[0].file)
		if err != nil {
			return "", nil, err
		}
		if !fileExists(pairs[0].file) {
			// No file: no output and no warnings.
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
		for _, t := range all {
			if wanted[strings.ToLower(t.Name)] {
				ordered = append(ordered, queryTopic{t.Name, []querySource{{t, pairs[0].tiers, ""}}})
			}
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
		done := map[string]bool{}
		for _, w := range topics {
			wl := strings.ToLower(strings.TrimSpace(w))
			for _, hl := range topicmatch.Headings(w, names) {
				matched[wl] = true
				if done[hl] {
					continue
				}
				done[hl] = true
				var selected queryTopic
				for i, p := range pairs {
					if !exists[i] {
						continue
					}
					for _, t := range section.Topics(contents[i]) {
						if strings.ToLower(t.Name) != hl {
							continue
						}
						if selected.name == "" {
							selected.name = t.Name
						}
						selected.sources = append(selected.sources, querySource{t, p.tiers, p.label})
						break
					}
				}
				ordered = append(ordered, selected)
			}
		}
	}

	for _, orig := range topics {
		if !matched[strings.ToLower(strings.TrimSpace(orig))] {
			missing = append(missing, orig)
		}
	}
	text, err = renderQuery(ordered, o)
	if err != nil {
		return "", nil, err
	}
	return text, missing, nil
}

// The selectors own ordering and file scope; rendering owns headers, source
// labels and tier filtering, including matched topics with empty bodies.
type queryTopic struct {
	name    string
	sources []querySource
}

type querySource struct {
	topic section.Topic
	tiers *Sidecar
	label string
}

func renderQuery(topics []queryTopic, o QueryOpts) (string, error) {
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
	for i, topic := range topics {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("## " + topic.name + "\n")
		for _, source := range topic.sources {
			lines, err := filter(source.topic.Body, source.topic.Name, source.tiers)
			if err != nil {
				return "", err
			}
			if source.label != "" {
				b.WriteString("> from: " + source.label + "\n")
			}
			b.WriteString(strings.TrimRight(strings.Join(lines, "\n"), " \t\n\r\f\v") + "\n")
		}
	}
	return b.String(), nil
}

// uniqueTopics drops empty requests and repeats of one (trimmed, case-folded),
// keeping the first spelling and the argument order.
func uniqueTopics(topics []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range topics {
		k := strings.ToLower(strings.TrimSpace(t))
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, t)
	}
	return out
}

// Stat is one topic's size.
type Stat struct {
	Name    string
	Bullets int
	Bytes   int
}

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
		out = append(out, Stat{t.Name, bulletfmt.Count(t.Body), len(t.Body)})
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
