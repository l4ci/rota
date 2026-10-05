package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/golden"
	"github.com/l4ci/rota/internal/tracker"
)

// The ship verbs run against fixtures in temp repos: exit code and `data`
// against goldens that freeze what the retired helpers answered (through the
// old test shim) on copies of the same fixture, and for the writers the same
// .rota/ bytes and git state. Fixed git identity and dates (shipDeterministic)
// keep the commit hashes in the goldens stable.

// shipDeterministic pins git identity and dates so two runs on copies of a
// fixture make identical commits.
func shipDeterministic(t *testing.T) {
	t.Helper()
	for k, v := range map[string]string{
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t",
		"GIT_AUTHOR_DATE": "2026-01-01T00:00:00Z", "GIT_COMMITTER_DATE": "2026-01-01T00:00:00Z",
	} {
		t.Setenv(k, v)
	}
}

// shipCallIn describes one call; the golden holds the retired helper's data for it.
type shipCallIn struct {
	Name  string
	Args  []string
	Stdin string
	Code  int
}

// shipSame runs args as the Go verb in dir and requires the exit code and the
// data the golden recorded.
func shipSame(t *testing.T, name, dir, stdin string, wantCode int, args ...string) map[string]any {
	t.Helper()
	return shipSameWith(t, testDeps(), name, dir, stdin, wantCode, args...)
}

// shipSameWith is shipSame with the given deps.
func shipSameWith(t *testing.T, deps *Deps, name, dir, stdin string, wantCode int, args ...string) map[string]any {
	t.Helper()
	masked := make([]string, len(args)) // the project's temp dir is not part of the case
	for i, a := range args {
		masked[i] = strings.ReplaceAll(a, dir, "DIR")
	}
	n := trRunWith(t, deps, dir, stdin, append(append([]string{}, args...), "--json")...)
	nenv := envelope(t, n.stdout)
	if n.code != wantCode {
		t.Errorf("%s: exit %d, want %d\n%s%s", name, n.code, wantCode, n.stdout, n.stderr)
		return nenv
	}
	golden.Check(t, shipCallIn{name, masked, stdin, wantCode}, nenv["data"])
	return nenv
}

// shipFixture is a project in <tmp>/work: backend file, a bare origin next to
// it, main pushed, and a BACKLOG with two completed items.
func shipFixture(t *testing.T, cfg string) string {
	t.Helper()
	base := t.TempDir()
	work := filepath.Join(base, "work")
	origin := filepath.Join(base, "origin.git")
	gitT(t, base, "init", "-q", "--bare", origin)
	gitT(t, base, "init", "-q", "-b", "main", work)
	if cfg == "" {
		cfg = `{"backlog":{"backend":"file"},"issues":{"provider":"github","retryWaitSeconds":0}}`
	}
	write(t, filepath.Join(work, ".rota", "config.json"), cfg+"\n")
	write(t, filepath.Join(work, ".rota", "BACKLOG.md"), `# TODO

## Bugs

## Features

## Tasks

## Completed
- ~~**[B70] [P1] Ship demo bug.** Broken badge. GH: #42~~ Done 2026-04-18 [`+"`aaa1111`"+`]
- ~~**[F70] [Minor] Ship demo feature.** Overlay. GL: #43 GH: #42~~ Done 2026-04-18 [`+"`bbb2222`"+`]
`)
	write(t, filepath.Join(work, "seed.txt"), "seed\n")
	gitT(t, work, "add", "-A")
	gitT(t, work, "commit", "-q", "-m", "seed")
	gitT(t, work, "remote", "add", "origin", origin)
	gitT(t, work, "push", "-q", "origin", "main")
	return work
}

// shipCopy copies a fixture tree (the origin it points at stays shared).
func shipCopy(t *testing.T, dir string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "work")
	if out, err := exec.Command("cp", "-a", dir, dst).CombinedOutput(); err != nil {
		t.Fatalf("cp: %v\n%s", err, out)
	}
	return dst
}

// shipCommit adds one commit with the given subject and body on the current branch.
func shipCommit(t *testing.T, dir, file, subject, body string) {
	t.Helper()
	write(t, filepath.Join(dir, file), subject+"\n")
	gitT(t, dir, "add", file)
	msg := subject
	if body != "" {
		msg += "\n\n" + body
	}
	gitT(t, dir, "commit", "-q", "-m", msg)
}

func shipBranchOf(t *testing.T, dir, name string, commits ...[3]string) {
	t.Helper()
	gitT(t, dir, "checkout", "-q", "-b", name)
	for _, c := range commits {
		shipCommit(t, dir, c[0], c[1], c[2])
	}
	gitT(t, dir, "checkout", "-q", "main")
}

// shipTree is every file under dir's .rota/ with its bytes.
func shipTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	filepath.Walk(filepath.Join(dir, ".rota"), func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() && !strings.HasSuffix(p, ".lock") {
			b, _ := os.ReadFile(p)
			rel, _ := filepath.Rel(dir, p)
			out[rel] = string(b)
		}
		return nil
	})
	return out
}

func shipErrHas(t *testing.T, name string, o trOut, code int, sub string) {
	t.Helper()
	if o.code != code || !strings.Contains(o.stderr, sub) {
		t.Errorf("%s: exit %d (want %d), stderr %q (want %q)", name, o.code, code, o.stderr, sub)
	}
}

// ---- ship body ---------------------------------------------------------------

