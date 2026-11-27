package worker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/proc"
	_ "github.com/l4ci/rota/internal/testiso" // tests never read the real global config
)

func TestIsolatedEnvironKeepsPnpmStore(t *testing.T) {
	bin := t.TempDir()
	stub := `#!/bin/sh
if [ "$1 $2" != "store path" ]; then exit 1; fi
store=${pnpm_config_store_dir:-${XDG_DATA_HOME:-$HOME/.local/share}/pnpm/store}
case "$store" in */v11) ;; *) store=$store/v11 ;; esac
printf '%s\n' "$store"
`
	if err := os.WriteFile(filepath.Join(bin, "pnpm"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	// proc.Run looks up executables in the process PATH, before using Environ.
	t.Setenv("PATH", bin)
	in := []string{"PATH=" + bin, "HOME=/caller/home", "XDG_DATA_HOME=/caller/data"}
	out, err := IsolatedEnviron(in, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result, err := proc.Run(context.Background(), proc.Cmd{Name: "pnpm", Args: []string{"store", "path"}, Environ: out})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("isolated pnpm store path: %+v, %v", result, err)
	}
	if got := strings.TrimSpace(result.Stdout); got != "/caller/data/pnpm/store/v11" {
		t.Fatalf("isolated pnpm store = %q, want caller store /caller/data/pnpm/store/v11", got)
	}
}

func TestIsolatedEnvironKeepsLegacyPnpmStore(t *testing.T) {
	bin := t.TempDir()
	stub := `#!/bin/sh
store=${npm_config_store_dir:-$HOME/.local/share/pnpm/store}
case "$store" in */v3) ;; *) store=$store/v3 ;; esac
printf '%s\n' "$store"
`
	if err := os.WriteFile(filepath.Join(bin, "pnpm"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	out, err := IsolatedEnviron([]string{"PATH=" + bin, "HOME=/caller/home"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result, err := proc.Run(context.Background(), proc.Cmd{Name: "pnpm", Args: []string{"store", "path"}, Environ: out})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("isolated legacy pnpm store path: %+v, %v", result, err)
	}
	if got := strings.TrimSpace(result.Stdout); got != "/caller/home/.local/share/pnpm/store/v3" {
		t.Fatalf("isolated legacy pnpm store = %q, want caller store /caller/home/.local/share/pnpm/store/v3", got)
	}
}

func TestIsolatedEnvironPreservesPnpmStoreOverrides(t *testing.T) {
	keys := []string{"pnpm_config_store_dir", "PNPM_CONFIG_STORE_DIR", "npm_config_store_dir", "NPM_CONFIG_STORE_DIR"}
	for _, key := range keys {
		for _, value := range []string{"/custom/store with spaces", ""} {
			t.Run(key+"="+value, func(t *testing.T) {
				bin := t.TempDir()
				if err := os.WriteFile(filepath.Join(bin, "pnpm"), []byte("#!/bin/sh\nprintf '/discovered/store/v11\\n'\n"), 0o755); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", bin)
				out, err := IsolatedEnviron([]string{"PATH=" + bin, key + "=" + value}, t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				var overrides []string
				for _, kv := range out {
					k, _, _ := strings.Cut(kv, "=")
					for _, candidate := range keys {
						if k == candidate {
							overrides = append(overrides, kv)
						}
					}
				}
				if len(overrides) != 1 || overrides[0] != key+"="+value {
					t.Fatalf("store overrides = %q, want only unchanged %q", overrides, key+"="+value)
				}
			})
		}
	}
}

func TestIsolatedEnvironWithoutPnpmStoreDiscovery(t *testing.T) {
	for _, tc := range []struct {
		name, stub string
	}{
		{name: "absent"},
		{name: "failed", stub: "#!/bin/sh\nprintf '/unusable/store/v11\\n'\nexit 1\n"},
		{name: "empty", stub: "#!/bin/sh\nprintf '\\n'\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			if tc.stub != "" {
				if err := os.WriteFile(filepath.Join(bin, "pnpm"), []byte(tc.stub), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", bin)
			out, err := IsolatedEnviron([]string{"PATH=" + bin, "KEEP_ME=1"}, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			for _, kv := range out {
				k, _, _ := strings.Cut(kv, "=")
				switch k {
				case "pnpm_config_store_dir", "PNPM_CONFIG_STORE_DIR", "npm_config_store_dir", "NPM_CONFIG_STORE_DIR":
					t.Errorf("added store override without successful discovery: %s", kv)
				}
			}
		})
	}
}

// Every host kind's session variables must be absent from a worker sandbox,
// and unrelated ones kept.
func TestIsolatedEnvironDropsEveryHostSession(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	in := []string{"HERDR_PANE_ID=p", "TMUX=/s,1,0", "HC_SESSION=x", "ORCA_PANE=y", "CLAUDE_CONFIG_DIR=/c", "CODEX_HOME=/x", "KEEP_ME=1", "PATH=/bin"}
	out, err := IsolatedEnviron(in, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kept := map[string]bool{}
	for _, kv := range out {
		k, _, _ := strings.Cut(kv, "=")
		kept[k] = true
	}
	for _, k := range []string{"HERDR_PANE_ID", "TMUX", "HC_SESSION", "ORCA_PANE", "CLAUDE_CONFIG_DIR", "CODEX_HOME"} {
		if kept[k] {
			t.Errorf("host variable %s survived isolation", k)
		}
	}
	if !kept["KEEP_ME"] {
		t.Error("isolation dropped an unrelated variable")
	}
}
