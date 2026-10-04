package update

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain is the safety net: rota update must never reach the network from a
// test. Any gh lookup that does not land in test/fakes stops the run.
func TestMain(m *testing.M) {
	wd, _ := os.Getwd()
	fakes, _ := filepath.Abs(filepath.Join(wd, "..", "..", "test", "fakes"))
	os.Setenv("PATH", fakes+string(os.PathListSeparator)+os.Getenv("PATH"))
	os.Unsetenv("ROTA_TEST_LATEST_VERSION")
	os.Unsetenv("ROTA_LATEST_VERSION")
	lookPath = func(name string) (string, error) {
		p, err := exec.LookPath(name)
		if err != nil {
			return "", err
		}
		if !strings.HasPrefix(p, fakes+string(os.PathSeparator)) {
			panic("update would exec " + p + " outside test/fakes")
		}
		return p, nil
	}
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

func TestLatestFromTestVariable(t *testing.T) {
	t.Setenv("ROTA_TEST_LATEST_VERSION", "7.8.9")
	if got := ghLatest(); got != "7.8.9" {
		t.Errorf("ghLatest = %q", got)
	}
}

// With no override, the only gh that may run is the fake, and it answers no
// release: latest stays empty and status unknown, never a network result.
func TestGhLookupStaysInFakes(t *testing.T) {
	p, err := lookPath("gh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p, filepath.Join("test", "fakes")) {
		t.Fatalf("gh = %s", p)
	}
	t.Setenv("FAKE_TRACKER_DB", filepath.Join(t.TempDir(), "db.json"))
	if got := ghLatest(); got != "" {
		t.Errorf("fake gh returned release %q", got)
	}
}

func TestGuardRefusesForeignGh(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\nexit 0\n"), 0o755)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	defer func() {
		if recover() == nil {
			t.Error("a gh outside test/fakes was not refused")
		}
	}()
	ghLatest()
}
