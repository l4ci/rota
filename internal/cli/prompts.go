package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/prompts"
)

func promptsCommands() *Command {
	return &Command{Name: "prompts", Summary: "version team reporting overrides", Subs: []*Command{
		{Name: "list", Summary: "list reporting sections and upstream status", Verb: promptsList},
		{Name: "show", Summary: "inspect effective content, baselines and revision history", Verb: promptsShow},
		{Name: "save", Summary: "save a reporting override against an expected revision", Verb: promptsSave},
		{Name: "adopt", Summary: "follow the current built-in default, keeping the history", Verb: promptsAdopt},
		{Name: "restore", Summary: "restore an earlier revision as a new one", Verb: promptsRestore},
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
			return Result{}, promptsErr(err, false)
		}
		lines := make([]string, 0, len(views))
		for _, v := range views {
			lines = append(lines, fmt.Sprintf("%s\trevision %d\t%s", v.Name, v.Revision, v.Status))
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
			return Result{}, promptsErr(err, false)
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
			return Result{}, promptsErr(err, true)
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
			return Result{}, promptsErr(err, true)
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
			return Result{}, promptsErr(err, true)
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
		return nil, []string{"prompts: " + err.Error()}
	}
	for _, v := range views {
		if v.Status != "changed" && v.Status != "removed" {
			continue
		}
		stale = append(stale, jsonObj("name", v.Name, "status", v.Status, "revision", v.Revision))
		lines = append(lines, fmt.Sprintf("prompt %s: built-in default %s since revision %d; compare with: rota prompts show %s", v.Name, v.Status, v.Revision, v.Name))
	}
	return stale, lines
}

func promptResult(v prompts.View, changed bool) Result {
	b, _ := json.MarshalIndent(v, "", "  ")
	if changed {
		return Result{Data: jsonObj("section", v, "changed", true), Text: string(b) + "\nSaved; activate with rota agents write."}
	}
	return Result{Data: v, Text: string(b)}
}

func promptsErr(err error, write bool) error {
	if errors.Is(err, prompts.ErrUnknown) {
		return Usage("%v", err)
	}
	if errors.Is(err, prompts.ErrInvalid) {
		return Usage("%v", err)
	}
	if write && (errors.Is(err, prompts.ErrConflict) || errors.Is(err, prompts.ErrRemoved)) {
		return Refused("%v", err)
	}
	return Failed("%v", err)
}
