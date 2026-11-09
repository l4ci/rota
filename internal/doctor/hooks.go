package doctor

import (
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/hook"
	"github.com/l4ci/rota/internal/host"
)

const hookHint = "herdr integration install claude"

const (
	statuslineHint = "rota hook install --wrap-statusline"
	stopHookHint   = "rota hook install"
	guardHookHint  = "rota hook install --scope project (or --scope user)"
)

func (d *runner) hook() Check {
	if !host.CapabilitiesForKind(d.roundHost()).Harness.HerdrIntegration {
		return skip("hook", "host is not herdr")
	}
	if len(d.in.Accounts) == 0 {
		return skip("hook", "no accounts in round.accounts")
	}
	bin, ok := d.in.Look(host.Herdr.Binary())
	if !ok {
		return skip("hook", "herdr not on PATH (see host)")
	}
	var parts []string
	failed := false
	for _, a := range d.in.Accounts {
		r, ran := d.run(bin, []string{"integration", "status"}, []string{"CLAUDE_CONFIG_DIR=" + d.expand(a.Dir)})
		if !ran || r.ExitCode != 0 {
			failed = true
			parts = append(parts, a.Name+": herdr integration status failed")
			continue
		}
		state := harness.ParseIntegration(r.Stdout, harness.ClaudeIntegration)
		if state != "current" {
			failed = true
		}
		parts = append(parts, a.Name+": "+state)
	}
	if failed {
		return fail("hook", strings.Join(parts, "; "), hookHint)
	}
	return pass("hook", strings.Join(parts, "; "))
}

// hooks returns the run's settings view, reading the files on first use.
func (d *runner) hooks() *hook.View {
	if d.settings == nil {
		var dirs []string
		for _, dir := range d.configDirs() {
			dirs = append(dirs, d.expand(dir))
		}
		d.settings = hook.LoadView(d.in.ProjectRoot, dirs)
	}
	return d.settings
}

// configDirs are the Claude config dirs whose settings.json counts for the
// orchestrator checks: each account's configDir, else the default user dir.
func (d *runner) configDirs() []string {
	var dirs []string
	for _, a := range d.in.Accounts {
		if a.Dir != "" {
			dirs = append(dirs, a.Dir)
		}
	}
	if len(dirs) == 0 && d.in.DefaultConfigDir != "" {
		dirs = []string{d.in.DefaultConfigDir}
	}
	return dirs
}

// optIn reports whether anything `rota hook install` writes is present in any
// settings file in scope: a `# rota-hook` entry for any event, or a statusLine
// that runs `rota statusline dump`. The hooks are opt-in, so with none of that
// the checks skip; they fail only on a broken or partial install. Unreadable
// files are returned so a skip can say it could not look there.
func (d *runner) optIn() (in bool, unreadable []string) {
	for _, f := range d.hooks().InScope() {
		if f.Err != nil {
			unreadable = append(unreadable, f.Path)
		}
		if len(f.Events) > 0 || (f.HasStatusLine && hook.IsOurStatusLine(f.StatusLineCmd)) {
			in = true
		}
	}
	return in, unreadable
}

func notInstalled(name string, unreadable []string) Check {
	detail := "hooks not installed (opt-in)"
	if len(unreadable) > 0 {
		detail += "; cannot read " + strings.Join(unreadable, ", ")
	}
	return skip(name, detail+": rota hook install")
}

func (d *runner) statusline() Check {
	const name = "statusline"
	if d.in.ProjectRoot == "" {
		return skip(name, "not inside a rota project")
	}
	if in, bad := d.optIn(); !in {
		return notInstalled(name, bad)
	}
	h := d.hooks()
	proj := h.Project()
	users := h.Users
	labels := d.configDirs()
	if len(users) == 0 {
		users, labels = []hook.File{{}}, []string{""}
	}
	anyFile := h.Local.Exists || h.Shared.Exists
	var parts, bad []string
	for i, dir := range labels {
		files := append([]hook.File{}, proj...)
		label := "project"
		if dir != "" {
			files = append(files, users[i])
			label = dir
			anyFile = anyFile || users[i].Exists
		}
		var eff *hook.File
		broken := ""
		for i := range files {
			if files[i].Err != nil {
				broken = files[i].Path
				break
			}
			if files[i].HasStatusLine {
				eff = &files[i]
				break
			}
		}
		switch {
		case broken != "":
			bad = append(bad, label+": cannot read "+broken)
		case eff == nil:
			bad = append(bad, label+": no statusLine")
		case !hook.IsOurStatusLine(eff.StatusLineCmd):
			bad = append(bad, label+": statusLine does not run rota statusline dump ("+eff.Path+")")
		default:
			parts = append(parts, label+": "+eff.Path)
		}
	}
	if !anyFile {
		return skip(name, "no Claude settings file in scope")
	}
	if len(bad) > 0 {
		return fail(name, strings.Join(bad, "; "), statuslineHint)
	}
	return pass(name, "rota statusline dump runs ("+strings.Join(parts, "; ")+")")
}

