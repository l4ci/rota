package hook

import (
	"strings"

	"github.com/l4ci/rota/internal/fsio"
)

// gitCall is one git invocation after its global options.
type gitCall struct {
	dir        string // effective directory (cwd with every -C applied)
	gitDir     string
	workTree   string
	sub        string
	args       []string
	dirUnknown bool
}

func (g *guard) git(args []string, sc scope) error {
	c := gitCall{dir: g.cwd, gitDir: sc.gitDir, workTree: sc.workTree}
	if sc.chdir {
		c.dir = sc.dir
	}
	c.dirUnknown = c.dir == ""
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
		return g.branch(c)
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

func (g *guard) branch(c gitCall) error {
	short, long, pos := flagsOf(c.args)
	force := short['f'] || long["--force"]
	del := short['d'] || long["--delete"]
	move := short['m'] || short['M'] || long["--move"]
	// -C (or -c with -f) force-copies over the destination, the same seat as -M (#3580).
	if short['C'] || ((short['c'] || long["--copy"]) && force) {
		move = true
	}
	switch {
	case short['D']:
		g.deny("git branch -D force-deletes a branch: use -d on a merged branch")
	case del && force:
		g.deny("git branch -d -f force-deletes a branch: use -d on a merged branch")
	case (force || move) && !del && len(pos) > 0:
		// -f rewrites the first branch; -m/-M renames the current branch or
		// the first of two, and -M overwrites the second. Each name touched
		// must be the worker's own and not a base (#3531).
		cur, own, err := g.branches(c.dir)
		if err != nil {
			return err
		}
		touched := pos[:1]
		if move {
			touched = pos
			if len(pos) == 1 {
				touched = []string{cur, pos[0]}
			}
		}
		for _, name := range touched {
			if g.isBase(name) || name != own {
				g.deny("git branch %s rewrites %s: a worker may only rewrite its own branch (%s)", branchFlag(short, long), name, own)
				return nil
			}
		}
	}
	return nil
}

func branchFlag(short map[rune]bool, long map[string]bool) string {
	switch {
	case short['C']:
		return "-C"
	case short['c'] || long["--copy"]:
		return "-c"
	case short['M']:
		return "-M"
	case short['m'] || long["--move"]:
		return "-m"
	}
	return "-f"
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
	if !fsio.SameDir(top, g.env.Worktree) {
		g.deny("git reset --hard outside this worktree (%s): it would discard work elsewhere", g.env.Worktree)
	}
	return nil
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
	if long["--prune"] {
		g.deny("git push --prune deletes every remote branch absent locally: not for workers")
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
	needBranch := force
	for _, spec := range pos {
		// A substitution (`...` or $(...)) leaves an empty word behind.
		if spec == "" || strings.ContainsAny(spec, "$`~{*?[") {
			return guardErr("git push refspec " + spec + " is not a literal")
		}
		if strings.HasPrefix(spec, ":") {
			g.deny("git push %s deletes a remote ref: not for workers", spec)
			return nil
		}
		if strings.HasPrefix(spec, "+") || pushesHead(spec) {
			needBranch = true
		}
	}
	// A plain push to a base-branch name needs no branch lookup: deny it now.
	for _, spec := range pos {
		if dst := pushDst(spec); !force && !strings.HasPrefix(spec, "+") && !pushesHead(spec) && g.isBase(dst) {
			g.deny("git push to %s: workers never land code on the base branch; open a PR", dst)
			return nil
		}
	}
	if !needBranch {
		return nil
	}
	cur, own, err := g.branches(c.dir)
	if err != nil {
		return err
	}
	for _, spec := range pos {
		forced := force || strings.HasPrefix(spec, "+")
		dst := pushDst(spec)
		if pushesHead(spec) {
			dst = cur
		}
		if forced && (g.isBase(dst) || dst != own) {
			g.deny("force-push to %s: a worker may force only its own branch (%s)", dst, own)
			return nil
		}
		if g.isBase(dst) {
			g.deny("git push to %s: workers never land code on the base branch; open a PR", dst)
			return nil
		}
	}
	return nil
}

// pushDst is the destination branch name of one push refspec.
func pushDst(spec string) string {
	spec = strings.TrimPrefix(spec, "+")
	src, dst, hasDst := strings.Cut(spec, ":")
	if !hasDst {
		dst = src
	}
	return strings.TrimPrefix(dst, "refs/heads/")
}

// pushesHead reports a refspec whose destination is the current branch: HEAD
// itself, or a bare `HEAD` source with no destination.
func pushesHead(spec string) bool {
	spec = strings.TrimPrefix(spec, "+")
	src, dst, hasDst := strings.Cut(spec, ":")
	return dst == "HEAD" || dst == "@" || ((src == "HEAD" || src == "@") && !hasDst)
}

// isBase reports a branch workers must never push to: main, master, or the
// project base the caller passed in GuardEnv.Base.
func (g *guard) isBase(name string) bool {
	return name == "main" || name == "master" || (g.env.Base != "" && name == g.env.Base)
}

// branches returns the branch checked out at dir and the worker's own branch,
// the one checked out in its worktree. They differ when -C or a cd points at
// another worktree, so a force from there must not pass as the worker's own.
func (g *guard) branches(dir string) (cur, own string, err error) {
	cur, err = g.env.Branch(dir)
	if err != nil || cur == "" {
		return "", "", guardErr("could not read the current branch for a forced push")
	}
	own = cur
	if g.env.Worktree != "" {
		own, err = g.env.Branch(g.env.Worktree)
		if err != nil || own == "" {
			return "", "", guardErr("could not read the worker's own branch for a forced push")
		}
	}
	return cur, own, nil
}

// forceUpstream: `git push --force[-with-lease]` with no refspec pushes the
// current branch to its upstream; only allow it when that is the same name.
func (g *guard) forceUpstream(c gitCall) error {
	cur, own, err := g.branches(c.dir)
	if err != nil {
		return err
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
	if g.isBase(name) || name != cur || cur != own {
		g.deny("force-push to upstream %s: a worker may force only its own branch (%s)", up, own)
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
