package cli

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/jsonx"
)

// `rota setup` is rota init with the main config choices asked up front. It is
// the one verb that reads a terminal (the maintainer's call on #25): bare
// `rota` in a directory without .rota/ is meant to land here. The questions
// come from config.Prompts, so choices, help and defaults follow the schema.
// Without a terminal it never blocks: --list prints the questions, --yes takes
// the defaults, --set key=value answers one.

func setupCommand() *Command {
	return &Command{Name: "setup", Summary: "init this directory, asking for the main config choices", Verb: setupVerb}
}

// setupIsTTY is whether in is an interactive terminal (no pipe, file or /dev/null). A var so tests can
// stand in for one.
var setupIsTTY = func(in io.Reader) bool {
	f, ok := in.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	// /dev/null is a character device too, and what CI and agents often hand over.
	if null, err := os.Stat(os.DevNull); err == nil && os.SameFile(fi, null) {
		return false
	}
	return true
}

type setAnswers struct{ pairs [][2]string }

func (s *setAnswers) String() string { return "" }
func (s *setAnswers) Set(v string) error {
	k, val, ok := strings.Cut(v, "=")
	if !ok || k == "" {
		return fmt.Errorf("want key=value")
	}
	s.pairs = append(s.pairs, [2]string{k, val})
	return nil
}

func setupVerb(fs *flag.FlagSet) RunFunc {
	var sets setAnswers
	yes := fs.Bool("yes", false, "take the default for every question not given with --set; never prompt")
	list := fs.Bool("list", false, "print the questions and how to answer them; write nothing")
	fs.Var(&sets, "set", "answer a question: `key=value` (repeatable)")
	return func(c *Ctx, args []string) (Result, error) {
		if err := knNoArgs(args); err != nil {
			return Result{}, err
		}
		if *list {
			return setupList(), nil
		}
		dir, err := initDir()
		if err != nil {
			return Result{}, err
		}
		if fi, err := os.Stat(filepath.Join(dir, ".rota")); err == nil && fi.IsDir() {
			return Result{}, Refused("%s is already initialized", dir).WithHint("run: rota config set <key> <value>")
		}
		given := map[string]string{}
		for _, kv := range sets.pairs {
			p := config.PromptFor(kv[0])
			if p == nil {
				return Result{}, Usage("--set %s: not a setup question", kv[0]).WithHint("run: rota setup --list")
			}
			if !p.Valid(kv[1]) {
				return Result{}, Usage("--set %s: %q is not one of %s", kv[0], kv[1], choiceValues(*p)).WithHint("run: rota setup --list")
			}
			given[kv[0]] = kv[1]
		}
		interactive := !*yes && !c.JSON && setupIsTTY(c.Stdin)
		if !interactive && !*yes && len(given) == 0 {
			return Result{}, Usage("setup needs answers and there is no terminal").
				WithHint("run: rota setup --list to see the questions, rota setup --yes for the defaults")
		}
		answers, err := resolveAnswers(c, given, interactive)
		if err != nil {
			return Result{}, err
		}
		res, err := runInit(c, false)
		if err != nil {
			return res, err
		}
		root, _ := res.Data.(*jsonx.Object)
		out := jsonx.NewObject()
		lines := []string{res.Text}
		for _, p := range config.Prompts {
			v, ok := answers[p.Key]
			if !ok {
				continue
			}
			if _, err := config.Set(dir, p.Key, v); err != nil {
				return Result{}, err
			}
			out.Set(p.Key, config.Coerce(v))
			lines = append(lines, fmt.Sprintf("set %s = %s", p.Key, v))
		}
		root.Set("answers", out)
		return Result{Data: root, Text: strings.Join(lines, "\n")}, nil
	}
}

// resolveAnswers settles every applicable question: the --set value, else the
// typed answer when interactive, else the default. A question whose condition
// does not hold is skipped, and --set naming it is a usage error.
func resolveAnswers(c *Ctx, given map[string]string, interactive bool) (map[string]string, error) {
	answers := map[string]string{}
	in := bufio.NewReader(c.Stdin)
	if interactive {
		fmt.Fprintln(c.Stderr, "rota setup: Enter takes the default [in brackets]; Ctrl-C aborts before anything is written.")
	}
	for _, p := range config.Prompts {
		v, ok := given[p.Key]
		if p.IfKey != "" && answers[p.IfKey] != p.IfValue {
			if ok {
				return nil, Usage("--set %s only applies when %s=%s", p.Key, p.IfKey, p.IfValue)
			}
			continue
		}
		switch {
		case ok:
		case interactive:
			var err error
			if v, err = ask(in, c.Stderr, p); err != nil {
				return nil, err
			}
		default:
			v = p.DefaultChoice()
		}
		answers[p.Key] = v
	}
	return answers, nil
}

// ask prints p and reads until the answer is a choice number, a choice value
// or empty (the default). End of input is a usage error, not a default: a
// closed pipe must not silently configure the project.
func ask(in *bufio.Reader, out io.Writer, p config.Prompt) (string, error) {
	def := p.DefaultChoice()
	fmt.Fprintf(out, "\n%s (%s)\n", p.Title, p.Key)
	defN := 0
	for i, ch := range p.Choices {
		mark := ""
		if ch.Value == def {
			mark, defN = " (default)", i+1
		}
		fmt.Fprintf(out, "  %d) %s%s: %s\n", i+1, ch.Value, mark, ch.Desc)
	}
	for {
		fmt.Fprintf(out, "Choose [%d]: ", defN)
		line, err := in.ReadString('\n')
		line = strings.TrimSpace(line)
		if err != nil && line == "" {
			return "", Usage("input ended before setup was answered; nothing was written").WithHint("run: rota setup --yes for the defaults")
		}
		if line == "" {
			return def, nil
		}
		if n, convErr := strconv.Atoi(line); convErr == nil && n >= 1 && n <= len(p.Choices) {
			return p.Choices[n-1].Value, nil
		}
		if p.Valid(line) {
			return line, nil
		}
		fmt.Fprintf(out, "  not a choice: %s\n", line)
	}
}

func choiceValues(p config.Prompt) string {
	vals := make([]string, len(p.Choices))
	for i, ch := range p.Choices {
		vals[i] = ch.Value
	}
	return strings.Join(vals, "|")
}

// setupList is `setup --list`: every question with its choices, default and
// the flag that answers it.
func setupList() Result {
	var qs []any
	var lines []string
	for _, p := range config.Prompts {
		var chs []any
		for _, ch := range p.Choices {
			chs = append(chs, knObj("value", ch.Value, "description", ch.Desc))
		}
		q := knObj("key", p.Key, "title", p.Title, "default", p.DefaultChoice(), "choices", chs)
		line := fmt.Sprintf("%s  %s  [%s]  default %s", p.Key, p.Title, choiceValues(p), p.DefaultChoice())
		if p.IfKey != "" {
			q.Set("if", p.IfKey+"="+p.IfValue)
			line += fmt.Sprintf("  (only when %s=%s)", p.IfKey, p.IfValue)
		}
		qs = append(qs, q)
		lines = append(lines, line)
	}
	lines = append(lines, "", "answer with: rota setup --set <key>=<value> (repeatable); rota setup --yes takes the defaults")
	return Result{Data: knObj("questions", qs), Text: strings.Join(lines, "\n")}
}
