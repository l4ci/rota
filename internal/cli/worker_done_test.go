package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/proof"
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
	runGit(t, dir, "add", ".gitignore")
	runGit(t, dir, "commit", "-q", "-m", "ignore rota")
	// done also wants the branch pushed: the PR is opened before it.
	remote := filepath.Join(t.TempDir(), "r.git")
	runGit(t, dir, "init", "-q", "--bare", remote)
	runGit(t, dir, "remote", "add", "origin", remote)
	runGit(t, dir, "push", "-q", "origin", "feat/x")
	return dir
}

func headOf(t *testing.T, dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
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
	t.Run("a FAIL row does not count", func(t *testing.T) {
		dir := doneProject(t, "true")
		rotaIn(t, dir, "proof", "add", "B07", "--check", "true", "--result", "FAIL", "--evidence", "x", "--sha", headOf(t, dir))
		if code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--json"); code != 4 {
			t.Fatalf("exit %d: %s", code, out)
		}
	})
	t.Run("a PASS row at an older sha does not count", func(t *testing.T) {
		dir := doneProject(t, "true")
		if code, out, _ := rotaIn(t, dir, "proof", "record", "B07", "--", "true"); code != 0 {
			t.Fatalf("record: %d %s", code, out)
		}
		c := exec.Command("git", "-c", "user.email=a@b", "-c", "user.name=n", "commit", "-q", "--allow-empty", "-m", "more")
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
		if code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--json"); code != 4 {
			t.Fatalf("stale sha: exit %d: %s", code, out)
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
	t.Run("accepts the whole-tier row", func(t *testing.T) {
		dir := doneProject(t, "true")
		rotaIn(t, dir, "proof", "add", "B07", "--check", "rota test run fast", "--result", "PASS", "--evidence", "x", "--sha", headOf(t, dir))
		if code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--json"); code != 0 {
			t.Fatalf("exit %d: %s", code, out)
		}
	})
	t.Run("accepts the whole-tier row recorded multi-arg", func(t *testing.T) {
		dir := doneProject(t, "true")
		rotaIn(t, dir, "proof", "add", "B07", "--check", "'rota' 'test' 'run' 'fast'", "--result", "PASS", "--evidence", "x", "--sha", headOf(t, dir))
		if code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--json"); code != 0 {
			t.Fatalf("exit %d: %s", code, out)
		}
	})
	t.Run("unset test.fast skips the proof half with a warning", func(t *testing.T) {
		dir := doneProject(t, "")
		code, out, errOut := rotaIn(t, dir, "worker", "done", "ben", "--json")
		if code != 0 || data(t, out)["proofSkipped"] != true || !strings.Contains(errOut+out, "test.fast is unset") {
			t.Fatalf("exit %d: %s %s", code, out, errOut)
		}
		if slotState(t, dir) != "done" {
			t.Error("slot not marked done")
		}
	})
	t.Run("a tracked detail file holding the proof row is not dirty", func(t *testing.T) {
		dir := doneProject(t, "true")
		// File backend: the detail file is tracked, so the proof row modifies it.
		os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".rota/workers.json\n.rota/**/*.lock\n"), 0o644)
		detail := filepath.Join(dir, ".rota", "bugs", "B07.md")
		os.MkdirAll(filepath.Dir(detail), 0o755)
		os.WriteFile(detail, []byte("# B07: Parser\n"), 0o644)
		runGit(t, dir, "add", ".gitignore", ".rota/bugs/B07.md", ".rota/BACKLOG.md", ".rota/config.json")
		runGit(t, dir, "commit", "-q", "-m", "track detail")
		runGit(t, dir, "push", "-q", "origin", "feat/x")
		if code, out, _ := rotaIn(t, dir, "proof", "record", "B07", "--", "true"); code != 0 {
			t.Fatalf("record: %d %s", code, out)
		}
		if out, _ := exec.Command("git", "-C", dir, "status", "--porcelain", "--", ".rota/bugs/B07.md").Output(); !strings.Contains(string(out), "B07.md") {
			t.Fatalf("fixture: proof row did not dirty the detail file: %q", out)
		}
		code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--json")
		if code != 0 || data(t, out)["changed"] != true {
			st, _ := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
			t.Fatalf("exit %d: %s\nstatus: %s", code, out, st)
		}
	})
	t.Run("another dirty file still refuses", func(t *testing.T) {
		dir := doneProject(t, "true")
		if code, out, _ := rotaIn(t, dir, "proof", "record", "B07", "--", "true"); code != 0 {
			t.Fatalf("record: %d %s", code, out)
		}
		os.WriteFile(filepath.Join(dir, "stray.txt"), []byte("x"), 0o644)
		if code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--json"); code != 4 {
			t.Fatalf("exit %d: %s", code, out)
		}
	})
	t.Run("unknown slot is 3", func(t *testing.T) {
		dir := doneProject(t, "true")
		if code, _, _ := rotaIn(t, dir, "worker", "done", "nope"); code != 3 {
			t.Errorf("exit %d, want 3", code)
		}
	})
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
	runGit(t, main, "init", "-q", "--bare", remote)
	runGit(t, main, "remote", "add", "origin", remote)
	runGit(t, wt, "push", "-q", "origin", branch)
}

