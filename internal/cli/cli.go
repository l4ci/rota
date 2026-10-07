// Package cli is the rota command dispatcher. It owns the global conventions
// in docs/contributing/contract/cli-conventions.md (global flags, the --json envelope,
// the stderr format and the exit codes); verbs return data or an *Error and
// never print those parts themselves.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/repos"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/tui"
)

// Command is a group (Subs) or a verb (Verb) in the rota tree.
type Command struct {
	Name    string
	Summary string
	// Repo marks a verb that takes the global --repo flag.
	Repo bool
	// Verb defines the verb's flags on fs and returns the function that
	// runs with the parsed values. Nil for a group.
	Verb func(fs *flag.FlagSet) RunFunc
	// View, when set, opens the verb's terminal screen under --ui: it builds
	// the screen from the verb's own Result. Nil means --ui is refused.
	View ViewFunc
	// UISub, on a group, names the verb that `rota <group> --ui` runs.
	UISub string
	Subs  []*Command
}

// ViewFunc builds the --ui screen from a verb's Result.
type ViewFunc func(c *Ctx, res Result) (tui.Model, error)

// RunFunc runs a verb with its positional args.
type RunFunc func(c *Ctx, args []string) (Result, error)

// Result is a verb's output: Data goes into the --json envelope, Text is
// printed otherwise. Data nil means {}. A verb failing with exit 1 or 4 may
// return a Result too: its answer, or what blocked it and what it changed.
type Result struct {
	Data any
	Text string
}

// Ctx is what a verb sees of the invocation.
type Ctx struct {
	Path   string // "rota group verb", the prefix of every stderr line
	JSON   bool
	Repo   string // the --repo value; resolve it with RepoPath
	Stdin  io.Reader
	Stdout io.Writer // for passthrough verbs only; others return Text
	Stderr io.Writer
	Deps   *Deps // what the verbs reach outside the process; nil means the real ones

	ctx      context.Context // set by run: cancelled on SIGINT and SIGTERM
	warnings []string

	// dashAt is how many positional arguments came before a bare "--", -1
	// when there was none. Verbs that run a command after "--" need to tell.
	dashAt int
}

// Context is the verb's context. run cancels it on SIGINT and SIGTERM, so a
// Ctrl-C reaches the forge and git calls in flight.
func (c *Ctx) Context() context.Context {
	if c.ctx == nil {
		return context.Background()
	}
	return c.ctx
}

// Warn records a warning: it goes to stderr now and into the envelope.
func (c *Ctx) Warn(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	c.warnings = append(c.warnings, msg)
	fmt.Fprintf(c.Stderr, "%s: warning: %s\n", c.Path, msg)
}

// Root finds the project root: the nearest directory at or above the
// working directory that holds .rota/.
func (c *Ctx) Root() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if rotatree.Exists(dir) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", Resolution("no .rota/ directory here or in any parent").WithHint("run: rota init")
		}
		dir = parent
	}
}

// RepoPath resolves --repo to the sub-repo's absolute path through
// .rota/repos.json (paths there are relative to the project root). It returns
// "" when --repo was not given, and a resolution error (exit 3) outside
// umbrella mode or for a name that is not registered.
func (c *Ctx) RepoPath() (string, error) {
	if c.Repo == "" {
		return "", nil
	}
	_, repos, err := c.Repos()
	if err != nil {
		return "", err
	}
	if len(repos) == 0 {
		return "", Resolution("--repo %s: not in umbrella mode (no sub-repos in .rota/repos.json)", c.Repo)
	}
	p, ok := repos[c.Repo]
	if !ok {
		return "", Resolution("--repo %s is not registered in .rota/repos.json", c.Repo)
	}
	return p, nil
}

// Repos returns the project root and the registered sub-repos of .rota/repos.json
// as name to absolute path (symlinks resolved when the path exists). The map
// is empty outside umbrella mode.
func (c *Ctx) Repos() (root string, paths map[string]string, err error) {
	root, err = c.Root()
	if err != nil {
		return "", nil, err
	}
	return root, repos.Paths(root), nil
}

// Repo is one registered sub-repo (see internal/repos).
type Repo = repos.Repo

// RepoList is Repos in registry order.
func (c *Ctx) RepoList() (root string, list []Repo, err error) {
	root, err = c.Root()
	if err != nil {
		return "", nil, err
	}
	return root, repos.Load(root), nil
}

