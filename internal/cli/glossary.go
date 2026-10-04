package cli

import (
	"flag"
	"fmt"
	"regexp"
	"strings"

	"github.com/l4ci/rota/internal/knowledge"
)

// glossaryCommands is the `rota glossary` group (A5, #49).
func glossaryCommands() *Command {
	return &Command{Name: "glossary", Summary: "terms in the ## Glossary of KNOWLEDGE.md", Subs: []*Command{
		{Name: "read", Summary: "print term entries", Repo: true, Verb: glRead},
		{Name: "write", Summary: "add or update one term", Repo: true, Verb: glWrite},
		{Name: "import", Summary: "add many terms atomically", Repo: true, Verb: glImport},
	}}
}

func strSlice(l []string) []any {
	out := []any{}
	for _, s := range l {
		out = append(out, s)
	}
	return out
}

func glRead(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) == 0 {
			return Result{}, Usage("name at least one term")
		}
		st, scope, err := knStore(c)
		if err != nil {
			return Result{}, err
		}
		text, missing, err := st.GlossaryRead(scope, args)
		if err != nil {
			return knFail(err)
		}
		return Result{Data: knObj("text", text, "missing", strSlice(missing)), Text: text}, nil
	}
}

func glWrite(fs *flag.FlagSet) RunFunc {
	def := fs.String("def", "", "the definition `text`")
	alias := fs.String("alias", "", "comma-separated aliases to add")
	not := fs.String("not", "", "comma-separated terms this one is not; empty clears the list")
	touch := fs.Bool("touch", false, "stamp today's date even when the term exists")
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) != 1 {
			return Result{}, Usage("name exactly one term")
		}
		if *def == "" {
			return Result{}, Usage("--def is required")
		}
		notsProvided := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "not" {
				notsProvided = true
			}
		})
		st, scope, err := knStore(c)
		if err != nil {
			return Result{}, err
		}
		term := knowledge.Term{Name: strings.TrimSpace(args[0]), Definition: *def, Aliases: knowledge.SplitCSV(*alias), Nots: knowledge.SplitCSV(*not)}
		name, changed, err := st.GlossaryWrite(scope, term, *touch, notsProvided)
		if err != nil {
			return knFail(err)
		}
		text := "wrote: Glossary/" + name
		if !changed {
			text = "unchanged: Glossary/" + name
		}
		return Result{Data: knObj("term", name, "changed", changed), Text: text}, nil
	}
}

func glImport(fs *flag.FlagSet) RunFunc {
	bodyFile := fs.String("body-file", "", "the manifest: a `path`, or - for stdin")
	touch := fs.Bool("touch", false, "stamp today's date on every imported term")
	return func(c *Ctx, args []string) (Result, error) {
		if err := knNoArgs(args); err != nil {
			return Result{}, err
		}
		if *bodyFile == "" {
			return Result{}, Usage("--body-file is required")
		}
		manifest, err := knReadBody(c, *bodyFile)
		if err != nil {
			return Result{}, err
		}
		st, scope, err := knStore(c)
		if err != nil {
			return Result{}, err
		}
		terms, changed, err := st.GlossaryImport(scope, manifest, *touch)
		if err != nil {
			return knFail(err)
		}
		text := fmt.Sprintf("imported %d term(s)", len(terms))
		return Result{Data: knObj("imported", len(terms), "terms", strSlice(terms), "changed", changed), Text: text}, nil
	}
}

var blockKeyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// blockCommand is `rota block <key>` and `rota block skills`.
func blockCommand() *Command {
	return &Command{Name: "block", Summary: "regenerate a managed block in the instructions file", Repo: true, Verb: blockVerb}
}

func blockVerb(fs *flag.FlagSet) RunFunc {
	bodyFile := fs.String("body-file", "", "block body: a `path`, or - for stdin (any key)")
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) != 1 {
			return Result{}, Usage("name exactly one block key").WithHint("keys: knowledge, decisions, skills, or any key with --body-file")
		}
		key := args[0]
		if !blockKeyRe.MatchString(key) {
			return Result{}, Usage("bad block key %q (lowercase letters, digits and dashes)", key)
		}
		var status string
		switch {
		case key == "skills":
			if *bodyFile != "" || c.Repo != "" {
				return Result{}, Usage("block skills takes no --body-file or --repo")
			}
			st, _, err := knStore(c)
			if err != nil {
				return Result{}, err
			}
			if status, err = st.WriteCustomBlock("skills", knowledge.SkillsBlockBody()); err != nil {
				return knFail(err)
			}
		case *bodyFile != "":
			if c.Repo != "" {
				return Result{}, Usage("--repo cannot be combined with --body-file")
			}
			body, err := knReadBody(c, *bodyFile)
			if err != nil {
				return Result{}, err
			}
			st, _, err := knStore(c)
			if err != nil {
				return Result{}, err
			}
			if status, err = st.WriteCustomBlock(key, body); err != nil {
				return knFail(err)
			}
		default:
			if !knowledge.IsGeneratedBlock(key) {
				return Result{}, Usage("unknown block key %q without --body-file (known: %s)", key, strings.Join(knowledge.GeneratedBlockKeys(), ", "))
			}
			if knowledge.UmbrellaOnlyBlock(key) && c.Repo != "" {
				return Result{}, Usage("%s is umbrella-only; --repo is not allowed", key)
			}
			st, scope, err := knStore(c)
			if err != nil {
				return Result{}, err
			}
			if knowledge.UmbrellaOnlyBlock(key) {
				scope = knowledge.Umbrella
			}
			if status, err = st.RegenerateBlock(key, scope); err != nil {
				return knFail(err)
			}
		}
		return Result{Data: knObj("key", key, "status", status, "changed", status != "unchanged"), Text: status}, nil
	}
}

func instructionsCommands() *Command {
	return &Command{Name: "instructions", Summary: "project instructions files", Subs: []*Command{
		{Name: "init", Summary: "make AGENTS.md the instructions file, CLAUDE.md its importer", Verb: noFlags(instructionsInit)},
	}}
}

func instructionsInit(c *Ctx, args []string) (Result, error) {
	if err := knNoArgs(args); err != nil {
		return Result{}, err
	}
	st, _, err := knStore(c)
	if err != nil {
		return Result{}, err
	}
	acts, err := st.InstructionsInit()
	if err != nil {
		return knFail(err)
	}
	list := []any{}
	var lines []string
	for _, a := range acts {
		o := knObj("action", a.Action, "file", a.File)
		if len(a.Keys) > 0 {
			o.Set("keys", strSlice(a.Keys))
		}
		list = append(list, o)
		switch a.Action {
		case "moved":
			lines = append(lines, fmt.Sprintf("moved: %s → %s", strings.Join(a.Keys, ", "), a.File))
		case "linked":
			lines = append(lines, fmt.Sprintf("linked: %s → @AGENTS.md", a.File))
		case "skippedSymlink":
			lines = append(lines, fmt.Sprintf("note: %s is a symlink; leaving instruction files untouched", a.File))
		default:
			lines = append(lines, fmt.Sprintf("%s: %s", a.Action, a.File))
		}
	}
	return Result{Data: knObj("actions", list, "changed", len(acts) > 0), Text: strings.Join(lines, "\n")}, nil
}
