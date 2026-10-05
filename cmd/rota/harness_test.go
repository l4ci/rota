package main

// Harness for the cmd/rota scenario suites (#48, #53). Each scenario builds a
// fixture project (git repo with commits, .rota/ tree), copies it, runs the Go
// binary in the copy and checks the exit code and the scenario's own
// assertions. Every scenario that is not goOnly is then compared with its
// frozen record (frozen_test.go): the exit code, the --json envelope, the
// .rota/ files the run changed and, for the forge suites, the fake forge's
// database. The records were taken while the old helpers still existed and
// Go matched them, so they stand in for the retired oracle.
//
// The suites: scenarios_item (item verbs), backlog (backlog, summary, status,
// refactor), config (config, repo, update), issues (issue sync and migrate),
// item_issue (item verbs in issue mode) and umbrella (umbrella projects).
//
// Safety: TestMain puts test/fakes first on PATH and checks that gh resolves
// there; the binary never reaches the real gh or glab. Fixtures live in temp
// dirs.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/gittest"
)

var (
	rotaBin    string // the built Go binary
	repoDir    string
	baseEnv    []string
	harnessTmp string
)

func TestMain(m *testing.M) { os.Exit(runMain(m)) }

func runMain(m *testing.M) int {
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	repoDir = filepath.Clean(filepath.Join(wd, "..", ".."))
	fakes := filepath.Join(repoDir, "test", "fakes")
	os.Setenv("PATH", fakes+string(os.PathListSeparator)+os.Getenv("PATH"))
	if gh, err := exec.LookPath("gh"); err != nil || !strings.HasPrefix(gh, fakes+string(os.PathSeparator)) {
		fmt.Fprintf(os.Stderr, "gh does not resolve into %s (got %q, %v); refusing to run\n", fakes, gh, err)
		return 1
	}
	pinDay()
	harnessTmp, err = os.MkdirTemp("", "rota-scenarios-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(harnessTmp)
	// Root every temp dir (t.TempDir, the binary's and the fakes' mktemp)
	// under harnessTmp, so the RemoveAll above takes them too (#110) and the
	// frozen records can name them (normFrozen).
	childTmp := filepath.Join(harnessTmp, "tmp")
	if err := os.Mkdir(childTmp, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	os.Setenv("TMPDIR", childTmp)
	rotaBin = filepath.Join(harnessTmp, "rota")
	build := exec.Command("go", "build", "-o", rotaBin, ".")
	build.Dir = filepath.Join(repoDir, "cmd", "rota")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "go build: %v\n%s", err, out)
		return 1
	}
	baseEnv = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + harnessTmp,
		"ROTA_TEST_DOCTOR_DISK=50:100", // a healthy disk: the real one must not change the goldens (#85)
		"FAKE_TRACKER_DB=" + filepath.Join(harnessTmp, "tracker.json"),
		"LC_ALL=C.UTF-8",
	}
	return m.Run()
}

// TestFakesFirst is the guard: tests must never reach the real gh or glab.
func TestFakesFirst(t *testing.T) {
	fakes := filepath.Join(repoDir, "test", "fakes") + string(os.PathSeparator)
	for _, tool := range []string{"gh", "glab"} {
		p, err := exec.LookPath(tool)
		if err != nil || !strings.HasPrefix(p, fakes) {
			t.Fatalf("%s resolves to %q (%v), not test/fakes", tool, p, err)
		}
	}
}

// ---- fixtures ---------------------------------------------------------------

type info struct {
	h1, refactor, head string
	x                  map[string]string
}

// fx describes a fixture project; the zero value is the standard one.
type fx struct {
	archive   string            // "" none, "plain" (no headings, as the 4.x archiver wrote it), "sectioned"
	noBacklog bool              // no .rota/BACKLOG.md
	noCommit  bool              // git repo without a commit
	noHV      bool              // no .rota/ at all
	backlog   string            // replaces BACKLOG.md ("{h1}" etc. are expanded)
	config    string            // replaces config.json
	counters  string            // replaces counters.json; "-" deletes it
	status    string            // status.json content
	commits   int               // extra commits that mention parser-core and lexer-v2
	files     map[string]string // extra files, path relative to the project; "{h1}" etc. expanded
	// subs makes umbrella sub-repos after the project's last commit: name to the
	// commit subjects to create, and a .rota/repos.json registering them by name.
	subs map[string][]string
	// after runs last, with the project built; it may commit, write files and
	// publish tokens with in.x["name"], which expand as {x:name}.
	after func(t *testing.T, dir string, in *info)
}

