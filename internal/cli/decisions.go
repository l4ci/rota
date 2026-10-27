package cli

import (
	"errors"
	"flag"
	"fmt"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/knowledge"
	"strings"

	"github.com/l4ci/rota/internal/mapqa"
)

// decisionsCommands is the `rota decisions` group (A5, #49).
func decisionsCommands() *Command {
	return &Command{Name: "decisions", Summary: "read and write .rota/DECISIONS.md", Subs: []*Command{
		{Name: "check", Summary: "validate decision entry shape", Verb: noFlags(decCheck)},
		{Name: "amend", Summary: "replace one part of an active decision", Verb: decAmend},
		{Name: "retire", Summary: "retire an active decision", Verb: decRetire},
		{Name: "supersede", Summary: "replace an active decision", Verb: decSupersede},
		{Name: "add", Summary: "add a canonical decision entry", Verb: decAdd},
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
		staleLimit := mapqa.StaleCommits(c.deps().Config(st.Root))
		var stale []string
		for _, s := range subs {
			row := knObj("name", s.Name, "bytes", s.Bytes, "touched", s.Touched, "entryPoints", s.EntryPoints, "brokenRefs", s.BrokenRefs)
			line := fmt.Sprintf("%s: %d bytes, touched %s, %d entry points, %d broken", s.Name, s.Bytes, s.Touched, s.EntryPoints, s.BrokenRefs)
			if s.CommitsBehind != nil {
				row.Set("commitsBehind", *s.CommitsBehind)
				line += fmt.Sprintf(", %d commits behind", *s.CommitsBehind)
				if staleLimit > 0 && *s.CommitsBehind > staleLimit {
					stale = append(stale, mapqa.StaleNote(s.Name, *s.CommitsBehind, staleLimit))
				}
			}
			list = append(list, row)
			lines = append(lines, line)
		}
		data := knObj("subsystems", list, "count", len(subs))
		text := strings.Join(lines, "\n")
		if *capFlag {
			limit := mapqa.SoftCap(c.deps().Config(st.Root))
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
		for _, note := range stale {
			c.Warn("%s", note)
			if text != "" {
				text += "\n"
			}
			text += "note: " + note
		}
		return Result{Data: data, Text: text}, nil
	}
}

func decWriteResult(topic, title string, changed bool, err error) (Result, error) {
	if errors.Is(err, knowledge.ErrDecisionInvalid) {
		return Result{}, Usage("%s", err)
	}
	if err != nil {
		return knFail(err)
	}
	return Result{Data: knObj("topic", topic, "title", title, "changed", changed), Text: fmt.Sprintf("updated: %s :: %s", topic, title)}, nil
}

func decAdd(fs *flag.FlagSet) RunFunc {
	topic := fs.String("topic", "", "topic heading")
	title := fs.String("title", "", "decision title")
	bodyFile := fs.String("body-file", "", "four-part body: a path, or - for stdin")
	date := fs.String("date", "", "date (YYYY-MM-DD), default today")
	return func(c *Ctx, args []string) (Result, error) {
		if err := knNoArgs(args); err != nil {
			return Result{}, err
		}
		if err := knRequire(map[string]string{"topic": *topic, "title": *title, "body-file": *bodyFile}, "topic", "title", "body-file"); err != nil {
			return Result{}, err
		}
		body, err := readBodyText(c, "body-file", *bodyFile, ExitResolution)
		if err != nil {
			return Result{}, err
		}
		st, _, err := knStore(c)
		if err != nil {
			return Result{}, err
		}
		changed, err := st.AddDecision(*topic, *title, body, *date)
		return decWriteResult(*topic, *title, changed, err)
	}
}

func decCheck(c *Ctx, args []string) (Result, error) {
	if err := knNoArgs(args); err != nil {
		return Result{}, err
	}
	st, _, err := knStore(c)
	if err != nil {
		return Result{}, err
	}
	check, err := st.CheckDecisions()
	if err != nil {
		return Result{}, err
	}
	lines := append([]string{}, check.Errors...)
	for _, warning := range check.Warnings {
		lines = append(lines, "notice: "+warning)
	}
	res := Result{Data: knObj("errors", strSlice(check.Errors), "warnings", strSlice(check.Warnings)), Text: strings.Join(lines, "\n")}
	if len(check.Errors) > 0 {
		return res, Failed("malformed decisions")
	}
	return res, nil
}

func decAmend(fs *flag.FlagSet) RunFunc {
	topic := fs.String("topic", "", "topic heading")
	title := fs.String("title", "", "decision title")
	part := fs.String("part", "", "part: rule, why, forbids or permits")
	bodyFile := fs.String("body-file", "", "replacement part: a path, or - for stdin")
	return func(c *Ctx, args []string) (Result, error) {
		if err := knNoArgs(args); err != nil {
			return Result{}, err
		}
		if err := knRequire(map[string]string{"topic": *topic, "title": *title, "part": *part, "body-file": *bodyFile}, "topic", "title", "part", "body-file"); err != nil {
			return Result{}, err
		}
		body, err := readBodyText(c, "body-file", *bodyFile, ExitResolution)
		if err != nil {
			return Result{}, err
		}
		st, _, err := knStore(c)
		if err != nil {
			return Result{}, err
		}
		changed, err := st.AmendDecision(*topic, *title, *part, body)
		return decWriteResult(*topic, *title, changed, err)
	}
}

func decRetire(fs *flag.FlagSet) RunFunc {
	topic := fs.String("topic", "", "topic heading")
	title := fs.String("title", "", "decision title")
	reason := fs.String("reason", "", "one-line reason for lifting the boundary")
	date := fs.String("date", "", "date (YYYY-MM-DD), default today")
	return func(c *Ctx, args []string) (Result, error) {
		if err := knNoArgs(args); err != nil {
			return Result{}, err
		}
		if err := knRequire(map[string]string{"topic": *topic, "title": *title, "reason": *reason}, "topic", "title", "reason"); err != nil {
			return Result{}, err
		}
		st, _, err := knStore(c)
		if err != nil {
			return Result{}, err
		}
		changed, err := st.RetireDecision(*topic, *title, *reason, *date)
		return decWriteResult(*topic, *title, changed, err)
	}
}

func decSupersede(fs *flag.FlagSet) RunFunc {
	topic := fs.String("topic", "", "topic heading")
	title := fs.String("title", "", "old decision title")
	newTitle := fs.String("new-title", "", "replacement decision title")
	bodyFile := fs.String("body-file", "", "replacement four-part body: a path, or - for stdin")
	date := fs.String("date", "", "date (YYYY-MM-DD), default today")
	return func(c *Ctx, args []string) (Result, error) {
		if err := knNoArgs(args); err != nil {
			return Result{}, err
		}
		if err := knRequire(map[string]string{"topic": *topic, "title": *title, "new-title": *newTitle, "body-file": *bodyFile}, "topic", "title", "new-title", "body-file"); err != nil {
			return Result{}, err
		}
		body, err := readBodyText(c, "body-file", *bodyFile, ExitResolution)
		if err != nil {
			return Result{}, err
		}
		st, _, err := knStore(c)
		if err != nil {
			return Result{}, err
		}
		changed, err := st.SupersedeDecision(*topic, *title, *newTitle, body, *date)
		res, err := decWriteResult(*topic, *title, changed, err)
		if err == nil {
			res.Data.(*jsonx.Object).Set("newTitle", *newTitle)
		}
		return res, err
	}
}
