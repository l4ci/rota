package extknowledge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/proc"
)

func TestMain(m *testing.M) {
	gittest.SetIdentity()
	os.Exit(m.Run())
}

const seedIndex = "# Knowledge index\n\n- [Go](topics/go.md) — testing and tooling quirks\n"
const seedGo = "# Go\n\n- **Table tests** — Use t.Run per case. <!-- 2026-01-02 source:rota -->\n"

type fixture struct {
	t      *testing.T
	remote string
	a, b   string // two clones of the remote; a is under test
	now    time.Time
	calls  []string // git verbs run through the fixture runner
	gh     [][]string
	hook   func(args []string) // runs before a git call
}

func newFixture(t *testing.T, policy string) *fixture {
	t.Helper()
	return newFixtureOn(t, policy, "main")
}

func newFixtureOn(t *testing.T, policy, branch string) *fixture {
	t.Helper()
	root := gittest.TempDir(t)
	seed := filepath.Join(root, "seed")
	gittest.Init(t, seed, branch)
	gittest.Write(t, seed, IndexFile, seedIndex)
	gittest.Write(t, seed, "topics/go.md", seedGo)
	if policy != "" {
		gittest.Write(t, seed, PolicyFile, policy)
	}
	gittest.Run(t, seed, "add", ".")
	gittest.Run(t, seed, "commit", "-q", "-m", "seed")
	remote := filepath.Join(root, "remote.git")
	gittest.Run(t, root, "clone", "-q", "--bare", seed, remote)
	gittest.Quiet(t, remote)
	f := &fixture{t: t, remote: remote, a: filepath.Join(root, "a"), b: filepath.Join(root, "b"), now: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)}
	for _, d := range []string{f.a, f.b} {
		gittest.Run(t, root, "clone", "-q", remote, d)
		gittest.Quiet(t, d)
	}
	return f
}

func (f *fixture) clone() Clone {
	return Clone{Dir: f.a, Env: Env{
		Git: func(ctx context.Context, dir string, args ...string) (git.Result, error) {
			f.calls = append(f.calls, strings.Join(args, " "))
			if f.hook != nil {
				f.hook(args)
			}
			return git.Exec(ctx, dir, args...)
		},
		Proc: func(ctx context.Context, c proc.Cmd) (proc.Result, error) {
			f.gh = append(f.gh, append([]string{c.Name}, c.Args...))
			return proc.Result{Stdout: "https://example.test/org/kb/pull/7\n"}, nil
		},
		Now:      func() time.Time { return f.now },
		StateDir: filepath.Join(filepath.Dir(f.a), "state"),
		TTL:      10 * time.Minute,
	}}
}

func (f *fixture) fetchCalls() int {
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, "fetch") {
			n++
		}
	}
	return n
}

// remoteAdds makes the other clone push a new topic to the remote.
func (f *fixture) remoteAdds(slug string) {
	f.t.Helper()
	gittest.Run(f.t, f.b, "pull", "-q", "--ff-only")
	gittest.Commit(f.t, f.b, "add "+slug, "topics/"+slug+".md", "# "+slug+"\n\n- **x** — y <!-- 2026-02-01 source:z -->\n")
	idx, _ := os.ReadFile(filepath.Join(f.b, IndexFile))
	gittest.Commit(f.t, f.b, "index "+slug, IndexFile, string(idx)+"- ["+slug+"](topics/"+slug+".md) — hook\n")
	gittest.Run(f.t, f.b, "push", "-q")
}

func topicNames(t *testing.T, c Clone) []string {
	t.Helper()
	ts, err := c.Topics()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, x := range ts {
		out = append(out, x.Name)
	}
	return out
}