func TestShipBody(t *testing.T) {
	shipDeterministic(t)
	work := shipFixture(t, "")
	shipBranchOf(t, work, "rota/ship-demo",
		[3]string{"a.txt", "fix: badge invalidation [B70]", ""},
		[3]string{"b.txt", "feat: overlay [F70]", "Refs [B70] again and [F99]"},
		[3]string{"c.txt", "chore: plain", ""})
	shipBranchOf(t, work, "rota/plain", [3]string{"p.txt", "chore: nothing", ""})

	nenv := shipSame(t, "items and closes", work, "", 0, "ship", "body", "rota/ship-demo")
	body := nenv["data"].(map[string]any)["body"].(string)
	for _, want := range []string{"## Summary\n\n- chore: plain\n- feat: overlay [F70]\n- fix: badge invalidation [B70]\n\n", "## Items resolved\n\n- [F70] Ship demo feature\n- [B70] Ship demo bug\n- [F99]\n\n", "Closes #43\nCloses #42\n\n"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
	shipSame(t, "no items", work, "", 0, "ship", "body", "rota/plain")
	gitT(t, work, "checkout", "-q", "rota/plain")
	shipSame(t, "current branch", work, "", 0, "ship", "body")
	gitT(t, work, "checkout", "-q", "main")
	shipSame(t, "base branch has no commits", work, "", 1, "ship", "body", "main")
	shipSame(t, "unknown branch", work, "", 3, "ship", "body", "rota/none")

	o := trRun(t, work, "", "ship", "body", "rota/plain")
	if o.code != 0 || !strings.HasPrefix(o.stdout, "## Summary\n\n- chore: nothing\n") || strings.HasSuffix(o.stdout, "\n\n\n") {
		t.Errorf("text mode: %q", o.stdout)
	}
	shipErrHas(t, "no commits message", trRun(t, work, "", "ship", "body", "main"), 1, "no commits between base and main")

	u := umbrella(t)
	shipBranchOf(t, filepath.Join(u, "svc"), "feat/x", [3]string{"x.txt", "feat: x [B01]", ""})
	shipSame(t, "umbrella root", u, "", 2, "ship", "body", "feat/x")
	shipSame(t, "--repo", u, "", 0, "ship", "body", "feat/x", "--repo", "svc")
}

// ---- ship merge --------------------------------------------------------------

func TestShipMerge(t *testing.T) {
	shipDeterministic(t)
	work := shipFixture(t, "")
	shipBranchOf(t, work, "rota/ok", [3]string{"ok.txt", "feat: ok", ""})
	shipBranchOf(t, work, "rota/wt", [3]string{"wt.txt", "feat: wt", ""})
	shipBranchOf(t, work, "rota/clash", [3]string{"clash.txt", "feat: other side", ""})
	shipCommit(t, work, "clash.txt", "chore: base side", "")

	// ok: a linked worktree on the branch is removed first.
	gitT(t, work, "worktree", "add", "-q", filepath.Join(t.TempDir(), "wt"), "rota/wt")
	nenv := shipSame(t, "merge with worktree", work, "merge: wt branch\n\nbody\n", 0, "ship", "merge", "rota/wt", "--body-file", "-")
	if d := nenv["data"].(map[string]any); d["base"] != "main" || d["changed"] != true || d["sha"] != gitT(t, work, "rev-parse", "--short", "HEAD") {
		t.Errorf("merge data %v", d)
	}
	golden.Check(t, "HEAD after the merge of rota/wt", gitT(t, work, "rev-parse", "HEAD"))
	if gitT(t, work, "branch", "--list", "rota/wt") != "" || strings.Contains(gitT(t, work, "worktree", "list", "--porcelain"), "rota/wt") {
		t.Errorf("branch or worktree left behind: %s", gitT(t, work, "worktree", "list", "--porcelain"))
	}
	if msg := gitT(t, work, "log", "-1", "--format=%B"); msg != "merge: wt branch\n\nbody" {
		t.Errorf("merge message %q", msg)
	}
	shipSame(t, "plain merge", work, "merge: ok", 0, "ship", "merge", "rota/ok", "--body-file", "-")

	// refusals
	shipSame(t, "base branch", work, "merge: x", 4, "ship", "merge", "main", "--body-file", "-")
	head := gitT(t, work, "rev-parse", "HEAD")
	shipSame(t, "conflict", work, "merge: x", 4, "ship", "merge", "rota/clash", "--body-file", "-")
	if gitT(t, work, "rev-parse", "HEAD") != head || gitT(t, work, "status", "--porcelain") != "" {
		t.Errorf("a conflicting merge must leave the tree as it was")
	}
	if gitT(t, work, "branch", "--list", "rota/clash") == "" {
		t.Errorf("the conflicting branch must stay")
	}
	// usage and resolution
	shipSame(t, "empty body", work, "\n", 2, "ship", "merge", "rota/clash", "--body-file", "-")
	shipSame(t, "no --body-file", work, "", 2, "ship", "merge", "rota/clash")
	shipSame(t, "unknown branch", work, "merge: x", 3, "ship", "merge", "rota/none", "--body-file", "-")
	shipSame(t, "unreadable file", work, "", 2, "ship", "merge", "rota/clash", "--body-file", filepath.Join(work, "nope"))
	// a body file
	bf := filepath.Join(t.TempDir(), "msg")
	write(t, bf, "merge: from file\n")
	shipBranchOf(t, work, "rota/file", [3]string{"f.txt", "feat: f", ""})
	if o := trRun(t, work, "", "ship", "merge", "rota/file", "--body-file", bf); o.code != 0 {
		t.Errorf("--body-file path: %+v", o)
	}
	// git failure: checking out the base fails on an uncommitted change
	shipBranchOf(t, work, "rota/dirty", [3]string{"seed.txt", "feat: touches seed", ""})
	gitT(t, work, "checkout", "-q", "rota/dirty")
	write(t, filepath.Join(work, "seed.txt"), "uncommitted\n")
	if o := trRun(t, work, "merge: x", "ship", "merge", "rota/dirty", "--body-file", "-"); o.code != ExitUnavailable {
		t.Errorf("a checkout that git refuses should exit 5: %+v", o)
	}
	gitT(t, work, "checkout", "-q", "--", "seed.txt")
	gitT(t, work, "checkout", "-q", "main")

	u := umbrella(t)
	shipBranchOf(t, filepath.Join(u, "svc"), "feat/x", [3]string{"x.txt", "feat: x", ""})
	shipSame(t, "umbrella root", u, "merge: x", 2, "ship", "merge", "feat/x", "--body-file", "-")
	n := trRun(t, u, "merge: x", "ship", "merge", "feat/x", "--body-file", "-", "--repo", "svc", "--json")
	if n.code != 0 || gitT(t, filepath.Join(u, "svc"), "branch", "--list", "feat/x") != "" {
		t.Errorf("--repo merge: %+v", n)
	}
}

// ---- ship pr -----------------------------------------------------------------

func shipPRForge(url string) *forge {
	return &forge{answer: func(name string, args []string) (string, string, int) {
		if len(args) > 1 && (args[0] == "pr" || args[0] == "mr") && args[1] == "create" {
			return url + "\n", "", 0
		}
		return "", "", 0
	}}
}

func TestShipPRFileMode(t *testing.T) {
	shipDeterministic(t)
	for _, tc := range []struct{ prov, url string }{
		{"github", "https://github.com/fake/repo/pull/1"},
		{"gitlab", "https://gitlab.com/fake/repo/-/merge_requests/1"},
	} {
		t.Run(tc.prov, func(t *testing.T) {
			cfg := `{"backlog":{"backend":"file"},"issues":{"provider":"` + tc.prov + `","retryWaitSeconds":0}}`
			work := shipFixture(t, cfg)
			shipBranchOf(t, work, "feat/x", [3]string{"x.txt", "work", ""})
			f := shipPRForge(tc.url)
			deps := useForge(t, f)

			n := trRunWith(t, deps, work, "Summary line\n", "ship", "pr", "feat/x", "--title", "My title", "--body-file", "-", "--items", "F1,2", "--json")
			if n.code != 0 {
				t.Fatalf("exit %d\n%s%s", n.code, n.stdout, n.stderr)
			}
			nenv := envelope(t, n.stdout)
			golden.Check(t, map[string]any{"provider": tc.prov, "url": tc.url, "stdin": "Summary line",
				"args": []string{"ship", "pr", "feat/x", "--title", "My title", "--body-file", "-", "--items", "F1,2"}}, nenv["data"])
			d := nenv["data"].(map[string]any)
			if d["provider"] != tc.prov || d["number"] != 1.0 || d["changed"] != true || !reflect.DeepEqual(d["items"], []any{"F1", "2"}) {
				t.Errorf("data %v", d)
			}
			if tc.prov == "github" {
				if len(f.calls) != 1 || f.calls[0] != "gh pr create --title My title --body-file -" || f.stdins[0] != "Summary line" {
					t.Errorf("gh call %q stdin %q", f.calls, f.stdins)
				}
			} else if len(f.calls) != 1 || f.calls[0] != "glab mr create --title My title --description Summary line --source-branch feat/x --target-branch main --yes" {
				t.Errorf("glab call %q", f.calls)
			}
			if f.dirs[0] != work {
				if real, _ := filepath.EvalSymlinks(work); f.dirs[0] != real {
					t.Errorf("CLI ran in %q, want %q", f.dirs[0], work)
				}
			}
			origin := filepath.Join(filepath.Dir(work), "origin.git")
			if gitT(t, work, "--git-dir="+origin, "rev-parse", "--verify", "-q", "refs/heads/feat/x") == "" {
				t.Errorf("branch not pushed")
			}
			if gitT(t, work, "config", "branch.feat/x.remote") != "origin" {
				t.Errorf("upstream not set")
			}
			if n.stdout == "" || trRunWith(t, deps, work, "b", "ship", "pr", "feat/x", "--title", "T", "--body-file", "-").stdout != tc.url+"\n" {
				t.Errorf("text mode must print the URL")
			}
		})
	}
}

func TestShipPRUsageAndResolution(t *testing.T) {
	shipDeterministic(t)
	work := shipFixture(t, "")
	shipBranchOf(t, work, "feat/y", [3]string{"y.txt", "work", ""})
	f := shipPRForge("https://github.com/fake/repo/pull/9")
	deps := useForge(t, f)
	pr := []string{"ship", "pr", "feat/y", "--title", "T", "--body-file", "-"}
	shipSameWith(t, deps, "no title", work, "b", 2, "ship", "pr", "feat/y", "--body-file", "-")
	shipSameWith(t, deps, "no body-file", work, "b", 2, "ship", "pr", "feat/y", "--title", "T")
	shipSameWith(t, deps, "empty body", work, "\n", 2, pr...)
	shipSameWith(t, deps, "unknown branch", work, "b", 3, "ship", "pr", "no/such", "--title", "T", "--body-file", "-")
	u := umbrella(t)
	shipBranchOf(t, filepath.Join(u, "svc"), "feat/y", [3]string{"y.txt", "work", ""})
	shipSameWith(t, deps, "umbrella root", u, "b", 2, pr...)
	if len(f.calls) != 0 {
		t.Errorf("no forge call expected: %q", f.calls)
	}
	// the push fails: no origin
	gitT(t, work, "remote", "remove", "origin")
	shipErrHas(t, "push fails", trRunWith(t, deps, work, "b", pr...), ExitUnavailable, "git push")
	gitT(t, work, "remote", "add", "origin", filepath.Join(t.TempDir(), "gone.git"))
	if o := trRunWith(t, deps, work, "b", pr...); o.code != ExitUnavailable {
		t.Errorf("unreachable origin: %+v", o)
	}
}

func TestShipPRTrackerFailures(t *testing.T) {
	shipDeterministic(t)
	work := shipFixture(t, "")
	shipBranchOf(t, work, "feat/z", [3]string{"z.txt", "work", ""})
	f := &forge{answer: func(string, []string) (string, string, int) { return "", "boom", 1 }}
	deps := useForge(t, f)
	pr := []string{"ship", "pr", "feat/z", "--title", "T", "--body-file", "-"}
	shipErrHas(t, "cli fails", trRunWith(t, deps, work, "b", pr...), ExitUnavailable, "boom")
	f.answer = func(string, []string) (string, string, int) { return "", "secondary rate limit", 1 }
	if o := trRunWith(t, deps, work, "b", pr...); o.code != ExitRetry {
		t.Errorf("rate limit: %+v", o)
	}
	f.found = false
	if o := trRunWith(t, deps, work, "b", pr...); o.code != ExitUnavailable {
		t.Errorf("gh missing: %+v", o)
	}
}

func TestShipPRProviderDetection(t *testing.T) {
	shipDeterministic(t)
	// no issues.provider: the origin host decides, an unknown host means github
	for _, tc := range []struct{ url, want string }{
		{"git@gitlab.example.com:g/r.git", "gitlab"},
		{"https://github.example.com/g/r.git", "github"},
		{"", "github"},
	} {
		work := shipFixture(t, `{"backlog":{"backend":"file"}}`)
		shipBranchOf(t, work, "feat/p", [3]string{"p.txt", "work", ""})
		if tc.url != "" {
			// fetch URL names the host, pushes still reach the bare repo
			origin := gitT(t, work, "remote", "get-url", "origin")
			gitT(t, work, "remote", "set-url", "origin", tc.url)
			gitT(t, work, "remote", "set-url", "--push", "origin", origin)
		}
		f := shipPRForge("https://x.test/p/7")
		answer := f.answer
		url := tc.url
		f.answer = func(name string, args []string) (string, string, int) {
			if name == "git" { // the fake serves the origin URL detection reads
				if url == "" {
					return "", "", 2
				}
				return url + "\n", "", 0
			}
			return answer(name, args)
		}
		deps := useForge(t, f)
		n := trRunWith(t, deps, work, "b", "ship", "pr", "feat/p", "--title", "T", "--body-file", "-", "--json")
		if n.code != 0 {
			t.Fatalf("%s: %+v", tc.url, n)
		}
		if d := envelope(t, n.stdout)["data"].(map[string]any); d["provider"] != tc.want {
			t.Errorf("%q: provider %v, want %s", tc.url, d["provider"], tc.want)
		}
	}
}

func TestShipPRIssueMode(t *testing.T) {
	shipDeterministic(t)
	for _, prov := range []string{"github", "gitlab"} {
		t.Run(prov, func(t *testing.T) {
			cfg := `{"backlog":{"backend":"issues"},"issues":{"provider":"` + prov + `","retryWaitSeconds":0}}`
			work := shipFixture(t, cfg)
			shipBranchOf(t, work, "feat/i", [3]string{"i.txt", "work", ""})
			f := shipPRForge("https://x.test/pr/5")
			deps := useForge(t, f)
			deps.NewTracker = withTracker(t, issueFixture()).NewTracker

			// --items resolves through the issue backend: F7 and 9 -> issues 7 and 9
			n := trRunWith(t, deps, work, "Summary line\n", "ship", "pr", "feat/i", "--title", "T", "--body-file", "-", "--items", "F7,9", "--json")
			if n.code != 0 {
				t.Fatalf("%+v", n)
			}
			d := envelope(t, n.stdout)["data"].(map[string]any)
			if !reflect.DeepEqual(d["items"], []any{"F7", "9"}) || d["number"] != 5.0 {
				t.Errorf("data %v", d)
			}
			want := "Summary line\n\nCloses #7\nCloses #9"
			if prov == "github" && f.stdins[0] != want {
				t.Errorf("body %q, want %q", f.stdins[0], want)
			}
			if prov == "gitlab" && !strings.Contains(f.calls[0], "--description "+want+" --source-branch") {
				t.Errorf("glab call %q", f.calls[0])
			}

			// an unknown item fails before anything is pushed
			shipBranchOf(t, work, "feat/j", [3]string{"j.txt", "work", ""})
			before := len(f.calls)
			o := trRunWith(t, deps, work, "b", "ship", "pr", "feat/j", "--title", "T", "--body-file", "-", "--items", "F99")
			shipErrHas(t, "unknown item", o, ExitResolution, "F99")
			if len(f.calls) != before {
				t.Errorf("forge called after a bad item")
			}
			origin := filepath.Join(filepath.Dir(work), "origin.git")
			if gitT(t, work, "--git-dir="+origin, "branch", "--list", "feat/j") != "" {
				t.Errorf("a bad --items must push nothing")
			}
		})
	}
}

func TestShipPRIssueModeUmbrella(t *testing.T) {
	shipDeterministic(t)
	u := umbrella(t)
	write(t, filepath.Join(u, ".rota", "config.json"), `{"backlog":{"backend":"issues"},"issues":{"provider":"github"}}`)
	svc := filepath.Join(u, "svc")
	shipBranchOf(t, svc, "feat/u", [3]string{"u.txt", "work", ""})
	deps := useForge(t, shipPRForge("https://x.test/pr/1"))
	deps.NewTracker = withTracker(t, issueFixture()).NewTracker
	origin := t.TempDir()
	gitT(t, origin, "init", "-q", "--bare")
	gitT(t, svc, "remote", "add", "origin", origin)
	// The items resolve inside the --repo sub-repo, and one qualified with
	// another sub-repo is unknown there.
	o := trRunWith(t, deps, u, "b", "ship", "pr", "feat/u", "--title", "T", "--body-file", "-", "--items", "web:7", "--repo", "svc")
	if o.code != 3 {
		t.Errorf("item of another sub-repo: %+v", o)
	}
	o = trRunWith(t, deps, u, "b", "ship", "pr", "feat/u", "--title", "T", "--body-file", "-", "--items", "7", "--repo", "svc", "--json")
	if o.code != 0 {
		t.Fatalf("umbrella ship pr --items: %+v", o)
	}
	if d := envelope(t, o.stdout)["data"].(map[string]any); d["url"] != "https://x.test/pr/1" {
		t.Errorf("data %v", d)
	}
}

// ---- ship pr-merge -----------------------------------------------------------

func TestShipPRMerge(t *testing.T) {
	f := a8Fixture()
	root, deps := a8Project(t, f)
	code, data, msg := a8RunWith(t, deps, root, "ship", "pr-merge", "10")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, msg)
	}
	if data["pr"] != float64(10) || data["sha"] != "0123456" || !reflect.DeepEqual(data["closed"], []any{"1"}) || data["changed"] != true {
		t.Fatalf("data %v", data)
	}
	if is := f.Fake.Issues[0]; is.State != "closed" || is.StateReason != "completed" || slices.Contains(is.Labels, "needs-review") {
		t.Fatalf("issue 1 %+v", is)
	}
	if !reflect.DeepEqual(f.merged, []int{10}) {
		t.Fatalf("merged %v", f.merged)
	}
	// Key order is the contract's, and text mode prints the old helper's lines.
	f2 := a8Fixture()
	o := trRunIn(t, f2, "", "--json", "ship", "pr-merge", "10")
	if !strings.Contains(o.stdout, `"data": {"pr": 10, "sha": "0123456", "closed": ["1"], "changed": true}`) {
		t.Fatalf("key order: %s", o.stdout)
	}
	f3 := a8Fixture()
	if o := trRunIn(t, f3, "", "ship", "pr-merge", "10"); o.code != 0 || o.stdout != "merged 10 as 0123456\nclosed F1\n" {
		t.Fatalf("text %+v", o)
	}
}

