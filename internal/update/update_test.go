package update

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/proc"
)

// TestMain clears the version overrides: rota update must never reach the
// network from a test, and the release read has only the runner seam.
func TestMain(m *testing.M) {
	os.Unsetenv("ROTA_LATEST_VERSION")
	os.Exit(m.Run())
}

func TestCompare(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0}, {"1.2.3", "1.2.4", -1}, {"1.10.0", "1.9.9", 1}, {"2", "2.0.0", 0},
		{"v1.2.3", "1.2.3", 0}, {"1.2.3.9", "1.2.3", 0}, {"dev", "0.0.1", -1}, {"dev", "", 0},
		{"1.2.3-rc1", "1.2.3", 0}, {"99999999999999999999.0.0", "1.0.0", 1}, {"5.0.0-dev", "4.9.9", 1},
	} {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func env(exeDir, current, latest string) Env {
	return Env{ExeDir: exeDir, Current: current, Latest: func() string { return latest }}
}

func TestDetect(t *testing.T) {
	for _, c := range []struct {
		name, exeDir, current, kind, cmd string
	}{
		{"brew arm", "/opt/homebrew/Cellar/rota/5.0.0/bin", "5.0.0", Brew, "brew update && brew upgrade rota && rota skills update"},
		{"brew shim", "/opt/homebrew/bin", "5.0.0", Brew, "brew update && brew upgrade rota && rota skills update"},
		{"intel cellar", "/usr/local/Cellar/rota/5.0.0/bin", "5.0.0", Brew, "brew update && brew upgrade rota && rota skills update"},
		{"linuxbrew", "/home/linuxbrew/.linuxbrew/bin", "5.0.0", Brew, "brew update && brew upgrade rota && rota skills update"},
		{"script", "/home/u/.local/bin", "5.0.0", Script, "curl -fsSL https://raw.githubusercontent.com/l4ci/rota/main/install.sh | sh && rota skills update"},
		{"dev suffix", "/home/u/.local/bin", "5.0.0-dev", Dev, "git pull && go build -o <where rota lives> ./cmd/rota && rota skills update"},
		{"unstamped", "/tmp/x", "dev", Dev, "git pull && go build -o <where rota lives> ./cmd/rota && rota skills update"},
		{"devel", "/tmp/x", "(devel)", Dev, "git pull && go build -o <where rota lives> ./cmd/rota && rota skills update"},
		{"unresolved", "", "5.0.0", Unknown, "curl -fsSL https://raw.githubusercontent.com/l4ci/rota/main/install.sh | sh && rota skills update"},
	} {
		r := Check(env(c.exeDir, c.current, "9.0.0"))
		if r.InstallType != c.kind || r.InstallRoot != c.exeDir || r.UpdateCommand != c.cmd || r.CurrentVersion != c.current {
			t.Errorf("%s: %+v", c.name, r)
		}
	}
	// a home directory named like a brew path does not make a script install brew
	for _, dir := range []string{"/home/homebrewer/.local/bin", "/home/linuxbrew-fan/.local/bin", "/home/u/homebrew/bin"} {
		if r := Check(env(dir, "5.0.0", "")); r.InstallType != Script {
			t.Errorf("lookalike path %s: %+v", dir, r)
		}
	}
}

// A checkout build stamped by stalebin.Rebuild carries a release-shaped version
// and the full commit; goreleaser stamps git's short abbreviation (8 chars here).
func TestStampedCheckoutBuildIsDev(t *testing.T) {
	full := "0123456789abcdef0123456789abcdef01234567"
	for _, c := range []struct{ name, exeDir, commit, kind string }{
		{"checkout", "/home/u/.local/bin", full, Dev},
		{"script short", "/home/u/.local/bin", "5cc79df4", Script},
		{"script no commit", "/home/u/.local/bin", "", Script},
		{"brew keeps brew", "/opt/homebrew/bin", full, Brew},
	} {
		e := env(c.exeDir, "5.0.0", "9.0.0")
		e.Commit = c.commit
		r := Check(e)
		if r.InstallType != c.kind {
			t.Errorf("%s: %+v", c.name, r)
		}
		if c.kind == Dev && r.UpdateCommand != "git pull && go build -o <where rota lives> ./cmd/rota && rota skills update" {
			t.Errorf("%s: hint %q", c.name, r.UpdateCommand)
		}
	}
}

func TestUnstampedBuildIsUnknown(t *testing.T) {
	for _, v := range []string{"dev", "(devel)", ""} {
		if r := Check(env("/tmp/x", v, "9.0.0")); r.Status != "unknown" {
			t.Errorf("version %q: %+v", v, r)
		}
	}
	if r := Check(env("/tmp/x", "5.0.0-dev", "9.0.0")); r.Status != "behind" {
		t.Errorf("-dev suffix is stamped: %+v", r)
	}
}

func TestStatuses(t *testing.T) {
	for latest, want := range map[string]string{"5.1.0": "behind", "5.0.0": "current", "4.9.0": "ahead", "": "unknown"} {
		if r := Check(env("/home/u/.local/bin", "5.0.0", latest)); r.Status != want {
			t.Errorf("latest %q: %s, want %s", latest, r.Status, want)
		}
	}
	if r := Check(env("/home/u/.local/bin", "", "5.0.0")); r.Status != "unknown" {
		t.Errorf("no current version: %+v", r)
	}
}

// The default release read goes through the runner it is given: one seam
// swaps the process, and the tag it prints is compared like any other version.
func TestLatestThroughRunner(t *testing.T) {
	var got proc.Cmd
	run := func(_ context.Context, c proc.Cmd) (proc.Result, error) {
		got = c
		return proc.Result{Stdout: "v5.1.0\n"}, nil
	}
	e := DefaultEnv("5.0.0", run)
	r := Check(Env{ExeDir: "/home/u/.local/bin", Current: e.Current, Latest: e.Latest})
	if got.Name != "gh" || strings.Join(got.Args, " ") != "api repos/"+Repo+"/releases/latest --jq .tag_name" {
		t.Errorf("command = %s %v", got.Name, got.Args)
	}
	if r.LatestVersion != "5.1.0" || r.Status != "behind" {
		t.Errorf("result = %+v", r)
	}
}

func TestLatestRunnerFailuresAreUnknown(t *testing.T) {
	for name, run := range map[string]proc.Runner{
		"missing gh": func(context.Context, proc.Cmd) (proc.Result, error) { return proc.Result{}, exec.ErrNotFound },
		"exit 1": func(context.Context, proc.Cmd) (proc.Result, error) {
			return proc.Result{ExitCode: 1, Stdout: "v9.9.9"}, nil
		},
	} {
		if got := ghLatest(run); got != "" {
			t.Errorf("%s: latest = %q", name, got)
		}
	}
}

func TestUpdateOmitsSkillsUpdateForCurrentStubs(t *testing.T) {
	for _, exeDir := range []string{"/opt/homebrew/Cellar/rota/1.0.0/bin", "/usr/local/bin", "/home/me/go/bin"} {
		for _, cur := range []string{"1.0.0", "dev"} {
			e := env(exeDir, cur, "1.1.0")
			if got := Check(e).UpdateCommand; !strings.HasSuffix(got, " && rota skills update") {
				t.Errorf("%s: no seam set keeps the refresh step: %q", exeDir, got)
			}
			e.SkillsNeedUpdate = func() bool { return true }
			if got := Check(e).UpdateCommand; !strings.HasSuffix(got, " && rota skills update") {
				t.Errorf("%s: a stale root needs the refresh step: %q", exeDir, got)
			}
			e.SkillsNeedUpdate = func() bool { return false }
			if got := Check(e).UpdateCommand; got == "" || strings.Contains(got, "rota skills update") {
				t.Errorf("%s: current stubs must drop the refresh step: %q", exeDir, got)
			}
		}
	}
}
