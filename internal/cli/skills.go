package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/skills"
	"github.com/l4ci/rota/internal/version"
	"github.com/l4ci/rota/internal/worker"
)

// The `rota skills` group (F6a): install the skills embedded in the binary for
// Claude Code and Codex. Every verb runs without .rota/.

func skillsCommands() *Command {
	return &Command{Name: "skills", Summary: "install the embedded skills for Claude Code and Codex", Subs: []*Command{
		{Name: "install", Summary: "write the skills into the agent skill directories", Verb: skillsVerb(skillsInstall, skills.User, true, true)},
		{Name: "update", Summary: "refresh the skill directories that already have a manifest", Verb: skillsVerb(skillsUpdate, "", true, true)},
		{Name: "uninstall", Summary: "remove what rota installed", Verb: skillsVerb(skillsUninstall, skills.User, true, false)},
		{Name: "status", Summary: "compare the installed skills with this binary", Verb: skillsVerb(skillsStatus, "", false, false)},
		{Name: "list", Summary: "name the skills embedded in this binary", Verb: skillsList},
		{Name: "get", Summary: "print an embedded skill or one of its references", Verb: skillsGet},
	}}
}

// skillsArgs are the parsed flags of a skills verb.
type skillsArgs struct {
	scope, agent   string
	overwrite      bool
	stub, full     bool
	currentAccount bool
}

func skillsVerb(run func(*Ctx, skillsArgs) (Result, error), defScope string, overwrite, stub bool) func(*flag.FlagSet) RunFunc {
	return func(fs *flag.FlagSet) RunFunc {
		var a skillsArgs
		scopeUsage := "user or project (default user)"
		if defScope == "" {
			scopeUsage = "user or project (default both)"
		}
		fs.StringVar(&a.scope, "scope", defScope, scopeUsage)
		fs.StringVar(&a.agent, "agent", "all", "claude, codex or all")
		if overwrite {
			fs.BoolVar(&a.overwrite, "overwrite", false, "replace edited and unmanaged files")
		}
		if stub {
			fs.BoolVar(&a.stub, "stub", false, "write a SKILL.md that runs `rota skills get <name>` instead of the full text (the install default; update keeps each root's recorded mode unless a flag is given)")
			fs.BoolVar(&a.full, "full", false, "write the full skill text, for harnesses that cannot run a command when a skill loads")
		}
		fs.BoolVar(&a.currentAccount, "current-account", false, "user scope: only the current Claude config dir, not every round.accounts dir")
		return func(c *Ctx, args []string) (Result, error) {
			if err := noArgs(args); err != nil {
				return Result{}, err
			}
			if a.scope != "" && a.scope != skills.User && a.scope != skills.Project {
				return Result{}, Usage("--scope must be user or project, got %q", a.scope)
			}
			if a.agent != "claude" && a.agent != "codex" && a.agent != "all" {
				return Result{}, Usage("--agent must be claude, codex or all, got %q", a.agent)
			}
			if a.stub && a.full {
				return Result{}, Failed("--stub and --full are exclusive")
			}
			return run(c, a)
		}
	}
}

// mode is the install mode the flags ask for; "" leaves it to the verb's default.
func (a skillsArgs) mode() string {
	switch {
	case a.stub:
		return skills.ModeStub
	case a.full:
		return skills.ModeFull
	}
	return ""
}

// skillsEnv resolves the roots a verb works on.
//
// At user scope the Claude roots cover the current config dir plus every
// round.accounts configDir (skipped lists the configured dirs that do not
// exist), since each dir holds its own copy of the skills.
func skillsEnv(c *Ctx, a skillsArgs) (set *skills.Set, roots []skills.Root, skipped []string, err error) {
	set, err = skills.Embedded()
	if err != nil {
		return nil, nil, nil, err
	}
	home, claudeDirs, skipped := skillsDirs(c, a)
	top := ""
	if a.scope != skills.User {
		top = c.deps().Toplevel(c.Context())
	}
	roots, err = skills.RootsFor(a.scope, a.agent, home, claudeDirs, top)
	switch {
	case errors.Is(err, skills.ErrNoProject):
		return nil, nil, nil, Resolution("--scope project: the working directory is not in a git work tree")
	case errors.Is(err, skills.ErrNoHome):
		return nil, nil, nil, Resolution("%v", err).WithHint("set HOME (or CLAUDE_CONFIG_DIR for --agent claude), or use --scope project")
	case err != nil:
		return nil, nil, nil, Resolution("%v", err)
	}
	return set, roots, skipped, nil
}

