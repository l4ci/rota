package cli

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/golden"
	"github.com/l4ci/rota/internal/tracker"
)

// releaseTree reads every file under dir (skipping .git) into a map.
func releaseTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		b, err := os.ReadFile(p)
		rel, _ := filepath.Rel(dir, p)
		out[rel] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// releaseProject makes a project dir holding files.
func releaseProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".rota", "config.json"), `{}`)
	for p, c := range files {
		write(t, filepath.Join(dir, p), c)
	}
	return dir
}

func releaseData(t *testing.T, o trOut) map[string]any {
	t.Helper()
	d, _ := envelope(t, o.stdout)["data"].(map[string]any)
	return d
}

func releaseMsg(t *testing.T, o trOut) string {
	t.Helper()
	e, _ := envelope(t, o.stdout)["error"].(map[string]any)
	s, _ := e["message"].(string)
	return s
}

var releaseBumpFixtures = map[string]struct{ file, kind, text string }{
	"plugin-json": {".claude-plugin/plugin.json", "plugin-json",
		"{\n  \"name\": \"h\u00e9llo \u2603 \\u00fc\",\n  \"version\": \"1.2.3\",\n  \"nested\": {\"b\": [1, 2, {\"x\": null}], \"a\": 1.5, \"e\": 1e3},\n  \"keywords\": [],\n  \"o\": {}\n}\n"},
	"package-json": {"package.json", "package-json", `{"version":"0.9.9","scripts":{"b":"x","a":"y"}}`},
	"pyproject": {"pyproject.toml", "pyproject",
		"[build-system]\nrequires = [\"x\"]\n\n[project]\nname = \"x\"\nversion = \"1.2.3\"  # c\n\n[tool.foo]\nversion = \"9.9.9\"\n"},
	"poetry": {"pyproject.toml", "pyproject",
		"[tool.poetry]\nname = \"x\"\nversion   =   \"2.0.9\"\n\n[tool.other]\nversion = \"7.0.0\"\n"},
	"pyproject-crlf": {"pyproject.toml", "pyproject", "[project]\r\nversion = \"1.0.0\"\r\nname = \"x\"\r\n"},
	"cargo":          {"Cargo.toml", "cargo", "[package]\nname = \"x\"\nversion = \"3.4.5\"\n\n[dependencies]\nversion = \"1.0.0\"\n"},
	"plain":          {"VERSION", "plain", "  1.2.3  \n\n"},
	"version.txt":    {"version.txt", "plain", "\n\n4.5.6"},
}

// releaseBumpCase is one fixture bumped one way; the goldens hold what the
// retired bump helper printed and wrote for it.
type releaseBumpCase struct {
	Name, File, Kind, Text, Flag, Arg string
}

type releaseBumpWant struct {
	To   string `json:"to"`   // the helper's stdout
	File string `json:"file"` // the version file afterwards
}

func releaseBumpCases() []releaseBumpCase {
	names := make([]string, 0, len(releaseBumpFixtures))
	for name := range releaseBumpFixtures {
		names = append(names, name)
	}
	slices.Sort(names)
	var out []releaseBumpCase
	for _, name := range names {
		fx := releaseBumpFixtures[name]
		for _, b := range [][2]string{{"--level", "patch"}, {"--level", "minor"}, {"--level", "major"}, {"--to", "10.0.0"}} {
			out = append(out, releaseBumpCase{name, fx.file, fx.kind, fx.text, b[0], b[1]})
		}
	}
	return out
}

func TestReleaseBumpParity(t *testing.T) {
	cases := releaseBumpCases()
	got := make([]releaseBumpWant, len(cases))
	for i, c := range cases {
		dir := releaseProject(t, map[string]string{c.File: c.Text})
		o := trRun(t, dir, "", "release", "bump", "--file", c.File, "--kind", c.Kind, c.Flag, c.Arg, "--json")
		if o.code != 0 {
			t.Fatalf("%s %s %s: exit %d\n%s%s", c.Name, c.Flag, c.Arg, o.code, o.stdout, o.stderr)
		}
		d := releaseData(t, o)
		if d["changed"] != true || d["kind"] != c.Kind || d["file"] != c.File {
			t.Errorf("%s %s %s: data %v", c.Name, c.Flag, c.Arg, d)
		}
		to, _ := d["to"].(string)
		got[i] = releaseBumpWant{To: to, File: releaseTree(t, dir)[c.File]}
	}
	golden.Check(t, cases, got)
}

func TestReleaseBumpDetectsFile(t *testing.T) {
	dir := releaseProject(t, map[string]string{"package.json": `{"version":"1.0.0"}`, "VERSION": "5.0.0\n"})
	o := trRun(t, dir, "", "release", "bump", "--level", "patch", "--json")
	d := releaseData(t, o)
	if o.code != 0 || d["file"] != "package.json" || d["kind"] != "package-json" || d["from"] != "1.0.0" || d["to"] != "1.0.1" {
		t.Fatalf("%d %v", o.code, d)
	}
	tree := releaseTree(t, dir)
	if tree["package.json"] != "{\n  \"version\": \"1.0.1\"\n}\n" || tree["VERSION"] != "5.0.0\n" {
		t.Errorf("files: %q", tree)
	}
	// An explicit --kind overrides the detected one; a bare name picks the kind.
	o = trRun(t, dir, "", "release", "bump", "--file", "VERSION", "--to", "5.0.1", "--json")
	if o.code != 0 || releaseData(t, o)["kind"] != "plain" || releaseTree(t, dir)["VERSION"] != "5.0.1\n" {
		t.Fatalf("%d %s", o.code, o.stdout)
	}
}

