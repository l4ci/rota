package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/proc"
)

func TestReviewProbeIsolatesConfig(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("TMPDIR", temp)
	developer := t.TempDir()
	for _, key := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "ROTA_CONFIG_DIR", "GH_CONFIG_DIR", "CODEX_HOME", "CLAUDE_CONFIG_DIR", "OPENCODE_CONFIG_DIR"} {
		t.Setenv(key, developer)
	}
	write(t, filepath.Join(developer, "sentinel"), "keep")
	o := trRun(t, t.TempDir(), "", "review", "probe", "--json", "--", "sh", "-c", `
for dir in "$HOME" "$XDG_CONFIG_HOME" "$XDG_DATA_HOME" "$XDG_CACHE_HOME" "$XDG_STATE_HOME" "$ROTA_CONFIG_DIR" "$GH_CONFIG_DIR" "$CODEX_HOME" "$CLAUDE_CONFIG_DIR" "$OPENCODE_CONFIG_DIR"; do
  printf probe > "$dir/sentinel" || exit 9
done
printf '%s' "$HOME"
printf diagnostic >&2
`)
	if o.code != 0 {
		t.Fatalf("isolated probe failed: rc=%d %s %s", o.code, o.stdout, o.stderr)
	}
	d := envelope(t, o.stdout)["data"].(map[string]any)
	if d["exitCode"] != float64(0) || d["stderr"] != "diagnostic" {
		t.Fatalf("probe result lost: %v", d)
	}
	home := d["stdout"].(string)
	if !strings.HasPrefix(home, temp+string(os.PathSeparator)) {
		t.Fatalf("probe inherited HOME: %q", home)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("probe config survived cleanup: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(developer, "sentinel")); err != nil || string(got) != "keep" {
		t.Fatalf("developer config changed: %q %v", got, err)
	}
	entries, err := os.ReadDir(temp)
	if err != nil || len(entries) != 0 {
		t.Fatalf("probe state leaked: %v %v", entries, err)
	}
}

func TestReviewProbeFailures(t *testing.T) {
	for _, mode := range []string{"setup", "exit", "launch", "cancel", "cleanup", "exit-cleanup"} {
		t.Run(mode, func(t *testing.T) {
			temp := t.TempDir()
			t.Setenv("TMPDIR", temp)
			deps := testDeps()
			args := []string{"sh", "-c", `printf '%s' "$HOME"; printf diagnostic >&2; exit 7`}
			wantCode := 5
			switch mode {
			case "setup":
				t.Setenv("TMPDIR", filepath.Join(temp, "missing"))
				deps.Proc = func(context.Context, proc.Cmd) (proc.Result, error) {
					t.Fatal("probe launched after setup failure")
					return proc.Result{}, nil
				}
			case "exit":
				wantCode = 1
			case "launch":
				args = []string{filepath.Join(temp, "missing-binary")}
			case "cancel":
				deps.Proc = func(context.Context, proc.Cmd) (proc.Result, error) {
					return proc.Result{}, context.Canceled
				}
			case "cleanup", "exit-cleanup":
				deps.ReviewRemoveAll = func(string) error { return errors.New("removal denied") }
				if mode == "cleanup" {
					args = []string{"true"}
				}
			}
			o := trRunWith(t, deps, t.TempDir(), "", append([]string{"review", "probe", "--json", "--"}, args...)...)
			if o.code != wantCode {
				t.Fatalf("%s: rc=%d want=%d: %s %s", mode, o.code, wantCode, o.stdout, o.stderr)
			}
			if mode == "exit" {
				d := envelope(t, o.stdout)["data"].(map[string]any)
				if d["exitCode"] != float64(7) || d["stderr"] != "diagnostic" || d["stdout"] == "" {
					t.Fatalf("failed probe output lost: %v", d)
				}
			}
			entries, err := os.ReadDir(temp)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(mode, "cleanup") {
				if len(entries) != 1 || !strings.Contains(o.stdout, filepath.Join(temp, entries[0].Name())) || !strings.Contains(o.stdout, "removal denied") {
					t.Fatalf("cleanup failure must name remaining state: %s, %v", o.stdout, entries)
				}
				if mode == "exit-cleanup" && !strings.Contains(o.stdout, "exited 7") {
					t.Fatalf("cleanup failure hid probe failure: %s", o.stdout)
				}
			} else if len(entries) != 0 {
				t.Fatalf("%s leaked config state: %v", mode, entries)
			}
		})
	}
}

func TestReviewProbeRequiresCommandSeparator(t *testing.T) {
	for _, args := range [][]string{nil, {"--"}, {"true"}, {"extra", "--", "true"}} {
		deps := testDeps()
		deps.Proc = func(context.Context, proc.Cmd) (proc.Result, error) {
			t.Fatal("invalid probe launched a subprocess")
			return proc.Result{}, nil
		}
		o := trRunWith(t, deps, t.TempDir(), "", append([]string{"review", "probe", "--json"}, args...)...)
		if o.code != 2 {
			t.Fatalf("invalid args %v: rc=%d %s", args, o.code, o.stdout)
		}
	}
}
