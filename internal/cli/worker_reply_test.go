package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

func TestWorkerReplyPostsWithMarker(t *testing.T) {
	dir := workerProject(t, ghCfg)
	doneSlot(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "reply.md"), []byte("Fixed in abc1234: renamed x.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &reviewForge{}
	code, out, errOut := rotaInWith(t, useReviewForge(f), dir, "worker", "reply", "nia", "--body-file", "reply.md", "--json")
	d := data(t, out)
	if code != 0 || d["commentId"] != "77" || d["url"] != "https://github.com/o/r/pull/9#issuecomment-77" {
		t.Fatalf("%d %v %s", code, d, errOut)
	}
	if len(f.posted) != 1 || !strings.HasPrefix(f.posted[0], "Fixed in abc1234: renamed x.") || !strings.Contains(f.posted[0], "<!-- rota:worker-reply nia -->") {
		t.Fatalf("posted %q", f.posted)
	}
	if !strings.Contains(f.calls[0], "issues/9/comments") {
		t.Errorf("call %q", f.calls[0])
	}
}

func TestWorkerReplyRefusals(t *testing.T) {
	dir := workerProject(t, ghCfg)
	doneSlot(t, dir)
	deps := useReviewForge(&reviewForge{})
	os.WriteFile(filepath.Join(dir, "empty.md"), []byte("  \n"), 0o644)
	os.WriteFile(filepath.Join(dir, "body.md"), []byte("Fixed.\n"), 0o644)
	if code, _, _ := rotaInWith(t, deps, dir, "worker", "reply", "nia", "--body-file", "empty.md"); code != 2 {
		t.Errorf("empty body: %d, want 2", code)
	}
	if code, _, _ := rotaInWith(t, deps, dir, "worker", "reply", "nia"); code != 2 {
		t.Errorf("no body file: %d, want 2", code)
	}
	if code, _, _ := rotaInWith(t, deps, dir, "worker", "reply", "zed", "--body-file", "body.md"); code != 3 {
		t.Errorf("unknown slot: %d, want 3", code)
	}
}

// A worker runs reply from its own worktree, whose .rota/ holds no registry:
// the slot lives in the main checkout's pool (#1876). Prefixed slot names such
// as mac-ben resolve the same way (#1922).
func TestWorkerReplyFromSlotWorktree(t *testing.T) {
	for _, slot := range []string{"nia", "mac-ben"} {
		t.Run(slot, func(t *testing.T) {
			main := workerProject(t, ghCfg)
			reg := `{"slots":[{"name":"` + slot + `","task":"577","branch":"` + slot + `/577-x","state":"done","pr":"https://github.com/o/r/pull/9"}]}`
			if err := os.WriteFile(filepath.Join(main, ".rota", "workers.json"), []byte(reg), 0o644); err != nil {
				t.Fatal(err)
			}
			wt := filepath.Join(main, ".worktrees", slot)
			gittest.Run(t, main, "worktree", "add", "-q", "-b", slot+"/577-x", wt)
			if err := os.MkdirAll(filepath.Join(wt, ".rota"), 0o755); err != nil {
				t.Fatal(err)
			}
			os.WriteFile(filepath.Join(wt, ".rota", "config.json"), []byte(ghCfg), 0o644)
			os.WriteFile(filepath.Join(wt, "reply.md"), []byte("Fixed in abc1234.\n"), 0o644)
			f := &reviewForge{}
			code, out, errOut := rotaInWith(t, useReviewForge(f), wt, "worker", "reply", slot, "--body-file", "reply.md", "--json")
			if d := data(t, out); code != 0 || d["commentId"] != "77" {
				t.Fatalf("%d %v %s", code, d, errOut)
			}
			if code, _, _ := rotaInWith(t, useReviewForge(f), wt, "worker", "reply", "zed", "--body-file", "reply.md"); code != 3 {
				t.Errorf("unknown slot: %d, want 3", code)
			}
		})
	}
}