func TestShipPRMergeItems(t *testing.T) {
	f := a8Fixture()
	root, deps := a8Project(t, f)
	// An explicit list replaces the PR body's links; spaces and empty entries are dropped.
	code, data, msg := a8RunWith(t, deps, root, "ship", "pr-merge", "12", "--items", " F1 ,, ")
	if code != 0 || !reflect.DeepEqual(data["closed"], []any{"1"}) {
		t.Fatalf("exit %d %v %s", code, data, msg)
	}
	// No linked items: the PR merges and nothing is closed.
	f = a8Fixture()
	code, data, msg = a8RunIn(t, f, "ship", "pr-merge", "12")
	if code != 0 || !reflect.DeepEqual(data["closed"], []any{}) || !reflect.DeepEqual(f.merged, []int{12}) {
		t.Fatalf("exit %d %v %s", code, data, msg)
	}
}

func TestShipPRMergeUnproven(t *testing.T) {
	f := a8Fixture()
	root, deps := a8Project(t, f)
	code, data, _ := a8RunWith(t, deps, root, "ship", "pr-merge", "11")
	want := map[string]any{"pr": float64(11), "merged": false, "unproven": []any{"2"}, "changesRequested": []any{"2"}, "changed": true}
	if code != 4 || !reflect.DeepEqual(data, want) {
		t.Fatalf("exit %d data %v", code, data)
	}
	if len(f.merged) != 0 {
		t.Fatal("merged an unproven PR")
	}
	is := f.Fake.Issues[1]
	if !slices.Contains(is.Labels, "changes-requested") || slices.Contains(is.Labels, "needs-review") || is.State != "open" ||
		!strings.Contains(is.Comments[len(is.Comments)-1].Body, "rota:comment feedback") {
		t.Fatalf("issue 2 %+v", is)
	}
	o := trRunIn(t, a8Fixture(), "", "--json", "ship", "pr-merge", "11")
	if !strings.Contains(o.stdout, `"data": {"pr": 11, "merged": false, "unproven": ["2"], "changesRequested": ["2"], "changed": true}`) {
		t.Fatalf("key order: %s", o.stdout)
	}
}

