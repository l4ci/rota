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
	// Base is the project base branch ("" when unknown); main and master are
	// always treated as base branches.
	Base string
}

const maxGuardDepth = 8

var mentionsGit = regexp.MustCompile(`(^|[^A-Za-z0-9_-])(git|gh)([^A-Za-z0-9_-]|$)`)

// Guard returns "" when a worker may run the Bash command line, else the one-line
// reason it may not (#702). It is a guardrail against a mistaken agent, not a
// security boundary: a script that calls git is not seen.
//
// The effective directory follows cd, pushd/popd, `git -C`, and the chdir
// options of env (-C) and sudo (-D); GIT_DIR and GIT_WORK_TREE (inline or via
// env) redirect the git call they prefix. A cd inside ( ... ) is not scoped:
// `(cd /repo); git reset --hard` stays denied, though the second command runs
// in the slot's worktree. That false deny is the safe direction, and the parser
// flattens groups, so scoping them is not worth a second parser.
//
// It fails closed only when the line mentions git or gh and cannot be parsed or
// the repo state it needs cannot be read; any other parse failure passes.
func Guard(command string, env GuardEnv) string {
	g := &guard{env: env, cwd: env.Cwd, line: command}
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
	line   string // the whole command line, for the mentions-git check on a stdin-fed shell
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
	done := 0 // substitutions already judged
	// subs judges the substitutions found up to index n, in the directory the
	// commands before them left (#3730).
	subs := func(n int) error {
		for ; done < n && g.reason == ""; done++ {
			// A substitution is a subshell: its cd does not move the line.
			saved := g.cwd
			err := g.script(p.nested[done], depth+1)
			g.cwd = saved
			if err != nil {
				return err
			}
		}
		return nil
	}
	for i, w := range p.cmds {
		// A substitution runs before its command.
		if err := subs(p.nestedBy[i]); err != nil {
			return err
		}
		if g.reason != "" {
			return nil
		}
		if err := g.command(w, depth, scope{}); err != nil {
			return err
		}
		if g.reason != "" {
			return nil
		}
	}
	// What follows the last command (a redirect target) runs where the line ends.
	return subs(len(p.nested))
}

var envAssign = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// reserved words that may precede a command on the same line.
var shellKeywords = map[string]bool{
	"{": true, "}": true, "!": true, "if": true, "then": true, "else": true, "elif": true,
	"do": true, "while": true, "until": true, "fi": true, "done": true, "time": true,
}

// wrappers run their argument as a command. The set is the options that take a
// value and the count of leading positionals (timeout's duration).
//
// chdir is the option that changes the wrapped command's directory ("" when
// there is none): env -C, sudo -D (sudo -C is closefrom, not a directory).
var wrappers = map[string]struct {
	withArg map[string]bool
	pos     int
	chdir   string
}{
	"sudo":    {map[string]bool{"-u": true, "-g": true, "-C": true, "-h": true, "-p": true, "-r": true, "-t": true, "-U": true, "-D": true, "-R": true, "-T": true}, 0, "-D"},
	"doas":    {map[string]bool{"-u": true, "-C": true}, 0, ""},
	"env":     {map[string]bool{"-u": true, "-C": true, "-S": true}, 0, "-C"},
	"nohup":   {nil, 0, ""},
	"command": {nil, 0, ""},
	"builtin": {nil, 0, ""},
	"exec":    {map[string]bool{"-a": true}, 0, ""},
	"nice":    {map[string]bool{"-n": true}, 0, ""},
	"ionice":  {map[string]bool{"-c": true, "-n": true, "-p": true}, 0, ""},
	"setsid":  {nil, 0, ""},
	"stdbuf":  {map[string]bool{"-i": true, "-o": true, "-e": true}, 0, ""},
	"timeout": {map[string]bool{"-s": true, "-k": true}, 1, ""},
	"xargs":   {map[string]bool{"-n": true, "-I": true, "-P": true, "-d": true, "-L": true, "-E": true, "-s": true, "-a": true}, 0, ""},
}

