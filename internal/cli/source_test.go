package cli

import (
	"context"
	"github.com/l4ci/rota/internal/projects"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/tracker"
)

// originDeps answers `git remote get-url origin` with origin (an empty origin
// is a repo with no remote) and nothing else.
func originDeps(origin string) *Deps {
	d := testDeps()
	d.TrackerOptions = []tracker.Option{tracker.WithExec(func(_ context.Context, _, name string, args []string, _ []byte) ([]byte, []byte, int, error) {
		if name == "git" {
			if origin == "" {
				return nil, nil, 2, nil
			}
			return []byte(origin + "\n"), nil, 0, nil
		}
		return nil, nil, 1, nil
	}, func(n string) (string, error) { return "/fake/" + n, nil })}
	return d
}

func readCfgText(t *testing.T, root string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, ".rota", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// rota migrate source: the legacy keys become issues.source (and forge.provider
// where the origin would miss it), preview first, and a second run is a no-op.
func TestMigrateSourceVerb(t *testing.T) {
	for _, c := range []struct {
		name, cfg, origin, source string
	}{
		{"file", `{"backlog":{"backend":"file"}}`, "", "file"},
		{"issues, provider auto, origin gitlab", `{"backlog":{"backend":"issues"},"issues":{"provider":"auto"}}`, "https://gitlab.com/o/r.git", "gitlab"},
		{"issues, provider github", `{"backlog":{"backend":"issues"},"issues":{"provider":"github"}}`, "git@github.com:o/r.git", "github"},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := trackerProject(t, c.cfg)
			d := originDeps(c.origin)
			before := readCfgText(t, root)

			code, env, _ := rotaRunWith(t, d, "--json", "-C", root, "migrate", "source")
			if code != 0 || get(dataOf(env), "planned") != true || get(dataOf(env), "changed") != false {
				t.Fatalf("preview: %d %v", code, env)
			}
			if readCfgText(t, root) != before {
				t.Fatal("the preview wrote the config")
			}

			code, env, _ = rotaRunWith(t, d, "--json", "-C", root, "migrate", "source", "--apply")
			if code != 0 || get(dataOf(env), "changed") != true || get(dataOf(env), "source") != c.source {
				t.Fatalf("apply: %d %v", code, env)
			}
			if got := config.Legacy(root); len(got) != 0 {
				t.Errorf("legacy keys left: %v", got)
			}
			if v, _ := config.IssueSource(config.Load(projects.OSEnv(), filepath.Join(root, ".rota", "config.json"))); v != c.source {
				t.Errorf("issues.source = %q, want %q", v, c.source)
			}
			after := readCfgText(t, root)
			if strings.Contains(after, "backend") || strings.Contains(after, `"provider": "auto"`) {
				t.Errorf("legacy keys in the migrated config:\n%s", after)
			}

			code, env, _ = rotaRunWith(t, d, "--json", "-C", root, "migrate", "source", "--apply")
			if code != 0 || get(dataOf(env), "changed") != false || get(dataOf(env), "planned") != false || readCfgText(t, root) != after {
				t.Fatalf("second run changed something: %d %v", code, env)
			}
		})
	}
}

// With no origin to read, provider auto stays on the legacy keys and the
// verb says so instead of guessing; the legacy keys keep deciding.
func TestMigrateSourceUnresolvedLeavesTheLegacyKeys(t *testing.T) {
	root := trackerProject(t, `{"backlog":{"backend":"issues"}}`)
	before := readCfgText(t, root)
	code, env, stderr := rotaRunWith(t, originDeps(""), "--json", "-C", root, "migrate", "source", "--apply")
	if code != 0 || get(dataOf(env), "unresolved") != true || get(dataOf(env), "changed") != false || readCfgText(t, root) != before {
		t.Fatalf("%d %v", code, env)
	}
	if !strings.Contains(stderr, "set issues.source by hand") {
		t.Errorf("no hint: %s", stderr)
	}
	if v, _ := config.IssueSource(config.Load(projects.OSEnv(), filepath.Join(root, ".rota", "config.json"))); v != config.SourceIssues {
		t.Errorf("an unmigrated issues project reads as %q", v)
	}
}

