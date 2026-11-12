package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/hook"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/skills"
)

// settingsPaths maps each scope to its file. Project scopes are "" outside a
// project root.
func settingsPaths(root, home string, getenv func(string) string) map[hook.Scope]string {
	cfgDir := skills.ClaudeDir(getenv, home)
	m := map[hook.Scope]string{}
	for _, sc := range hook.Scopes {
		base := root
		if sc == hook.ScopeUser {
			base = cfgDir
		}
		if p := hook.SettingsPath(sc, base); p != "" {
			m[sc] = p
		}
	}
	return m
}

// settingsTarget resolves --scope to its file, with the exit codes the
// contract names: 3 when a project scope is asked outside a project root.
func settingsTarget(c *Ctx, scopeFlag string) (hook.Scope, string, map[hook.Scope]string, error) {
	scope := hook.Scope(scopeFlag)
	if !scope.Valid() {
		return "", "", nil, Usage("--scope must be project-local, project or user")
	}
	root, rerr := c.Root()
	if rerr != nil && scope != hook.ScopeUser {
		return "", "", nil, rerr
	}
	home, _ := c.deps().UserHomeDir()
	paths := settingsPaths(root, home, c.deps().Getenv)
	p := paths[scope]
	if p == "" {
		return "", "", nil, Resolution("no settings path for scope %s (CLAUDE_CONFIG_DIR and HOME unset)", scope)
	}
	return scope, p, paths, nil
}

func writeSettings(path string, o *jsonx.Object) error {
	b, err := jsonx.Marshal(o)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		return err
	}
	return fsio.WriteFileAtomic(path, append(b, '\n'))
}

func hookInstall(fs *flag.FlagSet) RunFunc {
	scopeFlag := fs.String("scope", "project-local", "project-local, project or user")
	wrap := fs.Bool("wrap-statusline", false, "wrap an existing statusline instead of refusing")
	return func(c *Ctx, args []string) (Result, error) {
		if err := noArgs(args); err != nil {
			return Result{}, err
		}
		scope, path, paths, err := settingsTarget(c, *scopeFlag)
		if err != nil {
			return Result{}, err
		}
		files := map[hook.Scope]*jsonx.Object{}
		for sc, p := range paths {
			o, rerr := hook.ReadSettings(p)
			if rerr != nil {
				if sc == scope {
					return Result{}, &Error{Exit: ExitInternal, Message: rerr.Error(), Hint: "fix or move the settings file; rota will not overwrite a file it cannot parse"}
				}
				o = nil // another scope's broken file does not block this one
			}
			files[sc] = o
		}
		if files[scope] == nil {
			files[scope] = jsonx.NewObject()
		}
		out, err := hook.Install(hook.InstallIn{Scope: scope, Wrap: *wrap, Files: files})
		if err != nil {
			return Result{}, &Error{Exit: ExitInternal, Message: err.Error()}
		}
		if out.Blocked {
			d := jsonx.Obj("blockedBy", blockedby.StatuslineExists, "scope", string(scope), "settingsPath", path, "changed", false)
			return Result{Data: d}, Refused("a statusline is already configured and would be replaced").
				WithHint("run: rota hook install --wrap-statusline (keeps it, runs it after the dump)")
		}
		if out.Changed {
			if err := writeSettings(path, files[scope]); err != nil {
				return Result{}, &Error{Exit: ExitInternal, Message: err.Error()}
			}
		}
		d := jsonx.Obj("scope", string(scope), "settingsPath", path, "hooks", out.Hooks, "statusline", out.Statusline, "changed", out.Changed)
		text := fmt.Sprintf("%s: hooks %s, statusline %s", path, strings.Join(out.Hooks, ", "), out.Statusline)
		if !out.Changed {
			text = path + ": already installed"
		}
		if !guardReachesWorkers(files) {
			c.Warn("the worker guard (PreToolUse) sits only in project-local scope, which worker worktrees do not read: run rota hook install --scope project (or --scope user)")
		}
		return Result{Data: d, Text: text}, nil
	}
}

// guardReachesWorkers reports whether a marked PreToolUse entry sits in a scope
// a worker worktree reads: project or user. Project-local is untracked.
func guardReachesWorkers(files map[hook.Scope]*jsonx.Object) bool {
	for _, sc := range []hook.Scope{hook.ScopeProject, hook.ScopeUser} {
		if _, ok := hook.MarkedEvents(files[sc])[hook.EventGuard]; ok {
			return true
		}
	}
	return false
}

func hookUninstall(fs *flag.FlagSet) RunFunc {
	scopeFlag := fs.String("scope", "project-local", "project-local, project or user")
	return func(c *Ctx, args []string) (Result, error) {
		if err := noArgs(args); err != nil {
			return Result{}, err
		}
		scope, path, _, err := settingsTarget(c, *scopeFlag)
		if err != nil {
			return Result{}, err
		}
		o, rerr := hook.ReadSettings(path)
		if rerr != nil {
			return Result{}, &Error{Exit: ExitInternal, Message: rerr.Error()}
		}
		removed := hook.Uninstall(o)
		if removed == nil {
			removed = []string{}
		}
		if len(removed) > 0 {
			if err := writeSettings(path, o); err != nil {
				return Result{}, &Error{Exit: ExitInternal, Message: err.Error()}
			}
		}
		d := jsonx.Obj("scope", string(scope), "settingsPath", path, "removed", removed, "changed", len(removed) > 0)
		text := path + ": nothing to remove"
		if len(removed) > 0 {
			text = path + ": removed " + strings.Join(removed, ", ")
		}
		return Result{Data: d, Text: text}, nil
	}
}