// scope is what a prefix of one command changes for the command it wraps:
// a directory (env -C, sudo -D) and the GIT_DIR / GIT_WORK_TREE variables.
type scope struct {
	dir              string // meaningful when chdir is set; "" then means unknown
	chdir            bool
	gitDir, workTree string
}

// assign records an inline NAME=value that redirects git.
func (s *scope) assign(a string) {
	k, v, _ := strings.Cut(a, "=")
	switch k {
	case "GIT_DIR":
		s.gitDir = v
	case "GIT_WORK_TREE":
		s.workTree = v
	}
}

// setDir applies a chdir option's value on top of base, the directory the
// wrapped command would otherwise start in.
func (s *scope) setDir(base, d string) {
	if s.chdir {
		base = s.dir
	}
	s.chdir, s.dir = true, resolveDir(base, d)
}

var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "ash": true, "hush": true}

// multiCall names the multi-call binaries whose first argument is an applet:
// `busybox sh -c ...` is a shell like `sh -c ...`.
var multiCall = map[string]bool{"busybox": true, "toybox": true}

func (g *guard) command(words []string, depth int, sc scope) error {
	for len(words) > 0 && (envAssign.MatchString(words[0]) || shellKeywords[words[0]]) {
		if envAssign.MatchString(words[0]) {
			sc.assign(words[0])
		}
		words = words[1:]
	}
	if len(words) == 0 {
		return nil
	}
	prog := filepath.Base(words[0])
	rest := words[1:]
	if multiCall[prog] && len(rest) > 0 {
		// The applet is the real command: shell, wrapper (env, xargs), git, or
		// another multi-call binary.
		return g.command(rest, depth+1, sc)
	}
	switch {
	case prog == "cd":
		g.cd(rest)
		return nil
	case prog == "pushd":
		g.pushd(rest)
		return nil
	case prog == "popd":
		g.cwd = "" // the stack is not tracked
		return nil
	case prog == "eval":
		return g.nestedScript(strings.Join(rest, " "), depth)
	case shells[prog]:
		if s, ok := shellScript(rest); ok {
			return g.nestedScript(s, depth)
		}
		// A shell that reads its script from stdin (here-string, heredoc, pipe)
		// hides the commands from the parser (#3530).
		if shellReadsStdin(rest) && mentionsGit.MatchString(g.line) {
			return guardErr("a shell reading its script from stdin")
		}
		return nil
	case prog == "source" || prog == ".":
		// `source /dev/stdin` runs a script the parser never sees (#3530).
		for len(rest) > 0 && strings.HasPrefix(rest[0], "-") && rest[0] != "-" {
			rest = rest[1:] // skip -- and options
		}
		if len(rest) > 0 && isFdScriptPath(rest[0]) && mentionsGit.MatchString(g.line) {
			return guardErr("a shell reading its script from stdin")
		}
		return nil
	case prog == "git":
		return g.git(rest, sc)
	case prog == "gh":
		return g.gh(rest)
	}
	if w, ok := wrappers[prog]; ok {
		return g.command(g.skipWrapped(rest, w.withArg, w.pos, w.chdir, &sc), depth+1, sc)
	}
	return g.opaque(words, depth, sc)
}

// dataPrograms print or search their arguments and cannot run one (not sed, awk,
// man or less, which can): git or gh inside them is text.
var dataPrograms = map[string]bool{
	"echo": true, "printf": true, "grep": true, "egrep": true, "fgrep": true, "rg": true,
	"cat": true, "which": true, "type": true, "whereis": true, "head": true, "tail": true,
}

// rotaProseFlags are the rota flags whose value is text for a human (an issue
// title, a note body), not a command. Their value may mention git (#3631).
var rotaProseFlags = map[string]bool{
	"--title": true, "--body": true, "--summary": true, "--reason": true, "--gist": true,
	"--message": true, "--note": true, "--text": true, "--question": true,
	"--blocked": true, "--description": true,
}