// config check names the legacy keys without failing; config fill (and so
// rota update) migrates them.
func TestConfigCheckListsLegacyKeysAndFillMigrates(t *testing.T) {
	root := trackerProject(t, `{"backlog":{"backend":"issues"},"issues":{"provider":"gitlab"}}`)
	d := originDeps("https://gitlab.com/o/r.git")
	_, env, _ := rotaRunWith(t, d, "--json", "-C", root, "config", "check")
	legacy, _ := get(dataOf(env), "legacy").([]any)
	if len(legacy) != 2 || legacy[0] != "backlog.backend" || legacy[1] != "issues.provider" {
		t.Fatalf("legacy = %v (%v)", legacy, env)
	}
	code, env, _ := rotaRunWith(t, d, "--json", "-C", root, "config", "fill")
	if code != 0 || get(dataOf(env), "migrated") == nil {
		t.Fatalf("fill: %d %v", code, env)
	}
	_, env, _ = rotaRunWith(t, d, "--json", "-C", root, "config", "check")
	if _, has := dataOf(env).Get("legacy"); has {
		t.Errorf("legacy still listed after fill: %v", env)
	}
	if v, _ := config.IssueSource(config.Load(projects.OSEnv(), filepath.Join(root, ".rota", "config.json"))); v != "gitlab" {
		t.Errorf("issues.source = %q", v)
	}
}

// rota init writes an explicit issues.source: file for a project with nothing
// else to go on, the origin's tracker for a backlog already moved onto it.
func TestInitWritesAnExplicitIssueSource(t *testing.T) {
	for _, c := range []struct {
		name, origin string
		moved        bool
		want         string
	}{
		{"fresh project", "git@github.com:o/r.git", false, "file"},
		{"backlog moved to the tracker", "https://gitlab.com/o/r.git", true, "gitlab"},
		{"moved but no recognisable origin", "", true, "file"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if c.moved {
				os.MkdirAll(filepath.Join(dir, ".rota"), 0o755)
				os.WriteFile(filepath.Join(dir, ".rota", "issue-map.json"), []byte("{}\n"), 0o644)
			}
			code, _, stderr := rotaInWith(t, originDeps(c.origin), dir, "init")
			if code != 0 {
				t.Fatalf("init: %d %s", code, stderr)
			}
			if got := cfgValue(t, dir, "issues.source"); got != c.want {
				t.Errorf("issues.source = %v, want %s", got, c.want)
			}
			if _, set := config.Lookup(config.Load(projects.OSEnv(), filepath.Join(dir, ".rota", "config.json")), "forge.provider"); set {
				t.Error("init wrote forge.provider; auto is the default")
			}
		})
	}
}