// globals are the flags every verb accepts (docs/contributing/contract/cli-conventions.md,
// Invocation): before the verb they are the only flags allowed, after it they
// are parsed together with the verb's own flags.
type globals struct {
	json, help, version, ui bool
	cwd, repo               string
}

// register adds the globals to fs. preVerb adds --version and --repo, which
// before the verb are always accepted; after it, --repo only on repo verbs.
func (g *globals) register(fs *flag.FlagSet, preVerb, repo bool) {
	fs.BoolVar(&g.json, "json", false, "machine output: one JSON envelope on stdout")
	fs.BoolVar(&g.ui, "ui", false, "open the verb's terminal view")
	fs.BoolVar(&g.help, "h", false, "help")
	fs.BoolVar(&g.help, "help", false, "help")
	fs.StringVar(&g.cwd, "C", "", "run as if started in `dir`")
	fs.StringVar(&g.cwd, "cwd", "", "run as if started in `dir`")
	if preVerb || repo {
		fs.StringVar(&g.repo, "repo", "", "umbrella sub-repo `name`")
	}
	if preVerb {
		fs.BoolVar(&g.version, "version", false, "print the rota version")
	}
}

func (g *globals) merge(o globals) {
	g.json = g.json || o.json
	g.ui = g.ui || o.ui
	g.help = g.help || o.help
	g.version = g.version || o.version
	if o.cwd != "" {
		g.cwd = o.cwd
	}
	if o.repo != "" {
		g.repo = o.repo
	}
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// isFlag: a token starting with "-", except "-" alone (stdin).
func isFlag(tok string) bool { return len(tok) > 1 && tok[0] == '-' }

// parseFlag parses the flag at args[i] into fs and returns how many tokens
// it used. A value flag always takes the next token; booleans never do.
func parseFlag(fs *flag.FlagSet, args []string, i int) (int, error) {
	name := strings.TrimPrefix(strings.TrimPrefix(args[i], "-"), "-")
	name, val, hasVal := strings.Cut(name, "=")
	if name == "" || name[0] == '-' {
		return 0, Usage("bad flag syntax %q", args[i])
	}
	f := fs.Lookup(name)
	if f == nil {
		return 0, Usage("unknown flag %q", args[i])
	}
	used := 1
	if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
		if !hasVal {
			val = "true"
		}
	} else if !hasVal {
		if i+1 >= len(args) {
			return 0, Usage("flag --%s needs a value", name)
		}
		val, used = args[i+1], 2
	}
	if err := fs.Set(name, val); err != nil {
		return 0, Usage("invalid value %q for --%s", val, name)
	}
	return used, nil
}

// Main runs rota with the default command tree and returns the exit code.
func Main(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	// Hidden: test/hv-hybrid asks the binary which verbs it implements.
	if len(args) == 1 && args[0] == "__verbs" {
		for _, v := range VerbPaths(Tree()) {
			fmt.Fprintln(stdout, v)
		}
		return ExitOK
	}
	return run(Tree(), defaultDeps(), args, stdin, stdout, stderr)
}

