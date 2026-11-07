package cli

import (
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/prompts"
	"github.com/l4ci/rota/internal/tui"
)

func promptsCommands() *Command {
	return &Command{Name: "prompts", Summary: "version team reporting overrides", Subs: []*Command{
		{Name: "list", Summary: "list reporting sections and upstream status", Verb: promptsList},
		{Name: "show", Summary: "inspect effective content, baselines and revision history: rota prompts show <section>", Verb: promptsShow},
		{Name: "save", Summary: "save a reporting override against an expected revision: rota prompts save <section> --expect <n> --body-file <path|->", Verb: promptsSave},
		{Name: "adopt", Summary: "follow the current built-in default, keeping the history: rota prompts adopt <section> --expect <n>", Verb: promptsAdopt},
		{Name: "restore", Summary: "restore an earlier revision as a new one: rota prompts restore <section> --expect <n> --revision <r>", Verb: promptsRestore},
	}}
}

func promptsList(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if err := noArgs(args); err != nil {
			return Result{}, err
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		views, err := prompts.Open(root, prompts.Catalog()).List()
		if err != nil {
			return Result{}, promptsErr(err, "")
		}
		lines := make([]string, 0, len(views))
		for _, v := range views {
			lines = append(lines, fmt.Sprintf("%s\trevision %d\t%s", tui.Sanitize(v.Name, false), v.Revision, v.Status))
		}
		return Result{Data: jsonObj("sections", views), Text: strings.Join(lines, "\n")}, nil
	}
}

func promptsShow(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		name, err := oneArg(args, "section")
		if err != nil {
			return Result{}, err
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		v, err := prompts.Open(root, prompts.Catalog()).Show(name)
		if err != nil {
			return Result{}, promptsErr(err, name)
		}
		return promptResult(v, false), nil
	}
}

func promptsSave(fs *flag.FlagSet) RunFunc {
	expect := fs.Int("expect", -1, "expected saved revision (0 for an unsaved section)")
	bodyFile := fs.String("body-file", "", "reporting template: path, or - for stdin; draft is never modified")
	return func(c *Ctx, args []string) (Result, error) {
		name, err := oneArg(args, "section")
		if err != nil {
			return Result{}, err
		}
		if *expect < 0 || *bodyFile == "" {
			return Result{}, Usage("--expect (0 or higher) and --body-file are required")
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		body, err := readBodyText(c, "body-file", *bodyFile, ExitResolution)
		if err != nil {
			return Result{}, err
		}
		v, err := prompts.Open(root, prompts.Catalog()).Save(name, body, *expect)
		if err != nil {
			return Result{}, promptsErr(err, name)
		}
		return promptResult(v, true), nil
	}
}

func promptsAdopt(fs *flag.FlagSet) RunFunc {
	expect := fs.Int("expect", -1, "expected saved revision")
	return func(c *Ctx, args []string) (Result, error) {
		name, err := oneArg(args, "section")
		if err != nil {
			return Result{}, err
		}
		if *expect < 0 {
			return Result{}, Usage("--expect (0 or higher) is required")
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		v, err := prompts.Open(root, prompts.Catalog()).Adopt(name, *expect)
		if err != nil {
			return Result{}, promptsErr(err, name)
		}
		return promptResult(v, true), nil
	}
}

func promptsRestore(fs *flag.FlagSet) RunFunc {
	expect := fs.Int("expect", -1, "expected saved revision")
	rev := fs.Int("revision", 0, "the revision to restore")
	return func(c *Ctx, args []string) (Result, error) {
		name, err := oneArg(args, "section")
		if err != nil {
			return Result{}, err
		}
		if *expect < 0 || *rev < 1 {
			return Result{}, Usage("--expect (0 or higher) and --revision (1 or higher) are required")
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		v, err := prompts.Open(root, prompts.Catalog()).Restore(name, *rev, *expect)
		if err != nil {
			return Result{}, promptsErr(err, name)
		}
		return promptResult(v, true), nil
	}
}

// promptsUpgradeReport names the saved sections whose built-in default changed
// or was removed since their revision. It reads the store only; nothing is
// adopted or activated.
func promptsUpgradeReport(root string) (stale []any, lines []string) {
	if root == "" {
		return nil, nil
	}
	views, err := prompts.Open(root, prompts.Catalog()).List()
	if err != nil {
		return nil, []string{"prompts: " + tui.Sanitize(err.Error(), false)}
	}
	for _, v := range views {
		if v.Status != "changed" && v.Status != "removed" {
			continue
		}
		stale = append(stale, jsonObj("name", v.Name, "status", v.Status, "revision", v.Revision))
		name := tui.Sanitize(v.Name, false)
		lines = append(lines, fmt.Sprintf("prompt %s: built-in default %s since revision %d; compare with: rota prompts show %s", name, v.Status, v.Revision, name))
	}
	return stale, lines
}

func promptResult(v prompts.View, changed bool) Result {
	text := promptText(v)
	if changed {
		return Result{Data: jsonObj("section", v, "changed", true), Text: text + "\n\nSaved as revision " + fmt.Sprint(v.Revision) + "; activate with: rota agents write"}
	}
	return Result{Data: v, Text: text}
}

// promptText renders a View as labelled blocks, so the recorded baseline, the
// current default and the override read side by side; --json keeps the View.
// The store is tracked, so a cloned repo writes its text: every string from it
// is sanitized before it reaches the terminal.
func promptText(v prompts.View) string {
	override := "(none: follows the built-in default)"
	if v.Override != nil {
		override = *v.Override
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\trevision %d\t%s\n", tui.Sanitize(v.Name, false), v.Revision, v.Status)
	for _, blk := range [][2]string{
		{"Baseline (saved against)", v.Baseline},
		{"Default (this binary)", v.Default},
		{"Override", override},
		{"Effective (rota agents write renders)", v.Effective},
	} {
		fmt.Fprintf(&b, "\n%s:\n  %s\n", blk[0], strings.ReplaceAll(tui.Sanitize(blk[1], true), "\n", "\n  "))
	}
	if len(v.History) > 0 {
		b.WriteString("\nHistory:\n")
		for _, r := range v.History {
			kind := "override"
			if r.Override == nil {
				kind = "adopt default"
			}
			fmt.Fprintf(&b, "  %d\t%s\tbaseline %.12s\n", r.Revision, kind, tui.Sanitize(r.BaselineDigest, false))
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// promptsErr maps store errors to exits: a bad argument or template is 2, a
// stale or removed write is 4, and a damaged store (malformed, symlinked) or
// anything else is 1. name is the section the verb was given, "" for list.
func promptsErr(err error, name string) error {
	msg := tui.Sanitize(err.Error(), false)
	name = tui.Sanitize(name, false)
	switch {
	case errors.Is(err, prompts.ErrCorrupt):
		return Failed("%s", msg).WithHint("repair or restore .rota/prompts.json from git; rota never rewrites a damaged store")
	case errors.Is(err, prompts.ErrUnknown):
		return Usage("%s", msg).WithHint("run: rota prompts list")
	case errors.Is(err, prompts.ErrInvalid):
		return Usage("%s", msg)
	case errors.Is(err, prompts.ErrConflict):
		return Refused("%s", msg).WithHint(fmt.Sprintf("run: rota prompts show %s, then retry with --expect set to the current revision; your draft is untouched", name))
	case errors.Is(err, prompts.ErrRemoved):
		return Refused("%s", msg).WithHint(fmt.Sprintf("this binary no longer has the section; its history stays readable with rota prompts show %s", name))
	}
	return Failed("%s", msg)
}
