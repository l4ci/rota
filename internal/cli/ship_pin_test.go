package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/verdict"
)

// Move the ref after rev-parse has returned its commit, before either gate runs.
// All other git operations use the real temporary repository.
func shipMoveAfterPin(t *testing.T, deps *Deps, work, branch, next string) *bool {
	t.Helper()
	realGit := deps.Git
	moved := false
	reads := 0
	deps.Git = func(ctx context.Context, dir string, args ...string) (git.Result, error) {
		res, err := realGit(ctx, dir, args...)
		if !moved && err == nil && res.ExitCode == 0 && strings.Join(args, " ") == "rev-parse --verify -q "+branch+"^{commit}" {
			// The CLI first checks existence; MergeBranch's read owns the pin.
			reads++
			if reads == 2 {
				gittest.Run(t, work, "update-ref", "refs/heads/"+branch, next)
				moved = true
			}
		}
		return res, err
	}
	return &moved
}

func TestShipMergeReportsUnmergedTipAfterPin(t *testing.T) {
	shipDeterministic(t)
	for _, jsonMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "text", true: "json"}[jsonMode], func(t *testing.T) {
			work := shipFixture(t, "")
			shipBranchOf(t, work, "feat/x", [3]string{"x.txt", "pinned feature", ""})
			pin := gittest.Run(t, work, "rev-parse", "feat/x")
			base := gittest.Run(t, work, "rev-parse", "main")
			gittest.Run(t, work, "checkout", "-q", "feat/x")
			shipCommit(t, work, "later.txt", "later feature", "")
			tip := gittest.Run(t, work, "rev-parse", "HEAD")
			gittest.Run(t, work, "checkout", "-q", "main")
			gittest.Run(t, work, "update-ref", "refs/heads/feat/x", pin)
			deps := testDeps()
			moved := shipMoveAfterPin(t, deps, work, "feat/x", tip)
			args := []string{"ship", "merge", "feat/x", "--body-file", "-"}
			if jsonMode {
				args = append(args, "--json")
			}
			out := trRunWith(t, deps, work, "merge: pinned feature", args...)
			if !*moved || out.code != ExitRefused {
				t.Errorf("moved branch must report retained cleanup with exit 4: moved=%v output=%+v", *moved, out)
			}
			for _, want := range []string{"pinned commit " + pin + " landed", "feat/x", tip, "not gated or merged", "branch kept", "own review and merge"} {
				if !strings.Contains(out.stderr, want) {
					t.Errorf("report lacks %q: %s", want, out.stderr)
				}
			}
			if parents := gittest.Run(t, work, "log", "-1", "--format=%P", "main"); parents != base+" "+pin {
				t.Errorf("landed parents = %s, want base and pinned commit", parents)
			}
			if got := gittest.Run(t, work, "rev-parse", "feat/x"); got != tip {
				t.Errorf("retained branch tip = %s, want %s", got, tip)
			}
			if _, err := os.Stat(filepath.Join(work, "later.txt")); !os.IsNotExist(err) {
				t.Errorf("later commit landed: stat later.txt = %v", err)
			}
			merge := gittest.Run(t, work, "rev-parse", "--short", "main")
			if !strings.Contains(out.stderr, "landed on main as merge "+merge) {
				t.Errorf("report must name the base and landed merge: %s", out.stderr)
			}
			if !jsonMode && out.stdout != merge+"\n" {
				t.Errorf("text stdout = %q, want merge SHA as the last line", out.stdout)
			}
			if jsonMode {
				d := data(t, out.stdout)
				if d["changed"] != true || d["branch"] != "feat/x" || d["base"] != "main" || d["sha"] != merge || d["pinned"] != pin || d["tip"] != tip || d["branchKept"] != true || d["blockedBy"] != "branch moved" {
					t.Errorf("partial-success data = %v", d)
				}
			}
		})
	}
}

