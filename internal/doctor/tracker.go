package doctor

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// ConnCheck is the outcome of the project's tracker connection check. State
// is a connections.State: ok, unknown, nosecret, failed or unsupported.
type ConnCheck struct {
	Name, State, Detail, Hint string
}

// connection is the project's tracker connection: it must exist on this
// machine, hold a secret and pass its test read. Only a project that names one
// gets the line. A kind with no test read yet warns instead of failing: the
// connection is fine, this build cannot prove it.
func (d *runner) connection() (Check, bool) {
	c := d.in.Connection
	if c == nil {
		return Check{}, false
	}
	switch c.State {
	case "ok":
		return pass("connection", c.Detail), true
	case "unsupported":
		return Check{Name: "connection", Status: Warn, Detail: c.Detail, Hint: c.Hint}, true
	}
	return fail("connection", c.Detail, c.Hint), true
}

// trackerExec adapts the injected Exec and Look to the tracker's, so the
// tracker owners (OriginURL, CLI) run through doctor's test seam.
func (d *runner) trackerExec() tracker.Exec {
	return func(ctx context.Context, dir, name string, args []string, _ []byte) ([]byte, []byte, int, error) {
		bin, ok := d.in.Look(name)
		if !ok {
			return nil, nil, 0, exec.ErrNotFound
		}
		r, err := d.in.Exec(ctx, bin, args, nil, dir)
		if err != nil {
			return nil, nil, 0, err
		}
		return []byte(r.Stdout), []byte(r.Stderr), r.ExitCode, nil
	}
}

// resolveForge is the forge doctor resolves, as a reader
// (tracker.ResolveProvider): an origin naming no forge is an assumed github.
func (d *runner) resolveForge() tracker.Resolution {
	r, _ := tracker.ResolveProvider(d.ctx, tracker.ForgeReader, "", d.in.Tracker.Provider, d.in.Dir, d.trackerExec())
	return r
}

// forgeLine says which forge doctor resolved and how, and what issues.source
// is: "forge github (from origin), issues file". An empty source (outside a
// rota project) leaves the issues half out.
func (d *runner) forgeLine(r tracker.Resolution) string {
	how := map[string]string{
		tracker.HowConfig:   "forge.provider",
		tracker.HowOrigin:   "from origin",
		tracker.HowNoOrigin: "no origin remote",
		tracker.HowAssumed:  "assumed: origin " + r.Origin + " names no forge",
	}[r.How]
	line := "forge " + r.Provider + " (" + how + ")"
	switch src := d.in.Tracker.Source; src {
	case "":
	case config.SourceIssues:
		line += ", issues on the tracker (legacy backlog.backend, run: rota migrate source --apply)"
	default:
		line += ", issues " + src
	}
	return line
}

