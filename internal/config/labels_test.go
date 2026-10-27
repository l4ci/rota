package config

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/labels"
)

func TestResolvedLabelsProjectConfig(t *testing.T) {
	cfg, err := jsonx.Decode([]byte(`{"issues":{"labels":{"inProgress":"doing","sizePrefix":"effort/","extra":{"area:cli":"CLI work."}}},"review":{"crossLabels":["risk:custom"],"lenses":{"security":{"labels":["audit:security"]}}},"ship":{"review":{"default":"full","labels":{"audit:depth":"full"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := ResolvedLabels(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"doing", "effort/Major", "area:cli", "risk:custom", "audit:security", "audit:depth"} {
		l, ok := labels.Find(entries, n)
		if !ok || l.Description == "" {
			t.Errorf("label %s absent/undescribed: %+v", n, l)
		}
	}
	if l, _ := labels.Find(entries, "area:cli"); l.Description != "CLI work." {
		t.Errorf("extra description %+v", l)
	}
	if _, ok := labels.Find(entries, labels.InProgress); ok {
		t.Fatal("old configured label remains known")
	}
	page := labels.ReferencePage(entries)
	if !strings.Contains(page, "| `doing` |") || !strings.Contains(page, "| `area:cli` | CLI work.") {
		t.Fatal("generated page ignores project settings")
	}
}

func TestRegistryConfigDefaultsAgree(t *testing.T) {
	entries, err := ResolvedLabels(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range Keys {
		if !strings.HasPrefix(key.Name, "issues.labels.") || key.Name == labels.ExtraKey {
			continue
		}
		matched := false
		for _, e := range entries {
			if e.ConfigKey == key.Name {
				matched = true
				v, _ := key.Default.(string)
				if !strings.HasPrefix(e.Name, v) {
					t.Errorf("%s default %q != registry %q", key.Name, v, e.Name)
				}
			}
		}
		if !matched {
			t.Errorf("%s absent from registry", key.Name)
		}
	}
}

func TestExtraLabelsValidation(t *testing.T) {
	for _, raw := range []string{`{"area:cli":"CLI work."}`, `{}`} {
		if err := Validate(labels.ExtraKey, raw); err != nil {
			t.Errorf("valid %s: %v", raw, err)
		}
	}
	for _, raw := range []string{`{"area:cli":false}`, `{"area:cli":""}`, `{" ":"description"}`, `[]`} {
		if err := Validate(labels.ExtraKey, raw); err == nil {
			t.Errorf("accepted invalid declarations: %s", raw)
		}
	}
}
