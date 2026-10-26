package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/extknowledge"
	"github.com/l4ci/rota/internal/gittest"
)

// extFixture is a project plus a cloned external knowledge repo (with a local
// bare remote) whose path is set in the global config.
func extFixture(t *testing.T, policy string) (project, clone, remote string) {
	t.Helper()
	t.Setenv("ROTA_CONFIG_DIR", t.TempDir())
	root := gittest.TempDir(t)
	seed := filepath.Join(root, "seed")
	gittest.Init(t, seed, "main")
	gittest.Write(t, seed, "README.md", "# Knowledge index\n\n- [Go](topics/go.md) — testing quirks\n")
	gittest.Write(t, seed, "topics/go.md", "# Go\n\n- **Table tests** — Use t.Run. <!-- 2026-01-02 source:rota -->\n")
	if policy != "" {
		gittest.Write(t, seed, "rota-knowledge.json", policy)
	}
	gittest.Run(t, seed, "add", ".")
	gittest.Run(t, seed, "commit", "-q", "-m", "seed")
	remote = filepath.Join(root, "remote.git")
	gittest.Run(t, root, "clone", "-q", "--bare", seed, remote)
	gittest.Quiet(t, remote)
	clone = filepath.Join(root, "clone")
	gittest.Run(t, root, "clone", "-q", remote, clone)
	gittest.Quiet(t, clone)
	project = knProject(t, false)
	if o := knNew(t, project, "", "config", "set", "knowledge.external", clone, "--global"); o.rc != 0 {
		t.Fatalf("config set: %+v", o)
	}
	return project, clone, remote
}

func TestKnowledgeExternalUnsetKeyExitsWithHint(t *testing.T) {
	t.Setenv("ROTA_CONFIG_DIR", t.TempDir())
	project := knProject(t, false)
	for _, args := range [][]string{
		{"knowledge", "query", "--external", "Go"},
		{"knowledge", "topics", "--external"},
		{"knowledge", "add", "--external", "--topic", "Go", "--title", "T", "--body-file", "-"},
		{"knowledge", "amend", "--external", "--topic", "Go", "--fragment", "x", "--mode", "append", "--body-file", "-"},
	} {
		o := knNew(t, project, "body", args...)
		if o.rc != ExitResolution || !strings.Contains(o.stderr, "knowledge.external") {
			t.Errorf("%v: rc=%d stderr=%q", args, o.rc, o.stderr)
		}
	}
}

func TestKnowledgeExternalQueryAndTopics(t *testing.T) {
	project, _, _ := extFixture(t, "")
	o := knNew(t, project, "", "knowledge", "query", "--external", "go", "rust")
	if o.rc != 0 || !strings.HasPrefix(o.stdout, "## Go\n") || !strings.Contains(o.stdout, "**Table tests**") {
		t.Fatalf("query: %+v", o)
	}
	if !strings.Contains(o.stderr, `no external topic matches "rust"`) {
		t.Errorf("missing-topic warning: %q", o.stderr)
	}
	o = knNew(t, project, "", "knowledge", "topics", "--external", "--json")
	var env struct {
		Data struct {
			Topics []struct {
				Name    string
				Bullets int
			}
		}
	}
	if err := json.Unmarshal([]byte(o.stdout), &env); err != nil || len(env.Data.Topics) != 1 || env.Data.Topics[0].Name != "Go" || env.Data.Topics[0].Bullets != 1 {
		t.Fatalf("topics: %v %+v", err, o)
	}
}

