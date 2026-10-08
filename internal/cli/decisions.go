package cli

import (
	"flag"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/mapqa"
)

// decisionsCommands is the `rota decisions` group (A5, #49).
func decisionsCommands() *Command {
	return &Command{Name: "decisions", Summary: "read .rota/DECISIONS.md", Subs: []*Command{
		{Name: "query", Summary: "print topic sections", Verb: noFlags(decQuery)},
		{Name: "topics", Summary: "list topic headings with bullet counts", Verb: noFlags(decTopics)},
		{Name: "stats", Summary: "bullet count and size per topic", Verb: noFlags(decStats), View: decisionsView},
	}}
}

func decStats(c *Ctx, args []string) (Result, error) {
	if err := knNoArgs(args); err != nil {
		return Result{}, err
	}
	st, _, err := knStore(c)
	if err != nil {
		return Result{}, err
	}
	stats, err := st.DecisionsStats()
	if err != nil {
		return knFail(err)
	}
	return statsResult(stats), nil
}

func decTopics(c *Ctx, args []string) (Result, error) {
	if err := knNoArgs(args); err != nil {
		return Result{}, err
	}
	st, _, err := knStore(c)
	if err != nil {
		return Result{}, err
	}
	stats, err := st.DecisionsStats()
	if err != nil {
		return knFail(err)
	}
	return topicsResult(stats), nil
}

func decQuery(c *Ctx, args []string) (Result, error) {
	if len(args) == 0 {
		return Result{}, Usage("name at least one topic")
	}
	st, _, err := knStore(c)
	if err != nil {
		return Result{}, err
	}
	text, missing, err := st.DecisionsQuery(args)
	if err != nil {
		return knFail(err)
	}
	return Result{Data: knObj("text", text, "missing", strSlice(missing)), Text: text}, nil
}

// mapCommands is the `rota map` group.
func mapCommands() *Command {
	return &Command{Name: "map", Summary: "subsystem map in .rota/map", Subs: []*Command{
		{Name: "query", Summary: "print subsystem files", Verb: noFlags(mapQuery)},
		{Name: "index", Summary: "regenerate the map block", Verb: noFlags(mapIndex)},
		{Name: "stats", Summary: "size and broken-reference counts", Verb: mapStats},
	}}
}

// qaCommands is the `rota qa` group.
func qaCommands() *Command {
	return &Command{Name: "qa", Summary: "QA strategies in .rota/qa", Subs: []*Command{
		{Name: "query", Summary: "print QA target files", Verb: noFlags(qaQuery)},
		{Name: "index", Summary: "regenerate the QA block", Verb: noFlags(qaIndex)},
	}}
}

func dirQuery(c *Ctx, dir, what string, args []string) (Result, error) {
	if len(args) == 0 {
		return Result{}, Usage("name at least one %s", what)
	}
	st, _, err := knStore(c)
	if err != nil {
		return Result{}, err
	}
	text, missing, err := mapqa.Query(st.Root, dir, args)
	if err != nil {
		return Result{}, err
	}
	return Result{Data: knObj("text", text, "missing", strSlice(missing)), Text: text}, nil
}

func mapQuery(c *Ctx, args []string) (Result, error) { return dirQuery(c, "map", "subsystem", args) }
func qaQuery(c *Ctx, args []string) (Result, error)  { return dirQuery(c, "qa", "target", args) }

func indexVerb(c *Ctx, args []string, key string, body func(root string) string) (Result, error) {
	if err := knNoArgs(args); err != nil {
		return Result{}, err
	}
	st, _, err := knStore(c)
	if err != nil {
		return Result{}, err
	}
	status, err := mapqa.WriteIndex(st.Root, key, body(st.Root))
	if err != nil {
		return Result{}, err
	}
	return Result{Data: knObj("key", key, "status", status, "changed", status != "unchanged"), Text: status}, nil
}

func mapIndex(c *Ctx, args []string) (Result, error) {
	return indexVerb(c, args, "map", mapqa.MapIndexBlock)
}
func qaIndex(c *Ctx, args []string) (Result, error) {
	return indexVerb(c, args, "qa", mapqa.QAIndexBlock)
}

func mapStats(fs *flag.FlagSet) RunFunc {
	capFlag := fs.Bool("cap", false, "also check the subsystem count against map.softcap_subsystems")
	return func(c *Ctx, args []string) (Result, error) {
		if err := knNoArgs(args); err != nil {
			return Result{}, err
		}
		st, _, err := knStore(c)
		if err != nil {
			return Result{}, err
		}
		subs := mapqa.Stats(st.Root)
		list := []any{}
		var lines []string
		for _, s := range subs {
			list = append(list, knObj("name", s.Name, "bytes", s.Bytes, "touched", s.Touched, "entryPoints", s.EntryPoints, "brokenRefs", s.BrokenRefs))
			lines = append(lines, fmt.Sprintf("%s: %d bytes, touched %s, %d entry points, %d broken", s.Name, s.Bytes, s.Touched, s.EntryPoints, s.BrokenRefs))
		}
		data := knObj("subsystems", list, "count", len(subs))
		text := strings.Join(lines, "\n")
		if *capFlag {
			limit := mapqa.SoftCap(st.Root)
			over := len(subs) >= limit
			data.Set("cap", limit)
			data.Set("overCap", over)
			text = ""
			if over {
				note := mapqa.CapNote(len(subs), limit)
				c.Warn("%s", note)
				text = "note: " + note
			}
		}
		return Result{Data: data, Text: text}, nil
	}
}
