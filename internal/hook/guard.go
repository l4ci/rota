package hook

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// GuardEnv is what the guard needs from the outside world. Every git question
// goes through a func so tests need no repository.
type GuardEnv struct {
	// Cwd is the working directory of the Bash call; Worktree is the slot's
	// own worktree ("" when it could not be found).
	Cwd, Worktree string
	// Branch is the current branch of the repo at dir; Toplevel its worktree
	// root; Upstream the branch the current branch tracks ("" when none).
	Branch   func(dir string) (string, error)
	Toplevel func(dir string) (string, error)
	Upstream func(dir string) (string, error)
}

const maxGuardDepth = 8

var mentionsGit = regexp.MustCompile(`(^|[^A-Za-z0-9_-])(git|gh)([^A-Za-z0-9_-]|$)`)

// Guard returns "" when a worker may run the Bash command line, else the one-line
// reason it may not (#702). It is a guardrail against a mistaken agent, not a
// security boundary: a script that calls git is not seen.
//
// It fails closed only when the line mentions git or gh and cannot be parsed or
// the repo state it needs cannot be read; any other parse failure passes.
func Guard(command string, env GuardEnv) string {
	g := &guard{env: env, cwd: env.Cwd}
	if err := g.script(command, 0); err != nil {
		if mentionsGit.MatchString(command) {
			return "guard could not parse: " + err.Error() + "; run the command plainly"
		}
		return ""
	}
	return g.reason
}

type guard struct {
	env    GuardEnv
	cwd    string // "" once a cd made it unknowable
	reason string
}

type guardErr string

func (e guardErr) Error() string { return string(e) }

func (g *guard) deny(format string, a ...any) {
	if g.reason == "" {
		g.reason = fmt.Sprintf(format, a...)
	}
}

func (g *guard) script(s string, depth int) error {
	if depth > maxGuardDepth {
		return guardErr("nested too deeply")
	}
	p, err := parseScript(s)
	if err != nil {
		return err
	}
	for _, n := range p.nested {
		if err := g.script(n, depth+1); err != nil {
			return err
		}
	}
	for _, w := range p.cmds {
		if err := g.command(w, depth); err != nil {
			return err
		}
		if g.reason != "" {
			return nil
		}
	}
	return nil
}

var envAssign = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// reserved words that may precede a command on the same line.
var shellKeywords = map[string]bool{
	"{": true, "}": true, "!": true, "if": true, "then": true, "else": true, "elif": true,
	"do": true, "while": true, "until": true, "fi": true, "done": true, "time": true,
}

// wrappers run their argument as a command. The set is the options that take a
// value and the count of leading positionals (timeout's duration).
var wrappers = map[string]struct {
	withArg map[string]bool
	pos     int
}{
	"sudo":    {map[string]bool{"-u": true, "-g": true, "-C": true, "-h": true, "-p": true, "-r": true, "-t": true, "-U": true, "-D": true, "-R": true, "-T": true}, 0},
	"doas":    {map[string]bool{"-u": true, "-C": true}, 0},
	"env":     {map[string]bool{"-u": true, "-C": true, "-S": true}, 0},
	"nohup":   {nil, 0},
	"command": {nil, 0},
	"builtin": {nil, 0},
	"exec":    {map[string]bool{"-a": true}, 0},
	"nice":    {map[string]bool{"-n": true}, 0},
	"ionice":  {map[string]bool{"-c": true, "-n": true, "-p": true}, 0},
	"setsid":  {nil, 0},
	"stdbuf":  {map[string]bool{"-i": true, "-o": true, "-e": true}, 0},
	"timeout": {map[string]bool{"-s": true, "-k": true}, 1},
	"xargs":   {map[string]bool{"-n": true, "-I": true, "-P": true, "-d": true, "-L": true, "-E": true, "-s": true, "-a": true}, 0},
}

var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true}

func (g *guard) command(words []string, depth int) error {
	for len(words) > 0 && (envAssign.MatchString(words[0]) || shellKeywords[words[0]]) {
		words = words[1:]
	}
	if len(words) == 0 {
		return nil
	}
	prog := filepath.Base(words[0])
	rest := words[1:]
	switch {
	case prog == "cd":
		g.cd(rest)
		return nil
	case prog == "eval":
		return g.script(strings.Join(rest, " "), depth+1)
	case shells[prog]:
		if s, ok := shellScript(rest); ok {
			return g.script(s, depth+1)
		}
		return nil
	case prog == "git":
		return g.git(rest)
	case prog == "gh":
		g.gh(rest)
		return nil
	}
	if w, ok := wrappers[prog]; ok {
		return g.command(skipWrapped(rest, w.withArg, w.pos), depth+1)
	}
	return nil
}