// skillsDirs is the home and the Claude config dirs (see skillsClaudeDirs) a
// verb works on.
func skillsDirs(c *Ctx, a skillsArgs) (home string, claudeDirs, skipped []string) {
	home = c.deps().Getenv("HOME")
	if home == "" {
		home, _ = c.deps().UserHomeDir()
	}
	root := ""
	if !a.currentAccount {
		root, _ = c.Root()
	}
	claudeDirs, skipped = skillsClaudeDirs(c.deps().Getenv, home, root)
	return home, claudeDirs, skipped
}

// skillsPluginRoots are the installed rota plugin roots of the Claude config
// dirs that fit the scope ("" for both) and agent filters; top is the project
// root, "" outside a work tree.
func skillsPluginRoots(getenv func(string) string, claudeDirs []string, scope, agent, top string) ([]skills.Root, []error) {
	if agent != "" && agent != "all" && agent != skills.Claude {
		return nil, nil
	}
	var out []skills.Root
	all, errs := skills.PluginRoots(skills.PluginsDirs(getenv, claudeDirs), top)
	for _, r := range all {
		if scope == "" || scope == r.Scope {
			out = append(out, r)
		}
	}
	return out, errs
}

// skillsClaudeDirs is the Claude config dirs user-scope skills go to: the
// current one (CLAUDE_CONFIG_DIR, else ~/.claude), then each round.accounts
// configDir of the project at root ("" for none), de-duplicated with
// worker.SameConfigDir. An account dir that does not exist is returned in
// skipped, never created.
func skillsClaudeDirs(getenv func(string) string, home, root string) (dirs, skipped []string) {
	if cur := skills.ClaudeDir(getenv, home); cur != "" {
		dirs = append(dirs, cur)
	}
	if root == "" {
		return dirs, nil
	}
	for _, ac := range config.Accounts(config.Load(rotatree.Config(root))) {
		if ac.ConfigDir == "" {
			continue
		}
		dir := worker.ExpandConfigDir(ac.ConfigDir)
		dup := false
		for _, d := range append(append([]string{}, dirs...), skipped...) {
			dup = dup || worker.SameConfigDir(d, dir)
		}
		if dup {
			continue
		}
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			skipped = append(skipped, dir)
			continue
		}
		dirs = append(dirs, dir)
	}
	return dirs, skipped
}

// withSkipped reports the configured account dirs that were left out.
func withSkipped(res Result, skipped []string) Result {
	if len(skipped) == 0 {
		return res
	}
	if o, ok := res.Data.(*jsonx.Object); ok {
		o.Set("skipped", strs(skipped))
	}
	var lines []string
	if res.Text != "" {
		lines = append(lines, res.Text)
	}
	for _, d := range skipped {
		lines = append(lines, d+": config dir does not exist, skipped")
	}
	res.Text = strings.Join(lines, "\n")
	return res
}

// skillsErr maps a skills package failure: a busy lock is exit 6, anything
// else (a root that cannot be created or written) exit 5.
func skillsErr(err error) error {
	if errors.Is(err, fsio.ErrLockTimeout) {
		return err
	}
	return Unavailable("%v", err)
}

func skillsInstall(c *Ctx, a skillsArgs) (Result, error) {
	set, roots, skipped, err := skillsEnv(c, a)
	if err != nil {
		return Result{}, err
	}
	res, err := set.Install(roots, skills.Options{Version: version.Get().Version, Overwrite: a.overwrite, Mode: a.mode()})
	if err != nil {
		return Result{}, skillsErr(err)
	}
	out, err := skillsInstallResult(c, "install", res)
	return withSkipped(out, skipped), err
}

func skillsUpdate(c *Ctx, a skillsArgs) (Result, error) {
	set, roots, skipped, err := skillsEnv(c, a)
	if err != nil {
		return Result{}, err
	}
	res, err := set.Update(roots, skills.Options{Version: version.Get().Version, Overwrite: a.overwrite, Mode: a.mode()})
	if err != nil {
		return Result{}, skillsErr(err)
	}
	if len(res) == 0 {
		hint := "run: rota skills install"
		_, claudeDirs, _ := skillsDirs(c, a)
		top := ""
		if a.scope != skills.User {
			top = c.deps().Toplevel(c.Context())
		}
		if plugin, _ := skillsPluginRoots(c.deps().Getenv, claudeDirs, a.scope, a.agent, top); len(plugin) > 0 {
			hint = "the rota plugin is updated by Claude Code: claude plugin update rota@rota"
		}
		return withSkipped(Result{Data: knObj("roots", []any{}, "changed", false)}, skipped),
			Failed("no skills install found in scope").WithHint(hint)
	}
	out, err := skillsInstallResult(c, "update", res)
	return withSkipped(out, skipped), err
}