func TestReleaseBumpExits(t *testing.T) {
	files := map[string]string{
		"package.json":   `{"version":"2.0.0"}`,
		"bad.json":       `{"version":`,
		"nover.json":     `{"name":"x"}`,
		"pyproject.toml": "[project]\nname = \"x\"\n",
		"semver.json":    `{"version":"v1"}`,
	}
	dir := releaseProject(t, files)
	before := releaseTree(t, dir)
	cases := []struct {
		name      string
		args      []string
		code      int
		blockedBy string
	}{
		{"equal", []string{"--to", "2.0.0"}, 4, "not greater"},
		{"lower", []string{"--to", "1.9.9"}, 4, "not greater"},
		{"neither", nil, 2, ""},
		{"both", []string{"--level", "patch", "--to", "3.0.0"}, 2, ""},
		{"bad level", []string{"--level", "huge"}, 2, ""},
		{"bad to", []string{"--to", "3.0"}, 2, ""},
		{"bad kind", []string{"--level", "patch", "--kind", "yaml"}, 2, ""},
		{"unknown file name", []string{"--level", "patch", "--file", "x.cfg"}, 2, ""},
		{"missing file", []string{"--level", "patch", "--file", "gone.json"}, 3, ""},
		{"corrupt json", []string{"--level", "patch", "--file", "bad.json"}, 3, ""},
		{"no version field", []string{"--level", "patch", "--file", "nover.json"}, 3, ""},
		{"no version in toml", []string{"--level", "patch", "--file", "pyproject.toml"}, 3, ""},
		{"not semver", []string{"--level", "patch", "--file", "semver.json"}, 3, ""},
	}
	for _, c := range cases {
		o := trRun(t, dir, "", append(append([]string{"release", "bump"}, c.args...), "--json")...)
		if o.code != c.code {
			t.Errorf("%s: exit %d, want %d\n%s%s", c.name, o.code, c.code, o.stdout, o.stderr)
			continue
		}
		if c.blockedBy != "" {
			d := releaseData(t, o)
			if d["blockedBy"] != c.blockedBy || d["changed"] != false {
				t.Errorf("%s: failure data %v", c.name, d)
			}
		}
	}
	if !reflect.DeepEqual(releaseTree(t, dir), before) {
		t.Error("a refused or failed bump touched a file")
	}
}

// releaseVersionWant is what the retired detect and bump helpers printed for a
// fixture: the detect JSON, and the dry-run bump to the next minor.
type releaseVersionWant struct {
	Detect map[string]any `json:"detect"`
	Next   string         `json:"next"`
}

func TestReleaseVersionParity(t *testing.T) {
	var cases []releaseBumpCase // one per fixture; Flag and Arg unused
	for _, c := range releaseBumpCases() {
		if c.Flag == "--level" && c.Arg == "patch" && c.Name != "version.txt" && c.Name != "pyproject-crlf" {
			c.Flag, c.Arg = "", ""
			cases = append(cases, c)
		}
	}
	got := make([]releaseVersionWant, len(cases))
	for i, c := range cases {
		dir := releaseProject(t, map[string]string{c.File: c.Text})
		o := trRun(t, dir, "", "release", "version", "--json")
		if o.code != 0 {
			t.Errorf("%s: exit %d %s", c.Name, o.code, o.stdout)
		}
		got[i].Detect = releaseData(t, o)
		o = trRun(t, dir, "", "release", "version", "--level", "minor", "--json")
		if o.code != 0 {
			t.Errorf("%s: next exit %d %s", c.Name, o.code, o.stdout)
		}
		got[i].Next, _ = releaseData(t, o)["next"].(string)
		if tree := releaseTree(t, dir)[c.File]; tree != c.Text {
			t.Errorf("%s: version --level wrote the file", c.Name)
		}
	}
	golden.Check(t, cases, got)
}

// releaseVersionOverrides are the versionFile settings tried by
// TestReleaseVersionPriorityAndOverride.
var releaseVersionOverrides = []string{"package.json", "other/version.txt", "missing.json"}

var releaseVersionPriorityFiles = map[string]string{
	"VERSION": "9.9.9\n", "Cargo.toml": "[package]\nversion = \"3.0.0\"\n", "package.json": `{"version":"2.5.0"}`,
	".claude-plugin/plugin.json": `{"version":"1.0.0"}`, "other/version.txt": "8.0.0\n",
}

// releaseRunWant is a retired helper's exit code and stdout.
type releaseRunWant struct {
	RC  int    `json:"rc"`
	Out string `json:"out"`
}