const stdBacklog = `# TODO

## Bugs
- **[B01] [P1] First bug.** Something broke. Detail: ` + "`.rota/bugs/B01.md`" + ` Related: [F01] Milestone: M01 Since: {h1}
- **[B02] [P2] Second bug.** Other thing. Related: [B01], [F01] Since: {h1}
- **[B03] [P3] Third bug.** Third. Milestone: M02 Repos: web Subsystem: capture Since: {h1}
- **[B04] [P2] Fix v1.2 parser.** Dotted title. Since: {h1}

## Features
- **[F01] [Major] First feature.** Feature body. Detail: ` + "`.rota/features/F01.md`" + ` Related: [B01] Repos: web Since: {h1}
- **[F02] [Minor] Second feature.** Body. Related: [B01] Subsystem: capture Since: {h1}

## Tasks
- **[T01] First task.** Task body.
- **[T02] Second task.** Related: [F01]

## Completed
- ~~**[B08] [P2] Done bug.** body Since: {h1}~~ Done 2026-09-30 [` + "`{h1}`" + `]
- ~~**[F08] [Major] Done refactor feature.** body~~ Done 2026-09-30 [` + "`{refactor}`" + `]
- ~~**[T08] Done task.** body~~ Done 2026-09-30 [` + "`{h1}`" + `]
- ~~**[B09] [P2] Old bug.** x Related: [B01]~~ Done 2026-09-30 [` + "`abc1234`" + `] (dropped: no longer needed)
`

const stdCounters = `{
  "bugs": 4,
  "features": 2,
  "tasks": 2,
  "milestones": 1,
  "since_refactor": {
    "features": 1,
    "bugs": 2
  }
}
`

const stdConfig = `{
  "backlog": {
    "backend": "file"
  }
}
`

const issuesConfig = `{
  "backlog": {
    "backend": "issues"
  }
}
`

const archiveItems = "- ~~**[B05] [P2] Archived bug.** old Related: [B01]~~ Done 2026-05-15 [`e4abdbe`]\n" +
	"- ~~**[F05] [Minor] Archived feature.** old~~ Done 2026-05-15 [`e4abdbe`]\n"

var stdFiles = map[string]string{
	".rota/bugs/B01.md": "# B01: First bug\n\n## Acceptance\n- [ ] it works\n\n## Proof\n- build · PASS · ok\n- tests · PASS · ok\n\n## Log\n" +
		"- 2026-09-01 · question · Why?\n  more detail\n- 2026-09-02 · answer · Because.\n",
	".rota/features/F01.md":  "# F01: First feature\n\n## Plan\nno criteria here\n",
	".rota/designs/F02.md":   "# design\n",
	".rota/plans/M01-B02.md": "plan b02\n",
	".rota/plans/M02-F01.md": "plan f01\n",
	"src/main.go":            "package main\n",
	"body.md":                "# {ID}\n\nSee [{ID}] in the backlog.\n",
}

var (
	dayTok = regexp.MustCompile(`\{d(\d+)\}`)
	xTok   = regexp.MustCompile(`\{x:([a-z0-9-]+)\}`)
)

// expand fills {h1}, {refactor}, {head}, {dN} (the date N days ago, local, as
// the helpers' date.today() sees it) and {x:name} (hook tokens).
func expand(s string, i info) string {
	s = strings.NewReplacer("{h1}", i.h1, "{refactor}", i.refactor, "{head}", i.head).Replace(s)
	s = dayTok.ReplaceAllStringFunc(s, func(m string) string {
		n, _ := strconv.Atoi(dayTok.FindStringSubmatch(m)[1])
		d, _ := time.ParseInLocation("2006-01-02", localDay, time.Local)
		return d.AddDate(0, 0, -n).Format("2006-01-02")
	})
	return xTok.ReplaceAllStringFunc(s, func(m string) string { return i.x[xTok.FindStringSubmatch(m)[1]] })
}

