package initproj

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/pytest"
)

// tree builds an umbrella fixture: git children (dir or worktree-style .git
// file), a plain dir, a hidden git child, plus extra files.
func tree(t *testing.T, umbrellaGit bool, files map[string]string, gitKids ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, k := range gitKids {
		if err := os.MkdirAll(filepath.Join(root, k, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.MkdirAll(filepath.Join(root, "docs"), 0o755)
	os.MkdirAll(filepath.Join(root, ".hidden", ".git"), 0o755)
	if umbrellaGit {
		os.MkdirAll(filepath.Join(root, ".git"), 0o755)
	}
	for name, body := range files {
		p := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func read(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return "<missing>"
	}
	return string(b)
}

func TestCandidates(t *testing.T) {
	root := tree(t, false, nil, "web", "api")
	// a worktree-style child has a .git file; a symlink to a git dir counts
	os.MkdirAll(filepath.Join(root, "wt"), 0o755)
	os.WriteFile(filepath.Join(root, "wt", ".git"), []byte("gitdir: x\n"), 0o644)
	os.Symlink(filepath.Join(root, "web"), filepath.Join(root, "link"))
	got, err := Candidates(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"api", "link", "web", "wt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestListIsReadOnly(t *testing.T) {
	root := tree(t, true, nil, "web")
	l, err := List(root)
	if err != nil || !l.IsGitRepo || !reflect.DeepEqual(l.Candidates, []string{"web"}) {
		t.Fatalf("%+v %v", l, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".rota")); err == nil {
		t.Fatal("List wrote .rota")
	}
	empty, _ := List(t.TempDir())
	if empty.IsGitRepo || len(empty.Candidates) != 0 {
		t.Fatalf("%+v", empty)
	}
}

func TestNoCandidatesLeavesNothing(t *testing.T) {
	root := t.TempDir()
	seeded := false
	_, err := Umbrella(root, UmbrellaOptions{All: true}, func() error { seeded = true; return nil })
	if !errors.Is(err, ErrNoCandidates) || seeded {
		t.Fatalf("err=%v seeded=%v", err, seeded)
	}
	if _, err := os.Stat(filepath.Join(root, ".rota")); err == nil {
		t.Fatal(".rota created")
	}
}

func TestSeedFailureIsReturned(t *testing.T) {
	root := tree(t, false, nil, "web")
	boom := errors.New("corrupt")
	if _, err := Umbrella(root, UmbrellaOptions{All: true}, func() error { return boom }); !errors.Is(err, boom) {
		t.Fatal(err)
	}
}

func TestIdempotent(t *testing.T) {
	root := tree(t, true, nil, "web", "api")
	first, err := Umbrella(root, UmbrellaOptions{All: true}, nil)
	if err != nil || !first.Changed || len(first.Created) == 0 {
		t.Fatalf("%+v %v", first, err)
	}
	second, err := Umbrella(root, UmbrellaOptions{All: true}, nil)
	if err != nil || second.Changed || len(second.Created) != 0 {
		t.Fatalf("second run changed: %+v %v", second, err)
	}
}

type umbrellaCase struct {
	Name  string            `json:"name"`
	Git   bool              `json:"git"`
	Files map[string]string `json:"files"`
	Kids  []string          `json:"kids"`
	Stdin string            `json:"stdin"` // the retired helper's answer line
	opts  UmbrellaOptions
}

// umbrellaOutcome is what one run left behind and reported.
type umbrellaOutcome struct {
	Repos     string   `json:"repos.json"`
	Gitignore string   `json:".gitignore"`
	Tree      []string `json:"tree"`
	Summary   string   `json:"summary"`
	Warnings  []string `json:"warnings"`
}

func umbrellaCases() []umbrellaCase {
	prior := `{"repos": [{"name": "web", "path": "./web"}, {"name": "gone", "path": "./gone"}, {"name": "api", "path": "./api"}]}`
	return []umbrellaCase{
		{"all", true, nil, []string{"web", "api"}, "all", UmbrellaOptions{All: true}},
		{"subset", true, nil, []string{"web", "api", "db"}, "web,db", UmbrellaOptions{Names: []string{"web", "db"}}},
		{"none", false, nil, []string{"web"}, "none", UmbrellaOptions{}},
		{"empty line is none", false, nil, []string{"web"}, "", UmbrellaOptions{}},
		{"unknown and blank names", true, nil, []string{"web"}, " web , nope,,", UmbrellaOptions{Names: []string{" web ", " nope", "", ""}}},
		{"prior kept, stale dropped", false, map[string]string{".rota/repos.json": prior}, []string{"web", "api", "db"}, "db", UmbrellaOptions{Names: []string{"db"}}},
		{"gitignore appended", true, map[string]string{".gitignore": "node_modules/\n.rota/\n"}, []string{"web"}, "all", UmbrellaOptions{All: true}},
		{"gitignore no trailing newline", true, map[string]string{".gitignore": "a\nb"}, []string{"web"}, "all", UmbrellaOptions{All: true}},
		{"gitignore crlf", true, map[string]string{".gitignore": "a\r\n.claude/\r\n"}, []string{"web"}, "all", UmbrellaOptions{All: true}},
		{"gitignore complete", true, map[string]string{".gitignore": ".claude/\n.rota/\n/web/\n"}, []string{"web"}, "all", UmbrellaOptions{All: true}},
		{"corrupt repos.json", false, map[string]string{".rota/repos.json": "{nope"}, []string{"web"}, "all", UmbrellaOptions{All: true}},
	}
}

// TestUmbrellaMatchesHelperGolden checks Umbrella against what the retired
// hv-umbrella-init wrote and reported on each case, as recorded in
// testdata/golden: repos.json, .gitignore, the paths under .rota, the summary
// line and the warnings.
func TestUmbrellaMatchesHelperGolden(t *testing.T) {
	cases := umbrellaCases()
	var want []umbrellaOutcome
	pytest.Golden(t, cases, &want)
	if len(want) != len(cases) {
		t.Fatalf("golden has %d outcomes for %d cases", len(want), len(cases))
	}
	for i, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			root := tree(t, tc.Git, tc.Files, tc.Kids...)
			res, err := Umbrella(root, tc.opts, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, n := range res.Registered {
				if _, err := os.Stat(filepath.Join(root, ".rota", "knowledge", n)); err != nil {
					t.Errorf("knowledge dir for %s missing", n)
				}
			}
			var paths []string
			for p := range snapshot(root) {
				paths = append(paths, p)
			}
			sort.Strings(paths)
			got := umbrellaOutcome{
				Repos:     read(t, root, ".rota/repos.json"),
				Gitignore: read(t, root, ".gitignore"),
				Tree:      paths,
				Summary:   `{"registered":[` + quoteJoin(res.Registered) + `],"umbrellaIsGitRepo":` + map[bool]string{true: "true", false: "false"}[res.IsGitRepo] + "}\n",
				Warnings:  append([]string{}, res.Warnings...),
			}
			if !reflect.DeepEqual(got, want[i]) {
				t.Errorf("outcome differs\ngo:     %+v\ngolden: %+v", got, want[i])
			}
		})
	}
}

func quoteJoin(l []string) string {
	q := make([]string, len(l))
	for i, s := range l {
		q[i] = `"` + s + `"`
	}
	return strings.Join(q, ",")
}
