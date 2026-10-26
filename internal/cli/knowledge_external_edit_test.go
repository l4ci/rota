package cli

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

func TestKnowledgeExternalEditFlags(t *testing.T) {
	project, clone, remote := extFixture(t, "")
	ext := func(stdin string, args ...string) knOut {
		return knNew(t, project, stdin, append([]string{"knowledge"}, args...)...)
	}
	// Usage errors: stamp flags need --external; amend needs something to do.
	for _, args := range [][]string{
		{"amend", "--topic", "Go", "--fragment", "Table", "--date", "2026-10-26"},
		{"amend", "--topic", "Go", "--fragment", "Table", "--seen", "go@1.23"},
		{"amend", "--external", "--topic", "Go", "--fragment", "Table"},
		{"amend", "--external", "--topic", "Go", "--fragment", "Table", "--date", "2026-13-40"},
		{"amend", "--external", "--topic", "Go", "--fragment", "Table", "--seen", "Go@1"},
		{"amend", "--external", "--topic", "Go", "--fragment", "Table", "--seen", "go@1", "--mode", "append"},
		{"rename-topic", "--external", "--from", "Go", "--to", "Rust"},
	} {
		if o := ext("", args...); o.rc != ExitUsage {
			t.Errorf("%v: rc=%d %q", args, o.rc, o.stderr)
		}
	}
	if gittest.Run(t, clone, "rev-list", "--count", "HEAD") != "1" {
		t.Fatal("a refused call committed")
	}
	// Stamp-only amend: no body needed.
	o := ext("", "amend", "--external", "--topic", "Go", "--fragment", "Table", "--seen", "go@1.23", "--date", "2026-10-26")
	if o.rc != 0 || !strings.Contains(o.stdout, "amended: topics/go.md") {
		t.Fatalf("amend: %+v", o)
	}
	if got := gittest.Run(t, remote, "show", "main:topics/go.md"); !strings.Contains(got, "Use t.Run. <!-- 2026-10-26 source:rota seen:go@1.23 -->") {
		t.Fatalf("remote = %q", got)
	}
	// --seen "" drops the field.
	if o := ext("", "amend", "--external", "--topic", "Go", "--fragment", "Table", "--seen", ""); o.rc != 0 {
		t.Fatalf("drop: %+v", o)
	}
	if got := gittest.Run(t, remote, "show", "main:topics/go.md"); strings.Contains(got, "seen:") {
		t.Fatalf("remote = %q", got)
	}
	// replace: new text, then a stamp-only --old is exit 3, a bad --new exit 4.
	if o := ext("", "replace", "--external", "--topic", "Go", "--old", "Use t.Run.", "--new", "Use t.Run per case."); o.rc != 0 || !strings.Contains(o.stdout, "replaced:") {
		t.Fatalf("replace: %+v", o)
	}
	if o := ext("", "replace", "--external", "--topic", "Go", "--old", "source:rota", "--new", "x"); o.rc != ExitResolution {
		t.Errorf("stamp-only old: rc=%d", o.rc)
	}
	if o := ext("", "replace", "--external", "--topic", "Go", "--old", "Use", "--new", "a -->"); o.rc != ExitRefused {
		t.Errorf("--> in new: rc=%d", o.rc)
	}
	if o := ext("", "replace", "--external", "--topic", "Go", "--old", "Use"); o.rc != ExitUsage {
		t.Errorf("missing --new: rc=%d", o.rc)
	}
	// rm: unknown title is exit 3; known goes.
	if o := ext("", "rm", "--external", "--topic", "Go", "--title", "Nope"); o.rc != ExitResolution {
		t.Errorf("rm unknown: rc=%d", o.rc)
	}
	o = ext("", "rm", "--external", "--topic", "Go", "--title", "Table tests")
	if o.rc != 0 || !strings.Contains(o.stdout, "removed: Go :: Table tests") {
		t.Fatalf("rm: %+v", o)
	}
	if got := gittest.Run(t, remote, "show", "main:topics/go.md"); strings.Contains(got, "Table tests") {
		t.Fatalf("remote = %q", got)
	}
}
