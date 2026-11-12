package repoconfig

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	gitIn(t, dir, "config", "--local", "user.name", "ok")
	return dir
}

func runner(ctx context.Context, dir string, args ...string) (r Result, err error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	r.Stdout = string(out)
	if ee, ok := err.(*exec.ExitError); ok {
		r.ExitCode = ee.ExitCode()
		err = nil
	}
	return r, err
}

func TestUnchangedBaselineReportsNothing(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	if err := Record(ctx, runner, dir); err != nil {
		t.Fatal(err)
	}
	d, err := Check(ctx, runner, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !d.HasBaseline || !d.Clean() {
		t.Fatalf("want clean drift with a baseline, got %+v", d)
	}
}

func TestDriftNamesAddedChangedRemovedKeys(t *testing.T) {
	dir := newRepo(t)
	gitIn(t, dir, "config", "--local", "user.email", "a@b")
	ctx := context.Background()
	if err := Record(ctx, runner, dir); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "config", "--local", "user.name", "t")
	gitIn(t, dir, "config", "--local", "core.hooksPath", "/x")
	gitIn(t, dir, "config", "--local", "--unset", "user.email")
	d, err := Check(ctx, runner, dir)
	if err != nil {
		t.Fatal(err)
	}
	got := d.Summary()
	for _, want := range []string{"added: core.hookspath", "changed: user.name", "removed: user.email"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary %q lacks %q", got, want)
		}
	}
	if strings.Contains(got, "/x") || strings.Contains(got, "a@b") {
		t.Errorf("summary leaks values: %q", got)
	}
}

func TestNoBaselineIsNotDrift(t *testing.T) {
	dir := newRepo(t)
	d, err := Check(context.Background(), runner, dir)
	if err != nil {
		t.Fatal(err)
	}
	if d.HasBaseline || !d.Clean() {
		t.Fatalf("want no baseline and clean, got %+v", d)
	}
}

func TestEnsureKeepsExistingBaseline(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	if made, err := Ensure(ctx, runner, dir); err != nil || !made {
		t.Fatalf("first Ensure: made=%v err=%v", made, err)
	}
	gitIn(t, dir, "config", "--local", "user.name", "t")
	if made, err := Ensure(ctx, runner, dir); err != nil || made {
		t.Fatalf("second Ensure: made=%v err=%v", made, err)
	}
	d, _ := Check(ctx, runner, dir)
	if d.Clean() {
		t.Fatal("Ensure overwrote the baseline")
	}
}