func has(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestFreshenFastForwardsWhenRemoteAhead(t *testing.T) {
	f := newFixture(t, "")
	c := f.clone()
	f.remoteAdds("git")
	if w := c.Freshen(context.Background()); w != "" {
		t.Fatalf("unexpected warning %q", w)
	}
	if !has(topicNames(t, c), "git") {
		t.Fatalf("topic git missing after fast-forward: %v", topicNames(t, c))
	}
}

func TestFreshenWithinTTLDoesNotFetch(t *testing.T) {
	f := newFixture(t, "")
	c := f.clone()
	c.Freshen(context.Background())
	if f.fetchCalls() != 1 {
		t.Fatalf("first freshen fetches once, got %d", f.fetchCalls())
	}
	f.remoteAdds("git")
	f.now = f.now.Add(5 * time.Minute)
	c.Freshen(context.Background())
	if f.fetchCalls() != 1 {
		t.Fatalf("within the TTL there is no fetch, got %d", f.fetchCalls())
	}
	if has(topicNames(t, c), "git") {
		t.Fatal("remote change must not show within the TTL")
	}
	f.now = f.now.Add(6 * time.Minute)
	c.Freshen(context.Background())
	if f.fetchCalls() != 2 || !has(topicNames(t, c), "git") {
		t.Fatalf("past the TTL it fetches and fast-forwards: fetches=%d topics=%v", f.fetchCalls(), topicNames(t, c))
	}
}

func TestFreshenDirtyCloneWarnsAndKeepsFiles(t *testing.T) {
	f := newFixture(t, "")
	c := f.clone()
	f.remoteAdds("git")
	gittest.Write(t, f.a, "topics/go.md", seedGo+"- local edit\n")
	head := gittest.Run(t, f.a, "rev-parse", "HEAD")
	w := c.Freshen(context.Background())
	if !strings.Contains(w, "uncommitted") || strings.Contains(w, "\n") {
		t.Fatalf("want a one-line dirty warning, got %q", w)
	}
	if gittest.Run(t, f.a, "rev-parse", "HEAD") != head {
		t.Fatal("dirty clone must not move")
	}
	if b, _ := os.ReadFile(filepath.Join(f.a, "topics/go.md")); !strings.Contains(string(b), "local edit") {
		t.Fatal("dirty edit lost")
	}
}

func TestFreshenDivergedCloneWarnsAndNeverResets(t *testing.T) {
	f := newFixture(t, "")
	c := f.clone()
	gittest.Commit(t, f.a, "local", "topics/local.md", "# local\n")
	f.remoteAdds("git")
	head := gittest.Run(t, f.a, "rev-parse", "HEAD")
	w := c.Freshen(context.Background())
	if !strings.Contains(w, "diverged") || strings.Contains(w, "\n") {
		t.Fatalf("want a one-line diverged warning, got %q", w)
	}
	if gittest.Run(t, f.a, "rev-parse", "HEAD") != head {
		t.Fatal("diverged clone must not move")
	}
}

func TestFreshenOfflineReadsStaleAndDoesNotStamp(t *testing.T) {
	f := newFixture(t, "")
	c := f.clone()
	gittest.Run(t, f.a, "remote", "set-url", "origin", filepath.Join(f.remote, "gone"))
	w := c.Freshen(context.Background())
	if !strings.Contains(w, "reading stale copy") || strings.Contains(w, "\n") {
		t.Fatalf("want a one-line stale warning, got %q", w)
	}
	if !has(topicNames(t, c), "Go") {
		t.Fatal("local copy must still read")
	}
	if w2 := c.Freshen(context.Background()); !strings.Contains(w2, "reading stale copy") {
		t.Fatalf("a failed fetch is not stamped; got %q", w2)
	}
}

func TestQueryPrintsTopicSectionsAndListsMissing(t *testing.T) {
	f := newFixture(t, "")
	c := f.clone()
	text, missing, err := c.Query([]string{"go", "rust"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(text, "## Go\n") || !strings.Contains(text, "- **Table tests** — Use t.Run per case.") {
		t.Fatalf("text = %q", text)
	}
	if len(missing) != 1 || missing[0] != "rust" {
		t.Fatalf("missing = %v", missing)
	}
	ts, _ := c.Topics()
	if len(ts) != 1 || ts[0].Bullets != 1 || ts[0].Hook != "testing and tooling quirks" {
		t.Fatalf("topics = %+v", ts)
	}
}

func TestPolicyDefaultsToPushWhenFileMissing(t *testing.T) {
	f := newFixture(t, "")
	p, err := f.clone().Policy()
	if err != nil || p.Write != "push" || !p.Defaulted || p.Branch != "" {
		t.Fatalf("policy = %+v err=%v", p, err)
	}
	f2 := newFixture(t, `{"write":"pr","branch":"trunk"}`)
	p, err = f2.clone().Policy()
	if err != nil || p.Write != "pr" || p.Branch != "trunk" || p.Defaulted {
		t.Fatalf("policy = %+v err=%v", p, err)
	}
	f3 := newFixture(t, `{"write":"later"}`)
	if _, err = f3.clone().Policy(); err == nil {
		t.Fatal("unknown write mode must be an error")
	}
}

func TestAddPushesNewTopicAndIndexLine(t *testing.T) {
	f := newFixture(t, `{"write":"push","branch":"main"}`)
	c := f.clone()
	res, err := c.Add(context.Background(), AddIn{Topic: "Git", Title: "Worktrees", Body: "share one object store.", Date: "2026-10-25", Source: "rota"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || !res.Pushed || res.Policy.Write != "push" {
		t.Fatalf("res = %+v", res)
	}
	gittest.Run(t, f.b, "pull", "-q", "--ff-only")
	topic, _ := os.ReadFile(filepath.Join(f.b, "topics/git.md"))
	if !strings.Contains(string(topic), "- **Worktrees** — share one object store. <!-- 2026-10-25 source:rota -->") {
		t.Fatalf("topic file = %q", topic)
	}
	idx, _ := os.ReadFile(filepath.Join(f.b, IndexFile))
	if !strings.Contains(string(idx), "- [Git](topics/git.md) — ") {
		t.Fatalf("index = %q", idx)
	}
	msg := gittest.Run(t, f.b, "log", "-1", "--format=%B")
	if strings.Contains(msg, "Co-Authored-By") {
		t.Fatalf("commit message carries a trailer: %q", msg)
	}
	if gittest.Run(t, f.a, "status", "--porcelain") != "" {
		t.Fatal("clone must be clean after a write")
	}
}

func TestAddWithoutPolicyFileDefaultsToPush(t *testing.T) {
	f := newFixture(t, "")
	res, err := f.clone().Add(context.Background(), AddIn{Topic: "Go", Title: "New tip", Body: "b", Date: "2026-10-25", Source: "rota"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Policy.Defaulted || !res.Pushed {
		t.Fatalf("res = %+v", res)
	}
	if gittest.Run(t, f.remote, "log", "-1", "--format=%s") == "seed" {
		t.Fatal("remote did not receive the commit")
	}
}

func TestAddExistingTitleIsUnchanged(t *testing.T) {
	f := newFixture(t, "")
	res, err := f.clone().Add(context.Background(), AddIn{Topic: "Go", Title: "table tests", Body: "x", Date: "2026-10-25", Source: "rota"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed || res.Pushed {
		t.Fatalf("res = %+v", res)
	}
}

func TestAddRetriesRejectedPushOnce(t *testing.T) {
	f := newFixture(t, `{"write":"push"}`)
	c := f.clone()
	pushed := false
	f.hook = func(args []string) {
		if len(args) > 0 && args[0] == "push" && !pushed {
			pushed = true
			f.remoteAdds("rust")
		}
	}
	res, err := c.Add(context.Background(), AddIn{Topic: "Go", Title: "Retry", Body: "b", Date: "2026-10-25", Source: "rota"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Pushed || !res.Retried {
		t.Fatalf("res = %+v", res)
	}
	gittest.Run(t, f.b, "pull", "-q", "--ff-only")
	if _, err := os.Stat(filepath.Join(f.b, "topics/rust.md")); err != nil {
		t.Fatal("the other writer's topic was lost")
	}
	if b, _ := os.ReadFile(filepath.Join(f.b, "topics/go.md")); !strings.Contains(string(b), "**Retry**") {
		t.Fatal("retried bullet missing on the remote")
	}
}

func TestAddPRModePushesBranchAndOpensPR(t *testing.T) {
	f := newFixture(t, `{"write":"pr","branch":"main"}`)
	c := f.clone()
	res, err := c.Add(context.Background(), AddIn{Topic: "Go", Title: "Via PR", Body: "b", Date: "2026-10-25", Source: "rota"})
	if err != nil {
		t.Fatal(err)
	}
	if res.PR != "https://example.test/org/kb/pull/7" || res.Branch == "" || res.Pushed {
		t.Fatalf("res = %+v", res)
	}
	if len(f.gh) != 1 || f.gh[0][0] != "gh" || f.gh[0][1] != "pr" || f.gh[0][2] != "create" {
		t.Fatalf("gh calls = %v", f.gh)
	}
	joined := strings.Join(f.gh[0], " ")
	if !strings.Contains(joined, "--base main") || !strings.Contains(joined, "--head "+res.Branch) {
		t.Fatalf("gh args = %v", f.gh[0])
	}
	if gittest.Run(t, f.remote, "branch", "--list", res.Branch) == "" {
		t.Fatal("branch not pushed")
	}
	if strings.Contains(gittest.Run(t, f.remote, "show", "main:topics/go.md"), "Via PR") {
		t.Fatal("main must not change in pr mode")
	}
	if gittest.Run(t, f.a, "rev-parse", "--abbrev-ref", "HEAD") != "main" || gittest.Run(t, f.a, "status", "--porcelain") != "" {
		t.Fatal("clone must be back on main and clean")
	}
}

func TestAddRefusesDirtyClone(t *testing.T) {
	f := newFixture(t, "")
	gittest.Write(t, f.a, "topics/go.md", seedGo+"- local\n")
	_, err := f.clone().Add(context.Background(), AddIn{Topic: "Go", Title: "T", Body: "b", Date: "2026-10-25", Source: "rota"})
	if !errorsIs(err, ErrDirty) {
		t.Fatalf("err = %v", err)
	}
}

func TestAmendAppendsToOneBullet(t *testing.T) {
	f := newFixture(t, "")
	c := f.clone()
	res, err := c.Amend(context.Background(), AmendIn{Topic: "Go", Fragment: "Table tests", Text: "Name the cases."})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || !res.Pushed {
		t.Fatalf("res = %+v", res)
	}
	b, _ := os.ReadFile(filepath.Join(f.a, "topics/go.md"))
	if !strings.Contains(string(b), "Use t.Run per case. Name the cases. <!-- 2026-01-02 source:rota -->") {
		t.Fatalf("file = %q", b)
	}
	if _, err := c.Amend(context.Background(), AmendIn{Topic: "Go", Fragment: "no such", Text: "x"}); !errorsIs(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if _, err := c.Amend(context.Background(), AmendIn{Topic: "Nope", Fragment: "x", Text: "x"}); !errorsIs(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func errorsIs(err, target error) bool { return errors.Is(err, target) }

func TestPolicyRejectsOptionLikeBranch(t *testing.T) {
	for _, b := range []string{"--upload-pack=x", "-f", "a..b", "a b"} {
		f := newFixture(t, `{"write":"push","branch":"`+b+`"}`)
		if _, err := f.clone().Policy(); err == nil {
			t.Fatalf("branch %q must be refused", b)
		}
	}
}

func TestAddPushesToTheClonesBranchWithoutPolicy(t *testing.T) {
	f := newFixtureOn(t, "", "master")
	res, err := f.clone().Add(context.Background(), AddIn{Topic: "Go", Title: "On master", Body: "b", Date: "2026-10-25", Source: "rota"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Pushed {
		t.Fatalf("res = %+v", res)
	}
	if gittest.Run(t, f.remote, "branch", "--list", "main") != "" {
		t.Fatal("a stray main branch was created on the remote")
	}
	if !strings.Contains(gittest.Run(t, f.remote, "show", "master:topics/go.md"), "On master") {
		t.Fatal("remote master did not receive the bullet")
	}
	if strings.Contains(gittest.Run(t, f.a, "status", "-sb"), "ahead") {
		t.Fatal("clone is ahead of its upstream")
	}
}

func TestAddRefusesPushWhenNotOnPolicyBranch(t *testing.T) {
	f := newFixture(t, `{"write":"push","branch":"main"}`)
	gittest.Run(t, f.a, "switch", "-q", "-c", "side")
	_, err := f.clone().Add(context.Background(), AddIn{Topic: "Go", Title: "T", Body: "b", Date: "2026-10-25", Source: "rota"})
	if !errorsIs(err, ErrPolicy) {
		t.Fatalf("err = %v", err)
	}
	if gittest.Run(t, f.a, "status", "--porcelain") != "" {
		t.Fatal("clone must stay clean")
	}
}

func TestAddRefusesSymlinkedTopicFile(t *testing.T) {
	f := newFixture(t, "")
	outside := filepath.Join(filepath.Dir(f.a), "outside.txt")
	if err := os.WriteFile(outside, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(f.a, "topics/git.md")); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, f.a, "add", "-A")
	gittest.Run(t, f.a, "commit", "-q", "-m", "link")
	gittest.Run(t, f.a, "push", "-q")
	_, err := f.clone().Add(context.Background(), AddIn{Topic: "Git", Title: "T", Body: "b", Date: "2026-10-25", Source: "rota"})
	if !errorsIs(err, ErrDirty) {
		t.Fatalf("a symlinked topic must be refused with ErrDirty, got %v", err)
	}
	if b, _ := os.ReadFile(outside); string(b) != "keep" {
		t.Fatalf("outside file overwritten: %q", b)
	}
}

func TestPRModeFailedWriteReturnsToStartBranch(t *testing.T) {
	f := newFixture(t, `{"write":"pr","branch":"main"}`)
	// A new topic needs topics/<slug>.md; make its directory unwritable by
	// replacing topics/ with a file on disk after the switch.
	f.hook = func(args []string) {
		if len(args) > 0 && args[0] == "switch" {
			_ = os.RemoveAll(filepath.Join(f.a, TopicsDir))
			_ = os.WriteFile(filepath.Join(f.a, TopicsDir), []byte("x"), 0o644)
		}
	}
	_, err := f.clone().Add(context.Background(), AddIn{Topic: "Git", Title: "T", Body: "b", Date: "2026-10-25", Source: "rota"})
	if err == nil {
		t.Fatal("write must fail")
	}
	if got := gittest.Run(t, f.a, "rev-parse", "--abbrev-ref", "HEAD"); got != "main" {
		t.Fatalf("clone left on %q", got)
	}
}

func TestBulletContentIsRejected(t *testing.T) {
	f := newFixture(t, "")
	c := f.clone()
	ctx := context.Background()
	for _, in := range []AddIn{
		{Topic: "Go", Title: "a*b", Body: "b"},
		{Topic: "Go", Title: "t\nx", Body: "b"},
		{Topic: "Go", Title: "t", Body: "x --> y"},
		{Topic: "Go", Title: "t", Body: "l1\nl2"},
	} {
		if _, err := c.Add(ctx, in); !errorsIs(err, ErrContent) {
			t.Fatalf("add %+v: err = %v", in, err)
		}
	}
	for _, text := range []string{"x --> y", "a\nb"} {
		if _, err := c.Amend(ctx, AmendIn{Topic: "Go", Fragment: "Table tests", Text: text}); !errorsIs(err, ErrContent) {
			t.Fatalf("amend %q: err = %v", text, err)
		}
	}
}
