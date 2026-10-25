package config

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/overlap"
)

// LensesKey is the config key the review lenses live under.
const LensesKey = "review.lenses"

// LensNames are the fixed review lenses (#2064), in the order they report.
var LensNames = []string{"security", "perf", "ux"}

// LensHit is a lens a branch triggers and why: "path <file>" or "label <name>".
type LensHit struct{ Name, Why string }

// Lens is one lens's triggers: path globs over the diff's files and issue labels.
type Lens struct{ Paths, Labels []string }

// Lenses is review.lenses: per lens name, what triggers it. A lens with no
// entry never triggers.
type Lenses map[string]Lens

// LensesOf reads review.lenses from the merged config.
func LensesOf(cfg any) (Lenses, error) {
	v, _ := Value(cfg, LensesKey)
	return ParseLenses(v)
}

// ParseLenses reads a review.lenses value: an object keyed by lens name, each
// entry {paths: [globs], labels: [names]}. Every error names the key.
func ParseLenses(v any) (Lenses, error) {
	if v == nil {
		return Lenses{}, nil
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil, fmt.Errorf("%s must be an object of lens: {paths, labels}", LensesKey)
	}
	out := Lenses{}
	for _, name := range o.Keys() {
		if !slices.Contains(LensNames, name) {
			return nil, fmt.Errorf("%s: unknown lens %q (want %s)", LensesKey, name, strings.Join(LensNames, ", "))
		}
		key := LensesKey + "." + name
		ev, _ := o.Get(name)
		eo, ok := ev.(*jsonx.Object)
		if !ok {
			return nil, fmt.Errorf("%s must be an object with paths and labels", key)
		}
		var l Lens
		for _, f := range eo.Keys() {
			fv, _ := eo.Get(f)
			switch f {
			case "paths":
				strs, ok := stringList(fv)
				if !ok {
					return nil, fmt.Errorf("%s.paths must be a list of globs", key)
				}
				for _, g := range strs {
					// path.Match has no "**": only a trailing /** (a directory) is
					// supported, and any other use would silently match less.
					if _, err := path.Match(dirGlob(g), ""); err != nil || strings.Contains(dirGlob(g), "**") {
						return nil, fmt.Errorf("%s.paths: glob %q is not supported: use * within a segment, or a trailing /** for a directory", key, g)
					}
				}
				l.Paths = strs
			case "labels":
				strs, ok := stringList(fv)
				if !ok {
					return nil, fmt.Errorf("%s.labels must be a list of strings", key)
				}
				l.Labels = strs
			default:
				return nil, fmt.Errorf("%s: unknown field %q (want paths, labels)", key, f)
			}
		}
		out[name] = l
	}
	return out, nil
}

// stringList is v as non-empty trimmed strings; false when v is not a list of
// strings or holds an empty entry.
func stringList(v any) ([]string, bool) {
	arr, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		s, ok := e.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return nil, false
		}
		out = append(out, strings.TrimSpace(s))
	}
	return out, true
}

// UsesLabels reports whether any lens can trigger on an issue label, so a
// caller knows to read the branch's issue.
func (l Lenses) UsesLabels() bool {
	for _, lens := range l {
		if len(lens.Labels) > 0 {
			return true
		}
	}
	return false
}

// Match lists the lenses files or labels trigger, in LensNames order, each
// with the first path (else label) that did it.
func (l Lenses) Match(files, labels []string) []LensHit {
	var hits []LensHit
	for _, name := range LensNames {
		lens, ok := l[name]
		if !ok {
			continue
		}
		why := ""
	paths:
		for _, g := range lens.Paths {
			for _, f := range files {
				if overlap.MatchPath(dirGlob(g), f) {
					why = "path " + f
					break paths
				}
			}
		}
		if why == "" {
			for _, lb := range labels {
				if slices.Contains(lens.Labels, lb) {
					why = "label " + lb
					break
				}
			}
		}
		if why != "" {
			hits = append(hits, LensHit{name, why})
		}
	}
	return hits
}
