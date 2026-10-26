package cli

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

func TestMigrateExternalKnowledge(t *testing.T) {
	project, clone, remote := extFixture(t, "")
	run := func(args ...string) knOut {
		return knNew(t, project, "", append([]string{"migrate", "external-knowledge"}, args...)...)
	}
	// Nothing to move yet.
	if o := run(); o.rc != 0 || !strings.Contains(o.stdout, "nothing to migrate") {
		t.Fatalf("empty preview: %+v", o)
	}
	gittest.Commit(t, clone, "seed", "topics/go.md", "# Go\n\n- **Godot** — Kill tweens. (seen on Godot 4.6) <!-- 2026-09-01 source:game -->\n- **Mid** — Pinned (seen on 3.49.2). Go on. <!-- 2026-09-01 -->\n")
	gittest.Run(t, clone, "push", "-q")
	n := gittest.Run(t, clone, "rev-list", "--count", "HEAD")
	o := run()
	if o.rc != 0 || !strings.Contains(o.stdout, "Go :: Godot -> seen:godot@4.6") || !strings.Contains(o.stdout, "manual review") || !strings.Contains(o.stderr, "preview only") {
		t.Fatalf("preview: %+v", o)
	}
	if gittest.Run(t, clone, "rev-list", "--count", "HEAD") != n {
		t.Fatal("preview committed")
	}
	if o := run("extra"); o.rc != ExitUsage {
		t.Errorf("extra arg: rc=%d", o.rc)
	}
	if o := run("--apply"); o.rc != 0 || !strings.Contains(o.stdout, "moved 1") {
		t.Fatalf("apply: %+v", o)
	}
	if got := gittest.Run(t, remote, "show", "main:topics/go.md"); !strings.Contains(got, "Kill tweens. <!-- 2026-09-01 source:game seen:godot@4.6 -->") {
		t.Fatalf("remote = %q", got)
	}
	n = gittest.Run(t, clone, "rev-list", "--count", "HEAD")
	if o := run("--apply"); o.rc != 0 || !strings.Contains(o.stdout, "nothing to migrate") {
		t.Fatalf("re-apply: %+v", o)
	}
	if gittest.Run(t, clone, "rev-list", "--count", "HEAD") != n {
		t.Fatal("second apply committed")
	}
	// A dirty clone refuses --apply (exit 4) but previews.
	gittest.Write(t, clone, "topics/go.md", "# Go\n\n- **x** — y (seen on Godot 4.7) <!-- 2026-09-01 -->\n")
	if o := run("--apply"); o.rc != ExitRefused {
		t.Errorf("dirty apply: rc=%d", o.rc)
	}
	if o := run(); o.rc != 0 || !strings.Contains(o.stdout, "godot@4.7") {
		t.Errorf("dirty preview: %+v", o)
	}
}
