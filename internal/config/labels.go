package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/labels"
)

// ExtraLabels parses the project's name -> description declarations. Requiring
// descriptions prevents sync from creating another set of undescribed labels.
func ExtraLabels(v any) (map[string]string, error) {
	out := map[string]string{}
	if v == nil {
		return out, nil
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil, fmt.Errorf("%s must be an object of label names to descriptions", labels.ExtraKey)
	}
	for _, n := range o.Keys() {
		v, _ := o.Get(n)
		d, ok := v.(string)
		if strings.TrimSpace(n) == "" || strings.TrimSpace(n) != n || !ok || strings.TrimSpace(d) == "" {
			return nil, fmt.Errorf("%s: %q needs a non-empty label name and description", labels.ExtraKey, n)
		}
		out[n] = d
	}
	return out, nil
}

// ResolvedLabels bridges the leaf registry to the effective project config,
// including label-only review triggers that rota reads but never applies.
func ResolvedLabels(cfg any) ([]labels.Entry, error) {
	var getErr error
	out := labels.Resolve(func(key string) string {
		v, err := Value(cfg, key)
		if err != nil {
			getErr = err
			return ""
		}
		s, ok := v.(string)
		if !ok {
			getErr = fmt.Errorf("%s must be a string", key)
		}
		return s
	})
	if getErr != nil {
		return nil, getErr
	}
	v, _ := Value(cfg, labels.ExtraKey)
	extras, err := ExtraLabels(v)
	if err != nil {
		return nil, err
	}
	out = labels.WithExtras(out, extras)
	addRead := func(name, desc, key string) {
		if _, ok := labels.Find(out, name); ok {
			return
		}
		out = append(out, labels.Entry{Label: labels.Label{Name: name, Description: desc, Color: "b60205"}, Group: "review", ConfigKey: key, Sets: "maintainer", Reads: key, Removed: "explicit removal"})
	}
	v, _ = Value(cfg, "review.crossLabels")
	cross, ok := stringList(v)
	if !ok {
		return nil, fmt.Errorf("review.crossLabels must be a list of non-empty label names")
	}
	for _, n := range cross {
		addRead(n, "Require a cross-harness review.", "review.crossLabels")
	}
	lenses, err := LensesOf(cfg)
	if err != nil {
		return nil, err
	}
	for _, name := range LensNames {
		for _, n := range lenses[name].Labels {
			addRead(n, "Trigger the "+name+" review lens.", LensesKey)
		}
	}
	policyValue, _ := Value(cfg, ReviewKey)
	policy, err := ParseReviewPolicy(policyValue)
	if err != nil {
		return nil, err
	}
	var names []string
	for n := range policy.Labels {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		addRead(n, "Select the configured review depth.", ReviewKey)
	}
	return out, nil
}