// tracker checks the forge and the issue source together: it reports the
// detected forge (no remote with forge.provider auto is local) and the issue
// source, fails when issues.source names github or gitlab but the forge is
// something else (the issues and the code would live on different hosts), and
// otherwise checks that the forge CLI is installed and authenticated.
func (d *runner) tracker() Check {
	if d.in.SourceProblem != "" {
		return fail("tracker", d.in.SourceProblem, "run: rota config set forge.provider auto, and rota config set issues.source file|github|gitlab")
	}
	if d.in.Tracker.Source == config.SourceJira {
		switch {
		case d.in.Tracker.JiraProject == "":
			return fail("tracker", "issues.source is jira but issues.jira.project is not set", "run: rota config set issues.jira.project <KEY>")
		case d.in.Tracker.Connection == "":
			return fail("tracker", "issues.source is jira but issues.connection is not set", "run: rota connect add <name> --kind jira ..., then rota config set issues.connection <name>")
		}
	}
	if d.in.Tracker.Source == config.SourceLinear {
		switch {
		case d.in.Tracker.LinearTeam == "":
			return fail("tracker", "issues.source is linear but issues.linear.team is not set", "run: rota config set issues.linear.team <KEY>")
		case d.in.Tracker.Connection == "":
			return fail("tracker", "issues.source is linear but issues.connection is not set", "run: rota connect add <name> --kind linear --token-stdin, then rota config set issues.connection <name>")
		case d.in.TeamProblem != "":
			return fail("tracker", d.in.TeamProblem, "check issues.linear.team against the team's key in Linear, and the API key's access")
		}
	}
	if d.in.Tracker.Source == config.SourceItsaplan {
		switch {
		case d.in.Tracker.ItsaplanProject == "":
			return fail("tracker", "issues.source is itsaplan but issues.itsaplan.project is not set", "run: rota config set issues.itsaplan.project <KEY>")
		case d.in.Tracker.Connection == "":
			return fail("tracker", "issues.source is itsaplan but issues.connection is not set", "run: rota connect add <name> --kind itsaplan --site <url> --token-stdin, then rota config set issues.connection <name>")
		case d.in.TeamProblem != "":
			return fail("tracker", d.in.TeamProblem, "check issues.itsaplan.project against the project's key in It's a Plan, and the API key's access")
		}
	}
	r := d.resolveForge()
	f, line := r.Provider, d.forgeLine(r)
	if src := d.in.Tracker.Source; (src == config.SourceGitHub || src == config.SourceGitLab) && f != src {
		return fail("tracker", "issues.source is "+src+" but the forge is "+f+": "+line,
			"set issues.source "+strutil.OrDefault(f, "file")+", or forge.provider "+src+" if detection is wrong")
	}
	if f == tracker.ProviderLocal {
		return skip("tracker", "no pull requests: "+line)
	}
	cli := tracker.CLIName(f)
	if _, ok := d.in.Look(cli); !ok {
		return fail("tracker", cli+" not found on PATH", "install "+cli)
	}
	c := &tracker.CLI{Provider: f, Dir: d.in.Dir, Exec: d.trackerExec(), LookPath: func(n string) (string, error) {
		if bin, ok := d.in.Look(n); ok {
			return bin, nil
		}
		return "", exec.ErrNotFound
	}}
	if err := c.CheckAuth(d.ctx); err != nil {
		return fail("tracker", cli+" is not authenticated", cli+" auth login")
	}
	return pass("tracker", cli+" authenticated ("+line+")")
}

func (d *runner) expand(dir string) string { return worker.ExpandConfigDirIn(dir, d.in.Home) }

func (d *runner) accounts() Check {
	if w := worker.ConfigDirMismatch(d.in.OrchestratorConfigDir, d.in.Accounts, d.in.Home); w != "" {
		return Check{Name: "accounts", Status: Warn, Detail: w, Hint: "add that dir to round.accounts in .rota/config.local.json, or start the orchestrator under a listed one"}
	}
	if len(d.in.Accounts) == 0 {
		return skip("accounts", "no accounts in round.accounts")
	}
	var bad []string
	var hint string
	for _, a := range d.in.Accounts {
		var dir, why string
		if a.Dir == "" {
			why = "no dir"
			if hint == "" {
				hint = "set dir for account " + a.Name + " in round.accounts"
			}
		} else if p := config.DirProblem("dir", a.Dir); p != "" {
			why = p
			if hint == "" {
				hint = "set dir for account " + a.Name + " to an absolute path or ~/..."
			}
		} else if dir = worker.ExpandConfigDirIn(a.Dir, d.in.Home); !fsio.IsDir(dir) {
			why = "dir " + dir + " does not exist"
		} else if p := d.in.Credentials.Problem(dir); p != "" {
			why = p + " in " + dir
		}
		if why != "" {
			bad = append(bad, a.Name+": "+why)
			if hint == "" {
				hint = "CLAUDE_CONFIG_DIR=" + a.Dir + " claude /login"
				if strings.Contains(why, "Keychain") {
					hint += " (on macOS the login is stored in the Keychain, not a file)"
				}
			}
		}
	}
	if len(bad) > 0 {
		return fail("accounts", strings.Join(bad, "; "), hint)
	}
	return pass("accounts", fmt.Sprintf("%d accounts with credentials", len(d.in.Accounts)))
}
