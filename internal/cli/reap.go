package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/reap"
	"github.com/l4ci/rota/internal/round"
)

// reapEnv builds the round environment reap reads its live set from and the
// host it may close tabs on. Tests swap it for fakes.
var reapEnv = defaultReapEnv

// defaultReapEnv is the round's environment with the forge dropped (reap
// proves "merged" from git alone and never asks a forge). ROTA_TEST_REAP_HOST
// replaces the host entirely with a fixture file (a test hook, not part of
// the CLI); an unreadable fixture is an unavailable host.
func defaultReapEnv(ctx context.Context, root string) (round.Env, reap.HostOps) {
	if path := os.Getenv("ROTA_TEST_REAP_HOST"); path != "" {
		e := roundEnv(ctx, root)
		e.Forge, e.ForgeErr = nil, "not used by reap"
		fx, err := loadReapFixture(path)
		if err != nil {
			e.Snapshot, e.HostErr = nil, err.Error()
			return e, nil
		}
		e.Snapshot = func(context.Context) ([]host.Agent, error) { return fx.agents, nil }
		e.HostName = "fixture"
		return e, fx
	}
	e := roundEnv(ctx, root)
	e.Forge, e.ForgeErr = nil, "not used by reap"
	var ops reap.HostOps
	if e.Snapshot != nil && e.HostName == "herdr" {
		if tl, ok := host.New("herdr", host.Deps{}).(host.TabLister); ok {
			ops = herdrReapOps{tl}
		}
	}
	return e, ops
}

// herdrReapOps adapts herdr's tab listing. herdr reports no process tree it
// can prove agent-free, so reap lists no process on a real host.
type herdrReapOps struct{ host.TabLister }

func (herdrReapOps) Processes(context.Context) ([]reap.Process, error) { return nil, nil }
func (herdrReapOps) StopProcess(context.Context, int) error {
	return fmt.Errorf("stopping a process is not supported on a real host")
}

// reapFixture is the ROTA_TEST_REAP_HOST file: workspaces with their tabs, the
// live agents, and processes under tabs. Removals are appended to
// <file>.removed so a test can see what reap did.
type reapFixture struct {
	path   string
	agents []host.Agent
	tabs   []host.Tab
	procs  []reap.Process
}

