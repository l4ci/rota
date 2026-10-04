package cli

import (
	"flag"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/knowledge"
	"github.com/l4ci/rota/internal/mapqa"
)

// decisionsCommands is the `rota decisions` group (A5, #49).
func decisionsCommands() *Command {
	return &Command{Name: "decisions", Summary: "read and log .rota/DECISIONS.md", Subs: []*Command{
		{Name: "query", Summary: "print topic sections", Verb: noFlags(decQuery)},
		{Name: "auto-log", Summary: "log an [Auto:Loop] decision", Verb: decAutoLog},
		{Name: "auto-since", Summary: "list this loop session's auto-logged decisions", Verb: noFlags(decAutoSince)},
	}}
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

func decAutoLog(fs *flag.FlagSet) RunFunc {
	topic := fs.String("topic", "", "the `topic` heading")
	title := fs.String("title", "", "the rule `title`")
	why := fs.String("why", "", "why the loop chose this, one line")
	planKey := fs.String("plan-key", "", "the plan `key` the decision came from")
	date := fs.String("date", "", "entry `date` (YYYY-MM-DD), default today")
	return func(c *Ctx, args []string) (Result, error) {
		if err := knNoArgs(args); err != nil {
			return Result{}, err
		}
		if err := knRequire(map[string]string{"topic": *topic, "title": *title, "why": *why}, "topic", "title", "why"); err != nil {
			return Result{}, err
		}
		st, _, err := knStore(c)
		if err != nil {
			return Result{}, err
		}
		changed, err := st.AutoLog(*topic, *title, *why, *planKey, *date)
		if err != nil {
			return knFail(err)
		}
		text := fmt.Sprintf("logged: %s :: %s", *topic, *title)
		if !changed {
			text = fmt.Sprintf("unchanged: %s :: %s", *topic, *title)
		}
		return Result{Data: knObj("topic", *topic, "title", *title, "changed", changed), Text: text}, nil
	}
}

func decAutoSince(c *Ctx, args []string) (Result, error) {
	if err := knNoArgs(args); err != nil {
		return Result{}, err
	}
	st, _, err := knStore(c)
	if err != nil {
		return Result{}, err
	}
	since, ds, err := st.AutoSince()
	if err != nil {
		return knFail(err)
	}
	list := []any{}
	for _, d := range ds {
		list = append(list, knObj("topic", d.Topic, "title", d.Title, "date", d.Date, "status", d.Status))
	}
	data := knObj()
	if since != "" {
		data.Set("since", since)
	}
	data.Set("decisions", list)
	return Result{Data: data, Text: knowledge.DecisionsText(ds)}, nil
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