// releaseDetectJSON spells the detect data the way the golden records it:
// file, version, kind, indented by two.
func releaseDetectJSON(t *testing.T, d map[string]any) string {
	t.Helper()
	b, err := json.MarshalIndent(struct {
		File    any `json:"file"`
		Version any `json:"version"`
		Kind    any `json:"kind"`
	}{d["file"], d["version"], d["kind"]}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

func TestReleaseVersionPriorityAndOverride(t *testing.T) {
	dir := releaseProject(t, releaseVersionPriorityFiles)
	o := trRun(t, dir, "", "release", "version", "--json")
	d := releaseData(t, o)
	if o.code != 0 || d["file"] != ".claude-plugin/plugin.json" {
		t.Fatalf("priority: %d %v", o.code, d)
	}
	// The default detection, then one run per override; Out is the detect data as the golden spells it.
	got := []releaseRunWant{{RC: o.code, Out: releaseDetectJSON(t, d)}}
	for _, over := range releaseVersionOverrides {
		write(t, filepath.Join(dir, ".rota", "config.json"), `{"release":{"versionFile":"`+over+`"}}`)
		o := trRun(t, dir, "", "release", "version", "--json")
		if o.code == 0 {
			got = append(got, releaseRunWant{Out: releaseDetectJSON(t, releaseData(t, o))})
			continue
		}
		if o.code != 3 || !strings.Contains(releaseMsg(t, o), "does not exist") {
			t.Errorf("override %s: exit %d %s", over, o.code, o.stdout)
		}
		got = append(got, releaseRunWant{RC: 1}) // the helper exited 1 where rota exits 3
	}
	// config.local.json wins over config.json.
	write(t, filepath.Join(dir, ".rota", "config.local.json"), `{"release":{"versionFile":"package.json"}}`)
	if o := trRun(t, dir, "", "release", "version", "--json"); releaseData(t, o)["file"] != "package.json" {
		t.Errorf("local override ignored: %s", o.stdout)
	}
	golden.Check(t, map[string]any{"files": releaseVersionPriorityFiles, "overrides": releaseVersionOverrides}, got)
}

// TestReleaseVersionFileViaConfigSet sets the override through `config set`
// (not a hand-written config.json) and sees `release version` read that file.
func TestReleaseVersionFileViaConfigSet(t *testing.T) {
	dir := releaseProject(t, releaseVersionPriorityFiles)
	if o := trRun(t, dir, "", "config", "set", "release.versionFile", "other/version.txt"); o.code != 0 {
		t.Fatalf("config set: %d %s", o.code, o.stdout)
	}
	o := trRun(t, dir, "", "release", "version", "--json")
	if d := releaseData(t, o); o.code != 0 || d["file"] != "other/version.txt" || d["version"] != "8.0.0" {
		t.Fatalf("version: %d %v", o.code, d)
	}
	if o := trRun(t, dir, "", "config", "set", "release.versionFile", "../x"); o.code != 2 {
		t.Errorf("outside path: exit %d, want 2", o.code)
	}
}

func TestReleaseVersionExits(t *testing.T) {
	empty := releaseProject(t, nil)
	if o := trRun(t, empty, "", "release", "version", "--json"); o.code != 3 || !strings.Contains(releaseMsg(t, o), "no version file detected") {
		t.Errorf("none: %d %s", o.code, o.stdout)
	}
	broken := releaseProject(t, map[string]string{"package.json": `{"nope":1}`})
	if o := trRun(t, broken, "", "release", "version", "--json"); o.code != 3 || !strings.Contains(releaseMsg(t, o), "no version field found") {
		t.Errorf("no field: %d %s", o.code, o.stdout)
	}
	dir := releaseProject(t, map[string]string{"VERSION": "1.0.0\n"})
	o := trRun(t, dir, "", "release", "version", "--to", "1.0.0", "--json")
	d := releaseData(t, o)
	if o.code != 1 || d["version"] != "1.0.0" || d["next"] != nil {
		t.Errorf("not greater: %d %s", o.code, o.stdout)
	}
	for _, args := range [][]string{{"--level", "huge"}, {"--to", "1.x"}, {"--level", "patch", "--to", "2.0.0"}, {"extra"}} {
		if o := trRun(t, dir, "", append([]string{"release", "version"}, append(args, "--json")...)...); o.code != 2 {
			t.Errorf("%v: exit %d", args, o.code)
		}
	}
}

func TestReleaseRepoScope(t *testing.T) {
	root := umbrella(t)
	write(t, filepath.Join(root, "svc", "VERSION"), "1.4.0\n")
	write(t, filepath.Join(root, "VERSION"), "9.0.0\n")
	gitT(t, filepath.Join(root, "svc"), "remote", "add", "origin", "git@github.com:l4ci/svc.git")
	o := trRun(t, root, "", "release", "version", "--repo", "svc", "--json")
	if d := releaseData(t, o); o.code != 0 || d["version"] != "1.4.0" {
		t.Fatalf("version: %d %s", o.code, o.stdout)
	}
	if o := trRun(t, root, "", "release", "host", "--repo", "svc", "--json"); releaseData(t, o)["host"] != "github" {
		t.Errorf("host: %s", o.stdout)
	}
	if o := trRun(t, root, "", "release", "bump", "--repo", "svc", "--level", "minor", "--json"); o.code != 0 || releaseTree(t, root)["svc/VERSION"] != "1.5.0\n" || releaseTree(t, root)["VERSION"] != "9.0.0\n" {
		t.Errorf("bump: %s", o.stdout)
	}
	write(t, filepath.Join(root, "notes.md"), "- hi\n")
	if o := trRun(t, root, "", "release", "changelog", "1.5.0", "--body-file", "notes.md", "--repo", "svc", "--json"); o.code != 3 {
		t.Errorf("a relative notes file resolves in the sub-repo: %d %s", o.code, o.stdout)
	}
	write(t, filepath.Join(root, "svc", "notes.md"), "- hi\n")
	if o := trRun(t, root, "", "release", "changelog", "1.5.0", "--body-file", "notes.md", "--repo", "svc", "--json"); o.code != 0 {
		t.Errorf("changelog: %d %s", o.code, o.stdout)
	}
	if _, ok := releaseTree(t, root)["svc/CHANGELOG.md"]; !ok {
		t.Error("changelog not written in the sub-repo")
	}
	if o := trRun(t, root, "", "release", "pending", "--repo", "svc", "--json"); releaseData(t, o)["reason"] != "no-tag" {
		t.Errorf("pending: %s", o.stdout)
	}
	if o := trRun(t, root, "", "release", "version", "--repo", "nope", "--json"); o.code != 3 {
		t.Errorf("unknown repo: %d", o.code)
	}
}

var releaseHostURLs = []string{
	"", "git@github.com:l4ci/x.git", "https://github.com/l4ci/x", "HTTPS://GitHub.COM/l4ci/x", "ssh://git@github.com/l4ci/x",
	"git@gitlab.com:a/b.git", "https://gitlab.com/a/b", "https://github.acme.io/a/b", "git@ghe.github.acme.io:a/b", "https://gitlab.acme.io/a/b",
	"ssh://git@GitLab.internal:2222/a/b", "https://example.com/a/b", "/srv/git/repo.git", "https://user@github.com/a/b", "github.com",
}

func TestReleaseHostParity(t *testing.T) {
	var got []string // the host per URL
	for _, url := range releaseHostURLs {
		dir := t.TempDir()
		gitT(t, dir, "init", "-q", "-b", "main")
		if url != "" {
			gitT(t, dir, "remote", "add", "origin", url)
		}
		o := trRun(t, dir, "", "release", "host", "--json")
		if o.code != 0 {
			t.Errorf("%q: exit %d %v", url, o.code, o.stdout)
		}
		host, _ := releaseData(t, o)["host"].(string)
		got = append(got, host)
	}
	golden.Check(t, releaseHostURLs, got)
	// Not a git repo: no host.
	plain := t.TempDir()
	if o := trRun(t, plain, "", "release", "host", "--json"); releaseData(t, o)["host"] != "none" {
		t.Errorf("plain dir: %s", o.stdout)
	}
	if o := trRun(t, plain, "", "release", "host", "x", "--json"); o.code != 2 {
		t.Errorf("positional: %d", o.code)
	}
}

// releaseCommit adds a commit with subject and body.
func releaseCommit(t *testing.T, dir, subject, body string) {
	t.Helper()
	msg := subject
	if body != "" {
		msg += "\n\n" + body
	}
	gitT(t, dir, "commit", "-q", "--allow-empty", "-m", msg)
}

var releaseNotesCommits = [][2]string{
	{"feat: add login", ""},
	{"feature(api): add endpoint", ""},
	{"fix(core): crash (rare) on start", ""},
	{"fix: plain fix", "BREAKING CHANGE: behaviour moved"},
	{"perf: faster", ""},
	{"refactor(x): tidy", ""},
	{"chore: bump", ""},
	{"style(fmt): spaces", ""},
	{"docs: readme", ""},
	{"test: add cases", ""},
	{"test(unit): more", "BREAKING CHANGE: ignored, test is skipped"},
	{"random subject", ""},
	{"feat(a)b): odd scope", ""},
	{"feat(): empty scope", ""},
	{"feat!: bang", "breaking change: lowercase counts"},
	{"Fix: capital is Other", ""},
	{"feat: tabbed\tsubject", "body"},
}

var releaseNotesSinces = []string{"", "v0.1.0", "HEAD~3"}

// releaseHashes masks the abbreviated commit hashes in release notes.
var releaseHashes = regexp.MustCompile("`[0-9a-f]{7,40}`")

func releaseMaskHashes(s string) string { return releaseHashes.ReplaceAllString(s, "`HASH`") }

func TestReleaseNotesParity(t *testing.T) {
	var notes []string // the notes per since, hashes masked, h2 headings as h3
	dir := newRepo(t, t.TempDir(), "r", "main")
	write(t, filepath.Join(dir, ".rota", "config.json"), `{}`)
	gitT(t, dir, "tag", "v0.1.0")
	for _, c := range releaseNotesCommits {
		releaseCommit(t, dir, c[0], c[1])
	}
	for _, since := range releaseNotesSinces {
		args := []string{"release", "notes", "--from", "commits", "--json"}
		if since != "" {
			args = append(args, "--since", since)
		}
		o := trRun(t, dir, "", args...)
		d := releaseData(t, o)
		md, _ := d["markdown"].(string)
		if o.code != 0 || d["from"] != "commits" || d["empty"] != false {
			t.Errorf("since %q: exit %d %v", since, o.code, d)
		}
		notes = append(notes, releaseMaskHashes(md))
	}
	golden.Check(t, map[string]any{"commits": releaseNotesCommits, "sinces": releaseNotesSinces}, notes)
	// text mode prints the markdown verbatim
	o := trRun(t, dir, "", "release", "notes", "--from", "commits", "--since", "HEAD~1")
	if o.code != 0 || !strings.HasPrefix(o.stdout, "### ") || !strings.HasSuffix(o.stdout, "1 commits.\n") {
		t.Errorf("text: %q", o.stdout)
	}
}

func TestReleaseNotesEmptyAndErrors(t *testing.T) {
	dir := newRepo(t, t.TempDir(), "r", "main")
	write(t, filepath.Join(dir, ".rota", "config.json"), `{}`)
	// no commits in range
	o := trRun(t, dir, "", "release", "notes", "--from", "commits", "--since", "HEAD", "--json")
	d := releaseData(t, o)
	if o.code != 0 || d["empty"] != true || d["markdown"] != "" {
		t.Errorf("empty range: %d %v", o.code, d)
	}
	// only skipped commits
	releaseCommit(t, dir, "test: only tests", "")
	o = trRun(t, dir, "", "release", "notes", "--from", "commits", "--since", "HEAD~1", "--json")
	if releaseData(t, o)["empty"] != true {
		t.Errorf("skipped only: %s", o.stdout)
	}
	// unresolvable ref
	o = trRun(t, dir, "", "release", "notes", "--from", "commits", "--since", "nope", "--json")
	if o.code != 3 {
		t.Errorf("bad since: %d", o.code)
	}
	// a repo with no commit at all: HEAD does not resolve
	fresh := t.TempDir()
	gitT(t, fresh, "init", "-q", "-b", "main")
	if o := trRun(t, fresh, "", "release", "notes", "--from", "commits", "--json"); o.code != 3 {
		t.Errorf("unborn HEAD: %d", o.code)
	}
	// not a git repo at all: git fails for another reason
	if o := trRun(t, t.TempDir(), "", "release", "notes", "--from", "commits", "--json"); o.code != 5 {
		t.Errorf("not a repo: %d", o.code)
	}
}

func TestReleaseNotesArgs(t *testing.T) {
	dir := newRepo(t, t.TempDir(), "r", "main")
	write(t, filepath.Join(dir, ".rota", "config.json"), `{}`)
	for _, args := range [][]string{
		{"release", "notes"},
		{"release", "notes", "--from", "x"},
		{"release", "notes", "--from", "commits", "M01"},
		{"release", "notes", "--from", "issues"},
		{"release", "notes", "--from", "issues", "M01", "M02"},
	} {
		if o := trRun(t, dir, "", append(args, "--json")...); o.code != 2 {
			t.Errorf("%v: exit %d", args, o.code)
		}
	}
	if o := trRun(t, dir, "", "release", "notes", "--from", "issues", "M01", "--json"); o.code != 1 {
		t.Errorf("issues on the file backend: exit %d", o.code)
	}
	// umbrella root without --repo
	root := umbrella(t)
	if o := trRun(t, root, "", "release", "notes", "--from", "issues", "M01", "--json"); o.code != 2 {
		t.Errorf("umbrella: exit %d", o.code)
	}
	write(t, filepath.Join(root, ".rota", "config.json"), `{"backlog":{"backend":"issues"}}`)
	if o := trRun(t, root, "", "release", "notes", "--from", "issues", "M01", "--repo", "svc", "--json"); o.code != 5 {
		t.Errorf("umbrella with --repo: exit %d", o.code)
	}
}

var releaseChangelogNotes = "### New\n\n- thing (`abc1234`)\n\n\n"

type releaseChangelogCase struct {
	Name string
	File *string // nil: no changelog
}

var releaseChangelogCases = []releaseChangelogCase{
	{"no file", nil},
	{"h1 and blank", releaseStr("# Changelog\n\n## v1.0.0 — 2020-01-01\n\nold\n")},
	{"h1 then text", releaseStr("# Changelog\nsome intro\nmore\n")},
	{"h1 text then blank", releaseStr("# Changelog\nintro\n\nmore\n")},
	{"h1 only", releaseStr("# Changelog\n")},
	{"h1 only no newline", releaseStr("# Changelog")},
	{"no h1", releaseStr("## v1.0.0 — 2020-01-01\n\nold\n")},
	{"leading blanks", releaseStr("\n\n# Changelog\n\nbody\n")},
	{"h2 first", releaseStr("## Changelog\n\nbody\n")},
	{"h1 with spaces blank", releaseStr("# Changelog\n  \t\nbody\n")},
	{"empty file", releaseStr("")},
	{"crlf", releaseStr("# Changelog\r\n\r\nbody\r\n")},
	{"later h1", releaseStr("intro\n# Changelog\n\nbody\n")},
	{"hash only", releaseStr("#\n\nTitle\n")},
}

// releaseChangelogWant is the retired update-changelog helper's result: exit
// code, stdout and the changelog afterwards, with the date masked.
type releaseChangelogWant struct {
	RC        int    `json:"rc"`
	Out       string `json:"out"`
	Changelog string `json:"changelog"`
}

var releaseDates = regexp.MustCompile(`(## v\d+\.\d+\.\d+ — )\d{4}-\d{2}-\d{2}`)

// releaseMaskDates masks the date of every changelog section heading.
func releaseMaskDates(s string) string { return releaseDates.ReplaceAllString(s, "${1}DATE") }

func TestReleaseChangelogParity(t *testing.T) {
	got := make([]releaseChangelogWant, len(releaseChangelogCases))
	for i, c := range releaseChangelogCases {
		files := map[string]string{"notes.md": releaseChangelogNotes}
		if c.File != nil {
			files["CHANGELOG.md"] = *c.File
		}
		dir := releaseProject(t, files)
		o := trRun(t, dir, "", "release", "changelog", "1.1.0", "--body-file", "notes.md", "--json")
		changelog := releaseMaskDates(releaseTree(t, dir)["CHANGELOG.md"])
		if c.Name == "h1 only no newline" {
			// the old helper crashed here and left the file as it was; the port appends the section instead
			if o.code != 0 || !strings.HasPrefix(changelog, "# Changelog\n\n## v1.1.0 — ") {
				t.Errorf("%s: exit %d %q", c.Name, o.code, changelog)
			}
			got[i] = releaseChangelogWant{RC: 1, Changelog: *c.File}
			continue
		}
		if o.code != 0 {
			t.Errorf("%s: exit %d %s", c.Name, o.code, o.stdout)
			continue
		}
		d := releaseData(t, o)
		if d["version"] != "1.1.0" || d["changed"] != true {
			t.Errorf("%s: data %v", c.Name, d)
		}
		path, _ := d["path"].(string)
		got[i] = releaseChangelogWant{Out: path, Changelog: changelog}
	}
	golden.Check(t, map[string]any{"notes": releaseChangelogNotes, "cases": releaseChangelogCases}, got)
}

func releaseStr(s string) *string { return &s }

func TestReleaseChangelogOptions(t *testing.T) {
	files := map[string]string{"notes.md": "- x\n", "docs/CHANGES.md": "# Changes\n\nold\n"}
	dir := releaseProject(t, files)
	o := trRun(t, dir, "", "release", "changelog", "2.0.0", "--body-file", "notes.md", "--path", "docs/CHANGES.md", "--json")
	if o.code != 0 {
		t.Fatalf("path: %d %s", o.code, o.stdout)
	}
	path, _ := releaseData(t, o)["path"].(string)
	golden.Check(t, files, releaseChangelogWant{Out: path, Changelog: releaseMaskDates(releaseTree(t, dir)["docs/CHANGES.md"])})
	// stdin
	stdinDir := t.TempDir()
	write(t, filepath.Join(stdinDir, ".rota", "config.json"), `{}`)
	o = trRun(t, stdinDir, "- from stdin\r\n\n", "release", "changelog", "1.0.0", "--body-file", "-", "--json")
	got := releaseTree(t, stdinDir)["CHANGELOG.md"]
	if o.code != 0 || !strings.HasPrefix(got, "# Changelog\n\n## v1.0.0 — ") || !strings.HasSuffix(got, "\n\n- from stdin\n\n") {
		t.Errorf("stdin: %d %q", o.code, got)
	}
}

func TestReleaseChangelogExits(t *testing.T) {
	dir := releaseProject(t, map[string]string{"notes.md": "- x\n", "CHANGELOG.md": "# Changelog\n\n## v1.0.0 — 2020-01-01\n\nold\n\n## v1.0.01 — x\n"})
	o := trRun(t, dir, "", "release", "changelog", "1.0.0", "--body-file", "notes.md", "--json")
	if o.code != 4 {
		t.Fatalf("exists: exit %d", o.code)
	}
	if d := releaseData(t, o); d["blockedBy"] != "exists" || d["changed"] != false {
		t.Errorf("failure data %v", d)
	}
	// \b: v1.0.0 must not match a longer number
	if o := trRun(t, dir, "", "release", "changelog", "1.0.1", "--body-file", "notes.md", "--json"); o.code != 0 {
		t.Errorf("1.0.1 vs 1.0.01: %d", o.code)
	}
	cases := []struct {
		args []string
		code int
	}{
		{[]string{"1.0", "--body-file", "notes.md"}, 2},
		{[]string{"v1.0.0", "--body-file", "notes.md"}, 2},
		{[]string{"3.0.0"}, 2},
		{[]string{"--body-file", "notes.md"}, 2},
		{[]string{"3.0.0", "--body-file", "gone.md"}, 3},
	}
	for _, c := range cases {
		before := releaseTree(t, dir)
		o := trRun(t, dir, "", append(append([]string{"release", "changelog"}, c.args...), "--json")...)
		if o.code != c.code {
			t.Errorf("%v: exit %d, want %d", c.args, o.code, c.code)
		}
		if !reflect.DeepEqual(before, releaseTree(t, dir)) {
			t.Errorf("%v: wrote a file", c.args)
		}
	}
}

// releaseTagRepo makes a repo whose tag v1.0.0 sits daysAgo days back, with n commits after it.
func releaseTagRepo(t *testing.T, daysAgo, n int) string {
	t.Helper()
	dir := newRepo(t, t.TempDir(), "r", "main")
	write(t, filepath.Join(dir, ".rota", "config.json"), `{}`)
	when := "@" + releaseItoa(releaseNow()-int64(daysAgo)*86400) + " +0000"
	t.Setenv("GIT_COMMITTER_DATE", when)
	t.Setenv("GIT_AUTHOR_DATE", when)
	releaseCommit(t, dir, "feat: tagged", "")
	gitT(t, dir, "tag", "v1.0.0")
	t.Setenv("GIT_COMMITTER_DATE", "")
	t.Setenv("GIT_AUTHOR_DATE", "")
	os.Unsetenv("GIT_COMMITTER_DATE")
	os.Unsetenv("GIT_AUTHOR_DATE")
	for i := 0; i < n; i++ {
		releaseCommit(t, dir, "fix: more", "")
	}
	return dir
}

type releasePendingCase struct {
	Name, Cfg string
	Days, N   int
}

var releasePendingCases = []releasePendingCase{
	{"quiet", `{}`, 1, 2},
	{"commits threshold", `{}`, 1, 10},
	{"days threshold", `{}`, 20, 1},
	{"tag but no commits", `{}`, 30, 0},
	{"custom commits", `{"release":{"nudgeAfterCommits":2,"nudgeAfterDays":100}}`, 1, 2},
	{"custom days", `{"release":{"nudgeAfterCommits":100,"nudgeAfterDays":3}}`, 5, 1},
	{"bool thresholds", `{"release":{"nudgeAfterCommits":true,"nudgeAfterDays":false}}`, 0, 1},
	{"bool days", `{"release":{"nudgeAfterCommits":50,"nudgeAfterDays":true}}`, 2, 1},
	{"float ignored", `{"release":{"nudgeAfterCommits":2.0,"nudgeAfterDays":"3"}}`, 1, 5},
	{"negative", `{"release":{"nudgeAfterCommits":-1}}`, 1, 1},
	{"release not an object", `{"release":5}`, 1, 11},
}

func TestReleasePendingParity(t *testing.T) {
	got := make([]releaseRunWant, len(releasePendingCases)) // exit code and text output per case
	for i, c := range releasePendingCases {
		dir := releaseTagRepo(t, c.Days, c.N)
		write(t, filepath.Join(dir, ".rota", "config.json"), c.Cfg)
		o := trRun(t, dir, "", "release", "pending", "--json")
		if o.code != 0 {
			t.Errorf("%s: exit %d %s", c.Name, o.code, o.stdout)
		}
		tx := trRun(t, dir, "", "release", "pending")
		got[i] = releaseRunWant{RC: tx.code, Out: tx.stdout}
		if c.Name == "release not an object" {
			got[i] = releaseRunWant{RC: 1} // the old helper crashed on a non-object release; rota ignores it
		}
	}
	golden.Check(t, releasePendingCases, got)
}

func TestReleasePendingNoTagAndConfigLocal(t *testing.T) {
	dir := newRepo(t, t.TempDir(), "r", "main")
	write(t, filepath.Join(dir, ".rota", "config.json"), `{"release":{"nudgeAfterCommits":3}}`)
	o := trRun(t, dir, "", "release", "pending")
	if d := releaseData(t, trRun(t, dir, "", "release", "pending", "--json")); o.code != 0 || d["reason"] != "no-tag" || d["thresholdCommits"] != 10.0 {
		t.Errorf("no tag: %d %v", o.code, d)
	}
	// config.local.json overrides, as load_config merges it
	d2 := releaseTagRepo(t, 1, 3)
	write(t, filepath.Join(d2, ".rota", "config.json"), `{"release":{"nudgeAfterCommits":50}}`)
	write(t, filepath.Join(d2, ".rota", "config.local.json"), `{"release":{"nudgeAfterCommits":3}}`)
	o2 := trRun(t, d2, "", "release", "pending")
	if d := releaseData(t, trRun(t, d2, "", "release", "pending", "--json")); d["shouldNudge"] != true {
		t.Errorf("local: %v", d)
	}
	golden.Check(t, "no tag nudgeAfterCommits=3; tag, 3 commits, config 50 over local 3", [2]string{o.stdout, o2.stdout})
}

func TestReleasePendingGitFails(t *testing.T) {
	dir := releaseTagRepo(t, 1, 1)
	real, _ := exec.LookPath("git")
	fake := t.TempDir()
	write(t, filepath.Join(fake, "git"), "#!/bin/sh\n[ \"$1\" = rev-list ] && { echo 'fatal: boom' >&2; exit 128; }\nexec "+real+" \"$@\"\n")
	if err := os.Chmod(filepath.Join(fake, "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fake+string(os.PathListSeparator)+os.Getenv("PATH"))
	o := trRun(t, dir, "", "release", "pending", "--json")
	if o.code != 5 || !strings.Contains(releaseMsg(t, o), "boom") {
		t.Errorf("exit %d %s", o.code, o.stdout)
	}
	if o := trRun(t, dir, "", "release", "pending", "x", "--json"); o.code != 2 {
		t.Errorf("positional: %d", o.code)
	}
}

func TestReleaseIssueVerbsArgsAndBackend(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".rota", "config.json"), `{}`)
	cases := []struct {
		args []string
		code int
	}{
		{[]string{"milestone-check", "M01"}, 1},
		{[]string{"milestone-check"}, 2},
		{[]string{"close-milestone", "M01", "--release", "1.2.3"}, 4},
		{[]string{"close-milestone", "M01"}, 2},
		{[]string{"close-milestone", "M01", "--release", "v1.2.3"}, 2},
		{[]string{"close-milestone", "--release", "1.2.3"}, 2},
	}
	for _, c := range cases {
		if o := trRun(t, dir, "", append(append([]string{"release"}, c.args...), "--json")...); o.code != c.code {
			t.Errorf("%v: exit %d, want %d", c.args, o.code, c.code)
		}
	}
	root := umbrella(t)
	write(t, filepath.Join(root, ".rota", "config.json"), `{"backlog":{"backend":"issues"}}`)
	for _, args := range [][]string{{"milestone-check", "M01"}, {"close-milestone", "M01", "--release", "1.2.3"}} {
		if o := trRun(t, root, "", append(append([]string{"release"}, args...), "--json")...); o.code != 2 {
			t.Errorf("%v at umbrella root: exit %d", args, o.code)
		}
		if o := trRun(t, filepath.Join(root, "svc"), "", append(append([]string{"release"}, args...), "--json")...); o.code != 5 {
			t.Errorf("%v from the svc cwd (scope S), no forge: exit %d, want 5", args, o.code)
		}
		if o := trRun(t, root, "", append(append([]string{"release"}, args...), "--repo", "svc", "--json")...); o.code != 5 {
			t.Errorf("%v --repo: exit %d", args, o.code)
		}
	}
}

func TestReleaseMilestoneCheck(t *testing.T) {
	f := a8Fixture()
	root, deps := a8Project(t, f)
	code, data, msg := a8RunWith(t, deps, root, "release", "milestone-check", "M01")
	want := map[string]any{"clear": false,
		"blocked":   []any{map[string]any{"number": float64(1), "title": "One", "label": "needs-review"}, map[string]any{"number": float64(2), "title": "Two", "label": "needs-review"}, map[string]any{"number": float64(5), "title": "Wip", "label": "in-progress"}},
		"stillOpen": []any{map[string]any{"number": float64(6), "title": "Plain"}}}
	if code != 1 || !reflect.DeepEqual(data, want) || !strings.Contains(msg, "M01 is blocked by 3 open issue(s)") {
		t.Fatalf("blocked: exit %d data %v (%s)", code, data, msg)
	}
	o := trRunWith(t, deps, root, "", "release", "milestone-check", "M01")
	if o.code != 1 || o.stdout != "blocked: #1 One [needs-review]\nblocked: #2 Two [needs-review]\nblocked: #5 Wip [in-progress]\nwarning: #6 Plain (still open)\n" {
		t.Fatalf("text %+v", o)
	}
	// Open issues alone exit 0.
	f = a8Fixture()
	f.Fake.Issues[0].State, f.Fake.Issues[1].State, f.Fake.Issues[4].State = "closed", "closed", "closed"
	code, data, _ = a8RunIn(t, f, "release", "milestone-check", "M01")
	if code != 0 || data["clear"] != true || !reflect.DeepEqual(data["blocked"], []any{}) || len(data["stillOpen"].([]any)) != 1 {
		t.Fatalf("clear: exit %d data %v", code, data)
	}
	o = trRunIn(t, a8Fixture(), "", "--json", "release", "milestone-check", "M01")
	if !strings.Contains(o.stdout, `"data": {"clear": false, "blocked": [{"number": 1, "title": "One", "label": "needs-review"}`) {
		t.Fatalf("key order: %s", o.stdout)
	}
}

func TestReleaseMilestoneCheckExits(t *testing.T) {
	if code, _, _ := a8RunIn(t, a8Fixture(), "release", "milestone-check", "M99"); code != 3 {
		t.Errorf("unknown milestone: exit %d", code)
	}
	code, data, _ := a8Run(t, a8FileProject(t), "release", "milestone-check", "M01")
	if code != 1 || data["blockedBy"] != "backend" {
		t.Errorf("file: exit %d data %v", code, data)
	}
	f := a8Fixture()
	f.Fake.Fail = map[string]error{"list": &tracker.Error{Kind: tracker.KindRateLimited, Code: 4, Message: "slow down"}}
	if code, _, _ := a8RunIn(t, f, "release", "milestone-check", "M01"); code != 6 {
		t.Errorf("rate limited: exit %d", code)
	}
	f = a8Fixture()
	f.Fake.Fail = map[string]error{"list": &tracker.Error{Kind: tracker.KindUnavailable, Code: 3, Message: "no gh"}}
	if code, _, _ := a8RunIn(t, f, "release", "milestone-check", "M01"); code != 5 {
		t.Errorf("unavailable: exit %d", code)
	}
}

func TestReleaseNotesIssues(t *testing.T) {
	root, deps := a8Project(t, a8Fixture())
	code, data, msg := a8RunWith(t, deps, root, "release", "notes", "--from", "issues", "M01")
	// Completed issues only: 4 (task) and 7 (bug); 8 was dropped, the open ones do not count.
	md := "### Fixed\n\n- Fix crash (#7)\n\n### Changed\n\n- Old (#4)\n"
	if code != 0 || data["from"] != "issues" || data["markdown"] != md || data["empty"] != false {
		t.Fatalf("exit %d data %v (%s)", code, data, msg)
	}
	if o := trRunWith(t, deps, root, "", "release", "notes", "--from", "issues", "M01"); o.code != 0 || o.stdout != md {
		t.Fatalf("text %+v", o)
	}
	o := trRunWith(t, deps, root, "", "--json", "release", "notes", "--from", "issues", "M01")
	if !strings.Contains(o.stdout, `"data": {"from": "issues", "markdown": "### Fixed`) {
		t.Fatalf("key order: %s", o.stdout)
	}
}

func TestReleaseNotesIssuesSince(t *testing.T) {
	root, deps := a8Project(t, a8Fixture())
	gitT(t, root, "tag", "base")
	for _, m := range []string{"tidy things", "Fix crash [B7]", "ref #4 only", "more tidy"} {
		gitT(t, root, "commit", "-q", "--allow-empty", "-m", m)
	}
	code, data, msg := a8RunWith(t, deps, root, "release", "notes", "--from", "issues", "M01", "--since", "base")
	md := "### Fixed\n\n- Fix crash (#7)\n\n### Changed\n\n- Old (#4)\n\n### Other\n\n- more tidy\n- tidy things\n"
	if code != 0 || data["markdown"] != md {
		t.Fatalf("exit %d data %v (%s)", code, data, msg)
	}
	if code, _, _ := a8RunWith(t, deps, root, "release", "notes", "--from", "issues", "M01", "--since", "nope"); code != 3 {
		t.Errorf("bad ref: exit %d", code)
	}
}

func TestReleaseNotesIssuesEmpty(t *testing.T) {
	f := a8Fixture()
	f.Fake.Issues = f.Fake.Issues[:3]
	code, data, _ := a8RunIn(t, f, "release", "notes", "--from", "issues", "M01")
	if code != 0 || data["markdown"] != "\n" || data["empty"] != true {
		t.Fatalf("exit %d data %v", code, data)
	}
}

func TestReleaseNotesIssuesExits(t *testing.T) {
	root, deps := a8Project(t, a8Fixture())
	if code, _, _ := a8RunWith(t, deps, root, "release", "notes", "--from", "issues", "M99"); code != 3 {
		t.Errorf("unknown milestone: exit %d", code)
	}
	if code, _, _ := a8RunWith(t, deps, root, "release", "notes", "--from", "issues"); code != 2 {
		t.Errorf("no milestone: exit %d", code)
	}
	code, data, _ := a8Run(t, a8FileProject(t), "release", "notes", "--from", "issues", "M01")
	if code != 1 || data["blockedBy"] != "backend" {
		t.Errorf("file: exit %d data %v", code, data)
	}
	f := a8Fixture()
	f.Fake.Fail = map[string]error{"list": &tracker.Error{Kind: tracker.KindUnavailable, Code: 3, Message: "no gh"}}
	if code, _, _ := a8RunIn(t, f, "release", "notes", "--from", "issues", "M01"); code != 5 {
		t.Errorf("unavailable: exit %d", code)
	}
}

func TestReleaseCloseMilestone(t *testing.T) {
	f := a8Fixture()
	root, deps := a8Project(t, f)
	code, data, msg := a8RunWith(t, deps, root, "release", "close-milestone", "M01", "--release", "1.2.0")
	want := map[string]any{"milestone": "M01", "release": "1.2.0", "tag": "v1.2.0", "issues": float64(2), "changed": true}
	if code != 0 || !reflect.DeepEqual(data, want) {
		t.Fatalf("exit %d data %v (%s)", code, data, msg)
	}
	for _, n := range []int{4, 7} {
		is := f.Fake.Issues[n-1]
		if n == 4 && is.Number != 4 || n == 7 && is.Number != 7 {
			t.Fatal("fixture order")
		}
		if !slices.Contains(is.Labels, "released") || is.Comments[len(is.Comments)-1].Body != "Released in v1.2.0\n\n<!-- rota:released -->" {
			t.Errorf("issue %d %+v", n, is)
		}
	}
	if slices.Contains(f.Fake.Issues[7].Labels, "released") {
		t.Error("the dropped issue was released")
	}
	if f.Fake.Issues[2].State != "closed" || f.MS.Native[0].State != "closed" {
		t.Errorf("milestone open: %+v %+v", f.Fake.Issues[2], f.MS.Native)
	}
	// Re-running is a no-op success.
	code, data, _ = a8RunWith(t, deps, root, "release", "close-milestone", "M01", "--release", "1.2.0")
	if code != 0 || data["issues"] != float64(2) || data["changed"] != false {
		t.Fatalf("second run: exit %d data %v", code, data)
	}
	if n := len(f.Fake.Issues[3].Comments); n != 1 {
		t.Errorf("comments after second run: %d", n)
	}
	o := trRunIn(t, a8Fixture(), "", "release", "close-milestone", "M01", "--release", "1.2.0")
	if o.code != 0 || o.stdout != "closed-out M01 v1.2.0: 2 issues\n" {
		t.Fatalf("text %+v", o)
	}
	o = trRunIn(t, a8Fixture(), "", "--json", "release", "close-milestone", "M01", "--release", "1.2.0")
	if !strings.Contains(o.stdout, `"data": {"milestone": "M01", "release": "1.2.0", "tag": "v1.2.0", "issues": 2, "changed": true}`) {
		t.Fatalf("key order: %s", o.stdout)
	}
}

func TestReleaseCloseMilestoneExits(t *testing.T) {
	root, deps := a8Project(t, a8Fixture())
	for _, c := range []struct {
		args []string
		code int
	}{
		{[]string{"M99", "--release", "1.0.0"}, 3},
		{[]string{"M01"}, 2},
		{[]string{"M01", "--release", "v1.0.0"}, 2},
	} {
		if code, _, msg := a8RunWith(t, deps, root, append([]string{"release", "close-milestone"}, c.args...)...); code != c.code {
			t.Errorf("%v: exit %d (%s), want %d", c.args, code, msg, c.code)
		}
	}
	code, data, _ := a8Run(t, a8FileProject(t), "release", "close-milestone", "M01", "--release", "1.0.0")
	if code != 4 || data["blockedBy"] != "backend" || data["changed"] != false {
		t.Errorf("file: exit %d data %v", code, data)
	}
	f := a8Fixture()
	f.Fake.Fail = map[string]error{"close": &tracker.Error{Kind: tracker.KindRateLimited, Code: 4, Message: "slow down"}}
	if code, _, _ := a8RunIn(t, f, "release", "close-milestone", "M01", "--release", "1.0.0"); code != 6 {
		t.Errorf("rate limited: exit %d", code)
	}
	f = a8Fixture()
	f.Fake.Fail = map[string]error{"add_labels": &tracker.Error{Kind: tracker.KindUnavailable, Code: 3, Message: "no gh"}}
	if code, _, _ := a8RunIn(t, f, "release", "close-milestone", "M01", "--release", "1.0.0"); code != 5 {
		t.Errorf("unavailable: exit %d", code)
	}
}

func releaseNow() int64 { return time.Now().Unix() }

func releaseItoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestReleaseTaggedMatchesEveryItemForm(t *testing.T) {
	for _, s := range []string{"fix [B07] thing", "slice [M01-S2]", "milestone [M01]", "closes #12"} {
		if !releaseTagged.MatchString(s) {
			t.Errorf("%q should count as tagged", s)
		}
	}
	for _, s := range []string{"plain subject", "[X] nope", "see M01"} {
		if releaseTagged.MatchString(s) {
			t.Errorf("%q should not count as tagged", s)
		}
	}
}