// commitFile writes path and commits it, returning the short hash.
func commitFile(t *testing.T, dir, subject, path, content string) string {
	t.Helper()
	return gitRunner().Commit(t, dir, subject, path, content)
}

// gitRunner pins the fixture's commit time (noon UTC of the harness day) so its
// hashes repeat from run to run; the frozen records hold them.
func gitRunner() gittest.Runner {
	when := startDay + "T12:00:00Z"
	return gittest.Runner{Env: append(append([]string{}, baseEnv...), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_DATE="+when, "GIT_COMMITTER_DATE="+when)}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return gitRunner().Run(t, dir, args...)
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fxTemplates caches built fixtures for the process: most scenarios share a
// handful of fixture shapes, and building one costs about twenty git calls.
var fxTemplates = struct {
	sync.Mutex
	m map[string]*fxTemplate
}{m: map[string]*fxTemplate{}}

type fxTemplate struct {
	once sync.Once
	dir  string
	in   info
}

// build returns a fresh copy of the fixture (a temp dir of the test) with the
// hashes. The fixture is built once per distinct description; the after hook,
// which the description cannot capture, runs on each copy.
func (f fx) build(t *testing.T) (string, info) {
	t.Helper()
	after := f.after
	f.after = nil
	key := fmt.Sprintf("%#v", f)
	fxTemplates.Lock()
	tpl := fxTemplates.m[key]
	if tpl == nil {
		tpl = &fxTemplate{}
		fxTemplates.m[key] = tpl
	}
	fxTemplates.Unlock()
	tpl.once.Do(func() {
		dir, err := os.MkdirTemp(harnessTmp, "fx-")
		if err != nil {
			t.Fatal(err)
		}
		tpl.dir, tpl.in = dir, f.buildIn(t, dir)
	})
	if tpl.dir == "" {
		t.Fatal("fixture template failed to build")
	}
	dir := copyTree(t, tpl.dir)
	in := tpl.in
	in.x = map[string]string{}
	for k, v := range tpl.in.x {
		in.x[k] = v
	}
	if after != nil {
		after(t, dir, &in)
	}
	return dir, in
}

// buildIn makes the fixture in dir and returns the hashes.
func (f fx) buildIn(t *testing.T, dir string) info {
	t.Helper()
	var in info
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "config", "user.name", "Fixture")
	git(t, dir, "config", "user.email", "fixture@example.com")
	git(t, dir, "config", "commit.gpgsign", "false")
	if !f.noCommit {
		write(t, dir, "README.md", "readme\n")
		git(t, dir, "add", "README.md")
		git(t, dir, "commit", "-q", "-m", "chore: initial commit")
		in.h1 = git(t, dir, "rev-parse", "--short", "HEAD")
		write(t, dir, "tidy.txt", "tidy\n")
		git(t, dir, "add", "tidy.txt")
		git(t, dir, "commit", "-q", "-m", "refactor: tidy the lexer")
		in.refactor = git(t, dir, "rev-parse", "--short", "HEAD")
	} else {
		in.h1, in.refactor = "0000000", "0000001"
	}
	if !f.noHV {
		backlog := stdBacklog
		if f.backlog != "" {
			backlog = f.backlog
		}
		if !f.noBacklog {
			write(t, dir, ".rota/BACKLOG.md", expand(backlog, in))
		} else {
			if err := os.MkdirAll(filepath.Join(dir, ".rota"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		cfg := stdConfig
		if f.config != "" {
			cfg = f.config
		}
		write(t, dir, ".rota/config.json", cfg)
		switch f.counters {
		case "-":
		case "":
			write(t, dir, ".rota/counters.json", stdCounters)
		default:
			write(t, dir, ".rota/counters.json", f.counters)
		}
		switch f.archive {
		case "plain":
			write(t, dir, ".rota/ARCHIVE.md", "# Archive\n\nCompleted items older than the active window.\n"+archiveItems)
		case "sectioned":
			write(t, dir, ".rota/ARCHIVE.md", "# Archive\n\n## Completed\n"+archiveItems)
		}
		if f.status != "" {
			write(t, dir, ".rota/status.json", f.status)
		}
		for p, c := range stdFiles {
			if _, over := f.files[p]; !over {
				write(t, dir, p, expand(c, in))
			}
		}
		for p, c := range f.files {
			if c != "" {
				write(t, dir, p, expand(c, in))
			}
		}
	}
	if !f.noCommit {
		write(t, dir, "feature.txt", "x\n")
		git(t, dir, "add", "-A")
		git(t, dir, "commit", "-q", "-m", "feat: add `parser-core` lexer-v2 support")
		for n := 1; n <= f.commits; n++ {
			write(t, dir, fmt.Sprintf("part%d.txt", n), "x\n")
			git(t, dir, "add", "-A")
			git(t, dir, "commit", "-q", "-m", fmt.Sprintf("feat: add `parser-core` lexer-v2 part %d", n))
		}
		in.head = git(t, dir, "rev-parse", "--short", "HEAD")
	}
	in.x = map[string]string{}
	if len(f.subs) > 0 {
		var names []string
		for n := range f.subs {
			names = append(names, n)
		}
		sort.Strings(names)
		var entries []string
		for _, n := range names {
			sub := filepath.Join(dir, n)
			if err := os.MkdirAll(sub, 0o755); err != nil {
				t.Fatal(err)
			}
			git(t, sub, "init", "-q", "-b", "main")
			git(t, sub, "config", "user.name", "Fixture")
			git(t, sub, "config", "user.email", "fixture@example.com")
			git(t, sub, "config", "commit.gpgsign", "false")
			for k, subject := range f.subs[n] {
				commitFile(t, sub, subject, fmt.Sprintf("f%d.txt", k), "x\n")
			}
			entries = append(entries, fmt.Sprintf(`{"name": %q, "path": %q}`, n, n))
		}
		write(t, dir, ".rota/repos.json", `{"repos": [`+strings.Join(entries, ", ")+`]}`+"\n")
	}
	return in
}

func copyTree(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	if out, err := exec.Command("cp", "-a", src+"/.", dst).CombinedOutput(); err != nil {
		t.Fatalf("cp: %v\n%s", err, out)
	}
	return dst
}

// snapshot is the .rota/ tree: relative path to content, lock files left out.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	base := filepath.Join(root, ".rota")
	filepath.Walk(base, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || strings.HasSuffix(p, ".lock") {
			return nil
		}
		b, _ := os.ReadFile(p)
		rel, _ := filepath.Rel(root, p)
		out[rel] = string(b)
		return nil
	})
	return out
}

// ---- running ----------------------------------------------------------------

type run struct {
	code           int
	stdout, stderr string
	dir            string
}

func exec1e(t *testing.T, dir, stdin string, env []string, name string, args ...string) run {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(append([]string{}, baseEnv...), env...)
	cmd.Stdin = strings.NewReader(stdin)
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("%s: %v", name, err)
		}
		code = ee.ExitCode()
	}
	return run{code, so.String(), se.String(), dir}
}

type envl map[string]any

func parseEnv(t *testing.T, who string, r run) envl {
	t.Helper()
	var e envl
	if err := json.Unmarshal([]byte(r.stdout), &e); err != nil {
		t.Fatalf("%s: stdout is not one JSON document: %v\nstdout: %q\nstderr: %s", who, err, r.stdout, r.stderr)
	}
	return e
}

// at walks a dotted path through an envelope ("data.items.0.id").
func at(v any, path string) any {
	for _, part := range strings.Split(path, ".") {
		switch t := v.(type) {
		case envl:
			v = t[part]
		case map[string]any:
			v = t[part]
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i >= len(t) {
				return nil
			}
			v = t[i]
		default:
			return nil
		}
	}
	return v
}

func subst(args []string, i info) []string {
	out := make([]string, len(args))
	for k, a := range args {
		out[k] = expand(a, i)
	}
	return out
}

// scn is one scenario.
type scn struct {
	name  string
	fx    fx
	argv  []string // argv without "rota"; includes --json
	in    string   // stdin
	want  int      // the exit code
	check func(t *testing.T, goEnv envl)
	// goOnly scenarios have no frozen record: the old helpers had no
	// counterpart, so want and check are all they assert.
	goOnly bool
	// env is extra environment for every run of the scenario (ROTA_TEST_* hooks).
	env []string
	// text also records the plain, non --json stdout, for the read-only verbs
	// whose text the old helper's stdout defined.
	text bool
	// cwd is the subdirectory of the project the binary runs in ("" is the root).
	cwd string
	// prep runs on the copy of the fixture before the scenario, for state
	// that does not survive a copy (a git worktree points at its origin).
	prep func(t *testing.T, dir string)
	// bin replaces the Go binary for scenarios that need another install
	// layout (the update verb).
	bin string
}

func (s scn) goBin() string {
	if s.bin != "" {
		return s.bin
	}
	return rotaBin
}

// variants runs a scenario with no archive, a plain archive and a sectioned one.
func variants(s scn) []scn {
	var out []scn
	for _, a := range []string{"", "plain", "sectioned"} {
		v := s
		v.fx.archive = a
		if a == "" {
			v.name = s.name + "/noarchive"
		} else {
			v.name = s.name + "/archive-" + a
		}
		out = append(out, v)
	}
	return out
}

// startDay is the UTC date the harness started; a timestamp on or after it was
// written by the run under test, an older one came from a fixture.
var startDay = time.Now().UTC().Format("2006-01-02")

func (s scn) exec(t *testing.T) {
	t.Parallel()
	base, in := s.fx.build(t)
	dir := copyTree(t, base)
	if s.prep != nil {
		s.prep(t, dir)
	}
	argv := subst(s.argv, in)
	env := s.env
	if !s.goOnly {
		env = frozenEnv(env)
	}
	cwd := filepath.Join(dir, s.cwd)
	r := exec1e(t, cwd, s.in, env, s.goBin(), argv...)
	if r.code != s.want {
		t.Errorf("exit = %d, want %d\nargv: %v\nstdout: %s\nstderr: %s", r.code, s.want, argv, r.stdout, r.stderr)
	}
	var st frozenStep
	if !s.goOnly {
		st = newStep(t, r, snapshot(t, base), dir, nil)
		if s.text {
			st.text(exec1e(t, cwd, s.in, env, s.goBin(), withoutJSON(argv)...).stdout)
		}
	}
	e := parseEnv(t, "go", r)
	if e["ok"] != (s.want == 0) {
		t.Errorf("envelope ok = %v for exit %d", e["ok"], r.code)
	}
	if s.check != nil {
		e["__info"] = in
		e["__godir"] = dir
		s.check(t, e)
	}
	if !s.goOnly {
		frozenCheck(t, frozenRec{Steps: []frozenStep{st}})
	}
}

// ---- scenarios --------------------------------------------------------------

func eq(t *testing.T, e envl, path string, want any) {
	t.Helper()
	got := at(e, path)
	if w, ok := want.(string); ok {
		if in, ok := e["__info"].(info); ok {
			want = expand(w, in)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %#v, want %#v", path, got, want)
	}
}

func j(args ...string) []string { return append([]string{"--json"}, args...) }

// TestFixtureHashesRepeat: a fixed commit time makes an independent build
// reproduce a fixture's hashes, which the frozen records rely on.
func TestFixtureHashesRepeat(t *testing.T) {
	_, a := fx{commits: 2}.build(t)
	dir := t.TempDir()
	b := fx{commits: 2}.buildIn(t, dir)
	if a.h1 != b.h1 || a.refactor != b.refactor || a.head != b.head {
		t.Errorf("fixture hashes differ between builds: %+v vs %+v", a, b)
	}
}

func TestFixtureTemplatesAreIndependent(t *testing.T) {
	var seen []string
	f := fx{after: func(t *testing.T, dir string, in *info) {
		in.x["tag"] = dir
		seen = append(seen, dir)
	}}
	a, ia := f.build(t)
	b, ib := f.build(t)
	if a == b || ia.x["tag"] == ib.x["tag"] || len(seen) != 2 {
		t.Fatalf("copies share state: %s %s %v", a, b, seen)
	}
	write(t, a, ".rota/extra.md", "only in a\n")
	if _, err := os.Stat(filepath.Join(b, ".rota", "extra.md")); err == nil {
		t.Error("a write to one copy reached the other")
	}
	if _, err := os.Stat(filepath.Join(b, ".rota", "BACKLOG.md")); err != nil {
		t.Error("the template lost its files")
	}
	if ia.h1 == "" || ia.h1 != ib.h1 {
		t.Errorf("hashes: %q %q", ia.h1, ib.h1)
	}
}
