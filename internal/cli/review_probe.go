package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/proc"
)

// reviewProbe isolates only the probe subprocess's configuration paths. Harness
// launch and authentication keep their existing environment. This is not an OS
// sandbox: the command still has the user's absolute-path access.
func reviewProbe(c *Ctx, args []string) (result Result, err error) {
	if c.dashAt != 0 || len(args) == 0 || args[0] == "" {
		return Result{}, Usage("usage: rota review probe -- <command> [<arg>...]")
	}
	dir, err := c.deps().Getwd()
	if err != nil {
		return Result{}, Unavailable("resolve probe directory: %v", err)
	}
	temp, err := reviewWorkspaceTempDir()
	if err != nil {
		return Result{}, Unavailable("resolve probe temporary directory: %v", err)
	}
	root, err := os.MkdirTemp(temp, "rota-review-probe-")
	if err != nil {
		return Result{}, Unavailable("create probe config root: %v", err)
	}
	defer func() {
		if cleanErr := c.deps().ReviewRemoveAll(root); cleanErr != nil {
			if err != nil {
				err = Unavailable("%v; cleanup probe config %s: %v", err, root, cleanErr)
			} else {
				err = Unavailable("cleanup probe config %s: %v", root, cleanErr)
			}
		}
	}()
	environ, err := reviewProbeEnviron(c.deps().Environ(), root)
	if err != nil {
		return Result{}, Unavailable("set up probe config: %v", err)
	}
	res, runErr := c.deps().Proc(c.Context(), proc.Cmd{
		Name: args[0], Args: args[1:], Dir: dir, Environ: environ, Group: true,
	})
	if !c.JSON {
		fmt.Fprint(c.Stdout, res.Stdout)
		fmt.Fprint(c.Stderr, res.Stderr)
	}
	if runErr != nil {
		return Result{}, Unavailable("run probe %s: %v", args[0], runErr)
	}
	result = Result{Data: gitObj("changed", true, "exitCode", res.ExitCode, "stdout", res.Stdout, "stderr", res.Stderr)}
	if res.ExitCode != 0 {
		return result, Failed("probe %s exited %d", args[0], res.ExitCode)
	}
	return result, nil
}

func reviewProbeEnviron(parent []string, root string) ([]string, error) {
	home := filepath.Join(root, "home")
	config := filepath.Join(home, ".config")
	pins := []struct{ key, path string }{
		{"HOME", home},
		{"XDG_CONFIG_HOME", config},
		{"XDG_CACHE_HOME", filepath.Join(home, ".cache")},
		{"XDG_DATA_HOME", filepath.Join(home, ".local", "share")},
		{"XDG_STATE_HOME", filepath.Join(home, ".local", "state")},
		{"ROTA_CONFIG_DIR", filepath.Join(config, "rota")},
		{"GH_CONFIG_DIR", filepath.Join(config, "gh")},
		{"CODEX_HOME", filepath.Join(home, ".codex")},
		{"CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude")},
		{"OPENCODE_CONFIG_DIR", filepath.Join(config, "opencode")},
	}
	replaced := make(map[string]bool, len(pins))
	for _, pin := range pins {
		if err := os.MkdirAll(pin.path, 0o700); err != nil {
			return nil, err
		}
		replaced[pin.key] = true
	}
	environ := make([]string, 0, len(parent)+len(pins))
	for _, kv := range parent {
		key, _, _ := strings.Cut(kv, "=")
		if !replaced[key] {
			environ = append(environ, kv)
		}
	}
	for _, pin := range pins {
		environ = append(environ, pin.key+"="+pin.path)
	}
	return environ, nil
}