// switchCheck is D4's: with orchestrator.switchOnUsage on, the Stop hook must
// be installed and a second account must exist to move to. It reads config and
// settings files only.
func (d *runner) switchCheck() Check {
	const name = "switch"
	if !d.in.SwitchOnUsage {
		return skip(name, "orchestrator.switchOnUsage is off")
	}
	if d.in.ProjectRoot == "" {
		return skip(name, "not inside a rota project")
	}
	var with []string
	for _, a := range d.in.Accounts {
		if a.Dir != "" {
			with = append(with, a.Name)
		}
	}
	if len(with) < 2 {
		return fail(name, fmt.Sprintf("%d account with a dir in round.accounts, need 2 to move between", len(with)),
			"add a second account to round.accounts")
	}
	if h := d.stopHook(); h.Status != Pass {
		return fail(name, "the Stop hook is not installed: "+h.Detail, stopHookHint)
	}
	return pass(name, fmt.Sprintf("%d accounts, Stop hook installed", len(with)))
}

func (d *runner) stopHook() Check {
	const name = "stop-hook"
	if d.in.ProjectRoot == "" {
		return skip(name, "not inside a rota project")
	}
	if in, bad := d.optIn(); !in {
		return notInstalled(name, bad)
	}
	found := map[string]string{}
	var unreadable []string
	for _, f := range d.hooks().InScope() {
		if f.Err != nil {
			unreadable = append(unreadable, f.Path)
		}
		for ev, c := range f.Events {
			if _, ok := found[ev]; !ok {
				found[ev] = c
			}
		}
	}
	var missing []string
	for _, ev := range []string{"Stop", "SessionStart"} {
		if _, ok := found[ev]; !ok {
			missing = append(missing, ev)
		}
	}
	if len(missing) > 0 {
		detail := "no # rota-hook entry for " + strings.Join(missing, " and ")
		if len(unreadable) > 0 {
			detail += " (cannot read " + strings.Join(unreadable, ", ") + ")"
		}
		return fail(name, detail, stopHookHint)
	}
	for _, ev := range []string{"Stop", "SessionStart"} {
		first := strings.Fields(found[ev])[0]
		if _, ok := d.resolve(first); !ok {
			return fail(name, ev+" hook runs "+first+", which is not found", stopHookHint)
		}
	}
	return pass(name, "Stop and SessionStart hooks run rota")
}

// guardHook checks the worker guard (#702). It must sit in project or user
// scope: project-local is untracked, so a worker worktree never reads it.
func (d *runner) guardHook() Check {
	const name = "guard-hook"
	if d.in.ProjectRoot == "" {
		return skip(name, "not inside a rota project")
	}
	if in, bad := d.optIn(); !in {
		return notInstalled(name, bad)
	}
	var unreadable []string
	cmd := ""
	for _, f := range d.hooks().Worktree() {
		if f.Err != nil {
			unreadable = append(unreadable, f.Path)
		}
		if c, ok := f.Events[hook.EventGuard]; ok && cmd == "" {
			cmd = c
		}
	}
	if cmd == "" {
		detail := "no # rota-hook PreToolUse entry in project or user scope (project-local does not reach worker worktrees)"
		if len(unreadable) > 0 {
			detail += " (cannot read " + strings.Join(unreadable, ", ") + ")"
		}
		return fail(name, detail, guardHookHint)
	}
	first := strings.Fields(cmd)[0]
	if _, ok := d.resolve(first); !ok {
		return fail(name, "PreToolUse hook runs "+first+", which is not found", guardHookHint)
	}
	for _, k := range harness.Kinds() {
		if plan := harness.ResolveDiagnoser(k).Doctor(); plan.Unguarded && d.in.Kinds[k].Tiers {
			return Check{Name: name, Status: Warn, Detail: "PreToolUse guard covers Claude workers only; " + plan.Title + " workers are not guarded"}
		}
	}
	return pass(name, "PreToolUse guard runs rota")
}

// resolve finds the executable a hook command starts with; a path with a slash
// gets its ~ and $ expanded before Look sees it.
func (d *runner) resolve(word string) (string, bool) {
	if strings.Contains(word, "/") {
		word = d.expand(word)
	}
	return d.in.Look(word)
}