func TestShipPRMergeExits(t *testing.T) {
	root, deps := a8Project(t, a8Fixture())
	for _, c := range []struct {
		args []string
		code int
	}{
		{[]string{"x"}, 2},
		{[]string{"1x"}, 2},
		{[]string{}, 2},
		{[]string{"10", "11"}, 2},
		{[]string{"9999"}, 3},                 // unknown PR
		{[]string{"12", "--items", "99"}, 3},  // unknown item
		{[]string{"12", "--items", "B1"}, 3},  // type letter mismatch
		{[]string{"12", "--items", "M01"}, 3}, // not an item
		{[]string{"12", "--items", "3"}, 3},   // the milestone tracking issue
	} {
		code, _, msg := a8RunWith(t, deps, root, append([]string{"ship", "pr-merge"}, c.args...)...)
		if code != c.code {
			t.Errorf("%v: exit %d (%s), want %d", c.args, code, msg, c.code)
		}
	}
	// Already merged: no longer open.
	f := a8Fixture()
	root, deps = a8Project(t, f)
	if code, _, _ := a8RunWith(t, deps, root, "ship", "pr-merge", "12"); code != 0 {
		t.Fatalf("first merge: exit %d", code)
	}
	if code, _, _ := a8RunWith(t, deps, root, "ship", "pr-merge", "12"); code != 3 {
		t.Errorf("already merged: exit %d, want 3", code)
	}
}