func run(root *Command, deps *Deps, args []string, stdin io.Reader, stdout, stderr io.Writer) (code int) {
	// Until the arguments parse, an error answers in JSON if any token
	// before "--" is exactly --json.
	if deps != nil && deps.ReadCache != nil {
		// One invocation's reads are shared; the next starts from the forge.
		deps.ReadCache = tracker.NewReadCache()
	}
	c := &Ctx{Path: "rota", Stdin: stdin, Stdout: stdout, Stderr: stderr, Deps: deps, JSON: containsJSON(args), dashAt: -1}
	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	c.ctx = sigCtx
	defer func() {
		if r := recover(); r != nil {
			code = fail(c, stdout, asError(fmt.Errorf("panic: %v", r)))
		}
	}()

	// Before the verb: command words and global flags only.
	var g globals
	pre := newFlagSet("rota")
	g.register(pre, true, false)
	cmd := root
	i := 0
	// The root has a verb of its own (bare rota), yet global flags still come
	// before the command word.
	for ; i < len(args) && (cmd.Verb == nil || descends(cmd, args[i]) || (cmd == root && isFlag(args[i]))); i++ {
		tok := args[i]
		switch {
		case tok == "--":
			return fail(c, stdout, Usage("unexpected -- before the verb"))
		case isFlag(tok):
			n, err := parseFlag(pre, args, i)
			if err != nil {
				return fail(c, stdout, err)
			}
			i += n - 1
		default:
			sub := cmd.sub(tok)
			if sub == nil {
				return fail(c, stdout, Usage("unknown command %q", tok).WithHint("run: "+c.Path+" --help"))
			}
			cmd = sub
			c.Path += " " + cmd.Name
		}
	}
	if g.ui && cmd.Verb == nil && cmd.UISub != "" {
		cmd = cmd.sub(cmd.UISub)
		c.Path += " " + cmd.Name
	}
	if g.version && cmd == root {
		cmd = root.sub("version")
		c.Path = "rota version"
	}

	// After the verb: verb and global flags, mixed with positional args.
	var runVerb RunFunc
	var verbFlags *flag.FlagSet
	var positional []string
	if cmd.Verb != nil {
		verbFlags = newFlagSet(c.Path)
		runVerb = cmd.Verb(verbFlags)
		var post globals
		post.register(verbFlags, false, cmd.Repo)
		for ; i < len(args); i++ {
			tok := args[i]
			if tok == "--" {
				c.dashAt = len(positional)
				positional = append(positional, args[i+1:]...)
				break
			}
			if !isFlag(tok) {
				positional = append(positional, tok)
				continue
			}
			n, err := parseFlag(verbFlags, args, i)
			if err != nil {
				return fail(c, stdout, err)
			}
			i += n - 1
		}
		g.merge(post)
	}
	c.JSON = g.json

	if g.help {
		return ok(c, stdout, helpResult(c.Path, cmd, verbFlags))
	}
	if cmd.Verb == nil {
		if cmd == root {
			return fail(c, stdout, Usage("missing command").WithHint("run: rota --help"))
		}
		return fail(c, stdout, Usage("missing verb; one of: %s", strings.Join(cmd.subNames(), ", ")))
	}
	if g.repo != "" && !cmd.Repo {
		return fail(c, stdout, Usage("unknown flag \"--repo\""))
	}
	if g.ui {
		if err := uiRefuse(c, cmd); err != nil {
			return fail(c, stdout, err)
		}
	}
	c.Repo = g.repo
	if g.cwd != "" {
		if err := os.Chdir(g.cwd); err != nil {
			return fail(c, stdout, Resolution("cannot use -C %s: %v", g.cwd, unwrapPathErr(err)))
		}
	}
	physicalCwd()
	if err := legacyStateStop(c.Path); err != nil {
		return fail(c, stdout, err)
	}
	// An unregistered --repo is exit 3 on every repo-scoped verb, ahead of
	// the verb's own checks (contract rule 9).
	if c.Repo != "" {
		if _, err := c.RepoPath(); err != nil {
			return fail(c, stdout, err)
		}
	}
	res, err := runVerb(c, positional)
	if err != nil {
		return failWith(c, stdout, err, res)
	}
	if g.ui {
		return uiFinish(c, stdout, cmd, res)
	}
	return ok(c, stdout, res)
}

// containsJSON lets a failed global-flag parse still answer in JSON.
func containsJSON(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "--json" {
			return true
		}
	}
	return false
}