// skillsInstallResult renders install and update results, exit 4 when a path
// was kept.
func skillsInstallResult(c *Ctx, verb string, res []skills.RootResult) (Result, error) {
	rootsData := []any{}
	var text []string
	var kept []any
	changed := false
	blockedBy := ""
	for _, r := range res {
		files := []any{}
		counts := map[string]int{}
		var lines []string
		for _, f := range r.Files {
			counts[f.Status]++
			if f.Status == skills.Unchanged {
				if c.JSON {
					files = append(files, knObj("path", f.Path, "status", f.Status))
				}
				continue
			}
			files = append(files, knObj("path", f.Path, "status", f.Status))
			if f.Status == skills.Edited || f.Status == skills.Unmanaged {
				lines = append(lines, fmt.Sprintf("  %-9s %s", f.Status, f.Path))
			}
		}
		for _, p := range r.Kept {
			kept = append(kept, r.Path+"/"+p)
		}
		changed = changed || r.Changed
		if r.BlockedBy == skills.Edited || blockedBy == "" {
			blockedBy = r.BlockedBy
		}
		rootsData = append(rootsData, knObj("root", r.Path, "agent", r.Agent, "scope", r.Scope,
			"version", r.Version, "digest", r.Digest, "mode", r.Mode, "files", files))
		summary := fmt.Sprintf("%s (%s, %s, %s):", r.Path, r.Agent, r.Scope, r.Mode)
		for _, st := range []string{skills.Created, skills.Updated, skills.Replaced, skills.Removed, skills.Edited, skills.Unmanaged, skills.Unchanged} {
			if counts[st] > 0 {
				summary += fmt.Sprintf(" %d %s", counts[st], st)
			}
		}
		text = append(text, summary)
		text = append(text, lines...)
	}
	data := knObj("roots", rootsData)
	if len(kept) > 0 {
		data.Set("kept", kept)
	}
	data.Set("changed", changed)
	out := Result{Data: data, Text: strings.Join(text, "\n")}
	if blockedBy != "" {
		data.Set("blockedBy", blockedBy)
		return out, Refused("%d path(s) kept (%s)", len(kept), blockedBy).WithHint("run: rota skills " + verb + " --overwrite")
	}
	return out, nil
}

func skillsUninstall(c *Ctx, a skillsArgs) (Result, error) {
	_, roots, skipped, err := skillsEnv(c, a)
	if err != nil {
		return Result{}, err
	}
	res, err := skills.Uninstall(roots, skills.Options{Overwrite: a.overwrite})
	if err != nil {
		return Result{}, skillsErr(err)
	}
	rootsData := []any{}
	var text []string
	changed, keptAny := false, false
	for _, r := range res {
		if !r.Installed {
			continue
		}
		changed = changed || r.Changed
		keptAny = keptAny || len(r.Kept) > 0
		rootsData = append(rootsData, knObj("root", r.Path, "agent", r.Agent, "scope", r.Scope,
			"removed", strs(r.Removed), "kept", strs(r.Kept)))
		text = append(text, fmt.Sprintf("%s (%s, %s): %d removed, %d kept", r.Path, r.Agent, r.Scope, len(r.Removed), len(r.Kept)))
		for _, p := range r.Kept {
			text = append(text, "  edited    "+p)
		}
	}
	data := knObj("roots", rootsData, "changed", changed)
	out := Result{Data: data, Text: strings.Join(text, "\n")}
	if keptAny {
		data.Set("blockedBy", "edited")
		return withSkipped(out, skipped), Refused("edited files were kept").WithHint("run: rota skills uninstall --overwrite")
	}
	return withSkipped(out, skipped), nil
}

func skillsStatus(c *Ctx, a skillsArgs) (Result, error) {
	set, roots, skipped, err := skillsEnv(c, a)
	if err != nil {
		return Result{}, err
	}
	_, claudeDirs, _ := skillsDirs(c, a)
	top := ""
	if a.scope != skills.User {
		top = c.deps().Toplevel(c.Context())
	}
	pluginRoots, pluginErrs := skillsPluginRoots(c.deps().Getenv, claudeDirs, a.scope, a.agent, top)
	roots = append(roots, pluginRoots...)
	rep, err := set.Status(roots, version.Get().Version)
	if err != nil {
		return Result{}, skillsErr(err)
	}
	warnings := errStrings(pluginErrs)
	rootsData := []any{}
	text := []string{fmt.Sprintf("rota %s, skills %s (stub %s)", rep.Version, short(rep.Digest), short(rep.StubDigest))}
	for _, r := range rep.Roots {
		o := knObj("root", r.Path, "agent", r.Agent, "scope", r.Scope, "plugin", r.Plugin, "installed", r.Installed)
		line := fmt.Sprintf("%s (%s, %s): ", r.Path, r.Agent, r.Scope)
		if r.Plugin {
			line = fmt.Sprintf("%s (%s, %s, plugin): ", r.Path, r.Agent, r.Scope)
		}
		if !r.Installed {
			line += "not installed"
		} else {
			o.Set("version", r.Version)
			o.Set("digest", r.Digest)
			o.Set("mode", r.Mode)
			o.Set("changed", strs(r.Changed))
			line += "current"
			if !r.Current {
				want := rep.Digest
				if r.Mode == skills.ModeStub {
					want = rep.StubDigest
				}
				line = fmt.Sprintf("%sstale (installed %s, rota %s)", strings.TrimSuffix(line, "current"), short(r.Digest), short(want))
				if len(r.Changed) > 0 {
					line += ": " + strings.Join(r.Changed, ", ")
				}
			}
		}
		o.Set("current", r.Current)
		o.Set("edited", strs(r.Edited))
		o.Set("missing", strs(r.Missing))
		text = append(text, line)
		for _, p := range r.Edited {
			text = append(text, "  edited  "+p)
		}
		for _, p := range r.Missing {
			text = append(text, "  missing "+p)
		}
		rootsData = append(rootsData, o)
	}
	data := knObj("version", rep.Version, "digest", rep.Digest, "stubDigest", rep.StubDigest, "roots", rootsData)
	return withWarnings(withSkipped(Result{Data: data, Text: strings.Join(text, "\n")}, skipped), warnings), nil
}

