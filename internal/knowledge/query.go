package knowledge

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/l4ci/rota/internal/section"
)

// bulletRe is a dated, titled bullet: "- **Title** — body <!-- YYYY-MM-DD -->".
var bulletRe = regexp.MustCompile(`^- \*\*([^*]+)\*\* — (.*?)(\s*<!-- \d{4}-\d{2}-\d{2}\s*-->\s*)$`)

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
	umbrellaTier, err := s.tierMap(Umbrella)
	if err != nil {
		return "", nil, err
	}
	type pair struct {
		file  string
		tiers map[[2]string]string
		label string
	}
	pairs := []pair{{umbrellaFile, umbrellaTier, ".rota/KNOWLEDGE.md"}}
	hybrid := scope != "" && scope != Umbrella
	if hybrid {
		f, err := s.KnowledgePath(scope)
		if err != nil {
			return "", nil, err
		}
		t, err := s.tierMap(scope)
		if err != nil {
			return "", nil, err
		}
		pairs = append(pairs, pair{f, t, ".rota/knowledge/" + scope + "/KNOWLEDGE.md"})
	}

	filter := func(body, name string, tiers map[[2]string]string) []string {
		var out []string
		for _, line := range section.Lines(body) {
			m := bulletRe.FindStringSubmatch(strings.TrimRight(line, " \t\r\f\v"))
			if m == nil {
				out = append(out, line)
				continue
			}
			title := strings.TrimSpace(m[1])
			tier := tiers[[2]string{name, title}]
			if o.Tier != "" && tier != o.Tier {
				continue
			}
			if tier == Deprecated && !o.IncludeDeprecated {
				continue
			}
			if tier == Provisional {
				line = fmt.Sprintf("- **%s** — %s (provisional)%s", title, m[2], m[3])
			}
			out = append(out, line)
		}
		return out
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
		wanted := map[string]bool{}
		for _, t := range topics {
			wanted[strings.ToLower(strings.TrimSpace(t))] = true
		}
		first := true
		for _, t := range section.Topics(content) {
			if !wanted[strings.ToLower(t.Name)] {
				continue
			}
			matched[strings.ToLower(t.Name)] = true
			if !first {
				b.WriteString("\n")
			}
			b.WriteString("## " + t.Name + "\n")
			b.WriteString(strings.TrimRight(strings.Join(filter(t.Body, t.Name, pairs[0].tiers), "\n"), " \t\n\r\f\v") + "\n")
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
		first := true
		for _, w := range topics {
			wl := strings.ToLower(strings.TrimSpace(w))
			printed := false
			for i, p := range pairs {
				if !exists[i] {
					continue
				}
				for _, t := range section.Topics(contents[i]) {
					if strings.ToLower(t.Name) != wl {
						continue
					}
					matched[wl] = true
					lines := filter(t.Body, t.Name, p.tiers)
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

// tierMap loads the (topic, title) → tier lookup of scope's sidecar. A
// missing sidecar is empty, so every bullet prints unfiltered.
func (s Store) tierMap(scope string) (map[[2]string]string, error) {
	p, err := s.TierPath(scope)
	if err != nil {
		return nil, err
	}
	sc, err := LoadSidecar(p)
	if err != nil {
		return nil, err
	}
	m := map[[2]string]string{}
	for _, e := range sc.List("") {
		m[[2]string{e.Topic, e.Title}] = e.Tier
	}
	return m, nil
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
	out := []Stat{}
	for _, t := range section.Topics(content) {
		out = append(out, Stat{t.Name, len(bulletLine.FindAllStringIndex(t.Body, -1)), len(t.Body)})
	}
	return out, nil
}
