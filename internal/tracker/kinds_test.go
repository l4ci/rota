package tracker

import (
	"testing"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/connections"
)

// Every registered kind is complete: a constructor, a connection tester and a
// connection kind connections.json accepts. A kind added without a tester
// would otherwise answer StateUnsupported silently.
func TestOwnKindsAreComplete(t *testing.T) {
	testers := ConnectionTesters()
	seen := map[string]bool{}
	for _, k := range ownKinds {
		if k.source == "" || k.build == nil || k.tester == nil {
			t.Errorf("kind %q lacks a source, constructor or tester", k.source)
		}
		if seen[k.source] {
			t.Errorf("source %q registered twice", k.source)
		}
		seen[k.source] = true
		if testers[k.spec.kind] == nil {
			t.Errorf("no connection tester for kind %q", k.spec.kind)
		}
		if err := (connections.Connection{Name: "c", Kind: k.spec.kind, Site: "https://x.atlassian.net", User: "u@x"}).WithDefaults().Validate(); err != nil {
			t.Errorf("kind %q: %v", k.spec.kind, err)
		}
		if _, ok := ownKindFor(k.source); !ok {
			t.Errorf("ownKindFor(%q) missed a registered kind", k.source)
		}
	}
	for _, src := range []string{config.SourceJira, config.SourceLinear, config.SourceItsaplan} {
		if !seen[src] {
			t.Errorf("issues.source %q is not registered", src)
		}
	}
}

// A provider no forge claims is refused, not built as GitLab.
func TestAdapterForRefusesUnknownProvider(t *testing.T) {
	a, err := adapterFor(&CLI{Provider: "bitbucket"}, Settings{})
	if err == nil || !IsKind(err, KindUnavailable) || a != nil {
		t.Fatalf("adapterFor(bitbucket) = %v, %v; want an Unavailable refusal", a, err)
	}
	for _, p := range []string{"github", "gitlab"} {
		if a, err := adapterFor(&CLI{Provider: p}, Settings{}); err != nil || a == nil {
			t.Errorf("adapterFor(%s) = %v, %v", p, a, err)
		}
	}
}