// An umbrella has no origin of its own: the provider is what every sub-repo's
// origin agrees on, "forge" when they differ (a mixed GitHub/GitLab umbrella
// keeps each tracker on its own repo's forge), and none when a sub-repo has no
// recognizable origin.
func TestOriginProviderOfAnUmbrella(t *testing.T) {
	root := umbrella(t) // sub-repos svc, web (and a registered ghost with no checkout)
	byDir := func(urls map[string]string) *Deps {
		d := testDeps()
		d.TrackerOptions = []tracker.Option{tracker.WithExec(func(_ context.Context, dir, name string, args []string, _ []byte) ([]byte, []byte, int, error) {
			if u := urls[filepath.Base(dir)]; u != "" {
				return []byte(u + "\n"), nil, 0, nil
			}
			return nil, nil, 2, nil
		}, func(n string) (string, error) { return "/fake/" + n, nil })}
		return d
	}
	ctx := context.Background()
	for _, c := range []struct {
		name string
		urls map[string]string
		want string
	}{
		{"all github", map[string]string{"svc": "git@github.com:o/a.git", "web": "https://github.com/o/b", "ghost": "git@github.com:o/c.git"}, "github"},
		{"mixed", map[string]string{"svc": "git@github.com:o/a.git", "web": "https://gitlab.com/o/b", "ghost": "git@github.com:o/c.git"}, "forge"},
		{"one without a remote", map[string]string{"svc": "git@github.com:o/a.git", "ghost": "git@github.com:o/c.git"}, ""},
	} {
		if got := byDir(c.urls).originProvider(ctx, root)(); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// A mixed-host umbrella migrates to issues.source forge, not to one provider
// and not to the unresolved legacy keys.
func TestMigrateSourceMixedUmbrellaIsForge(t *testing.T) {
	root := umbrella(t)
	write(t, filepath.Join(root, ".rota", "config.json"), `{"backlog":{"backend":"issues"},"issues":{"provider":"auto"}}`)
	urls := map[string]string{"svc": "git@github.com:o/a.git", "web": "https://gitlab.com/o/b", "ghost": "git@github.com:o/c.git"}
	d := testDeps()
	d.TrackerOptions = []tracker.Option{tracker.WithExec(func(_ context.Context, dir, _ string, _ []string, _ []byte) ([]byte, []byte, int, error) {
		if u := urls[filepath.Base(dir)]; u != "" {
			return []byte(u + "\n"), nil, 0, nil
		}
		return nil, nil, 2, nil
	}, func(n string) (string, error) { return "/fake/" + n, nil })}
	code, env, _ := rotaRunWith(t, d, "--json", "-C", root, "migrate", "source", "--apply")
	if code != 0 || get(dataOf(env), "source") != "forge" || get(dataOf(env), "unresolved") != false {
		t.Fatalf("%d %v", code, env)
	}
	if got := config.Legacy(root); len(got) != 0 {
		t.Errorf("legacy keys left: %v", got)
	}
}

func TestInvalidForgeProviderExits70(t *testing.T) {
	root := trackerProject(t, `{"forge":{"provider":"bitbucket"}}`)
	code, _, stderr := rotaRunWith(t, testDeps(), "--json", "-C", root, "backlog", "list")
	if code != ExitInternal || !strings.Contains(stderr, "invalid forge.provider") {
		t.Fatalf("%d %s", code, stderr)
	}
}

// issues.source forge on an origin naming no forge: a backlog read takes the
// reader's github and says so on stderr; a write is the actor refusal, exit 4
// blockedBy forge, with the origin and the hint (#889).
func TestForgeSourceUnknownOriginReadsAndRefusesWrites(t *testing.T) {
	root := trackerProject(t, `{"issues":{"source":"forge"}}`)
	f := &forge{answer: func(name string, args []string) (string, string, int) {
		if name == "git" {
			return "https://git.example.com/o/r.git\n", "", 0
		}
		return "[]", "", 0
	}}
	deps := useForge(t, f)
	code, _, stderr := rotaRunWith(t, deps, "--json", "-C", root, "backlog", "list")
	if code != 0 || !strings.Contains(stderr, "origin https://git.example.com/o/r.git names no forge; assumed github") {
		t.Fatalf("read: %d %s", code, stderr)
	}
	before := len(f.calls)
	code, env, stderr := rotaRunWith(t, deps, "--json", "-C", root, "item", "create", "--kind", "bugs", "--title", "T", "--body-file", "-")
	if code != ExitRefused || get(dataOf(env), "blockedBy") != "forge" ||
		!strings.Contains(stderr, "cannot determine the forge from origin https://git.example.com/o/r.git") ||
		!strings.Contains(stderr, "set forge.provider to github, gitlab or local") {
		t.Fatalf("write: %d %v %s", code, env, stderr)
	}
	for _, c := range f.calls[before:] {
		if !strings.HasPrefix(c, "git ") {
			t.Errorf("the refused write ran %q", c)
		}
	}
}
