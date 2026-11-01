package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/proof"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// doneProject is a backlog project whose slot "ben" holds B07 on branch feat/x.
func doneProject(t *testing.T, fast string) string {
	dir := backlogProject(t)
	cfg := `{}`
	if fast != "" {
		cfg = `{"test":{"fast":["` + fast + `"]}}`
	}
	os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(cfg), 0o644)
	os.WriteFile(filepath.Join(dir, ".rota", "workers.json"),
		[]byte(`{"slots":[{"name":"ben","branch":"feat/x","task":"B07","state":"busy"}]}`), 0o644)
	// done refuses a dirty tree: ignore the fixture's own .rota files.
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".rota/\n"), 0o644)
	gittest.Run(t, dir, "add", ".gitignore")
	gittest.Run(t, dir, "commit", "-q", "-m", "ignore rota")
	// done also wants the branch pushed: the PR is opened before it.
	remote := filepath.Join(t.TempDir(), "r.git")
	gittest.Run(t, dir, "init", "-q", "--bare", remote)
	gittest.Run(t, dir, "remote", "add", "origin", remote)
	gittest.Run(t, dir, "push", "-q", "origin", "feat/x")
	return dir
}

func headOf(t *testing.T, dir string) string {
	t.Helper()
	return gittest.Run(t, dir, "rev-parse", "--short", "HEAD")
}

func slotState(t *testing.T, dir string) string {
	b, _ := os.ReadFile(filepath.Join(dir, ".rota", "workers.json"))
	if strings.Contains(string(b), `"state": "done"`) || strings.Contains(string(b), `"state":"done"`) {
		return "done"
	}
	return "busy"
}

func TestWorkerDone(t *testing.T) {
	t.Run("refuses without a row", func(t *testing.T) {
		dir := doneProject(t, "true")
		code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--json")
		d := data(t, out)
		if code != 4 || d["changed"] != false || d["missing"] == nil {
			t.Fatalf("exit %d: %s", code, out)
		}
		if !strings.Contains(out, "rota proof record") {
			t.Errorf("hint must name rota proof record: %s", out)
		}
		if slotState(t, dir) == "done" {
			t.Error("a refusal must not mark the slot done")
		}
	})
	t.Run("accepts a PASS row at HEAD", func(t *testing.T) {
		dir := doneProject(t, "true")
		if code, out, _ := rotaIn(t, dir, "proof", "record", "B07", "--", "true"); code != 0 {
			t.Fatalf("record: %d %s", code, out)
		}
		code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--json")
		if code != 0 || data(t, out)["changed"] != true {
			t.Fatalf("exit %d: %s", code, out)
		}
		if slotState(t, dir) != "done" {
			t.Error("slot not marked done")
		}
	})
	t.Run("unknown slot is 3", func(t *testing.T) {
		dir := doneProject(t, "true")
		if code, _, _ := rotaIn(t, dir, "worker", "done", "nope"); code != 3 {
			t.Errorf("exit %d, want 3", code)
		}
	})
}

