package knowledge

import (
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/section"
)

type blockCfg struct {
	heading, intro, empty string
	umbrellaOnly          bool
}

var blockKinds = map[string]blockCfg{
	"knowledge": {
		heading: "## Project Knowledge",
		intro:   "Durable learnings live in `.rota/KNOWLEDGE.md`. Consult it when work touches these topics:",
		empty:   "- _(no topics yet — run `/rota-learn` to capture learnings)_",
	},
	"decisions": {
		heading:      "## Project Decisions",
		intro:        "Hard boundaries live in `.rota/DECISIONS.md`. Consult them before acting on work that touches these topics:",
		empty:        "- _(no decisions yet — run `/rota-decide` to capture a hard boundary)_",
		umbrellaOnly: true,
	},
}

// GeneratedBlockKeys are the keys a block can be generated for without a body.
func GeneratedBlockKeys() []string { return []string{"knowledge", "decisions"} }

// IsGeneratedBlock reports whether key can be generated from topic headings.
func IsGeneratedBlock(key string) bool { _, ok := blockKinds[key]; return ok }

// UmbrellaOnlyBlock reports whether key ignores sub-repo scopes.
func UmbrellaOnlyBlock(key string) bool { return blockKinds[key].umbrellaOnly }

// BlockInputs returns the topic names a generated block lists and the file it
// is written to. Knowledge with a sub-repo scope lists the umbrella topics
// first, then the sub-repo's new ones, and targets the sub-repo's own
// instructions file. A missing source file just yields no topics.
func (s Store) BlockInputs(key, scope string) (topics []string, target string, err error) {
	topicsOf := func(path string) []string {
		raw, err := readTextBytes(path)
		if err != nil {
			return nil
		}
		text := string(raw)
		if key == "decisions" {
			text = ActiveDecisions(text)
		}
		var out []string
		for _, t := range section.Topics(text) {
			out = append(out, t.Name)
		}
		return out
	}
	switch key {
	case "decisions":
		return topicsOf(rotatree.Decisions(s.Root)), section.InstructionsFile(s.Root), nil
	case "knowledge":
		up, _ := s.KnowledgePath(Umbrella)
		if scope == "" || scope == Umbrella {
			return topicsOf(up), section.InstructionsFile(s.Root), nil
		}
		repoPath, ok := s.Repos[scope]
		if !ok {
			return nil, "", fmt.Errorf("%w: sub-repo '%s' not registered in .rota/repos.json", ErrScope, scope)
		}
		sp, err := s.KnowledgePath(scope)
		if err != nil {
			return nil, "", err
		}
		seen := map[string]bool{}
		for _, p := range []string{up, sp} {
			for _, name := range topicsOf(p) {
				if !seen[name] {
					seen[name] = true
					topics = append(topics, name)
				}
			}
		}
		return topics, section.InstructionsFile(repoPath), nil
	}
	return nil, "", fmt.Errorf("unknown key '%s' (known: %s)", key, strings.Join(GeneratedBlockKeys(), ", "))
}

// RegenerateBlock rewrites the generated block for key in the instructions
// file and returns the upsert status (created, updated, appended, unchanged).
func (s Store) RegenerateBlock(key, scope string) (string, error) {
	cfg, ok := blockKinds[key]
	if !ok {
		return "", fmt.Errorf("unknown key '%s' (known: %s)", key, strings.Join(GeneratedBlockKeys(), ", "))
	}
	topics, target, err := s.BlockInputs(key, scope)
	if err != nil {
		return "", err
	}
	body := cfg.empty
	if len(topics) > 0 {
		lines := make([]string, len(topics))
		for i, t := range topics {
			lines[i] = "- " + t
		}
		body = strings.Join(lines, "\n")
	}
	return section.UpsertBlock(target, key, section.Wrap(key, fmt.Sprintf("%s\n\n%s\n\n%s\n", cfg.heading, cfg.intro, body)))
}

// WriteCustomBlock wraps body in the key's markers and upserts it into the
// project's instructions file.
func (s Store) WriteCustomBlock(key, body string) (string, error) {
	body = strings.TrimRight(body, "\n")
	return section.UpsertManaged(s.Root, key, body)
}