func TestShipMergePostMergeFailuresReportLanding(t *testing.T) {
	shipDeterministic(t)
	for _, tc := range []struct {
		name, command string
		runnerError   bool
		unreadableSHA bool
	}{
		{name: "tip exit", command: "rev-parse --verify -q feat/x^{commit}"},
		{name: "tip runner", command: "rev-parse --verify -q feat/x^{commit}", runnerError: true},
		{name: "delete exit", command: "branch -d feat/x"},
		{name: "delete runner", command: "branch -d feat/x", runnerError: true},
		{name: "hash exit", command: "log -1 --format=%h"},
		{name: "hash runner", command: "log -1 --format=%h", runnerError: true},
		{name: "hash unreadable", command: "log -1 --format=%h", unreadableSHA: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			work := shipFixture(t, "")
			shipBranchOf(t, work, "feat/x", [3]string{"x.txt", "pinned feature", ""})
			pin := gittest.Run(t, work, "rev-parse", "feat/x")
			base := gittest.Run(t, work, "rev-parse", "main")
			deps := testDeps()
			realGit := deps.Git
			landed := false
			deps.Git = func(ctx context.Context, dir string, args ...string) (git.Result, error) {
				command := strings.Join(args, " ")
				if landed && (command == tc.command || (tc.unreadableSHA && command == "rev-parse --short main")) {
					if tc.runnerError {
						return git.Result{}, errors.New("injected git runner failure")
					}
					return git.Result{ExitCode: 128, Stderr: "injected git failure"}, nil
				}
				res, err := realGit(ctx, dir, args...)
				if strings.HasPrefix(command, "merge --no-ff ") && err == nil && res.ExitCode == 0 {
					landed = true
				}
				return res, err
			}
			out := trRunWith(t, deps, work, "merge: pinned feature", "ship", "merge", "feat/x", "--body-file", "-")
			if !landed || out.code != ExitUnavailable {
				t.Errorf("post-merge failure must exit 5 after landing: landed=%v output=%+v", landed, out)
			}
			merge := gittest.Run(t, work, "rev-parse", "--short", "main")
			for _, want := range []string{"pinned commit " + pin + " landed on main", "injected git", "Do not retry the merge"} {
				if !strings.Contains(out.stderr, want) {
					t.Errorf("post-merge failure lacks %q: %s", want, out.stderr)
				}
			}
			if tc.unreadableSHA {
				if !strings.Contains(out.stderr, "merge SHA could not be read") {
					t.Errorf("unreadable merge must be explicit: %s", out.stderr)
				}
			} else if !strings.Contains(out.stderr, "as merge "+merge) {
				t.Errorf("post-merge failure must name merge %s: %s", merge, out.stderr)
			}
			if parents := gittest.Run(t, work, "log", "-1", "--format=%P", "main"); parents != base+" "+pin {
				t.Errorf("landed parents = %s, want base and pinned commit", parents)
			}
			if got := gittest.Run(t, work, "rev-parse", "feat/x"); got != pin {
				t.Errorf("post-merge failure must keep branch: %s, want %s", got, pin)
			}
		})
	}
}