// rgPre checks the command of `--pre <cmd>` and `--pre=<cmd>`, the one way
// ripgrep runs a program; the rest of rg is a data program.
func (g *guard) rgPre(args []string, depth int, sc scope) error {
	for i, a := range args {
		// No stop at `--`: it can be the value of `-e` or `-g`, and a stray
		// `--pre` pattern after it is a rarer miss than a hidden command.
		var v string
		switch {
		case a == "--pre" && i+1 < len(args):
			v = args[i+1]
		case strings.HasPrefix(a, "--pre="):
			v = strings.TrimPrefix(a, "--pre=")
		default:
			continue
		}
		if err := g.command(strings.Fields(v), depth+1, sc); err != nil {
			return err
		}
	}
	return nil
}

// opaque handles a program the guard does not know. Any program can run its
// arguments as a command (flock, watch, su -c, chroot, script -c, python -c
// os.system), and a list of those would never be complete (#3626). So it reads
// the arguments instead: a git or gh word is a command from there on, and an
// argument with spaces that mentions git or gh is parsed as a script.
func (g *guard) opaque(words []string, depth int, sc scope) error {
	prog := filepath.Base(words[0])
	if prog == "rg" {
		// `rg --pre <cmd>` runs <cmd> on every file it searches.
		return g.rgPre(words[1:], depth, sc)
	}
	if dataPrograms[prog] {
		return nil
	}
	// The prose exemption covers rota's own flag values only: the bare `rota`
	// command (not `./rota`, which may be any binary) and not after `--`, where
	// `rota review probe -- <prog> ...` runs <prog> (#3646).
	rota := words[0] == "rota"
	prose := false // the previous word is a rota prose flag awaiting its value
	for i, w := range words {
		if g.reason != "" {
			return nil
		}
		if rota && w == "--" {
			rota, prose = false, false
		}
		if rota {
			if prose {
				prose = false
				continue
			}
			if f, _, hasV := strings.Cut(w, "="); rotaProseFlags[f] {
				prose = !hasV
				continue
			}
		}
		if i > 0 {
			if b := filepath.Base(w); b == "git" || b == "gh" {
				if err := g.command(words[i:], depth+1, sc); err != nil {
					return err
				}
			}
		}
		if !strings.ContainsAny(w, " \t\n") || !mentionsGit.MatchString(w) {
			continue
		}
		if err := g.script(w, depth+1); err != nil {
			return err
		}
		// `--command=git push ...`: the script follows the first =.
		if _, v, ok := strings.Cut(w, "="); ok && g.reason == "" {
			if err := g.script(v, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// nestedScript runs the guard over a script given as an argument. A script
// built by substitution (`eval "$(...)"`, `bash -c "$(...)"`) is produced at
// run time, and the scanner drops the substitution's text from the word, so the
// line is refused when it has a substitution and mentions git.
func (g *guard) nestedScript(s string, depth int) error {
	if (strings.Contains(g.line, "$(") || strings.Contains(g.line, "`")) && mentionsGit.MatchString(g.line) {
		return guardErr("a shell script built by command substitution")
	}
	return g.script(s, depth+1)
}

// skipWrapped drops a wrapper's own options (and assignments, for env) so the
// wrapped command is left.
// A chdir option and NAME=value assignments are recorded in sc.
func (g *guard) skipWrapped(args []string, withArg map[string]bool, pos int, chdir string, sc *scope) []string {
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "--":
			i++
			return args[i:]
		case chdir != "" && (a == chdir || a == "--chdir"):
			if i+1 < len(args) {
				sc.setDir(g.cwd, args[i+1])
			} else {
				sc.chdir, sc.dir = true, ""
			}
			i += 2
		case chdir != "" && strings.HasPrefix(a, "--chdir="):
			sc.setDir(g.cwd, strings.TrimPrefix(a, "--chdir="))
			i++
		case withArg[a]:
			i += 2
		case strings.HasPrefix(a, "-") && len(a) > 1:
			i++
		case pos > 0 && !envAssign.MatchString(a):
			pos--
			i++
		default:
			if envAssign.MatchString(a) {
				sc.assign(a)
				i++
				continue
			}
			return args[i:]
		}
	}
	return nil
}

// shellScript finds the script of `sh -c '<script>'`, with options like -lc
// or `-o pipefail` before the -c.
func shellScript(args []string) (string, bool) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		isOpt := (strings.HasPrefix(a, "-") || strings.HasPrefix(a, "+")) && len(a) > 1
		if !isOpt {
			return "", false
		}
		switch {
		case strings.HasPrefix(a, "--"):
			if a == "--rcfile" || a == "--init-file" {
				i++
			}
		case strings.HasPrefix(a, "-") && strings.Contains(a, "c"):
			if i+1 < len(args) {
				return args[i+1], true
			}
			return "", false
		case strings.HasSuffix(a, "o") || strings.HasSuffix(a, "O"):
			i++ // -o / -O take an option name
		}
	}
	return "", false
}

