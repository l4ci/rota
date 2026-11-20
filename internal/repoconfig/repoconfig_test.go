package repoconfig

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/rotastate"
)

func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gittest.Run(t, dir, "init", "-q")
	gittest.Run(t, dir, "config", "--local", "user.name", "ok")
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
	gittest.Run(t, dir, "config", "--local", "user.email", "a@b")
	ctx := context.Background()
	if err := Record(ctx, runner, dir); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, dir, "config", "--local", "user.name", "t")
	gittest.Run(t, dir, "config", "--local", "core.hooksPath", "/x")
	gittest.Run(t, dir, "config", "--local", "--unset", "user.email")
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
	if d.HasBaseline || len(d.Added)+len(d.Changed)+len(d.Removed) != 0 {
		t.Fatalf("want no baseline and no drift, got %+v", d)
	}
}

func TestNoBaselineListsPreexistingRiskyKeys(t *testing.T) {
	dir := newRepo(t)
	gittest.Run(t, dir, "config", "--local", "core.hooksPath", "/x")
	gittest.Run(t, dir, "config", "--local", "core.fsmonitor", "/y")
	gittest.Run(t, dir, "config", "--local", "filter.lfs.smudge", "z")
	d, err := Check(context.Background(), runner, dir)
	if err != nil {
		t.Fatal(err)
	}
	if d.HasBaseline {
		t.Fatal("Check must not record a baseline")
	}
	got := strings.Join(d.Risky, ",")
	for _, want := range []string{"core.hookspath", "core.fsmonitor", "filter.lfs.smudge", "user.name"} {
		if !strings.Contains(got, want) {
			t.Errorf("risky %q lacks %q", got, want)
		}
	}
	if strings.Contains(got, "/x") {
		t.Errorf("risky leaks values: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "rota")); err == nil {
		t.Fatal("Check wrote under .git/rota")
	}
}

func TestBaselineRecordedSilencesRisky(t *testing.T) {
	dir := newRepo(t)
	gittest.Run(t, dir, "config", "--local", "core.hooksPath", "/x")
	ctx := context.Background()
	if err := Record(ctx, runner, dir); err != nil {
		t.Fatal(err)
	}
	d, _ := Check(ctx, runner, dir)
	if len(d.Risky) != 0 {
		t.Fatalf("risky with a baseline: %v", d.Risky)
	}
}

func TestNotARepoSharesRotastateError(t *testing.T) {
	_, err := Check(context.Background(), runner, t.TempDir())
	if !errors.Is(err, rotastate.ErrNotRepo) {
		t.Fatalf("Check outside a repo = %v, want rotastate.ErrNotRepo", err)
	}
}

func TestRiskyCoversProgramRunningKeyFamilies(t *testing.T) {
	risky := []string{
		"credential.https://example.com.helper", "gpg.ssh.program", "gpg.x509.program",
		"diff.myd.command", "sequence.editor", "mergetool.kdiff.cmd", "difftool.vim.cmd",
	}
	benign := []string{"core.autocrlf", "gpg.format", "diff.myd.binary", "mergetool.kdiff.path", "credential.https://example.com.username"}
	dir := newRepo(t)
	valid := map[string]string{"core.autocrlf": "false", "gpg.format": "openpgp", "diff.myd.binary": "true"}
	for _, k := range append(append([]string{}, risky...), benign...) {
		v := "v"
		if x, ok := valid[k]; ok {
			v = x
		}
		gittest.Run(t, dir, "config", "--local", k, v)
	}
	d, err := Check(context.Background(), runner, dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, k := range d.Risky {
		got[k] = true
	}
	for _, k := range risky {
		if !got[k] {
			t.Errorf("risky %q lacks %q", d.Risky, k)
		}
	}
	for _, k := range benign {
		if got[k] {
			t.Errorf("risky wrongly lists %q", k)
		}
	}
}