func TestShipPRMergeRefused(t *testing.T) {
	f := a8Fixture()
	f.mergeErr = &tracker.Error{Kind: tracker.KindFailed, Code: 1, Message: "gh pr merge 12: not mergeable"}
	code, data, _ := a8RunIn(t, f, "ship", "pr-merge", "12")
	want := map[string]any{"pr": float64(12), "merged": false, "unproven": []any{}, "changesRequested": []any{}, "changed": false}
	if code != 4 || !reflect.DeepEqual(data, want) {
		t.Fatalf("exit %d data %v", code, data)
	}
	f = a8Fixture()
	f.mergeErr = &tracker.Error{Kind: tracker.KindRateLimited, Code: 4, Message: "rate limit"}
	if code, _, _ := a8RunIn(t, f, "ship", "pr-merge", "12"); code != 6 {
		t.Errorf("rate limited: exit %d", code)
	}
	f = a8Fixture()
	f.mergeErr = &tracker.Error{Kind: tracker.KindUnavailable, Code: 3, Message: "gh missing"}
	if code, _, _ := a8RunIn(t, f, "ship", "pr-merge", "12"); code != 5 {
		t.Errorf("unavailable: exit %d", code)
	}
	f = a8Fixture()
	f.Fake.Fail = map[string]error{"open_prs": &tracker.Error{Kind: tracker.KindUnavailable, Code: 3, Message: "boom"}}
	if code, _, _ := a8RunIn(t, f, "ship", "pr-merge", "12"); code != 5 {
		t.Errorf("PR list failure: exit %d", code)
	}
}