// TestWorkerDoneFromWorktree: the registry lives in the main checkout, and
// {files} is the slot branch's diff, from whichever directory the verb runs.
func TestWorkerDoneFromWorktree(t *testing.T) {
	main := backlogProject(t)
	os.WriteFile(filepath.Join(main, ".rota", "config.json"), []byte(`{"test":{"fast":["true {files}"]}}`), 0o644)
	// Proof detail files are untracked noise; ignore them so {files} is stable.
	os.WriteFile(filepath.Join(main, ".gitignore"), []byte(".rota/*\n!.rota/config.json\n!.rota/BACKLOG.md\n"), 0o644)
	gitT(t, main, "add", ".gitignore", ".rota/config.json", ".rota/BACKLOG.md")
	gitT(t, main, "-c", "user.email=a@b", "-c", "user.name=n", "commit", "-q", "-m", "rota")
	gitT(t, main, "branch", "base0")
	wt := filepath.Join(main, ".worktrees", "ben")
	gitT(t, main, "worktree", "add", "-q", "-b", "ben/x", wt, "base0")
	os.WriteFile(filepath.Join(wt, "a.go"), []byte("package a\n"), 0o644)
	gitT(t, wt, "add", "a.go")
	gitT(t, wt, "-c", "user.email=a@b", "-c", "user.name=n", "commit", "-q", "-m", "work")
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

// A worker whose slot was reclaimed or reassigned still holds its old attempt
// id: its done is refused (exit 4) before anything is read or written.
func TestWorkerDoneFencesAStaleAttempt(t *testing.T) {
	dir := doneProject(t, "")
	os.WriteFile(filepath.Join(dir, ".rota", "workers.json"),
		[]byte(`{"slots":[{"name":"ben","branch":"feat/x","task":"B07","state":"busy","attempt":"bbbb2222"}]}`), 0o644)
	code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--attempt", "aaaa1111", "--json")
	if code != 4 || !strings.Contains(out, "attempt") || strings.Contains(out, "bbbb2222") {
		t.Fatalf("stale attempt: exit %d: %s", code, out)
	}
	if slotState(t, dir) == "done" {
		t.Error("a fenced done must not mark the slot done")
	}
	if code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--attempt", "bbbb2222", "--json"); code != 0 {
		t.Fatalf("current attempt: exit %d: %s", code, out)
	}
	if slotState(t, dir) != "done" {
		t.Error("the current attempt marks the slot done")
	}
}

// A slot reclaimed and not yet reassigned holds no attempt at all.
func TestWorkerDoneFencesAReclaimedSlot(t *testing.T) {
	dir := doneProject(t, "")
	os.WriteFile(filepath.Join(dir, ".rota", "workers.json"),
		[]byte(`{"slots":[{"name":"ben","branch":"park/ben","state":"idle"}]}`), 0o644)
	if code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--attempt", "aaaa1111", "--json"); code != 4 {
		t.Fatalf("exit %d: %s", code, out)
	}
}