func TestKnowledgeExternalAddPushesAndSaysDefault(t *testing.T) {
	project, clone, remote := extFixture(t, "")
	o := knNew(t, project, "worktrees share one object store.", "knowledge", "add", "--external", "--topic", "Git", "--title", "Worktrees", "--body-file", "-")
	if o.rc != 0 || !strings.Contains(o.stdout, "added: Git :: Worktrees") || !strings.Contains(o.stdout, "defaulted to") {
		t.Fatalf("add: %+v", o)
	}
	if got := gittest.Run(t, remote, "show", "main:topics/git.md"); !strings.Contains(got, "**Worktrees** — worktrees share one object store.") || !strings.Contains(got, "source:"+filepath.Base(project)) {
		t.Fatalf("remote file = %q", got)
	}
	if gittest.Run(t, clone, "status", "--porcelain") != "" {
		t.Fatal("clone left dirty")
	}
	o = knNew(t, project, "also append", "knowledge", "amend", "--external", "--topic", "Git", "--fragment", "Worktrees", "--mode", "append", "--body-file", "-")
	if o.rc != 0 || !strings.Contains(o.stdout, "amended: topics/git.md") {
		t.Fatalf("amend: %+v", o)
	}
}

func TestKnowledgeExternalRefusesProjectOnlyFlags(t *testing.T) {
	project, _, _ := extFixture(t, "")
	if o := knNew(t, project, "", "knowledge", "query", "--external", "--tier", "confirmed", "Go"); o.rc != ExitUsage {
		t.Errorf("--tier with --external: rc=%d", o.rc)
	}
	if o := knNew(t, project, "b", "knowledge", "add", "--topic", "Architecture", "--title", "T", "--hook", "h", "--body-file", "-"); o.rc != ExitUsage {
		t.Errorf("--hook without --external: rc=%d", o.rc)
	}
	_ = os.Stdout
}

func TestDoctorInputReadsExternalKnowledgeState(t *testing.T) {
	project, clone, _ := extFixture(t, "")
	old, _ := os.Getwd()
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	got := doctorInput(context.Background(), testDeps()).ExternalKnowledge
	if got.Path != clone || !got.Exists || !got.IsGit || !got.HasRemote || got.HasPolicy {
		t.Fatalf("state = %+v", got)
	}
	gittest.Write(t, clone, "rota-knowledge.json", `{"write":"pr"}`)
	if got := doctorInput(context.Background(), testDeps()).ExternalKnowledge; !got.HasPolicy {
		t.Fatalf("policy file not seen: %+v", got)
	}
	if o := knNew(t, project, "", "config", "set", "knowledge.external", filepath.Join(clone, "gone"), "--global"); o.rc != 0 {
		t.Fatal(o)
	}
	if got := doctorInput(context.Background(), testDeps()).ExternalKnowledge; got.Exists || got.Path == "" {
		t.Fatalf("missing path: %+v", got)
	}
	if o := knNew(t, project, "", "config", "set", "knowledge.external", "", "--global"); o.rc != 0 {
		t.Fatal(o)
	}
	if got := doctorInput(context.Background(), testDeps()).ExternalKnowledge; got.Path != "" {
		t.Fatalf("unset: %+v", got)
	}
}

func TestExtErrExitCodes(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{extknowledge.ErrOffline, ExitUnavailable},
		{extknowledge.ErrPush, ExitFailed},
		{extknowledge.ErrDirty, ExitRefused},
	}
	for _, tc := range cases {
		var e *Error
		if !errors.As(extErr(fmt.Errorf("%w: x", tc.err)), &e) || e.Exit != tc.want {
			t.Fatalf("%v: got %+v, want exit %d", tc.err, e, tc.want)
		}
	}
}

