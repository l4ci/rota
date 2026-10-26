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
