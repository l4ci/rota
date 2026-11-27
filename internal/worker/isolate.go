package worker

import (
	"context"
	"os"
	"strings"

	"github.com/l4ci/rota/internal/hostenv"
	"github.com/l4ci/rota/internal/proc"
)

// goCacheVars are the Go build variables kept at their real locations when
// HOME moves, so an isolated run finds its module cache instead of rebuilding
// from cold. Same list as isolate_env in test/lib/isolate.sh.
var goCacheVars = []string{"GOCACHE", "GOMODCACHE", "GOPATH", "GOENV"}

// IsolatedEnviron is environ with host and ssh identity removed (the hostenv
// prefixes, SSH_AUTH_SOCK, SSH_AGENT_PID) and HOME and the XDG dirs pinned under
// root, created on the way. Go's caches and pnpm's store are resolved from
// environ first and pinned, so they survive the HOME change. It is test.isolate
// (#388): the shape of test/lib/isolate.sh for projects that run `rota test run`.
func IsolatedEnviron(environ []string, root string) ([]string, error) {
	pins := []string{
		"HOME=" + root + "/home",
		"XDG_CONFIG_HOME=" + root + "/xdg/config",
		"XDG_CACHE_HOME=" + root + "/xdg/cache",
		"XDG_DATA_HOME=" + root + "/xdg/data",
		"XDG_STATE_HOME=" + root + "/xdg/state",
	}
	for _, p := range pins {
		if err := os.MkdirAll(p[strings.IndexByte(p, '=')+1:], 0o755); err != nil {
			return nil, err
		}
	}
	drop := map[string]bool{"SSH_AUTH_SOCK": true, "SSH_AGENT_PID": true, "HOME": true,
		"XDG_CONFIG_HOME": true, "XDG_CACHE_HOME": true, "XDG_DATA_HOME": true, "XDG_STATE_HOME": true}
	caches := goCaches(environ)
	caches = append(caches, pnpmStore(environ)...)
	for _, v := range goCacheVars {
		drop[v] = true
	}
	var out []string
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		if drop[k] || hostenv.Drop(k) {
			continue
		}
		out = append(out, kv)
	}
	out = append(out, caches...)
	return append(out, pins...), nil
}

// pnpmStore resolves the store before HOME and the XDG dirs move. An explicit
// override, including an empty one, belongs to the caller and stays untouched.
func pnpmStore(environ []string) []string {
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "pnpm_config_store_dir", "PNPM_CONFIG_STORE_DIR", "npm_config_store_dir", "NPM_CONFIG_STORE_DIR":
			return nil
		}
	}
	res, err := proc.Run(context.Background(), proc.Cmd{Name: "pnpm", Args: []string{"store", "path"}, Environ: environ})
	if err != nil || res.ExitCode != 0 {
		return nil
	}
	store := strings.TrimSpace(res.Stdout)
	if store == "" {
		return nil
	}
	// pnpm 11+ reads pnpm_config_*, while older releases read npm_config_*.
	return []string{"pnpm_config_store_dir=" + store, "npm_config_store_dir=" + store}
}

// goCaches is KEY=value for each Go cache variable: environ's own value when
// set, else what `go env` reports under environ. Empty when go is not
// installed, in which case there is nothing to keep.
func goCaches(environ []string) []string {
	have := map[string]string{}
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok && v != "" {
			have[k] = v
		}
	}
	var missing []string
	for _, v := range goCacheVars {
		if have[v] == "" {
			missing = append(missing, v)
		}
	}
	if len(missing) > 0 {
		// context.Background: IsolatedEnviron has no ctx to pass.
		if res, err := proc.Run(context.Background(), proc.Cmd{Name: "go", Args: append([]string{"env"}, missing...), Environ: environ}); err == nil && res.ExitCode == 0 {
			lines := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")
			if len(lines) == len(missing) {
				for i, v := range missing {
					have[v] = lines[i]
				}
			}
		}
	}
	var out []string
	for _, v := range goCacheVars {
		if have[v] != "" {
			out = append(out, v+"="+have[v])
		}
	}
	return out
}

// IsolatedShell is Env.Shell running each command under environ.
func IsolatedShell(environ []string) func(ctx context.Context, dir, command string) (string, int) {
	return func(ctx context.Context, dir, command string) (string, int) {
		res, err := proc.Run(ctx, proc.Cmd{
			Name: "sh", Args: []string{"-c", command}, Dir: dir, Environ: environ,
			Timeout: proc.NoTimeout, Combined: true, Group: true, Kill: killTree,
		})
		if err != nil {
			return err.Error(), 127
		}
		return res.Stdout, res.ExitCode
	}
}