func TestKnowledgeExternalSeenFlags(t *testing.T) {
	project, clone, _ := extFixture(t, "")
	add := func(stdin string, args ...string) knOut {
		return knNew(t, project, stdin, append([]string{"knowledge", "add", "--topic", "Go", "--title", "T1", "--body-file", "-"}, args...)...)
	}
	// Usage errors.
	if o := add("b", "--seen", "godot@4.6"); o.rc != ExitUsage {
		t.Errorf("--seen without --external: rc=%d", o.rc)
	}
	for _, bad := range []string{"godot@", "@4.6", "Go Dot@4", "Godot@4.6"} {
		if o := add("b", "--external", "--seen", bad); o.rc != ExitUsage {
			t.Errorf("--seen %q: rc=%d %q", bad, o.rc, o.stderr)
		}
	}
	// Bare tool with no manifest: exit 3 naming the tool.
	if o := add("b", "--external", "--seen", "godot"); o.rc != ExitResolution || !strings.Contains(o.stderr, "godot") {
		t.Errorf("bare godot: rc=%d %q", o.rc, o.stderr)
	}
	if gittest.Run(t, clone, "status", "--porcelain") != "" || gittest.Run(t, clone, "rev-list", "--count", "HEAD") != "1" {
		t.Fatal("a refused add wrote something")
	}
	// Explicit version writes the stamp.
	if o := add("b", "--external", "--seen", "godot@4.6"); o.rc != 0 {
		t.Fatalf("add: %+v", o)
	}
	if got := gittest.Run(t, clone, "show", "HEAD:topics/go.md"); !strings.Contains(got, "seen:godot@4.6 -->") {
		t.Fatalf("file = %q", got)
	}
	// Bare tool takes the project's version.
	os.WriteFile(filepath.Join(project, "project.godot"), []byte("config/features=PackedStringArray(\"4.8\", \"Forward Plus\")\n"), 0o644)
	if o := knNew(t, project, "b", "knowledge", "add", "--external", "--topic", "Go", "--title", "T2", "--body-file", "-", "--seen", "godot"); o.rc != 0 {
		t.Fatalf("bare add: %+v", o)
	}
	if got := gittest.Run(t, clone, "show", "HEAD:topics/go.md"); !strings.Contains(got, "seen:godot@4.8 -->") {
		t.Fatalf("file = %q", got)
	}
	// Mixed bare + explicit: godot from project.godot, swift as given.
	if o := knNew(t, project, "b", "knowledge", "add", "--external", "--topic", "Go", "--title", "T3", "--body-file", "-", "--seen", "godot swift@6"); o.rc != 0 {
		t.Fatalf("mixed add: %+v", o)
	}
	if got := gittest.Run(t, clone, "show", "HEAD:topics/go.md"); !strings.Contains(got, "seen:godot@4.8,swift@6 -->") {
		t.Fatalf("file = %q", got)
	}
	// The project is on 4.8: T1 (seen 4.6) is flagged, T2 is not, JSON lists it.
	o := knNew(t, project, "", "knowledge", "query", "--external", "go")
	if !strings.Contains(o.stdout, "**T1** — b (seen on godot 4.6, project on 4.8: recheck) <!--") || strings.Contains(o.stdout, "**T2** — b (seen") {
		t.Fatalf("query: %q", o.stdout)
	}
	o = knNew(t, project, "", "knowledge", "query", "--external", "go", "--json")
	var env struct {
		Data struct {
			Recheck []struct{ Topic, Title, Tool, Seen, Project string }
		}
	}
	if err := json.Unmarshal([]byte(o.stdout), &env); err != nil || len(env.Data.Recheck) != 1 || env.Data.Recheck[0].Title != "T1" || env.Data.Recheck[0].Project != "4.8" {
		t.Fatalf("json: %v %q", err, o.stdout)
	}
}

