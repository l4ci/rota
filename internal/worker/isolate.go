package worker

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"github.com/l4ci/rota/internal/proc"
)

// goCacheVars are the Go build variables kept at their real locations when
// HOME moves, so an isolated run finds its module cache instead of rebuilding
// from cold. Same list as isolate_env in test/lib/isolate.sh.
var goCacheVars = []string{"GOCACHE", "GOMODCACHE", "GOPATH", "GOENV"}

// IsolatedEnviron is environ with host and ssh identity removed (HERDR_*,
// TMUX*, SSH_AUTH_SOCK, SSH_AGENT_PID) and HOME and the XDG dirs pinned under
// root, created on the way. Go's caches are resolved from environ first and
// pinned, so they survive the HOME change. It is test.isolate (#388): the
// shape of test/lib/isolate.sh for projects that run `rota test run`.
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
	for _, v := range goCacheVars {
		drop[v] = true
	}
	var out []string
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		if drop[k] || strings.HasPrefix(k, "HERDR_") || strings.HasPrefix(k, "TMUX") {
			continue
		}
		out = append(out, kv)
	}
	out = append(out, caches...)
	return append(out, pins...), nil
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
		if gobin, err := exec.LookPath("go"); err == nil {
			cmd := exec.Command(gobin, append([]string{"env"}, missing...)...)
			cmd.Env = environ
			if out, err := cmd.Output(); err == nil {
				lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
				if len(lines) == len(missing) {
					for i, v := range missing {
						have[v] = lines[i]
					}
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
			Timeout: proc.NoTimeout, Combined: true, Group: true,
		})
		if err != nil {
			return err.Error(), 127
		}
		return res.Stdout, res.ExitCode
	}
}
