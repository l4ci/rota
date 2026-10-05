package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/jsonx"
)

// `rota config edit` (#193): a prompt loop over the schema keys. Booleans
// toggle, keys with a config.Prompt pick from its choices, the rest take a
// typed value checked against the type of the schema default. Every change
// goes through config.Set, the path `config set` takes, and is written at
// once, so ending the loop (Enter, q, Ctrl-D) never loses or half-applies
// anything. Like `rota setup` it reads the terminal and never blocks without
// one: off a TTY it refuses and points at `config set`.

func configEdit(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 0, 0, "config edit takes no arguments"); err != nil {
			return Result{}, err
		}
		d := c.deps()
		if c.JSON || !d.IsTerminal(c.Stdin) || !d.IsTerminal(c.Stdout) {
			return Result{}, Refused("config edit needs an interactive terminal").
				WithHint("run: rota config set <key> <value>")
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		return editLoop(c, root, bufio.NewReader(c.Stdin), c.Stderr)
	}
}

func editLoop(c *Ctx, root string, in *bufio.Reader, out io.Writer) (Result, error) {
	entries, err := config.Show(root, "", false)
	if err != nil {
		return Result{}, err
	}
	fmt.Fprintln(out, "rota config edit: type a number or part of a key to change it; Enter or q finishes.")
	for i, e := range entries {
		fmt.Fprintf(out, "%4d  %s\n", i+1, e.Line())
	}
	var changed []string
	for {
		fmt.Fprint(out, "\nkey> ")
		line, rerr := in.ReadString('\n')
		ans := strings.TrimSpace(line)
		if ans == "" || ans == "q" || ans == "quit" {
			break
		}
		matches := matchKeys(entries, ans)
		switch len(matches) {
		case 0:
			fmt.Fprintf(out, "  no key matches %q\n", ans)
		case 1:
			if key, ok, err := editKey(c, root, in, out, matches[0]); err != nil {
				return Result{}, err
			} else if ok {
				changed = append(changed, key)
				if entries, err = config.Show(root, "", false); err != nil {
					return Result{}, err
				}
			}
		default:
			fmt.Fprintf(out, "  %d keys match %q, be more specific:\n", len(matches), ans)
			for _, e := range matches {
				fmt.Fprintf(out, "    %s\n", e.Key)
			}
		}
		if rerr != nil {
			break
		}
	}
	text := "no changes"
	if len(changed) > 0 {
		text = "changed: " + strings.Join(changed, ", ")
	}
	return Result{Data: jsonObj("changed", anySlice(changed)), Text: text}, nil
}

// matchKeys resolves a 1-based row number, an exact key, or a substring.
func matchKeys(entries []config.Entry, ans string) []config.Entry {
	if n, err := strconv.Atoi(ans); err == nil {
		if n >= 1 && n <= len(entries) {
			return entries[n-1 : n]
		}
		return nil
	}
	var sub []config.Entry
	for _, e := range entries {
		if e.Key == ans {
			return []config.Entry{e}
		}
		if strings.Contains(strings.ToLower(e.Key), strings.ToLower(ans)) {
			sub = append(sub, e)
		}
	}
	return sub
}

// editKey asks for a new value of e and writes it. ok is false when the person
// kept the current value (or the input ended).
func editKey(c *Ctx, root string, in *bufio.Reader, out io.Writer, e config.Entry) (key string, ok bool, err error) {
	raw, ok := "", false
	if b, isBool := e.Value.(bool); isBool {
		raw, ok = strconv.FormatBool(!b), true
	} else if p := config.PromptFor(e.Key); p != nil {
		raw, ok = pickChoice(in, out, *p, fmt.Sprint(e.Value))
	} else {
		raw, ok = askValue(in, out, e)
	}
	if !ok {
		return e.Key, false, nil
	}
	if _, err := config.Set(root, e.Key, raw); err != nil {
		if errors.Is(err, config.ErrNotObject) {
			return "", false, &Error{Exit: ExitInternal, Message: err.Error()}
		}
		return "", false, err
	}
	fmt.Fprintf(out, "  %s = %s\n", e.Key, raw)
	if e.Source == "local" {
		fmt.Fprintf(out, "  note: .rota/config.local.json still overrides %s on this machine\n", e.Key)
	}
	return e.Key, true, nil
}

// pickChoice lists p's choices and reads a number or value; Enter keeps cur.
func pickChoice(in *bufio.Reader, out io.Writer, p config.Prompt, cur string) (string, bool) {
	fmt.Fprintf(out, "  %s (%s)\n", p.Title, p.Key)
	for i, ch := range p.Choices {
		mark := ""
		if ch.Value == cur {
			mark = " (current)"
		}
		fmt.Fprintf(out, "    %d) %s%s: %s\n", i+1, ch.Value, mark, ch.Desc)
	}
	for {
		fmt.Fprint(out, "  choose, Enter keeps: ")
		line, err := in.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			return "", false
		}
		if n, convErr := strconv.Atoi(line); convErr == nil && n >= 1 && n <= len(p.Choices) {
			return p.Choices[n-1].Value, true
		}
		if p.Valid(line) {
			return line, true
		}
		fmt.Fprintf(out, "  not a choice: %s\n", line)
		if err != nil {
			return "", false
		}
	}
}

// askValue reads a free value and re-asks until it fits the schema type.
func askValue(in *bufio.Reader, out io.Writer, e config.Entry) (string, bool) {
	kind := valueKind(e.Key)
	cur, _ := jsonx.MarshalCompact(e.Value)
	fmt.Fprintf(out, "  %s is %s; current %s\n", e.Key, kind, cur)
	for {
		fmt.Fprint(out, "  new value, Enter keeps: ")
		line, err := in.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			return "", false
		}
		if msg := validateValue(e.Key, line); msg != "" {
			fmt.Fprintf(out, "  %s\n", msg)
			if err != nil {
				return "", false
			}
			continue
		}
		return line, true
	}
}

func valueKind(key string) string {
	for _, k := range config.Keys {
		if k.Name != key {
			continue
		}
		switch k.Default.(type) {
		case bool:
			return "true or false"
		case json.Number:
			return "a number"
		case []any:
			return "a JSON list, e.g. [\"a\",\"b\"]"
		}
	}
	return "text"
}

// validateValue is "" when raw, parsed the way `config set` parses it, has the
// type of the key's schema default; else a one-line reason.
func validateValue(key, raw string) string {
	v := config.Coerce(raw)
	for _, k := range config.Keys {
		if k.Name != key {
			continue
		}
		switch k.Default.(type) {
		case bool:
			if _, ok := v.(bool); !ok {
				return "want true or false"
			}
		case json.Number:
			if _, ok := v.(json.Number); !ok {
				return "want a number"
			}
		case []any:
			if _, ok := v.([]any); !ok {
				return "want a JSON list, e.g. [\"a\",\"b\"]"
			}
		case string:
			if _, ok := v.(string); !ok {
				b, _ := jsonx.MarshalCompact(raw)
				return "want text; quote it to store it as a string: " + string(b)
			}
		}
		return ""
	}
	return ""
}