func TestShipPRMergeBackend(t *testing.T) {
	code, data, _ := a8Run(t, a8FileProject(t), "ship", "pr-merge", "1")
	if code != 4 || data["blockedBy"] != "backend" || data["changed"] != false {
		t.Fatalf("file: exit %d data %v", code, data)
	}
	// Umbrella: without --repo a usage error, with it not ported.
	_, umb := reviewProject(t)
	if o := trRun(t, umb, "", "ship", "pr-merge", "1"); o.code != 2 {
		t.Errorf("umbrella without --repo: exit %d", o.code)
	}
	if o := trRun(t, umb, "", "ship", "pr-merge", "1", "--repo", "svc"); o.code != 4 {
		t.Errorf("file-backend umbrella --repo: exit %d, want 4 (backend)", o.code)
	}
	write(t, filepath.Join(umb, ".rota", "config.json"), `{"backlog":{"backend":"issues"}}`)
	if o := trRun(t, umb, "", "ship", "pr-merge", "1", "--repo", "svc"); o.code != 5 {
		t.Errorf("umbrella --repo, no forge: exit %d, want 5", o.code)
	}
	// Scope S: inside a sub-repo no --repo is needed; it reaches that tracker.
	if o := trRun(t, filepath.Join(umb, "svc"), "", "ship", "pr-merge", "1"); o.code != 5 {
		t.Errorf("from the svc cwd, no forge: exit %d, want 5\n%s", o.code, o.stderr)
	}
}

// ---- ship undo ---------------------------------------------------------------

// shipCycle builds a /rota-work cycle on a fixture: feature branch with two
// commits, a no-ff `merge: ` into main, and F03 completed against the impl hash.
func shipCycle(t *testing.T) string {
	t.Helper()
	shipDeterministic(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "work")
	gitT(t, dir, "init", "-q", "-b", "main", root)
	write(t, filepath.Join(root, ".rota", "BACKLOG.md"), "# TODO\n\n## Bugs\n- **[B01] [P1] Sample bug.** Body.\n\n## Features\n- **[F03] [Minor] Sample feature.** Body.\n\n## Tasks\n\n## Completed\n")
	write(t, filepath.Join(root, ".rota", "counters.json"), "{\n  \"bugs\": 1,\n  \"features\": 3,\n  \"tasks\": 0,\n  \"milestones\": 0,\n  \"since_refactor\": {\"features\": 0, \"bugs\": 0, \"tasks\": 0}\n}\n")
	write(t, filepath.Join(root, ".rota", "config.json"), "{}\n")
	write(t, filepath.Join(root, "seed.txt"), "seed\n")
	gitT(t, root, "add", "-A")
	gitT(t, root, "commit", "-q", "-m", "seed")
	gitT(t, root, "checkout", "-q", "-b", "rota/F03-test")
	shipCommit(t, root, "prep.txt", "chore: prep for F03", "")
	shipCommit(t, root, "impl.txt", "feat: implement F03", "")
	short := gitT(t, root, "rev-parse", "--short", "HEAD")
	shortPrep := gitT(t, root, "rev-parse", "--short", "HEAD~1")
	gitT(t, root, "checkout", "-q", "main")
	gitT(t, root, "merge", "--no-ff", "-q", "rota/F03-test", "-m", "merge: F03 — test cycle")
	gitT(t, root, "branch", "-q", "-d", "rota/F03-test")
	b, _ := os.ReadFile(filepath.Join(root, ".rota", "BACKLOG.md"))
	text := strings.Replace(string(b), "- **[F03] [Minor] Sample feature.** Body.\n", "", 1)
	text = strings.Replace(text, "## Completed\n", "## Completed\n- ~~**[F03] [Minor] Sample feature.** Body.~~ Done 2026-01-15 [`"+short+"`]\n"+
		"- ~~**[B01] [P1] Sample bug.** Body.~~ Done 2026-01-15 [`"+shortPrep+"`]\n", 1)
	text = strings.Replace(text, "- **[B01] [P1] Sample bug.** Body.\n", "", 1)
	write(t, filepath.Join(root, ".rota", "BACKLOG.md"), text)
	gitT(t, root, "add", ".rota/BACKLOG.md")
	gitT(t, root, "commit", "-q", "--amend", "--no-edit")
	return root
}

// shipUndoWant is what an undo answered and left behind: data,
// the .rota/ tree, HEAD and git status.
type shipUndoWant struct {
	Data   any
	Tree   map[string]string
	Head   string
	Status string
}

// shipUndoBoth runs ship undo on a copy of src and compares exit, data, the
// .rota/ tree, HEAD and status with the golden.
func shipUndoBoth(t *testing.T, name string, src string, wantCode int, args ...string) map[string]any {
	t.Helper()
	nu := shipCopy(t, src)
	oargs := append([]string{"ship", "undo"}, args...)
	n := trRun(t, nu, "", append(append([]string{}, oargs...), "--json")...)
	nenv := envelope(t, n.stdout)
	if n.code != wantCode {
		t.Fatalf("%s: exit %d want %d\n%s%s", name, n.code, wantCode, n.stdout, n.stderr)
	}
	golden.Check(t, shipCallIn{Name: name, Args: oargs, Code: wantCode}, shipUndoWant{
		Data: nenv["data"], Tree: shipTree(t, nu), Head: gitT(t, nu, "rev-parse", "HEAD"), Status: gitT(t, nu, "status", "--porcelain")})
	return nenv
}