func loadReapFixture(path string) (*reapFixture, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ROTA_TEST_REAP_HOST: %v", err)
	}
	var doc struct {
		Workspaces []struct {
			ID   string `json:"id"`
			Tabs []struct {
				ID    string `json:"id"`
				Cwd   string `json:"cwd"`
				Agent bool   `json:"agent"`
			} `json:"tabs"`
		} `json:"workspaces"`
		Agents []struct {
			Tab    string `json:"tab"`
			Name   string `json:"name"`
			Cwd    string `json:"cwd"`
			Status string `json:"status"`
		} `json:"agents"`
		Processes []struct {
			PID  int    `json:"pid"`
			Name string `json:"name"`
			Tab  string `json:"tab"`
			Cwd  string `json:"cwd"`
		} `json:"processes"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("ROTA_TEST_REAP_HOST: %v", err)
	}
	fx := &reapFixture{path: path, agents: []host.Agent{}}
	for _, ws := range doc.Workspaces {
		for _, t := range ws.Tabs {
			fx.tabs = append(fx.tabs, host.Tab{ID: t.ID, Cwds: []string{t.Cwd}, Agentless: !t.Agent})
		}
	}
	for _, a := range doc.Agents {
		fx.agents = append(fx.agents, host.Agent{Tab: a.Tab, Name: a.Name, Cwd: a.Cwd, Status: a.Status})
	}
	for _, p := range doc.Processes {
		fx.procs = append(fx.procs, reap.Process{PID: p.PID, Name: p.Name, Tab: p.Tab, Cwd: p.Cwd})
	}
	return fx, nil
}

func (f *reapFixture) Tabs(context.Context) ([]host.Tab, error)          { return f.tabs, nil }
func (f *reapFixture) Processes(context.Context) ([]reap.Process, error) { return f.procs, nil }
func (f *reapFixture) CloseTab(_ context.Context, id string) error       { return f.removed("tab " + id) }
func (f *reapFixture) StopProcess(_ context.Context, pid int) error {
	return f.removed(fmt.Sprintf("process %d", pid))
}

func (f *reapFixture) removed(line string) error {
	fh, err := os.OpenFile(f.path+".removed", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer fh.Close()
	_, err = fmt.Fprintln(fh, line)
	return err
}

func reapCommand() *Command {
	return &Command{Name: "reap", Summary: "list (and with --apply remove) what a round left behind that nothing live owns", Verb: reapVerb}
}

func reapVerb(fs *flag.FlagSet) RunFunc {
	kind := fs.String("kind", "", "only these kinds, comma-separated: worktree, branch, tab, process, lease")
	apply := fs.Bool("apply", false, "delete the candidates that hold no work (default: preview)")
	return func(c *Ctx, args []string) (Result, error) {
		if err := noArgs(args); err != nil {
			return Result{}, err
		}
		kinds, err := parseReapKinds(*kind)
		if err != nil {
			return Result{}, err
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		ctx := c.Context()
		env, ops := reapEnv(ctx, root)
		var agents []host.Agent
		if snap := env.Snapshot; snap != nil {
			env.Snapshot = func(ctx context.Context) ([]host.Agent, error) {
				a, err := snap(ctx)
				agents = a
				return a, err
			}
		}
		rep, err := env.Status(ctx, root)
		if err != nil {
			return Result{}, fromWorker(err)
		}
		in := reap.Input{Root: root, Base: env.Base, Git: env.Git, Report: rep, Agents: agents, Host: ops, Lease: env}
		found, err := reap.Find(ctx, in, kinds)
		if err != nil {
			return Result{}, fromWorker(err)
		}
		for _, w := range found.Warnings {
			c.Warn("%s", w)
		}
		var applied reap.Result
		if *apply {
			applied = reap.Apply(ctx, in, found.Candidates)
			for _, w := range applied.Warnings {
				c.Warn("%s", w)
			}
		} else {
			c.Warn("preview only; pass --apply")
		}

		cands := make([]any, 0, len(found.Candidates))
		var lines []string
		for _, cd := range found.Candidates {
			o := jsonx.NewObject()
			o.Set("id", cd.ID)
			o.Set("kind", cd.Kind)
			o.Set("name", cd.Name)
			setIf(o, "path", cd.Path)
			o.Set("reason", cd.Reason)
			setIf(o, "held", cd.Held)
			cands = append(cands, o)
			line := cd.ID + "\t" + cd.Reason
			if cd.Held != "" {
				line += "\theld: " + cd.Held
			}
			lines = append(lines, line)
		}
		failed := make([]any, 0, len(applied.Failed))
		for _, f := range applied.Failed {
			o := jsonx.NewObject()
			o.Set("id", f.ID)
			o.Set("error", f.Error)
			failed = append(failed, o)
			lines = append(lines, "failed\t"+f.ID+"\t"+f.Error)
		}
		d := jsonx.NewObject()
		d.Set("candidates", cands)
		d.Set("reaped", strs(applied.Reaped))
		d.Set("failed", failed)
		d.Set("changed", len(applied.Reaped) > 0)
		res := Result{Data: d, Text: strings.Join(lines, "\n")}
		if len(applied.Failed) > 0 {
			return res, Failed("reap: %d deletion(s) failed", len(applied.Failed))
		}
		return res, nil
	}
}

func parseReapKinds(s string) ([]string, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	var out []string
	for _, k := range strings.Split(s, ",") {
		k = strings.TrimSpace(k)
		ok := false
		for _, known := range reap.Kinds {
			ok = ok || k == known
		}
		if !ok {
			return nil, Usage("unknown --kind %q (want %s)", k, strings.Join(reap.Kinds, ", "))
		}
		out = append(out, k)
	}
	return out, nil
}
