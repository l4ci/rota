package cli

import (
	"context"
	"encoding/json"
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
			if out.code != 0 {
				t.Fatalf("approved merge refused: %+v", out)
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
