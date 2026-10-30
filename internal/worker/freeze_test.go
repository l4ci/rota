package worker

import (
	"encoding/json"
	"github.com/l4ci/rota/internal/gittest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/landfreeze"
)

func frozenWorld(t *testing.T) (*world, string) {
	t.Helper()
	w := newWorld(t, "")
	// Keep runtime state out of git status, as rota init does in real projects.
	if err := os.WriteFile(filepath.Join(w.dir, ".git", "info", "exclude"), []byte(".rota/\n"), 0644); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(w.dir, ".git", "rota", "freeze.json")
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"active":true,"base":"main","sha":"0123456789012345678901234567890123456789","checks":["false"]}`), 0644); err != nil {
		t.Fatal(err)
	}
	return w, p
}

func TestFreezeVerifiedRecovery(t *testing.T) {
	w, p := frozenWorld(t)
	e := w.env(false)
	r, err := e.Freeze(bg, w.dir, FreezeOpts{Verify: true})
	if err != nil || r.Active || r.ClearedBy == "" || r.ClearedSHA != gittest.Run(t, w.dir, "rev-parse", "HEAD") {
		t.Fatalf("green recovery: %+v %v", r, err)
	}
	stored, err := landfreeze.Read(p)
	if err != nil || stored.Active {
		t.Fatalf("clear not persisted: %+v %v", stored, err)
	}
}

func TestFreezeRecoveryKeepsRedOrUnverifiedBase(t *testing.T) {
	for _, cfg := range []string{`{"test":{"full":["false"]}}`, `{"test":{"full":[]}}`, `{"test":{"full":["true"],"e2e":["false"]}}`} {
		t.Run(cfg, func(t *testing.T) {
			w, p := frozenWorld(t)
			w.setConfig(cfg)
			if _, err := w.env(false).Freeze(bg, w.dir, FreezeOpts{Verify: true}); err == nil {
				t.Fatal("red or absent verification must not clear freeze")
			}
			r, err := landfreeze.Read(p)
			if err != nil || !r.Active {
				t.Fatalf("freeze lost: %+v %v", r, err)
			}
		})
	}
}

func TestFreezeRecoveryRefusesDirtyOrWrongBranch(t *testing.T) {
	for _, kind := range []string{"dirty", "branch", "stale-origin", "moved-during-verify"} {
		t.Run(kind, func(t *testing.T) {
			w, p := frozenWorld(t)
			switch kind {
			case "dirty":
				os.WriteFile(filepath.Join(w.dir, "seed.txt"), []byte("dirty"), 0644)
			case "branch":
				gittest.Run(t, w.dir, "checkout", "-q", "-b", "other")
			case "stale-origin":
				advanceMainOn(w, "more.txt")
			case "moved-during-verify":
				w.setConfig(`{"test":{"full":["git commit --allow-empty -m moved"]}}`)
			}
			if _, err := w.env(false).Freeze(bg, w.dir, FreezeOpts{Verify: true}); err == nil {
				t.Fatal("unsafe base cleared freeze")
			}
			r, err := landfreeze.Read(p)
			if err != nil || !r.Active {
				t.Fatalf("freeze lost: %+v %v", r, err)
			}
		})
	}
}

func TestFreezeSharedAcrossLinkedWorktrees(t *testing.T) {
	w, p := frozenWorld(t)
	wt := filepath.Join(t.TempDir(), "linked")
	gittest.Run(t, w.dir, "worktree", "add", "-q", "--detach", wt)
	r, err := w.env(false).Freeze(bg, wt, FreezeOpts{RepairIssue: "99"})
	if err != nil || r.RepairIssue != "99" {
		t.Fatalf("linked repair: %+v %v", r, err)
	}
	b, _ := os.ReadFile(p)
	var stored landfreeze.Record
	if err := json.Unmarshal(b, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.RepairIssue != "99" {
		t.Fatalf("linked worktree wrote a separate freeze: %s", b)
	}
}

func TestFreezeClearDeletedBase(t *testing.T) {
	w, p := frozenWorld(t)
	gittest.Run(t, w.dir, "branch", "gone")
	r, err := landfreeze.Read(p)
	if err != nil {
		t.Fatal(err)
	}
	r.Base = "gone"
	if err := landfreeze.Save(p, r); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, w.dir, "branch", "-d", "gone")
	cleared, err := w.env(false).Freeze(bg, w.dir, FreezeOpts{Clear: true, Reason: "base was deleted"})
	if err != nil {
		t.Fatalf("manual clear must not resolve deleted base: %v", err)
	}
	if cleared.Active || cleared.Base != "gone" || cleared.ClearedSHA != gittest.Run(t, w.dir, "rev-parse", "HEAD") {
		t.Fatalf("clear must retain cause and record operator HEAD: %+v", cleared)
	}
	audit, err := os.ReadFile(filepath.Join(w.dir, ".rota", "gate-audit.jsonl"))
	if err != nil || !strings.Contains(string(audit), "base was deleted") {
		t.Fatalf("missing clear audit: %s %v", audit, err)
	}
	stored, err := landfreeze.Read(p)
	if err != nil || stored.Active {
		t.Fatalf("clear must persist: %+v %v", stored, err)
	}
}

func TestFreezeClearRecordsExistingBase(t *testing.T) {
	w, p := frozenWorld(t)
	base := gittest.Run(t, w.dir, "rev-parse", "main")
	gittest.Run(t, w.dir, "checkout", "-q", "-b", "unrelated")
	gittest.Run(t, w.dir, "commit", "-q", "--allow-empty", "-m", "unrelated work")
	if base == gittest.Run(t, w.dir, "rev-parse", "HEAD") {
		t.Fatal("fixture must have an unrelated operator HEAD")
	}
	cleared, err := w.env(false).Freeze(bg, w.dir, FreezeOpts{Clear: true, Reason: "known obsolete check"})
	if err != nil || cleared.ClearedSHA != base {
		t.Fatalf("clear must record the frozen base, not unrelated HEAD: %+v %v", cleared, err)
	}
	stored, err := landfreeze.Read(p)
	if err != nil || stored.ClearedSHA != base || stored.Active {
		t.Fatalf("clearance must persist the base SHA: %+v %v", stored, err)
	}
}
