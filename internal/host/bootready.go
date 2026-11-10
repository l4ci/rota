package host

import (
	"regexp"
	"strings"
	"sync"

	"github.com/l4ci/rota/internal/harness"
)

// bootRules holds every pattern bootReady needs, compiled once. The harness
// adapters return static syntax, so a snapshot taken on first use stays valid.
type bootRules struct {
	evidence []*regexp.Regexp // any match counts as boot evidence
	models   []modelRule      // harnesses that declare a ReadyModel row
	composer string           // default harness composer glyph
}

type modelRule struct {
	model  *regexp.Regexp
	prompt *regexp.Regexp // composer glyph at the start of a line
}

var (
	bootRulesOnce sync.Once
	bootRulesVal  bootRules
)

func loadBootRules() *bootRules {
	bootRulesOnce.Do(func() {
		r := &bootRulesVal
		for _, syntax := range harness.InputSyntaxes() {
			for _, pattern := range syntax.BootPatterns {
				r.evidence = append(r.evidence, regexp.MustCompile(pattern))
			}
			if syntax.ReadyModel != nil {
				r.models = append(r.models, modelRule{
					model:  syntax.ReadyModel,
					prompt: regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(syntax.Composer) + `(?:[ \t]|$)`),
				})
			}
		}
		r.evidence = append(r.evidence, bootPatterns...)
		r.composer = harness.DefaultInputSyntax().Composer
	})
	return &bootRulesVal
}

// bootReady is the one text-readiness rule for hosts that read a pane as
// plain text (tmux, herdr, Orca). Core reads terminal state instead.
//
// Without requireInput, any boot evidence counts (tmux, herdr). With it, the
// pane must also show an input the host can paste into: for a harness that
// declares a model row, a loaded model and a composer prompt; otherwise the
// default composer plus boot evidence (Orca). A dialog check is the caller's.
func bootReady(text string, requireInput bool) bool {
	r := loadBootRules()
	if requireInput {
		for _, m := range r.models {
			model := m.model.FindStringSubmatch(text)
			prompt := m.prompt.MatchString(text)
			if len(model) > 0 || prompt {
				return len(model) > 1 && !strings.HasPrefix(strings.ToLower(strings.TrimSpace(model[1])), "loading") && prompt
			}
		}
		if !strings.Contains(text, r.composer) {
			return false
		}
	}
	for _, re := range r.evidence {
		if re.MatchString(text) {
			return true
		}
	}
	return false
}
