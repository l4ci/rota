package tracker

import (
	"slices"
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
}

// Each registered kind is wired everywhere else a kind appears: its source is
// an issues.source choice, its project/team key and the connection kind are
// known to config and connections, and doctor has a label for it. The reverse
// holds too, so a kind added to config or connections alone fails here.
func TestOwnKindsWiredEverywhere(t *testing.T) {
	sourceKey, ok := config.SchemaKey("issues.source")
	if !ok {
		t.Fatal("issues.source is not a schema key")
	}
	byConn := map[string]bool{}
	for _, k := range OwnKinds() {
		byConn[k.ConnectionKind] = true
		if !slices.Contains(sourceKey.Choices, k.Source) {
			t.Errorf("%s: not an issues.source choice", k.Source)
		}
		if _, ok := config.SchemaKey(k.KeyName); !ok {
			t.Errorf("%s: config key %q is not in the schema", k.Source, k.KeyName)
		}
		if !slices.Contains(connections.Kinds, k.ConnectionKind) {
			t.Errorf("%s: connection kind %q is not in connections.Kinds", k.Source, k.ConnectionKind)
		}
		if k.ConnectArgs == "" || k.Key == nil || k.KeyName == "" {
			t.Errorf("%s: missing doctor label (connect args or key)", k.Source)
		}
		var s Settings
		*ownKindMust(t, k.Source).field(&s) = "X"
		if k.Key(s) != "X" {
			t.Errorf("%s: Key does not read the Settings field SettingsFromConfig fills", k.Source)
		}
	}
	for _, kind := range connections.Kinds {
		if !byConn[kind] {
			t.Errorf("connection kind %q has no registered tracker kind", kind)
		}
	}
	for _, src := range sourceKey.Choices {
		if _, own := ownKindFor(src); !own && !slices.Contains([]string{config.SourceFile, config.SourceGitHub, config.SourceGitLab, config.SourceForge}, src) {
			t.Errorf("issues.source %q is not registered", src)
		}
	}
}

func ownKindMust(t *testing.T, source string) ownKind {
	t.Helper()
	k, ok := ownKindFor(source)
	if !ok {
		t.Fatalf("no kind %q", source)
	}
	return k
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
