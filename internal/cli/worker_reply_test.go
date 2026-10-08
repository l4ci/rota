package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