func TestKnowledgeExternalSplit(t *testing.T) {
	project, clone, _ := extFixture(t, "")
	for _, title := range []string{"Parallel subtests", "Golden files"} {
		if o := knNew(t, project, "body of "+title, "knowledge", "add", "--external", "--topic", "Go", "--title", title, "--body-file", "-"); o.rc != 0 {
			t.Fatalf("add: %+v", o)
		}
	}
	plan := `{"subtopics":[{"name":"Testing","hook":"test habits","titles":["Table tests","Parallel subtests"]}]}`
	if o := knNew(t, project, plan, "knowledge", "split", "--topic", "Go", "--plan", "-"); o.rc != ExitUsage {
		t.Fatalf("split without --external: %+v", o)
	}
	if o := knNew(t, project, "{nope", "knowledge", "split", "--external", "--topic", "Go", "--plan", "-"); o.rc != ExitUsage {
		t.Fatalf("malformed plan: %+v", o)
	}
	if o := knNew(t, project, `{"subtopics":[{"name":"T","titles":["Nope"]}]}`, "knowledge", "split", "--external", "--topic", "Go", "--plan", "-"); o.rc != ExitResolution {
		t.Fatalf("unknown title: %+v", o)
	}
	if o := knNew(t, project, "", "knowledge", "split", "--external", "--topic", "Go"); o.rc != ExitUsage {
		t.Fatalf("flat topic without a plan: %+v", o)
	}
	o := knNew(t, project, plan, "knowledge", "split", "--external", "--topic", "Go", "--plan", "-", "--json")
	if o.rc != 0 {
		t.Fatalf("split: %+v", o)
	}
	var out struct {
		Data struct {
			Topic     string
			Changed   bool
			Left      int
			Folded    int
			Subtopics []struct {
				Name, File string
				Bullets    int
			}
		}
	}
	if err := json.Unmarshal([]byte(o.stdout), &out); err != nil {
		t.Fatalf("json: %v: %s", err, o.stdout)
	}
	d := out.Data
	if d.Topic != "Go" || !d.Changed || d.Left != 1 || d.Folded != 0 || len(d.Subtopics) != 1 || d.Subtopics[0].Name != "Go: Testing" || d.Subtopics[0].File != "topics/go/testing.md" || d.Subtopics[0].Bullets != 2 {
		t.Fatalf("data = %+v", d)
	}
	if _, err := os.Stat(filepath.Join(clone, "topics/go.md")); !os.IsNotExist(err) {
		t.Fatal("flat file must be gone")
	}
	o = knNew(t, project, "", "knowledge", "query", "--external", "--all", "Go")
	for _, want := range []string{"## Go\n", "- [Testing](testing.md) — test habits", "**Golden files**", "## Go: Testing\n", "**Table tests**"} {
		if o.rc != 0 || !strings.Contains(o.stdout, want) {
			t.Fatalf("query --all missing %q: %+v", want, o)
		}
	}
	o = knNew(t, project, plan, "knowledge", "split", "--external", "--topic", "Go", "--plan", "-")
	if o.rc != 0 || !strings.Contains(o.stdout, "unchanged: Go") {
		t.Fatalf("rerun: %+v", o)
	}
}

func TestKnowledgeExternalMatch(t *testing.T) {
	project, _, _ := extFixture(t, "")
	o := knNew(t, project, "", "knowledge", "query", "--external", "--match", "t.run")
	if o.rc != 0 || o.stdout != "## Go\n- **Table tests** — Use t.Run. <!-- 2026-01-02 source:rota -->\n" {
		t.Fatalf("match: %+v", o)
	}
	o = knNew(t, project, "", "knowledge", "query", "--external", "--match", "nothing here", "--json")
	var env struct {
		Data struct {
			Text    string
			Matches int
			Missing []string
			Recheck []any
		}
	}
	if err := json.Unmarshal([]byte(o.stdout), &env); err != nil || o.rc != 0 || env.Data.Text != "" || env.Data.Matches != 0 || env.Data.Missing == nil || env.Data.Recheck == nil {
		t.Fatalf("no hit: %v %+v", err, o)
	}
	for _, args := range [][]string{
		{"query", "--external", "--match", ""},
		{"query", "--external", "--match", "  "},
		{"query", "--match", "x", "Go"},
	} {
		if o := knNew(t, project, "", append([]string{"knowledge"}, args...)...); o.rc != ExitUsage {
			t.Errorf("%v: rc=%d", args, o.rc)
		}
	}
}