func acceptanceDoneProject(t *testing.T) (string, *trackertest.Fake, *Deps) {
	t.Helper()
	dir := doneProject(t, "")
	if err := os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(issuesConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".rota", "workers.json"), []byte(`{"slots":[{"name":"ben","branch":"feat/x","task":"7","state":"busy"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := acceptFixture()
	fake.Issues[0].Body = "## Acceptance\n- [ ] AC-1: first\n- [ ] AC-2: second\n"
	deps := withTracker(t, fake)
	donePR(deps, fake, "Closes #7\nCloses #8")
	return dir, fake, deps
}

type doneForge struct {
	worker.Forge
	body string
	err  error
}

func (f doneForge) OpenPRs(context.Context) ([]tracker.PR, error) {
	return []tracker.PR{{Branch: "feat/x", Body: f.body}}, f.err
}

func TestWorkerDoneOptionalForge(t *testing.T) {
	for _, tc := range []struct {
		name string
		mark bool
		want int
	}{{"undeterminable", true, 0}, {"unreachable", true, 0}, {"unmarked", false, 4}} {
		t.Run(tc.name, func(t *testing.T) {
			dir, _, deps := acceptanceDoneProject(t)
			if err := os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(`{"issues":{"source":"github"}}`), 0o644); err != nil {
				t.Fatal(err)
			}
			if tc.mark {
				addProof(t, deps, dir, "PASS", "abc1234", "check")
				for _, ac := range []string{"AC-1", "AC-2"} {
					if code, _, stderr := issueRunWith(t, deps, dir, "plan", "pass", "#7", ac, "--proof", "abc1234:check"); code != 0 {
						t.Fatalf("mark: %d %s", code, stderr)
					}
				}
			}
			base := deps.WorkerEnv
			deps.WorkerEnv = func() worker.Env {
				e := base()
				// Use the real resolver against the fixture's local bare
				// origin; the unknown forge fails before any forge CLI runs.
				e.Forge = nil
				if tc.name == "unreachable" {
					e.Forge = func(string, string, tracker.Settings) (worker.Forge, error) {
						return doneForge{err: errors.New("forge offline")}, nil
					}
				}
				return e
			}
			code, out, stderr := rotaInWith(t, deps, dir, "worker", "done", "ben", "--outcome", "done", "--json")
			if code != tc.want {
				t.Fatalf("optional forge: exit %d, want %d: %s %s", code, tc.want, out, stderr)
			}
			if !tc.mark && !strings.Contains(out+stderr, "AC-1: first") {
				t.Fatalf("fallback must still check acceptance: %s %s", out, stderr)
			}
		})
	}
}

func donePR(deps *Deps, fake *trackertest.Fake, body string) {
	base := deps.WorkerEnv
	deps.WorkerEnv = func() worker.Env {
		e := base()
		e.Forge = func(string, string, tracker.Settings) (worker.Forge, error) { return doneForge{body: body}, nil }
		e.Issues = func(string, tracker.Settings) (worker.Issues, error) {
			return &a8Forge{MS: &trackertest.MS{Fake: fake}}, nil
		}
		return e
	}
}

func TestWorkerDoneSliceAcceptance(t *testing.T) {
	for _, tc := range []struct {
		body string
		want int
	}{{"Refs #7", 0}, {"Closes #7", 4}} {
		t.Run(tc.body, func(t *testing.T) {
			dir, fake, deps := acceptanceDoneProject(t)
			fake.Issues[0].Labels = append(fake.Issues[0].Labels, "partial-slice")
			donePR(deps, fake, tc.body)
			code, out, stderr := rotaInWith(t, deps, dir, "worker", "done", "ben", "--outcome", "done", "--json")
			if code != tc.want || (slotState(t, dir) == "done") != (tc.want == 0) {
				t.Fatalf("slice with parent criteria unmet: %d %s %s", code, out, stderr)
			}
		})
	}
}

func TestWorkerDoneBundleClosingAcceptance(t *testing.T) {
	dir, fake, deps := acceptanceDoneProject(t)
	if err := os.WriteFile(filepath.Join(dir, ".rota", "workers.json"), []byte(`{"slots":[{"name":"ben","branch":"feat/x","task":"7","bundle":["8"],"state":"busy"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	member := fake.Issues[0]
	member.Key = tracker.NumKey(8)
	member.Body = "## Acceptance\n- [ ] AC-3: bundled\n"
	fake.Issues = append(fake.Issues, member)
	donePR(deps, fake, "Refs #7\nCloses #8")
	code, out, stderr := rotaInWith(t, deps, dir, "worker", "done", "ben", "--outcome", "done", "--json")
	if code != 4 || strings.Contains(out+stderr, "[7] AC-1") || !strings.Contains(out+stderr, "[8] AC-3") {
		t.Fatalf("only the closing bundle member needs marks: %d %s %s", code, out, stderr)
	}
}

func TestWorkerDoneUnmetAcceptance(t *testing.T) {
	dir, _, deps := acceptanceDoneProject(t)
	code, out, stderr := rotaInWith(t, deps, dir, "worker", "done", "ben", "--outcome", "done", "--json")
	if code != 4 || data(t, out)["changed"] != false {
		t.Fatalf("unmet acceptance: exit %d: %s %s", code, out, stderr)
	}
	for _, want := range []string{"AC-1: first", "AC-2: second", "rota plan pass '#7' AC-1 --proof", "rota plan pass '#7' AC-2 --proof"} {
		if !strings.Contains(out+stderr, want) {
			t.Errorf("refusal lacks %q: %s %s", want, out, stderr)
		}
	}
	if slotState(t, dir) != "busy" {
		t.Fatal("refused slot was marked done")
	}
}

func TestWorkerDoneAcceptanceCoverage(t *testing.T) {
	for _, tc := range []struct {
		name, body, outcome string
		mark, edit, revoke  bool
		want                int
	}{
		{name: "plain done refuses", want: 4},
		{name: "proof-backed marks", mark: true},
		{name: "changed criterion", mark: true, edit: true, want: 4},
		{name: "revoked proof", mark: true, revoke: true, want: 4},
		{name: "legacy criteria", body: "## Acceptance\n- [ ] first\n"},
		{name: "mixed criteria", body: "## Acceptance\n- [ ] AC-1: first\n- [ ] legacy\n", mark: true},
		{name: "no criteria", body: "Nothing explicit.\n"},
		{name: "partial", outcome: "partial"},
		{name: "failed", outcome: "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, fake, deps := acceptanceDoneProject(t)
			fake.Issues[0].Body = "## Acceptance\n- [ ] AC-1: first\n"
			if tc.body != "" {
				fake.Issues[0].Body = tc.body
			}
			if tc.mark {
				addProof(t, deps, dir, "PASS", "abc1234", "check")
				if code, _, stderr := issueRunWith(t, deps, dir, "plan", "pass", "#7", "AC-1", "--proof", "abc1234:check"); code != 0 {
					t.Fatalf("mark: %d %s", code, stderr)
				}
				if tc.body != "" {
					// plan pass numbers legacy lines; restore the mixed body
					// to test that only its explicit ids block completion.
					fake.Issues[0].Body = tc.body
				}
			}
			if tc.edit {
				fake.Issues[0].Body = "## Acceptance\n- [ ] AC-1: edited\n"
			}
			if tc.revoke {
				addProof(t, deps, dir, "FAIL", "abc1234", "check")
			}
			args := []string{"worker", "done", "ben", "--json"}
			if tc.outcome != "" {
				args = append(args, "--outcome", tc.outcome, "--summary-file", bodyFile(t, "Did part\nFound a gap\nAC-1 remains: cannot deliver\n"))
			}
			code, out, stderr := rotaInWith(t, deps, dir, args...)
			if code != tc.want {
				t.Fatalf("exit %d, want %d: %s %s", code, tc.want, out, stderr)
			}
			if tc.outcome != "" {
				// A plain done preserves the unresolved outcome, rather than
				// turning it into a completed report requiring marks.
				if code, out, stderr := rotaInWith(t, deps, dir, "worker", "done", "ben", "--json"); code != 0 {
					t.Fatalf("standing %s: %d %s %s", tc.outcome, code, out, stderr)
				}
				if code, out, stderr := rotaInWith(t, deps, dir, "worker", "done", "ben", "--outcome", "done", "--json"); code != 4 || !strings.Contains(out+stderr, "AC-1") {
					t.Fatalf("clearing %s without marks: %d %s %s", tc.outcome, code, out, stderr)
				}
			}
		})
	}
}

func TestWorkerDoneFileAcceptance(t *testing.T) {
	dir := doneProject(t, "")
	deps := testDeps()
	base := deps.WorkerEnv
	deps.WorkerEnv = func() worker.Env {
		e := base()
		e.Forge = func(string, string, tracker.Settings) (worker.Forge, error) {
			t.Fatal("file-mode done must not look up a forge")
			return nil, nil
		}
		return e
	}
	if err := os.MkdirAll(filepath.Join(dir, ".rota", "bugs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".rota", "bugs", "B07.md"), []byte("## Acceptance\n- [ ] AC-1: first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, stderr := rotaInWith(t, deps, dir, "worker", "done", "ben", "--json")
	if code != 0 || slotState(t, dir) != "done" {
		t.Fatalf("file-mode criteria must not acquire the tracker-only refusal: %d %s %s", code, out, stderr)
	}
}

func TestWorkerDoneAcceptanceReadFailure(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint("legacy=", legacy), func(t *testing.T) {
			dir, fake, deps := acceptanceDoneProject(t)
			if legacy {
				fake.Issues[0].Body = "## Acceptance\n- [ ] first\n"
			}
			fake.Fail = map[string]error{"comments": errors.New("notes unavailable")}
			code, out, stderr := rotaInWith(t, deps, dir, "worker", "done", "ben", "--json")
			if legacy {
				if code != 0 {
					t.Fatalf("legacy reads need no notes: %d %s %s", code, out, stderr)
				}
			} else if code == 0 || !strings.Contains(out+stderr, "notes unavailable") || slotState(t, dir) != "busy" {
				t.Fatalf("acceptance read failed open: %d %s %s", code, out, stderr)
			}
		})
	}
}

func TestWorkerDoneBundleAcceptance(t *testing.T) {
	for _, closed := range []bool{false, true} {
		t.Run(fmt.Sprint("closed=", closed), func(t *testing.T) {
			dir, fake, deps := acceptanceDoneProject(t)
			if err := os.WriteFile(filepath.Join(dir, ".rota", "workers.json"), []byte(`{"slots":[{"name":"ben","branch":"feat/x","task":"7","bundle":["8"],"state":"busy"}]}`), 0o644); err != nil {
				t.Fatal(err)
			}
			member := fake.Issues[0]
			member.Key = tracker.NumKey(8)
			member.Body = "## Acceptance\n- [ ] AC-3: bundled\n"
			if closed {
				member.State = "closed"
			}
			fake.Issues = append(fake.Issues, member)
			code, out, stderr := rotaInWith(t, deps, dir, "worker", "done", "ben", "--outcome", "done", "--json")
			if code != 4 || !strings.Contains(out+stderr, "[7] AC-1") || strings.Contains(out+stderr, "[8] AC-3") == closed {
				t.Fatalf("bundle refusal: %d %s %s", code, out, stderr)
			}
			addProof(t, deps, dir, "PASS", "abc1234", "check")
			for _, ac := range []string{"AC-1", "AC-2"} {
				if code, _, stderr := issueRunWith(t, deps, dir, "plan", "pass", "#7", ac, "--proof", "abc1234:check"); code != 0 {
					t.Fatalf("mark: %d %s", code, stderr)
				}
			}
			code, out, stderr = rotaInWith(t, deps, dir, "worker", "done", "ben", "--json")
			if closed && code != 0 || !closed && (code != 4 || !strings.Contains(out+stderr, "rota plan pass '#8' AC-3")) {
				t.Fatalf("member acceptance: %d %s %s", code, out, stderr)
			}
		})
	}
}

// TestWorkerDoneBaselinesReviewCursor: the contract runs `rota worker done`
// before the PR is opened, so the poll never sees the edge; the verb itself
// must start the review cursor or every earlier comment counts as review input.
func TestWorkerDoneBaselinesReviewCursor(t *testing.T) {
	dir := doneProject(t, "")
	if code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--json"); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".rota", "workers.json"))
	if !strings.Contains(string(b), "reviewSeen") {
		t.Fatalf("review cursor not baselined: %s", b)
	}
}

// pushWorktreeBranch gives the repo a bare origin and pushes the slot branch to
// it: done refuses a branch whose PR was never pushed (#1529).
func pushWorktreeBranch(t *testing.T, main, wt, branch string) {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "r.git")
	gittest.Run(t, main, "init", "-q", "--bare", remote)
	gittest.Run(t, main, "remote", "add", "origin", remote)
	gittest.Run(t, wt, "push", "-q", "origin", branch)
}

// TestWorkerDoneFromWorktree: the registry lives in the main checkout, and
// {files} is the slot branch's diff, from whichever directory the verb runs.
func TestWorkerDoneFromWorktree(t *testing.T) {
	main := backlogProject(t)
	os.WriteFile(filepath.Join(main, ".rota", "config.json"), []byte(`{"test":{"fast":["true {files}"]}}`), 0o644)
	// Proof detail files are untracked noise; ignore them so {files} is stable.
	os.WriteFile(filepath.Join(main, ".gitignore"), []byte(".rota/*\n!.rota/config.json\n!.rota/BACKLOG.md\n"), 0o644)
	gittest.Run(t, main, "add", ".gitignore", ".rota/config.json", ".rota/BACKLOG.md")
	gittest.Run(t, main, "commit", "-q", "-m", "rota")
	gittest.Run(t, main, "branch", "base0")
	wt := filepath.Join(main, ".worktrees", "ben")
	gittest.Run(t, main, "worktree", "add", "-q", "-b", "ben/x", wt, "base0")
	os.WriteFile(filepath.Join(wt, "a.go"), []byte("package a\n"), 0o644)
	gittest.Run(t, wt, "add", "a.go")
	gittest.Run(t, wt, "commit", "-q", "-m", "work")
	pushWorktreeBranch(t, main, wt, "ben/x")
	os.WriteFile(filepath.Join(main, ".rota", "workers.json"),
		[]byte(`{"slots":[{"name":"ben","branch":"ben/x","task":"B07","state":"busy","worktree":"`+wt+`"}]}`), 0o644)

	if code, out, errOut := rotaIn(t, wt, "proof", "record", "B07", "--base", "base0", "--", "true {files}"); code != 0 {
		t.Fatalf("record: %d %s %s", code, out, errOut)
	}
	// The same row, as the main checkout's proof store sees it.
	sha := headOf(t, wt)
	if code, out, _ := rotaIn(t, main, "proof", "add", "B07", "--check", "true 'a.go'", "--result", "PASS", "--evidence", "x", "--sha", sha); code != 0 {
		t.Fatalf("add: %d %s", code, out)
	}

	t.Run("from the slot worktree", func(t *testing.T) {
		code, out, errOut := rotaIn(t, wt, "worker", "done", "ben", "--base", "base0", "--json")
		if code != 0 || data(t, out)["changed"] != true {
			t.Fatalf("exit %d: %s %s", code, out, errOut)
		}
	})
	// Reset the slot, then dirty the main checkout with an unrelated file.
	os.WriteFile(filepath.Join(main, ".rota", "workers.json"),
		[]byte(`{"slots":[{"name":"ben","branch":"ben/x","task":"B07","state":"busy","worktree":"`+wt+`"}]}`), 0o644)
	os.WriteFile(filepath.Join(main, "unrelated.txt"), []byte("x"), 0o644)
	t.Run("from the main checkout with a dirty file", func(t *testing.T) {
		code, out, errOut := rotaIn(t, main, "worker", "done", "ben", "--base", "base0", "--json")
		if code != 0 || data(t, out)["changed"] != true {
			t.Fatalf("exit %d: %s %s", code, out, errOut)
		}
	})
}

// worker done run from a worktree whose tracked config still has the old
// work.accounts reads round.accounts from the main checkout: the ledger row
// carries the headroom of the slot's account (#1190).
func TestWorkerDoneLedgerAccountsFromMainCheckout(t *testing.T) {
	main := workerProject(t, `{"round":{"accounts":[{"name":"work","configDir":"/w"}]}}`)
	wt := filepath.Join(main, ".worktrees", "ben")
	os.WriteFile(filepath.Join(main, ".gitignore"), []byte(".rota/\n"), 0o644)
	gittest.Run(t, main, "add", ".gitignore")
	gittest.Run(t, main, "commit", "-q", "-m", "ignore rota")
	gittest.Run(t, main, "worktree", "add", "-q", "-b", "ben/x", wt)
	pushWorktreeBranch(t, main, wt, "ben/x")
	os.MkdirAll(filepath.Join(wt, ".rota"), 0o755)
	os.WriteFile(filepath.Join(wt, ".rota", "config.json"), []byte(`{"work":{"accounts":[{"name":"work","configDir":"/w"}]}}`), 0o644)
	os.WriteFile(filepath.Join(main, ".rota", "workers.json"),
		[]byte(`{"slots":[{"name":"ben","branch":"ben/x","task":"B07","state":"busy","kind":"claude","account":"work","worktree":"`+wt+`"}]}`), 0o644)

	d := testDeps()
	d.WorkerAccounts = func() *worker.Accounts {
		return &worker.Accounts{Env: worker.Env{Config: d.Config}, Fetch: func(_ context.Context, _, _ string) (*jsonx.Object, string) {
			v, _ := jsonx.Decode([]byte(`{"five_hour":{"utilization":40,"resets_at":null},"seven_day":{"utilization":10,"resets_at":null}}`))
			return v.(*jsonx.Object), ""
		}}
	}
	if code, out, errOut := rotaInWith(t, d, wt, "worker", "done", "ben", "--base", "main", "--json"); code != 0 {
		t.Fatalf("exit %d: %s %s", code, out, errOut)
	}
	es, err := ledger.Load(main)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		if e.Kind == ledger.KindDone && e.Slot == "ben" {
			if e.Account != "work" || e.Detail == nil || e.Detail.Len() == 0 {
				t.Fatalf("done row = %+v, want account work with a headroom reading", e)
			}
			return
		}
	}
	t.Fatalf("no done row in the main checkout's ledger: %+v", es)
}

// Record through the CLI, then prove through worker.Done without supplying any
// knowledge of command spelling. Also exercise the CLI's done wiring.
func TestProofRecordFeedsWorkerDone(t *testing.T) {
	stub := t.TempDir()
	write(t, filepath.Join(stub, "rota"), "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(filepath.Join(stub, "rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stub+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, tc := range []struct {
		name  string
		args  []string
		fail  bool
		stale bool
	}{
		{name: "argv tier", args: []string{"rota", "test", "run", "fast"}},
		{name: "shell tier", args: []string{"rota test run fast"}},
		{name: "withdrawn tier", args: []string{"rota", "test", "run", "fast"}, fail: true},
		{name: "stale tier", args: []string{"rota test run fast"}, stale: true},
		{name: "individual command", args: []string{"true {files}"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := doneProject(t, "true {files}")
			gittest.Run(t, dir, "branch", "proof-base")
			write(t, filepath.Join(dir, "it's here.txt"), "changed")
			gittest.Run(t, dir, "add", "it's here.txt")
			gittest.Run(t, dir, "commit", "-q", "-m", "change")
			gittest.Run(t, dir, "push", "-q", "origin", "feat/x")
			args := append([]string{"proof", "record", "B07", "--base", "proof-base", "--json", "--"}, tc.args...)
			code, out, stderr := rotaIn(t, dir, args...)
			if code != 0 {
				t.Fatalf("record: %d %s %s", code, out, stderr)
			}
			check := data(t, out)["check"].(string)
			if tc.fail {
				if code, out, stderr := rotaIn(t, dir, "proof", "add", "B07", "--check", check, "--result", "FAIL", "--evidence", "withdrawn"); code != 0 {
					t.Fatalf("withdraw: %d %s %s", code, out, stderr)
				}
			}
			if tc.stale {
				gittest.Run(t, dir, "commit", "-q", "--allow-empty", "-m", "new head")
				gittest.Run(t, dir, "push", "-q", "origin", "feat/x")
			}
			deps := testDeps()
			deps.Getwd = func() (string, error) { return dir, nil }
			c := &Ctx{Deps: deps}
			// Only IO belongs to the caller: no list of accepted tier spellings.
			res, err := worker.Done(worker.DoneRequest{Root: dir, PoolRoot: dir, Slot: "ben", Base: "proof-base"}, worker.DoneDeps{
				Env:          deps.workerEnv(),
				ChangedFiles: func(dir, base string) ([]string, error) { return changedFiles(c, dir, base) },
				ExpandFiles: func(dir, base string, lines []string) ([]string, error) {
					return expandFiles(c, dir, base, proof.ShellLines(lines)...)
				},
				ProofRows: func(id string) ([]proof.Row, error) {
					rows, _, err := proof.Show(proof.Files(dir), id)
					return rows, err
				},
			})
			if tc.fail || tc.stale {
				if err == nil || res == nil || len(res.Missing) != 1 || slotState(t, dir) != "busy" {
					t.Fatalf("unproven row accepted: res=%+v err=%v", res, err)
				}
				return
			}
			if err != nil || res == nil || !res.Changed || len(res.Missing) != 0 || slotState(t, dir) != "done" {
				t.Fatalf("recorded proof must satisfy done without caller spelling: res=%+v err=%v", res, err)
			}
			if code, out, stderr := rotaIn(t, dir, "worker", "done", "ben", "--base", "proof-base", "--json"); code != 0 || data(t, out)["changed"] != false {
				t.Fatalf("CLI done: %d %s %s", code, out, stderr)
			}
		})
	}
}