func TestShipMergeApprovesAndLandsPinAfterRefMoves(t *testing.T) {
	shipDeterministic(t)
	for _, confirmed := range []bool{false, true} {
		t.Run(map[bool]string{false: "refused", true: "approved"}[confirmed], func(t *testing.T) {
			work := shipFixture(t, `{"forge":{"provider":"github"},"issues":{"source":"file"},"ship":{"mergeApproval":"paths","mergeApprovalPaths":["protected.txt"]}}`)
			shipBranchOf(t, work, "feat/x", [3]string{"protected.txt", "protected feature", ""})
			pin := gittest.Run(t, work, "rev-parse", "feat/x")
			base := gittest.Run(t, work, "rev-parse", "main")
			deps := testDeps()
			moved := shipMoveAfterPin(t, deps, work, "feat/x", base)
			args := []string{"ship", "merge", "feat/x", "--body-file", "-", "--json"}
			if confirmed {
				args = append(args, "--confirm", "--confirm-note", "approved protected feature")
			}
			out := trRunWith(t, deps, work, "merge: protected feature", args...)
			d := data(t, out.stdout)
			if !*moved {
				t.Fatal("fixture did not move the branch after pin resolution")
			}
			if !confirmed {
				if out.code != ExitRefused || d["blockedBy"] != "manual gate" || d["gate"] != "merge-approval" || d["changed"] != false || !reflect.DeepEqual(d["paths"], []any{"protected.txt"}) {
					t.Errorf("protected path at landing pin must require approval: output=%+v data=%v", out, d)
				}
				if got := gittest.Run(t, work, "rev-parse", "main"); got != base {
					t.Errorf("refused merge changed base: %s", got)
				}
				if gittest.Run(t, work, "branch", "--list", "feat/x") == "" {
					t.Error("refused merge deleted branch")
				}
				return
			}
			if out.code != ExitRefused || d["changed"] != true || d["pinned"] != pin || d["tip"] != base || d["branchKept"] != true {
				t.Fatalf("approved pin must land and keep the moved branch: %+v", out)
			}
			if got := gittest.Run(t, work, "rev-parse", "feat/x"); got != base {
				t.Errorf("retained branch tip = %s, want %s", got, base)
			}
			if parents := gittest.Run(t, work, "log", "-1", "--format=%P", "main"); parents != base+" "+pin {
				t.Errorf("landed parents = %s, want base and pinned commit", parents)
			}
			if content, err := os.ReadFile(filepath.Join(work, "protected.txt")); err != nil || string(content) != "protected feature\n" {
				t.Errorf("landed protected path = %q, %v", content, err)
			}
			if audit := gateAudit(t, work); len(audit) != 1 || audit[0]["target"] != "feat/x" {
				t.Errorf("approval audit = %+v", audit)
			}
		})
	}
}

func TestShipMergeRecoversFailAtPinAfterRefMoves(t *testing.T) {
	shipDeterministic(t)
	work := shipFixture(t, `{"forge":{"provider":"github"},"issues":{"source":"github"}}`)
	shipBranchOf(t, work, "feat/x", [3]string{"x.txt", "feature", ""})
	pin := gittest.Run(t, work, "rev-parse", "feat/x")
	base := gittest.Run(t, work, "rev-parse", "main")
	report := verdict.RenderReport(verdict.Record{Kind: verdict.ReviewSpec, Verdict: verdict.Fail, Sha: pin, Summary: "remote FAIL"}, nil)
	fake := &forge{answer: func(_ string, args []string) (string, string, int) {
		call := strings.Join(args, " ")
		switch {
		case strings.HasPrefix(call, "pr list "):
			return `[{"number":20,"headRefName":"feat/x","isCrossRepository":false}]`, "", 0
		case strings.Contains(call, "issues/20/comments"):
			raw, _ := json.Marshal([]any{map[string]any{"id": 1, "body": report, "user": map[string]string{"login": "reviewer"}}})
			return string(raw), "", 0
		case strings.HasPrefix(call, "api user"):
			return `{"login":"reviewer"}`, "", 0
		}
		return "[]", "", 0
	}}
	deps := useForge(t, fake)
	moved := shipMoveAfterPin(t, deps, work, "feat/x", base)
	out := trRunWith(t, deps, work, "merge: feature", "ship", "merge", "feat/x", "--body-file", "-", "--json")
	d := data(t, out.stdout)
	if !*moved || out.code != ExitRefused || d["blockedBy"] != "verdict" || d["sha"] != pin || d["stale"] != false {
		t.Errorf("FAIL at landing pin must refuse even after ref moves: moved=%v output=%+v data=%v", *moved, out, d)
	}
	if got := gittest.Run(t, work, "rev-parse", "main"); got != base {
		t.Errorf("refused merge changed base: %s", got)
	}
	if gittest.Run(t, work, "branch", "--list", "feat/x") == "" {
		t.Error("refused merge deleted branch")
	}
	if records := verdict.Load(work).Branches["feat/x"]; len(records) != 1 || !records[0].FromReport || records[0].Sha != pin {
		t.Errorf("branch-keyed recovered verdict = %+v", records)
	}
}