func unwrapPathErr(err error) error {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

func (cmd *Command) sub(name string) *Command {
	for _, s := range cmd.Subs {
		if s.Name == name {
			return s
		}
	}
	return nil
}

func (cmd *Command) subNames() []string {
	names := make([]string, 0, len(cmd.Subs))
	for _, s := range cmd.Subs {
		names = append(names, s.Name)
	}
	sort.Strings(names)
	return names
}

func ok(c *Ctx, stdout io.Writer, res Result) int {
	if !c.JSON {
		if res.Text != "" {
			fmt.Fprint(stdout, strings.TrimSuffix(res.Text, "\n")+"\n")
		}
		return ExitOK
	}
	env := jsonx.NewObject()
	env.Set("ok", true)
	data := res.Data
	if data == nil {
		data = jsonx.NewObject()
	}
	env.Set("data", data)
	if len(c.warnings) > 0 {
		env.Set("warnings", append([]string(nil), c.warnings...))
	}
	if err := writeEnvelope(stdout, env); err != nil {
		return fail(c, stdout, err)
	}
	return ExitOK
}

func fail(c *Ctx, stdout io.Writer, err error) int { return failWith(c, stdout, err, Result{}) }

// failWith reports err; res is kept only on exit 1 and 4, the codes whose
// failure may carry data.
func failWith(c *Ctx, stdout io.Writer, err error, res Result) int {
	e := asError(err)
	if e.Exit != ExitFailed && e.Exit != ExitRefused {
		res = Result{}
	}
	if !c.JSON && res.Text != "" {
		fmt.Fprint(stdout, strings.TrimSuffix(res.Text, "\n")+"\n")
	}
	fmt.Fprintf(c.Stderr, "%s: %s\n", c.Path, e.Message)
	if e.Hint != "" {
		fmt.Fprintf(c.Stderr, "hint: %s\n", e.Hint)
	}
	if c.JSON {
		eo := jsonx.NewObject()
		eo.Set("code", CodeName(e.Exit))
		eo.Set("exit", e.Exit)
		eo.Set("message", e.Message)
		if e.Hint != "" {
			eo.Set("hint", e.Hint)
		}
		env := jsonx.NewObject()
		env.Set("ok", false)
		env.Set("error", eo)
		if res.Data != nil {
			env.Set("data", res.Data)
		}
		if len(c.warnings) > 0 {
			env.Set("warnings", append([]string(nil), c.warnings...))
		}
		writeEnvelope(stdout, env)
	}
	return e.Exit
}

func writeEnvelope(w io.Writer, env *jsonx.Object) error {
	b, err := jsonx.MarshalCompact(env)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// globalNames are left out of a verb's own flag list in help.
var globalNames = map[string]bool{"json": true, "ui": true, "h": true, "help": true, "C": true, "cwd": true, "repo": true, "version": true}

func helpResult(path string, cmd *Command, verbFlags *flag.FlagSet) Result {
	var b strings.Builder
	data := jsonx.NewObject()
	data.Set("command", path)
	data.Set("summary", cmd.Summary)
	fmt.Fprintf(&b, "%s: %s\n", path, cmd.Summary)
	if cmd.Subs != nil {
		subs := []any{}
		fmt.Fprintf(&b, "\nUsage: %s <command> [flags]\n\nCommands:\n", path)
		for _, name := range cmd.subNames() {
			s := cmd.sub(name)
			fmt.Fprintf(&b, "  %-12s %s\n", s.Name, s.Summary)
			so := jsonx.NewObject()
			so.Set("name", s.Name)
			so.Set("summary", s.Summary)
			subs = append(subs, so)
		}
		data.Set("commands", subs)
	}
	if verbFlags != nil {
		flags := []any{}
		verbFlags.VisitAll(func(f *flag.Flag) {
			if globalNames[f.Name] {
				return
			}
			if len(flags) == 0 {
				b.WriteString("\nFlags:\n")
			}
			fmt.Fprintf(&b, "  --%-14s %s\n", f.Name, f.Usage)
			fo := jsonx.NewObject()
			fo.Set("name", f.Name)
			fo.Set("usage", f.Usage)
			flags = append(flags, fo)
		})
		data.Set("flags", flags)
	}
	b.WriteString("\nGlobal flags: --json, -C/--cwd <dir>, --repo <name>, -h/--help")
	if cmd.View != nil {
		b.WriteString(", --ui")
	}
	b.WriteString("\n")
	return Result{Data: data, Text: b.String()}
}

// VerbPaths lists every verb in the tree as its space-separated command path
// ("knowledge tier get"), sorted. It is what `rota __verbs` prints.
func VerbPaths(root *Command) []string {
	var out []string
	var walk func(c *Command, prefix string)
	walk = func(c *Command, prefix string) {
		for _, s := range c.Subs {
			p := strings.TrimSpace(prefix + " " + s.Name)
			if s.Verb != nil {
				out = append(out, p)
			}
			walk(s, p)
		}
	}
	walk(root, "")
	sort.Strings(out)
	return out
}

// descends: a verb with sub-verbs (`init` and `init check`) keeps the
// command-word scan going when the next word names one. Such a verb takes no positional argument that could
// collide with a sub-verb name.
func descends(cmd *Command, tok string) bool {
	return !isFlag(tok) && cmd.sub(tok) != nil
}

func hasHelp(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "-h" || a == "--help" || a == "-help" {
			return true
		}
	}
	return false
}

// physicalCwd makes the working directory its symlink-free path, as Python's
// os.getcwd() is. Go's os.Getwd returns $PWD while it still names the cwd, so
// from a symlinked directory (link -> umb/web) every walk-up and sub-repo
// match would see the link instead. Setting PWD too keeps later os.Getwd
// calls, in every verb, on the physical path.
func physicalCwd() {
	wd, err := os.Getwd()
	if err != nil {
		return
	}
	if real := repos.Realpath(wd); real != wd && os.Chdir(real) == nil {
		os.Setenv("PWD", real)
	}
}