func TestShipUndoPreviewAndApply(t *testing.T) {
	src := shipCycle(t)
	pre := gitT(t, src, "rev-parse", "HEAD~1")
	n := shipUndoBoth(t, "preview", src, 0)
	d := n["data"].(map[string]any)
	if d["applied"] != false || d["changed"] != false || d["base"] != "main" || d["subject"] != "merge: F03 — test cycle" ||
		!reflect.DeepEqual(d["items"], []any{"F03", "B01"}) {
		t.Errorf("preview data %v", d)
	}
	if w, _ := n["warnings"].([]any); len(w) != 1 || w[0] != "preview only; pass --apply" {
		t.Errorf("warnings %v", n["warnings"])
	}
	// the preview changes nothing
	o := trRun(t, src, "", "ship", "undo")
	short := gitT(t, src, "rev-parse", "--short", "HEAD")
	wantText := "Undo plan for last cycle: " + short + "\n\nSubject:  merge: F03 — test cycle\n" +
		"Base:     main will reset --hard " + short + "^1 (currently " + short + ")\n" +
		"Items:    F03 will be restored to BACKLOG.md (Features)\n          B01 will be restored to BACKLOG.md (Bugs)\n\n" +
		"Branch:   deleted by rota ship merge; rerun `git branch <name> " + short + "^2` to keep the work\n" +
		"Status:   no active entry to clear (cycle already removed it)\nHandoff:  gitignored — not restorable\nPlans:    gitignored — not restorable\n\nRe-run with --apply to apply.\n"
	if o.code != 0 || o.stdout != wantText {
		t.Errorf("plan text:\n%q\nwant\n%q", o.stdout, wantText)
	}
	if gitT(t, src, "rev-parse", "HEAD") == pre {
		t.Errorf("preview moved HEAD")
	}

	n = shipUndoBoth(t, "apply", src, 0, "--apply")
	d = n["data"].(map[string]any)
	if d["applied"] != true || d["changed"] != true || d["restoredTo"] != gitT(t, src, "rev-parse", "--short", "HEAD~1") ||
		!reflect.DeepEqual(d["restored"], []any{"F03", "B01"}) {
		t.Errorf("apply data %v", d)
	}
	nu := shipCopy(t, src)
	o = trRun(t, nu, "", "ship", "undo", "--apply")
	if o.code != 0 || o.stdout != "Undone cycle "+short+". Reset main to "+gitT(t, nu, "rev-parse", "--short", "HEAD")+". Restored: F03,B01.\n" {
		t.Errorf("apply text %q", o.stdout)
	}
	if gitT(t, nu, "rev-parse", "HEAD") != pre {
		t.Errorf("not reset to the pre-merge commit")
	}
}

func TestShipUndoNoItems(t *testing.T) {
	src := shipCycle(t)
	write(t, filepath.Join(src, ".rota", "BACKLOG.md"), "# TODO\n\n## Completed\n")
	gitT(t, src, "add", "-A")
	gitT(t, src, "commit", "-q", "--amend", "--no-edit")
	n := shipUndoBoth(t, "no items", src, 0, "--apply")
	d := n["data"].(map[string]any)
	if !reflect.DeepEqual(d["items"], []any{}) || !reflect.DeepEqual(d["restored"], []any{}) {
		t.Errorf("data %v", d)
	}
	o := trRun(t, shipCopy(t, src), "", "ship", "undo")
	if !strings.Contains(o.stdout, "Items:    none (cycle resolved no tracked items)\n") {
		t.Errorf("%q", o.stdout)
	}
}

func TestShipUndoRefusals(t *testing.T) {
	src := shipCycle(t)
	blocked := func(by string) map[string]any { return map[string]any{"blockedBy": by, "changed": false} }
	check := func(name string, dir string, code int, by string, args ...string) {
		t.Helper()
		n := shipUndoBoth(t, name, dir, code, args...)
		if by != "" && !reflect.DeepEqual(n["data"], any(blocked(by))) {
			t.Errorf("%s: data %v, want blockedBy %s", name, n["data"], by)
		}
	}

	feat := shipCopy(t, src)
	gitT(t, feat, "checkout", "-q", "-b", "feat/other")
	check("not on base", feat, 4, "not on base branch")

	dirty := shipCopy(t, src)
	write(t, filepath.Join(dirty, "stray.txt"), "x\n")
	check("dirty tree", dirty, 4, "dirty tree")

	flat := shipCopy(t, src)
	shipCommit(t, flat, "after.txt", "chore: after", "")
	check("HEAD is not a merge", flat, 4, "not a merge")
	check("--cycle with post-merge commits", flat, 4, "post-merge commits", "--cycle", "HEAD~1")
	n := shipUndoBoth(t, "--allow-post-merge", flat, 0, "--cycle", "HEAD~1", "--allow-post-merge", "--apply")
	if n["data"].(map[string]any)["restoredTo"] == nil {
		t.Errorf("allow-post-merge data %v", n["data"])
	}
	po := trRun(t, shipCopy(t, flat), "", "ship", "undo", "--cycle", "HEAD~1", "--allow-post-merge")
	if !strings.Contains(po.stdout, "(currently ") || !strings.Contains(po.stdout, ", 1 commit(s) past the merge will be discarded)") {
		t.Errorf("post-merge note: %q", po.stdout)
	}
	check("--cycle not a merge", flat, 4, "not a merge", "--cycle", "HEAD")
	check("--cycle bad hash", src, 3, "", "--cycle", "nonsense")

	plain := shipCopy(t, src)
	gitT(t, plain, "reset", "-q", "--hard", "HEAD~1")
	check("root commit", plain, 4, "not a merge")

	sub := shipCopy(t, src)
	gitT(t, sub, "commit", "-q", "--amend", "-m", "Merge branch rota/F03-test")
	check("subject is not merge:", sub, 4, "merge subject")
	check("--cycle subject is not merge:", sub, 4, "merge subject", "--cycle", "HEAD")

	pr := shipCopy(t, src)
	gitT(t, pr, "update-ref", "refs/remotes/origin/rota/F03-test", gitT(t, pr, "rev-parse", "HEAD^2"))
	check("PR mode", pr, 4, "pr mode")

	empty := t.TempDir()
	gitT(t, empty, "init", "-q", "-b", "main")
	write(t, filepath.Join(empty, ".rota", "config.json"), "{}\n")
	o := trRun(t, empty, "", "ship", "undo")
	if o.code == 0 {
		t.Errorf("an empty repo has nothing to undo: %+v", o)
	}

	u := umbrella(t)
	if o := trRun(t, u, "", "ship", "undo"); o.code != ExitUsage {
		t.Errorf("umbrella root: %+v", o)
	}
}