// errStrings renders errors as their messages.
func errStrings(errs []error) []string {
	var out []string
	for _, e := range errs {
		out = append(out, e.Error())
	}
	return out
}

// withWarnings reports problems that did not stop the verb.
func withWarnings(res Result, warnings []string) Result {
	if len(warnings) == 0 {
		return res
	}
	if o, ok := res.Data.(*jsonx.Object); ok {
		o.Set("warnings", strs(warnings))
	}
	lines := []string{res.Text}
	for _, w := range warnings {
		lines = append(lines, "warning: "+w)
	}
	res.Text = strings.Join(lines, "\n")
	return res
}

func short(digest string) string {
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
}

func skillsList(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if err := noArgs(args); err != nil {
			return Result{}, err
		}
		set, err := skills.Embedded()
		if err != nil {
			return Result{}, err
		}
		v := version.Get().Version
		names := set.Skills()
		return Result{
			Data: knObj("version", v, "digest", set.Digest(), "skills", strs(names)),
			Text: fmt.Sprintf("rota %s\n%s", v, strings.Join(names, "\n")),
		}, nil
	}
}

func skillsGet(fs *flag.FlagSet) RunFunc {
	refs := fs.Bool("references", false, "list the skill's reference files")
	ref := fs.String("reference", "", "print one reference file")
	return func(c *Ctx, args []string) (Result, error) {
		name, err := oneArg(args, "skill name")
		if err != nil {
			return Result{}, err
		}
		if *refs && *ref != "" {
			return Result{}, Usage("--references and --reference are exclusive")
		}
		set, err := skills.Embedded()
		if err != nil {
			return Result{}, err
		}
		if _, ok := set.Skill(name); !ok {
			return Result{}, Usage("unknown skill %q", name).WithHint("known skills: " + strings.Join(set.Skills(), ", "))
		}
		if *refs {
			l := set.References(name)
			return Result{Data: knObj("name", name, "references", strs(l)), Text: strings.Join(l, "\n")}, nil
		}
		file := *ref
		if file != "" {
			if _, ok := set.Reference(name, file); !ok {
				return Result{}, Usage("%s has no reference %q", name, file).WithHint("known references: " + strings.Join(set.References(name), ", "))
			}
		}
		text, warn, err := set.Serve(name, file, skills.CacheDir(c.deps().Getenv), version.Get().Version)
		if err != nil {
			return Result{}, err
		}
		data := knObj("name", name)
		if file != "" {
			data.Set("reference", file)
		}
		data.Set("text", text)
		res := Result{Data: data, Text: strings.TrimSuffix(text, "\n")}
		if warn != "" {
			res = withWarnings(res, []string{warn})
		}
		return res, nil
	}
}

// skillsNeedUpdate is whether `rota skills update` has work: false only when
// at least one root is installed and every installed root is a current,
// untouched stub. Plugin roots are not this verb's. Anything unreadable counts
// as needing the update.
func skillsNeedUpdate(c *Ctx) bool {
	a := skillsArgs{agent: "all"}
	set, roots, _, err := skillsEnv(c, a)
	if err != nil {
		return true
	}
	rep, err := set.Status(roots, version.Get().Version)
	if err != nil {
		return true
	}
	installed := 0
	for _, r := range rep.Roots {
		if !r.Installed {
			continue
		}
		installed++
		if r.Mode != skills.ModeStub || !r.Current || len(r.Edited) > 0 || len(r.Missing) > 0 {
			return true
		}
	}
	return installed == 0
}
