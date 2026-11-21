package harness

import (
	"regexp"
	"sync"
)

// PaneRules are a Pane's PaneVocab compiled: the form the pane classifier
// reads. The harness owns them so the vocabulary and its compiled form live
// together; a registered adapter is compiled once, any other Pane (a test
// adapter) on each call.
type PaneRules struct {
	Retry, Busy, Waiting, Background, Limits []*regexp.Regexp
	AuthDead, BadModel                       *regexp.Regexp
	LoggedOut                                *regexp.Regexp
	LoggedOutAdvice                          string
	Gutter                                   bool
}

// registeredRules are the registered adapters' rules, compiled on first use.
var registeredRules = sync.OnceValue(func() map[string]*PaneRules {
	m := map[string]*PaneRules{}
	for _, k := range kinds {
		if h, ok := lookup(k); ok {
			m[k] = CompilePaneRules(h.PaneVocab())
		}
	}
	return m
})

// RulesOf is the compiled vocabulary of p. A nil Pane (a kind that is not a
// worker harness) has no rules.
func RulesOf(p Pane) *PaneRules {
	if p == nil {
		return &PaneRules{}
	}
	if a, ok := p.(adapter); ok {
		if r, ok := registeredRules()[a.Kind()]; ok {
			return r
		}
	}
	return CompilePaneRules(p.PaneVocab())
}

// CompilePaneRules compiles one vocabulary. Patterns match case-insensitively,
// except BadModel, which keeps its own case.
func CompilePaneRules(v PaneVocab) *PaneRules {
	r := &PaneRules{Gutter: v.Gutter}
	all := func(pats []string) []*regexp.Regexp {
		out := make([]*regexp.Regexp, len(pats))
		for i, p := range pats {
			out[i] = regexp.MustCompile(`(?i)` + p)
		}
		return out
	}
	r.Retry, r.Busy, r.Waiting = all(v.Retry), all(v.Busy), all(v.Waiting)
	r.Background, r.Limits = all(v.Background), all(v.Limits)
	if v.AuthDead != "" {
		r.AuthDead = regexp.MustCompile(`(?i)` + v.AuthDead)
	}
	if v.LoggedOut != "" {
		r.LoggedOut = regexp.MustCompile(`(?i)` + v.LoggedOut)
		r.LoggedOutAdvice = v.LoggedOutAdvice
	}
	if v.BadModel != "" {
		r.BadModel = regexp.MustCompile(v.BadModel)
	}
	return r
}