func TestShipUndoItemFromArchive(t *testing.T) {
	src := shipCycle(t)
	// the done line sits in ARCHIVE.md instead of BACKLOG.md ## Completed
	b, _ := os.ReadFile(filepath.Join(src, ".rota", "BACKLOG.md"))
	var keep, arch []string
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "- ~~**[F03]") {
			arch = append(arch, l)
		} else {
			keep = append(keep, l)
		}
	}
	write(t, filepath.Join(src, ".rota", "BACKLOG.md"), strings.Join(keep, "\n"))
	write(t, filepath.Join(src, ".rota", "ARCHIVE.md"), "# Archive\n\n"+strings.Join(arch, "\n")+"\n")
	gitT(t, src, "add", "-A")
	gitT(t, src, "commit", "-q", "--amend", "--no-edit")
	n := shipUndoBoth(t, "archive", src, 0, "--apply")
	if d := n["data"].(map[string]any); !reflect.DeepEqual(d["restored"], []any{"B01", "F03"}) && !reflect.DeepEqual(d["restored"], []any{"F03", "B01"}) {
		t.Errorf("data %v", d)
	}
}

// A restore that fails after the reset is exit 5 and says the reset happened.
func TestShipUndoRestoreFails(t *testing.T) {
	shipDeterministic(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "work")
	gitT(t, dir, "init", "-q", "-b", "main", root)
	write(t, filepath.Join(root, "seed.txt"), "seed\n")
	gitT(t, root, "add", "-A")
	gitT(t, root, "commit", "-q", "-m", "seed")
	gitT(t, root, "checkout", "-q", "-b", "rota/F03-x")
	shipCommit(t, root, "impl.txt", "feat: impl", "")
	short := gitT(t, root, "rev-parse", "--short", "HEAD")
	// BACKLOG.md exists only on the cycle side, so the reset removes it.
	write(t, filepath.Join(root, ".rota", "BACKLOG.md"), "# TODO\n\n## Features\n\n## Completed\n- ~~**[F03] [Minor] Sample.** Body.~~ Done 2026-01-15 [`"+short+"`]\n")
	gitT(t, root, "add", "-A")
	gitT(t, root, "commit", "-q", "-m", "chore: backlog")
	gitT(t, root, "checkout", "-q", "main")
	gitT(t, root, "merge", "--no-ff", "-q", "rota/F03-x", "-m", "merge: F03")
	gitT(t, root, "branch", "-q", "-d", "rota/F03-x")
	// the done line must carry a hash of the cycle: it does (short is on the branch)
	nu := shipCopy(t, root)
	n := trRun(t, nu, "", "ship", "undo", "--apply", "--json")
	if n.code != 5 {
		t.Fatalf("exit %d %s%s", n.code, n.stdout, n.stderr)
	}
	if !strings.Contains(n.stderr, "the reset already happened (HEAD is now "+gitT(t, nu, "rev-parse", "--short", "HEAD")+")") {
		t.Errorf("message %q", n.stderr)
	}
	if gitT(t, nu, "rev-parse", "HEAD") != gitT(t, root, "rev-parse", "HEAD^1") {
		t.Errorf("the run must stop at the reset")
	}
}

func TestShipUndoRepo(t *testing.T) {
	u := umbrella(t)
	svc := filepath.Join(u, "svc")
	shipDeterministic(t)
	write(t, filepath.Join(u, ".rota", "BACKLOG.md"), "# TODO\n\n## Features\n\n## Completed\n")
	gitT(t, svc, "checkout", "-q", "-b", "rota/F03-t")
	shipCommit(t, svc, "impl.txt", "feat: impl F03", "")
	short := gitT(t, svc, "rev-parse", "--short", "HEAD")
	gitT(t, svc, "checkout", "-q", "main")
	gitT(t, svc, "merge", "--no-ff", "-q", "rota/F03-t", "-m", "merge: F03")
	write(t, filepath.Join(u, ".rota", "BACKLOG.md"), "# TODO\n\n## Features\n\n## Completed\n- ~~**[F03] [Minor] Sample.** Body.~~ Done 2026-01-15 [`"+short+"`]\n")
	n := trRun(t, u, "", "ship", "undo", "--repo", "svc", "--apply", "--json")
	if n.code != 0 {
		t.Fatalf("%+v", n)
	}
	if d := envelope(t, n.stdout)["data"].(map[string]any); !reflect.DeepEqual(d["restored"], []any{"F03"}) {
		t.Errorf("data %v", d)
	}
	b, _ := os.ReadFile(filepath.Join(u, ".rota", "BACKLOG.md"))
	if !strings.Contains(string(b), "## Features\n\n- **[F03] [Minor] Sample.** Body.") {
		t.Errorf("F03 not restored in the umbrella backlog:\n%s", b)
	}
	if gitT(t, svc, "rev-list", "--count", "HEAD") != "1" {
		t.Errorf("svc not reset")
	}
}