// skipWrapped drops a wrapper's own options (and assignments, for env) so the
// wrapped command is left.
func skipWrapped(args []string, withArg map[string]bool, pos int) []string {
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "--":
			i++
			return args[i:]
		case withArg[a]:
			i += 2
		case strings.HasPrefix(a, "-") && len(a) > 1:
			i++
		case pos > 0 && !envAssign.MatchString(a):
			pos--
			i++
		default:
			if envAssign.MatchString(a) {
				i++
				continue
			}
			return args[i:]
		}
	}
	return nil
}

// shellScript finds the script of `sh -c '<script>'`, with options like -lc.
func shellScript(args []string) (string, bool) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "c") {
			if i+1 < len(args) {
				return args[i+1], true
			}
			return "", false
		}
		if !strings.HasPrefix(a, "-") {
			return "", false
		}
	}
	return "", false
}

func (g *guard) cd(args []string) {
	var dir string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") || a == "-" {
			dir = a
			break
		}
	}
	if dir == "" || dir == "-" || strings.ContainsAny(dir, "$~") {
		g.cwd = ""
		return
	}
	g.cwd = joinDir(g.cwd, dir)
}

func joinDir(base, dir string) string {
	if filepath.IsAbs(dir) {
		return filepath.Clean(dir)
	}
	if base == "" {
		return ""
	}
	return filepath.Join(base, dir)
}

func (g *guard) gh(args []string) {
	var pos []string
	for i := 0; i < len(args) && len(pos) < 2; i++ {
		a := args[i]
		switch {
		case a == "-R" || a == "--repo" || a == "--hostname":
			i++
		case strings.HasPrefix(a, "-"):
		default:
			pos = append(pos, a)
		}
	}
	if len(pos) == 2 && pos[0] == "pr" && pos[1] == "merge" {
		g.deny("workers never merge: gh pr merge is the orchestrator's seat")
	}
}

// gitCall is one git invocation after its global options.
type gitCall struct {
	dir        string // effective directory (cwd with every -C applied)
	gitDir     string
	workTree   string
	sub        string
	args       []string
	dirUnknown bool
}

func (g *guard) git(args []string) error {
	c := gitCall{dir: g.cwd, dirUnknown: g.cwd == ""}
	i := 0
loop:
	for i < len(args) {
		a := args[i]
		switch {
		case a == "-C":
			if i+1 >= len(args) {
				return guardErr("git -C without a directory")
			}
			c.chdir(args[i+1])
			i += 2
		case a == "-c" || a == "--super-prefix" || a == "--config-env" || a == "--namespace":
			i += 2
		case a == "--git-dir" || a == "--work-tree":
			if i+1 >= len(args) {
				return guardErr("git " + a + " without a path")
			}
			c.set(a, args[i+1])
			i += 2
		case strings.HasPrefix(a, "--git-dir=") || strings.HasPrefix(a, "--work-tree="):
			k, v, _ := strings.Cut(a, "=")
			c.set(k, v)
			i++
		case strings.HasPrefix(a, "-"):
			i++
		default:
			break loop
		}
	}
	if i >= len(args) {
		return nil
	}
	c.sub, c.args = args[i], args[i+1:]
	switch c.sub {
	case "push":
		return g.push(c)
	case "reset":
		return g.reset(c)
	case "branch":
		g.branch(c)
	case "clean":
		g.clean(c)
	case "add":
		g.add(c)
	}
	return nil
}

func (c *gitCall) chdir(d string) {
	if c.dirUnknown {
		return
	}
	c.dir = joinDir(c.dir, d)
	c.dirUnknown = c.dir == ""
}

func (c *gitCall) set(flag, v string) {
	if flag == "--git-dir" {
		c.gitDir = v
	} else {
		c.workTree = v
	}
}

// flagsOf returns the letters of every short option cluster (`-fd` is f, d),
// and the long options, among args before a `--`.
func flagsOf(args []string) (short map[rune]bool, long map[string]bool, positional []string) {
	short, long = map[rune]bool{}, map[string]bool{}
	rest := false
	for _, a := range args {
		switch {
		case rest:
			positional = append(positional, a)
		case a == "--":
			rest = true
		case strings.HasPrefix(a, "--"):
			long[strings.SplitN(a, "=", 2)[0]] = true
		case strings.HasPrefix(a, "-") && len(a) > 1:
			for _, r := range a[1:] {
				short[r] = true
			}
		default:
			positional = append(positional, a)
		}
	}
	return short, long, positional
}

func (g *guard) add(c gitCall) {
	short, long, pos := flagsOf(c.args)
	if short['A'] || long["--all"] {
		g.deny("git add -A/--all stages every file: stage explicit paths")
		return
	}
	for _, p := range pos {
		if p == "." || p == "./" || p == ":/" {
			g.deny("git add . stages every file: stage explicit paths")
			return
		}
	}
}

