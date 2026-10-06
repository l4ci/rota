package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/config"
)

// cfgOf is the parsed project config for a JSON document.
func cfgOf(t *testing.T, doc string) any {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if doc != "" {
		if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return config.Load(p)
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// usageRefusal reports whether err is a harness refusal of class Usage.
func usageRefusal(err error) bool {
	r, ok := err.(*Refusal)
	return ok && r.Class == Usage
}

func TestParseIntegration(t *testing.T) {
	cur, miss := fixture(t, "integration_status_current.txt"), fixture(t, "integration_status_missing.txt")
	for _, tc := range []struct{ name, out, agent, want string }{
		{"current", cur, "claude", "current"},
		{"not installed", miss, "claude", "not installed"},
		{"other agent untouched", miss, "codex", "current"},
		{"absent agent", cur, "nope", "no status"},
		{"empty output", "", "claude", "no status"},
		{"case and spacing", "  Claude :  Current (v11) (/x)\n", "claude", "current"},
		{"outdated", "claude: outdated (v9, latest v10) (/x)\n", "claude", "outdated"},
		{"no version suffix", "claude: not installed\n", "claude", "not installed"},
	} {
		if got := ParseIntegration(tc.out, tc.agent); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestParseCodexVersion(t *testing.T) {
	for _, tc := range []struct {
		name, out string
		want      string // "" means unparseable
	}{
		{"plain", "codex-cli 0.159.2\n", "0.159.2"},
		{"next major", "codex-cli 1.0.0\n", "1.0.0"},
		{"warning lines around it", "WARNING: proceeding\ncodex-cli 0.159.5\nWARNING: x\n", "0.159.5"},
		{"stderr joined", "\nWARNING: Failed to load config\ncodex-cli 0.159.1", "0.159.1"},
		{"prerelease is not a version", "codex-cli 0.159.2-alpha.1\n", ""},
		{"other tool", "claude 2.1.0\n", ""},
		{"bare number", "0.159.2\n", ""},
		{"empty", "", ""},
	} {
		v, ok := ParseCodexVersion(tc.out)
		if tc.want == "" {
			if ok {
				t.Errorf("%s: parsed %v from %q", tc.name, v, tc.out)
			}
			continue
		}
		if !ok || v.String() != tc.want {
			t.Errorf("%s: got %v %v, want %s", tc.name, v, ok, tc.want)
		}
	}
}

func TestLaunchFlags(t *testing.T) {
	for _, tc := range []struct {
		launch string
		want   string
	}{
		{DefaultCodexCommand, "--model --dangerously-bypass-approvals-and-sandbox --dangerously-bypass-hook-trust --no-daemon --no-alt-screen"},
		{"codex -m x -c a=b --foo=bar --foo --", "--foo"},
		{"codex \"oops", ""},
		{"codex", ""},
	} {
		if got := strings.Join(LaunchFlags(tc.launch), " "); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.launch, got, tc.want)
		}
	}
}

func TestHelpHasFlag(t *testing.T) {
	help := "  -m, --model <MODEL>\n      --no-daemon-foo\n      --no-alt-screen, --x"
	for flag, want := range map[string]bool{
		"--model": true, "--no-alt-screen": true, "--x": true,
		"--no-daemon": false, "--mod": false, "--no-alt": false,
	} {
		if got := helpHasFlag(help, flag); got != want {
			t.Errorf("%s: got %v, want %v", flag, got, want)
		}
	}
}