// shellReadsStdin reports a shell call with neither -c nor a script file, so it
// reads the script from stdin; `-s` forces that even with positional arguments.
func shellReadsStdin(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-" {
			return true // a lone - reads the script from stdin
		}
		isOpt := (strings.HasPrefix(a, "-") || strings.HasPrefix(a, "+")) && len(a) > 1
		if !isOpt {
			return isFdScriptPath(a) // a script file, unless it is an fd
		}
		switch {
		case strings.HasPrefix(a, "--"):
			if a == "--rcfile" || a == "--init-file" {
				i++
			}
		case strings.HasPrefix(a, "-") && strings.Contains(a, "s"):
			return true
		case strings.HasSuffix(a, "o") || strings.HasSuffix(a, "O"):
			i++
		}
	}
	return true
}

// isFdScriptPath reports a script path the guard cannot read: standard input
// or any other file descriptor (`bash /dev/fd/3 3<<<'...'`, a process
// substitution at /dev/fd/63).
func isFdScriptPath(p string) bool {
	p = filepath.Clean(p)
	if strings.HasPrefix(p, "/dev/fd/") {
		return p != "/dev/fd/"
	}
	if p == "/dev/stdin" {
		return true
	}
	// /proc/<pid>/fd/N and /proc/self/fd/N
	if !strings.HasPrefix(p, "/proc/") {
		return false
	}
	i := strings.LastIndex(p, "/fd/")
	return i > 0 && i+4 < len(p)
}

// pushd: options come before the directory and end at `--`. -n leaves the
// directory alone. Anything the guard does not model (no argument, +N/-N stack
// rotation, other options, extra words) makes the directory unknown, which
// fails closed.
func (g *guard) pushd(args []string) {
	noChdir := false
	i := 0
	for ; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			i++
			break
		}
		if a == "-n" {
			noChdir = true
			continue
		}
		if len(a) > 1 && (a[0] == '-' || a[0] == '+') {
			g.cwd = ""
			return
		}
		break
	}
	rest := args[i:]
	switch {
	case noChdir && len(rest) <= 1:
		return
	case len(rest) != 1:
		g.cwd = ""
	default:
		g.cwd = resolveDir(g.cwd, rest[0])
	}
}

func (g *guard) cd(args []string) {
	var dir string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") || a == "-" {
			dir = a
			break
		}
	}
	g.cwd = resolveDir(g.cwd, dir)
}

// resolveDir is base moved to dir, or "" when dir is not a literal path
// (`cd -`, `$VAR`, `~`) or base is unknown.
func resolveDir(base, dir string) string {
	if dir == "" || dir == "-" || strings.ContainsAny(dir, "$~") {
		return ""
	}
	return joinDir(base, dir)
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