func (g *guard) clean(c gitCall) {
	short, long, _ := flagsOf(c.args)
	if short['n'] || long["--dry-run"] {
		return
	}
	if short['f'] || long["--force"] {
		g.deny("git clean -f deletes untracked files: not for workers")
	}
}

func (g *guard) branch(c gitCall) {
	short, long, _ := flagsOf(c.args)
	force := short['f'] || long["--force"]
	del := short['d'] || long["--delete"]
	switch {
	case short['D']:
		g.deny("git branch -D force-deletes a branch: use -d on a merged branch")
	case del && force:
		g.deny("git branch -d -f force-deletes a branch: use -d on a merged branch")
	}
}

func (g *guard) reset(c gitCall) error {
	_, long, _ := flagsOf(c.args)
	if !long["--hard"] {
		return nil
	}
	if g.env.Worktree == "" {
		return guardErr("could not find this slot's worktree for git reset --hard")
	}
	dir := c.dir
	if c.workTree != "" {
		dir = joinDir(c.dir, c.workTree)
	} else if c.gitDir != "" {
		return guardErr("git --git-dir with reset --hard")
	}
	if dir == "" {
		return guardErr("git reset --hard in a directory the guard cannot resolve")
	}
	top, err := g.env.Toplevel(dir)
	if err != nil {
		return guardErr("could not resolve the repo for git reset --hard: " + err.Error())
	}
	if !sameDir(top, g.env.Worktree) {
		g.deny("git reset --hard outside this worktree (%s): it would discard work elsewhere", g.env.Worktree)
	}
	return nil
}

func sameDir(a, b string) bool {
	norm := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = r
		}
		return filepath.Clean(p)
	}
	return norm(a) == norm(b)
}

func (g *guard) push(c gitCall) error {
	short, long, pos := flagsOf(pushArgs(c.args))
	if long["--mirror"] {
		g.deny("git push --mirror overwrites every remote ref")
		return nil
	}
	if short['d'] || long["--delete"] {
		g.deny("git push --delete removes a remote ref: not for workers")
		return nil
	}
	force := short['f'] || long["--force"] || long["--force-with-lease"] || long["--force-if-includes"]
	if len(pos) > 0 {
		pos = pos[1:] // the remote
	}
	if len(pos) == 0 {
		if !force {
			return nil
		}
		if long["--all"] {
			g.deny("git push --force --all rewrites every branch")
			return nil
		}
		return g.forceUpstream(c)
	}
	needBranch := false
	for _, spec := range pos {
		if strings.HasPrefix(spec, ":") {
			g.deny("git push %s deletes a remote ref: not for workers", spec)
			return nil
		}
		if force || strings.HasPrefix(spec, "+") {
			needBranch = true
		}
	}
	if !needBranch {
		return nil
	}
	cur, err := g.env.Branch(c.dir)
	if err != nil || cur == "" {
		return guardErr("could not read the current branch for a forced push")
	}
	for _, spec := range pos {
		forced := force || strings.HasPrefix(spec, "+")
		if !forced {
			continue
		}
		spec = strings.TrimPrefix(spec, "+")
		src, dst, hasDst := strings.Cut(spec, ":")
		if !hasDst {
			dst = src
		}
		dst = strings.TrimPrefix(dst, "refs/heads/")
		if dst == "HEAD" || (src == "HEAD" && !hasDst) {
			dst = cur
		}
		if dst == "main" || dst == "master" || dst != cur {
			g.deny("force-push to %s: a worker may force only its own branch (%s)", dst, cur)
			return nil
		}
	}
	return nil
}

// forceUpstream: `git push --force[-with-lease]` with no refspec pushes the
// current branch to its upstream; only allow it when that is the same name.
func (g *guard) forceUpstream(c gitCall) error {
	cur, err := g.env.Branch(c.dir)
	if err != nil || cur == "" {
		return guardErr("could not read the current branch for a forced push")
	}
	up, err := g.env.Upstream(c.dir)
	if err != nil {
		return guardErr("could not read the upstream for a forced push")
	}
	if up == "" {
		return nil
	}
	name := up
	if _, after, ok := strings.Cut(up, "/"); ok {
		name = after
	}
	if name == "main" || name == "master" || name != cur {
		g.deny("force-push to upstream %s: a worker may force only its own branch (%s)", up, cur)
	}
	return nil
}

// pushArgs drops the values of push options that take one, so they are not read
// as the remote or a refspec.
func pushArgs(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-o", "--push-option", "--repo", "--receive-pack", "--exec":
			i++
		default:
			out = append(out, args[i])
		}
	}
	return out
}