// worker done run from a worktree whose tracked config still has the old
// work.accounts reads round.accounts from the main checkout: the ledger row
// carries the headroom of the slot's account (#1190).
func TestWorkerDoneLedgerAccountsFromMainCheckout(t *testing.T) {
	main := workerProject(t, `{"round":{"accounts":[{"name":"work","configDir":"/w"}]}}`)
	wt := filepath.Join(main, ".worktrees", "ben")
	os.WriteFile(filepath.Join(main, ".gitignore"), []byte(".rota/\n"), 0o644)
	runGit(t, main, "add", ".gitignore")
	runGit(t, main, "commit", "-q", "-m", "ignore rota")
	gitT(t, main, "worktree", "add", "-q", "-b", "ben/x", wt)
	pushWorktreeBranch(t, main, wt, "ben/x")
	os.MkdirAll(filepath.Join(wt, ".rota"), 0o755)
	os.WriteFile(filepath.Join(wt, ".rota", "config.json"), []byte(`{"work":{"accounts":[{"name":"work","configDir":"/w"}]}}`), 0o644)
	os.WriteFile(filepath.Join(main, ".rota", "workers.json"),
		[]byte(`{"slots":[{"name":"ben","branch":"ben/x","task":"B07","state":"busy","kind":"claude","account":"work","worktree":"`+wt+`"}]}`), 0o644)

	d := testDeps()
	d.WorkerAccounts = func() *worker.Accounts {
		return &worker.Accounts{Fetch: func(_ context.Context, _, _ string) (*jsonx.Object, string) {
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

func TestMissingFastProofLatestRowWins(t *testing.T) {
	const head = "abc1234def5678"
	pass := proof.Row{Check: fastTierCheck, Result: "PASS", Sha: "abc1234"}
	fail := proof.Row{Check: fastTierCheck, Result: "FAIL", Sha: "abc1234"}
	if got := missingFastProof([]proof.Row{pass}, head, []string{"go test ./..."}); got != nil {
		t.Errorf("PASS at head: missing = %v", got)
	}
	if got := missingFastProof([]proof.Row{pass, fail}, head, []string{"go test ./..."}); len(got) != 1 {
		t.Errorf("PASS then FAIL at head must be unproven, missing = %v", got)
	}
}

// commitFile commits one file on the checked-out branch.
func commitFile(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", name}, {"-c", "user.email=a@b", "-c", "user.name=n", "commit", "-q", "-m", "add " + name}} {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	runGit(t, dir, "push", "-q", "origin", "feat/x")
}

// TestIsProdGo pins the missing-RED file filter: testdata fixtures are not
// production code, a cmd/ Go file is.
func TestIsProdGo(t *testing.T) {
	for f, want := range map[string]bool{
		"cmd/rota/main.go":                  true,
		"internal/cli/worker_done.go":       true,
		"internal/cli/testdata/fixture.go":  false,
		"testdata/sample.go":                false,
		"internal/cli/worker_done_test.go":  false,
		"internal/cli/testdata/golden.json": false,
		"cmd/rota/README.md":                false,
	} {
		if got := isProdGo(f); got != want {
			t.Errorf("isProdGo(%q) = %v, want %v", f, got, want)
		}
	}
}

// TestWorkerDoneFlagsMissingRed: with work.tdd on, a branch that changes
// non-test Go code and holds no FAIL proof row warns, and --require-red refuses (#1300).
func TestWorkerDoneFlagsMissingRed(t *testing.T) {
	base := func(t *testing.T) (dir, baseSha string) {
		dir = doneProject(t, "")
		baseSha = headOf(t, dir)
		commitFile(t, dir, "prod.go")
		return dir, baseSha
	}
	t.Run("warns without a FAIL row", func(t *testing.T) {
		dir, b := base(t)
		code, out, errOut := rotaIn(t, dir, "worker", "done", "ben", "--base", b, "--json")
		d := data(t, out)
		if code != 0 || d["noRed"] != true || !strings.Contains(errOut+out, "no failing (RED) proof row") {
			t.Fatalf("exit %d: %s %s", code, out, errOut)
		}
	})
	t.Run("--require-red refuses", func(t *testing.T) {
		dir, b := base(t)
		code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--base", b, "--require-red", "--json")
		if code != 4 || slotState(t, dir) == "done" {
			t.Fatalf("exit %d: %s", code, out)
		}
	})
	t.Run("unresolvable base warns", func(t *testing.T) {
		dir, _ := base(t)
		code, out, errOut := rotaIn(t, dir, "worker", "done", "ben", "--base", "no-such-ref", "--json")
		if code != 0 || data(t, out)["noRedUnchecked"] != true || !strings.Contains(errOut, "cannot check for a RED proof row") {
			t.Fatalf("exit %d: %s %s", code, out, errOut)
		}
	})
	t.Run("unresolvable base refuses under --require-red", func(t *testing.T) {
		dir, _ := base(t)
		code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--base", "no-such-ref", "--require-red", "--json")
		if code != 4 || slotState(t, dir) == "done" {
			t.Fatalf("exit %d: %s", code, out)
		}
	})
	t.Run("a FAIL row satisfies it", func(t *testing.T) {
		dir, b := base(t)
		rotaIn(t, dir, "proof", "add", "B07", "--check", "go test ./x", "--result", "FAIL", "--evidence", "x", "--sha", headOf(t, dir))
		code, out, errOut := rotaIn(t, dir, "worker", "done", "ben", "--base", b, "--require-red", "--json")
		if code != 0 || data(t, out)["noRed"] == true || strings.Contains(errOut, "RED") {
			t.Fatalf("exit %d: %s %s", code, out, errOut)
		}
	})
	t.Run("test-only change is not flagged", func(t *testing.T) {
		dir := doneProject(t, "")
		b := headOf(t, dir)
		commitFile(t, dir, "prod_test.go")
		code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--base", b, "--require-red", "--json")
		if code != 0 || data(t, out)["noRed"] == true {
			t.Fatalf("exit %d: %s", code, out)
		}
	})
	t.Run("work.tdd off is not flagged", func(t *testing.T) {
		dir, b := base(t)
		os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(`{"work":{"tdd":false}}`), 0o644)
		code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--base", b, "--require-red", "--json")
		if code != 0 || data(t, out)["noRed"] == true {
			t.Fatalf("exit %d: %s", code, out)
		}
	})
}
