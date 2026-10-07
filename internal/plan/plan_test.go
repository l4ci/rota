package plan

import (
	"errors"
	"github.com/l4ci/rota/internal/exitcode"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/l4ci/rota/internal/proof"
)

func exitOf(err error) int {
	var ae *exitcode.Error
	if errors.As(err, &ae) {
		return ae.Exit
	}
	return 0
}

var created = regexp.MustCompile(`(?m)^created: \d{4}-\d{2}-\d{2}$`)

func golden(t *testing.T, name string) string {
	b, err := os.ReadFile(filepath.Join("testdata", "golden", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// project is the scenario the goldens were recorded on: repos web, api, web-docs.
func project(t *testing.T) string {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	os.MkdirAll(filepath.Join(root, ".rota", "designs"), 0o777)
	for _, d := range []string{"web", "api", "web-docs"} {
		os.MkdirAll(filepath.Join(root, d), 0o777)
	}
	os.WriteFile(filepath.Join(root, ".rota", "repos.json"),
		[]byte(`{"repos": [{"name": "web", "path": "web"}, {"name": "api", "path": "api"}, {"name": "web-docs", "path": "web-docs"}]}`), 0o644)
	return root
}

// The plan files must stay byte-identical to what the retired plan-add helper
// wrote (testdata/golden, frozen, changed only by reviewed edit), dates masked.
func TestAddMatchesOldHelper(t *testing.T) {
	root := project(t)
	os.WriteFile(filepath.Join(root, ".rota/designs/B07.md"), []byte("---\nid: B07\n---\n"), 0o644)
	steps := []struct {
		o        AddOpts
		key, knd string
	}{
		{AddOpts{Milestone: "M01", Slice: true, Title: "First slice"}, "M01-S01", "slice"},
		{AddOpts{Milestone: "M01", Slice: true, Title: "Second slice"}, "M01-S02", "slice"},
		{AddOpts{Key: "M01-B07", Title: "Item plan"}, "M01-B07", "item"},
		{AddOpts{Key: "M02-S05", Title: "Repos and design", Design: "B07", Repos: "web, api"}, "M02-S05", "slice"},
		{AddOpts{Milestone: "M02", Slice: true, Title: "After S05"}, "M02-S06", "slice"},
	}
	for _, s := range steps {
		key, kind, err := Add(root, Files(root), s.o)
		if err != nil || key != s.key || kind != s.knd {
			t.Fatalf("Add(%+v) = %q %q %v", s.o, key, kind, err)
		}
		got, _ := os.ReadFile(path(root, key))
		if g := created.ReplaceAllString(string(got), "created: DATE"); g != golden(t, key+".md") {
			t.Errorf("%s differs from golden:\n%s", key, g)
		}
	}
}

func TestAddArgumentExits(t *testing.T) {
	root := project(t)
	cases := []struct {
		name string
		o    AddOpts
		exit int
	}{
		{"no title", AddOpts{Key: "M01-B07"}, 2},
		{"bad key", AddOpts{Key: "M1-B07", Title: "t"}, 2},
		{"bad unit", AddOpts{Key: "M01-X07", Title: "t"}, 2},
		{"one-digit unit", AddOpts{Key: "M01-B7", Title: "t"}, 2},
		{"key and slice", AddOpts{Key: "M01-B07", Slice: true, Milestone: "M01", Title: "t"}, 2},
		{"key and milestone", AddOpts{Key: "M01-B07", Milestone: "M01", Title: "t"}, 2},
		{"slice without milestone", AddOpts{Slice: true, Title: "t"}, 2},
		{"nothing", AddOpts{Title: "t"}, 2},
		{"bad milestone", AddOpts{Milestone: "X", Slice: true, Title: "t"}, 2},
		{"bad design id", AddOpts{Key: "M01-B07", Title: "t", Design: "S01"}, 2},
		{"empty repos", AddOpts{Key: "M01-B07", Title: "t", Repos: " , "}, 2},
		{"design missing", AddOpts{Key: "M01-B07", Title: "t", Design: "B99"}, 3},
		{"repos unregistered", AddOpts{Key: "M01-B07", Title: "t", Repos: "web,ghost"}, 3},
	}
	for _, c := range cases {
		if _, _, err := Add(root, Files(root), c.o); exitOf(err) != c.exit {
			t.Errorf("%s: %v, want exit %d", c.name, err, c.exit)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".rota/plans")); err == nil {
		t.Error("a failed add created .rota/plans")
	}
	Add(root, Files(root), AddOpts{Key: "M01-B07", Title: "t"})
	if _, _, err := Add(root, Files(root), AddOpts{Key: "M01-B07", Title: "t"}); exitOf(err) != 4 {
		t.Errorf("duplicate: %v", err)
	}
}

func TestSliceMintingIgnoresOtherMilestonesAndItems(t *testing.T) {
	root := project(t)
	Add(root, Files(root), AddOpts{Key: "M01-S09", Title: "t"})
	Add(root, Files(root), AddOpts{Key: "M02-S30", Title: "t"})
	Add(root, Files(root), AddOpts{Key: "M01-B50", Title: "t"})
	key, _, _ := Add(root, Files(root), AddOpts{Milestone: "M01", Slice: true, Title: "t"})
	if key != "M01-S10" {
		t.Fatalf("key = %s", key)
	}
}

func TestSliceMintingConcurrent(t *testing.T) {
	root := project(t)
	var mu sync.Mutex
	keys := map[string]bool{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key, _, err := Add(root, Files(root), AddOpts{Milestone: "M01", Slice: true, Title: "t"})
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			keys[key] = true
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(keys) != 8 {
		t.Fatalf("minted %d distinct keys, want 8: %v", len(keys), keys)
	}
}

func TestAddConcurrentExplicitKey(t *testing.T) {
	root := project(t)
	var ok, refused atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch _, _, err := Add(root, Files(root), AddOpts{Key: "M01-B07", Title: "t"}); {
			case err == nil:
				ok.Add(1)
			case exitOf(err) == 4:
				refused.Add(1)
			default:
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 1 || refused.Load() != 7 {
		t.Fatalf("ok=%d refused=%d", ok.Load(), refused.Load())
	}
}

func TestListShowPutRm(t *testing.T) {
	root := project(t)
	Add(root, Files(root), AddOpts{Milestone: "M01", Slice: true, Title: "One"})
	Add(root, Files(root), AddOpts{Key: "M02-B07", Title: "Two", Repos: "web,api"})
	os.WriteFile(path(root, "M01-S99"), []byte("# no frontmatter\n"), 0o644)
	all, _ := List(Files(root), "")
	if len(all) != 2 || all[0].Key != "M01-S01" || all[0].UnitKind != "slice" || !reflect.DeepEqual(all[0].Repos, []string{}) ||
		all[1].Key != "M02-B07" || all[1].UnitKind != "item" || !reflect.DeepEqual(all[1].Repos, []string{"web", "api"}) {
		t.Fatalf("list = %+v", all)
	}
	if one, _ := List(Files(root), "M02"); len(one) != 1 || one[0].Key != "M02-B07" {
		t.Fatalf("filtered = %+v", one)
	}
	for _, k := range []string{"M1-B07", "M01-b07", "M01-X1", "B07"} {
		if _, err := Show(Files(root), k); exitOf(err) != 2 {
			t.Errorf("Show(%q) = %v, want exit 2", k, err)
		}
	}
	if _, err := Show(Files(root), "M09-B01"); exitOf(err) != 3 {
		t.Errorf("show missing: %v", err)
	}
	if changed, err := Put(Files(root), "M01-S01", "x\r\n"); err != nil || !changed {
		t.Fatalf("put: %v %v", changed, err)
	}
	if changed, _ := Put(Files(root), "M01-S01", "x\r\n"); changed {
		t.Error("identical put reported changed")
	}
	if _, err := Put(Files(root), "M09-B01", "x"); exitOf(err) != 3 {
		t.Errorf("put missing: %v", err)
	}
	if err := Rm(Files(root), "M01-S01"); err != nil {
		t.Fatal(err)
	}
	if err := Rm(Files(root), "M01-S01"); exitOf(err) != 3 {
		t.Errorf("second rm: %v", err)
	}
}

func TestValidateDocsMatchesOldHelper(t *testing.T) {
	root := project(t)
	os.MkdirAll(filepath.Join(root, ".rota/plans"), 0o777)
	os.WriteFile(path(root, "M03-B01"), []byte(golden(t, "validate-docs.plan.md")), 0o644)
	ms, text, err := ValidateDocs(root, "M03-B01")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.ReplaceAll(text, root, "ROOT"); got != golden(t, "validate-docs.out") {
		t.Errorf("text differs from golden:\n%s", got)
	}
	if len(ms) != 9 || ms[0].TargetRepo != "web" || ms[0].Suggestion == "" || ms[2].Issue != "sub-repo 'ghost' is not registered in .rota/repos.json" {
		t.Errorf("mismatches = %+v", ms)
	}
	// Doc homes present: clean, empty text.
	for _, d := range []string{"web/docs", "api/docs"} {
		os.MkdirAll(filepath.Join(root, d), 0o777)
	}
	os.WriteFile(path(root, "M03-B02"), []byte("---\nkey: M03-B02\nrepo: web\n---\n## Tasks\n- Files: docs/x.md, src/y.go\n"), 0o644)
	if ms, text, err := ValidateDocs(root, "M03-B02"); err != nil || len(ms) != 0 || text != "" {
		t.Errorf("clean plan: %v %q %v", ms, text, err)
	}
}

func TestValidateDocsExits(t *testing.T) {
	root := project(t)
	os.MkdirAll(filepath.Join(root, ".rota/plans"), 0o777)
	if _, _, err := ValidateDocs(root, "M01-B01"); exitOf(err) != 3 {
		t.Errorf("missing: %v", err)
	}
	if _, _, err := ValidateDocs(root, "nope"); exitOf(err) != 2 {
		t.Errorf("malformed: %v", err)
	}
	os.WriteFile(path(root, "M01-B01"), []byte("no frontmatter\n"), 0o644)
	if _, _, err := ValidateDocs(root, "M01-B01"); exitOf(err) != 70 {
		t.Errorf("no frontmatter: %v", err)
	}
}

func TestValidateDocsCustomSegmentAndCRLF(t *testing.T) {
	root := project(t)
	os.MkdirAll(filepath.Join(root, ".rota/plans"), 0o777)
	os.WriteFile(filepath.Join(root, ".rota/config.json"), []byte(`{"docs": {"path": "/handbook/"}}`), 0o644)
	os.WriteFile(path(root, "M01-B01"), []byte("---\r\nkey: M01-B01\r\n---\r\n## Tasks\r\n- Files: handbook/a.md, docs/b.md\r\n"), 0o644)
	ms, _, err := ValidateDocs(root, "M01-B01")
	if err != nil || len(ms) != 1 || ms[0].Path != "handbook/a.md" {
		t.Fatalf("got %+v %v", ms, err)
	}
}

func TestRenameCheck(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	for _, a := range [][]string{{"init", "-q"}} {
		c := exec.Command("git", a...)
		c.Dir = dir
		c.Run()
	}
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("oldname here\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0o777)
	os.WriteFile(filepath.Join(dir, "sub/b.txt"), []byte("oldname too\n"), 0o644)
	c := exec.Command("git", "add", "a.txt", "sub/b.txt")
	c.Dir = dir
	c.Run()
	if got := RenameCheck(dir, "oldname", nil); !reflect.DeepEqual(got, []string{"a.txt", "sub/b.txt"}) {
		t.Errorf("all = %v", got)
	}
	if got := RenameCheck(dir, "oldname", []string{"sub"}); !reflect.DeepEqual(got, []string{"sub/b.txt"}) {
		t.Errorf("scoped = %v", got)
	}
	if got := RenameCheck(dir, "nomatch", nil); got == nil || len(got) != 0 {
		t.Errorf("no match = %#v", got)
	}
	if got := RenameCheck(t.TempDir(), "x", nil); got == nil || len(got) != 0 {
		t.Errorf("non-git = %#v", got)
	}
}

// An explicit slice key and a minted one can name the same file; one lock
// guards both, so exactly one plan is created per key and none is overwritten.
func TestExplicitAndMintedSliceRace(t *testing.T) {
	for round := 0; round < 20; round++ {
		root := project(t)
		Add(root, Files(root), AddOpts{Key: "M01-S01", Title: "seed"}) // so minting targets S02
		var wg sync.WaitGroup
		var okExplicit, okMint atomic.Int32
		var mintedKey atomic.Value
		wg.Add(2)
		go func() {
			defer wg.Done()
			if _, _, err := Add(root, Files(root), AddOpts{Key: "M01-S02", Title: "explicit"}); err == nil {
				okExplicit.Add(1)
			} else if exitOf(err) != 4 {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			key, _, err := Add(root, Files(root), AddOpts{Milestone: "M01", Slice: true, Title: "minted"})
			if err != nil {
				t.Error(err)
				return
			}
			okMint.Add(1)
			mintedKey.Store(key)
		}()
		wg.Wait()
		// Minting always succeeds; if it took S02 the explicit add must have been refused,
		// otherwise it took S03 and both plans exist.
		files, _ := filepath.Glob(filepath.Join(root, ".rota/plans/M01-S*.md"))
		want := 2 + int(okExplicit.Load())
		if okMint.Load() != 1 || len(files) != want {
			t.Fatalf("round %d: explicit ok=%d mint=%v files=%v", round, okExplicit.Load(), mintedKey.Load(), files)
		}
		if got, _ := os.ReadFile(path(root, "M01-S02")); okExplicit.Load() == 1 &&
			!strings.Contains(string(got), "title: explicit") && mintedKey.Load() == "M01-S02" {
			t.Fatalf("round %d: minted plan overwrote the explicit one", round)
		}
	}
}

// Uncertain runs on the proof fixture (../proof/testdata/fixture) and must
// agree with the retired uncertain helper: the .rc and .txt goldens are its exit code and stdout.
func TestUncertainMatchesOldHelper(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join("..", "proof", "testdata", "fixture")
	rota := filepath.Join(root, ".rota")
	filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		rel, _ := filepath.Rel(src, p)
		if fi.IsDir() {
			return os.MkdirAll(filepath.Join(rota, rel), 0o777)
		}
		b, _ := os.ReadFile(p)
		return os.WriteFile(filepath.Join(rota, rel), b, 0o644)
	})
	// The recorded run did uncertain after its proof adds, so B07 has the detail file they create.
	if _, _, err := proof.Add(proof.Files(root), root, "B07", proof.AddOpts{Check: "unit  tests", Result: "PASS", Evidence: "go test ./... ok", Sha: "abc1234"}); err != nil {
		t.Fatal(err)
	}
	gold := filepath.Join("..", "proof", "testdata", "golden")
	for _, id := range []string{"B07", "B08", "B09", "F12", "F13", "T03"} {
		rc, _ := os.ReadFile(filepath.Join(gold, "uncertain-"+id+".rc"))
		txt, _ := os.ReadFile(filepath.Join(gold, "uncertain-"+id+".txt"))
		typ, reasons, err := uncertain(root, id)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		wantUncertain := strings.TrimSpace(string(rc)) == "0"
		if (len(reasons) > 0) != wantUncertain || typ != id[:1] {
			t.Errorf("%s: reasons %v, old rc %s", id, reasons, rc)
		}
		if got := strings.Join(reasons, "\n"); strings.TrimRight(string(txt), "\n") != got {
			t.Errorf("%s: reasons %q, old stdout %q", id, got, txt)
		}
	}
	if _, _, err := uncertain(root, "B99"); exitOf(err) != 3 {
		t.Errorf("missing item: %v", err)
	}
	if _, _, err := uncertain(t.TempDir(), "B07"); exitOf(err) != 3 {
		t.Errorf("missing BACKLOG.md: %v", err)
	}
}

func TestUncertainHonoursOpenSections(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".rota"), 0o777)
	os.WriteFile(filepath.Join(root, ".rota/BACKLOG.md"), []byte("## Bugs\n\n- **[B07] [Major] Vague.** unclear? TBD?\n\n## Features\n\n- **[F01] [Major] Vague.** unclear? TBD?\n"), 0o644)
	if _, r, err := uncertain(root, "F01"); err != nil || len(r) == 0 {
		t.Fatalf("default sections: %v %v", r, err)
	}
	if _, _, err := uncertainIn(root, "F01", "Bugs"); exitOf(err) != 3 {
		t.Errorf("F01 outside the Bugs-only sections: %v", err)
	}
	if _, r, err := uncertainIn(root, "B07", "Bugs"); err != nil || len(r) == 0 {
		t.Errorf("B07 inside: %v %v", r, err)
	}
}

// uncertain is Uncertain on the file backlog with the default sections.
func uncertain(root, id string) (string, []string, error) {
	return uncertainIn(root, id, "")
}

// uncertainIn is Uncertain on the file backlog with openSections as passed in.
func uncertainIn(root, id, openSections string) (string, []string, error) {
	_, typ, reasons, err := Uncertain(FileItems(root, openSections), id)
	return typ, reasons, err
}

func asErr(err error, ae **exitcode.Error) bool { return errors.As(err, ae) }

// A plan stub carries a `## Relies on` section so /rota-plan lists the
// KNOWLEDGE and DECISIONS entries the plan depends on and /rota-review can
// check the diff against them.
func TestStubHasReliesOnHeading(t *testing.T) {
	root := project(t)
	for _, o := range []AddOpts{
		{Key: "M01-B07", Title: "Item plan"},
		{Milestone: "M01", Slice: true, Title: "Slice plan"},
	} {
		key, _, err := Add(root, Files(root), o)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(path(root, key))
		if n := strings.Count(string(got), "\n## Relies on\n"); n != 1 {
			t.Errorf("%s has %d `## Relies on` headings, want 1:\n%s", key, n, got)
		}
	}
}
