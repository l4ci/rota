package cli

import (
	"errors"
	"flag"
	"strings"

	"github.com/l4ci/rota/internal/agents"
	"github.com/l4ci/rota/internal/jsonx"
)

// `rota agents write [--check]` (#405): emit the Claude and Codex subagent
// definitions for the three tier roles from config.

func agentsCommands() *Command {
	return &Command{Name: "agents", Summary: "subagent definitions generated from the role config", Subs: []*Command{
		{Name: "write", Summary: "write .claude/agents/rota-*.md (and .codex/agents/rota-*.toml when Codex is configured)", Verb: agentsWrite},
	}}
}

func agentsWrite(fs *flag.FlagSet) RunFunc {
	check := fs.Bool("check", false, "report drift and exit 1 when a file is missing or stale; write nothing")
	return func(c *Ctx, args []string) (Result, error) {
		if err := noArgs(args); err != nil {
			return Result{}, err
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		if *check {
			es, err := agents.Compare(root)
			if err != nil {
				return Result{}, agentsErr(err)
			}
			data, text, bad := agentsReport(es, "")
			if bad {
				return Result{}, Failed("agent files drift from config (run: rota agents write):\n%s", text)
			}
			return Result{Data: data, Text: text}, nil
		}
		es, err := agents.Write(root)
		if err != nil {
			return Result{}, agentsErr(err)
		}
		data, text, _ := agentsReport(es, "write")
		return Result{Data: data, Text: text}, nil
	}
}

// agentsErr maps a refusal to overwrite a foreign file, and a role effort Codex
// cannot express, to exit 4; anything else is a config or I/O failure.
func agentsErr(err error) error {
	if errors.Is(err, agents.ErrForeign) || errors.Is(err, agents.ErrEffort) {
		return Refused("%v", err)
	}
	return Failed("%v", err)
}

// agentsReport describes entries as they stood before the verb ran. mode
// "write" words a stale file as updated; any other mode as drift.
func agentsReport(es []agents.Entry, mode string) (*jsonx.Object, string, bool) {
	var files []any
	var lines []string
	bad := false
	for _, e := range es {
		status := e.Status
		if mode == "write" {
			switch status {
			case agents.StatusMissing:
				status = "created"
			case agents.StatusDrift:
				status = "updated"
			}
		} else if status != agents.StatusCurrent {
			bad = true
		}
		files = append(files, jsonObj("path", e.Path, "status", status))
		lines = append(lines, status+": "+e.Path)
	}
	return jsonObj("files", files), strings.Join(lines, "\n"), bad
}
