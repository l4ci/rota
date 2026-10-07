package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/issues"
	"github.com/l4ci/rota/internal/migrate"
	"github.com/l4ci/rota/internal/repos"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/tracker"
)

// The `rota issues label|imported|close|provider` and `rota migrate
// issues` verbs. Shapes, flags and exits are the verb contract's
// (docs/contributing/contract/); the old helpers named in each `old:`
// line are the behaviour to match. These verbs exec gh or glab, always
// through internal/tracker.

func issuesCommands() []*Command {
	return []*Command{
		{Name: "issues", Summary: "upstream issues on GitHub or GitLab", Subs: []*Command{
			{Name: "label", Summary: "add or remove a label on an upstream issue", Repo: true, Verb: issuesLabel},
			{Name: "imported", Summary: "backlog items that point at upstream issues", Verb: issuesImported},
			{Name: "close", Summary: "close an upstream issue naming the shipping commit", Repo: true, Verb: issuesClose},
			{Name: "provider", Summary: "github, gitlab or unknown for the origin remote", Repo: true, Verb: issuesProvider},
		}},
	}
}

// issuesScope is the project root, the issues config and the directory the
// forge CLI runs in (scope S): --repo's sub-repo, else the registered
// sub-repo the working directory is in, else the root.
func issuesScope(c *Ctx) (root, dir string, env issues.Env, err error) {
	root, err = c.Root()
	if err != nil {
		return
	}
	dir = root
	if c.Repo != "" {
		if dir, err = c.RepoPath(); err != nil {
			return
		}
	} else if cwd, werr := os.Getwd(); werr == nil {
		here := repos.Realpath(cwd)
		for _, r := range repos.Load(root) {
			if rel, rerr := filepath.Rel(r.Path, here); rerr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				dir = r.Path
				break
			}
		}
	}
	env = issues.Env{Config: config.Load(rotatree.Config(root)), Opts: c.deps().TrackerOptions}
	return
}

// issuesErr maps the issues package's failures onto the exit table.
func issuesErr(err error) error {
	var te *tracker.Error
	switch {
	case err == nil:
		return nil
	case errors.Is(err, issues.ErrCommitNotFound):
		return Resolution("%s", strings.TrimPrefix(err.Error(), issues.ErrCommitNotFound.Error()+": "))
	case errors.Is(err, issues.ErrNoProvider):
		return Resolution("%s", strings.TrimPrefix(err.Error(), issues.ErrNoProvider.Error()+": "))
	case errors.As(err, &te):
		switch te.Kind {
		case tracker.KindNotFound:
			return Resolution("%s", te.Message)
		case tracker.KindRateLimited:
			return Retry("%s", te.Message)
		case tracker.KindInternal:
			return &Error{Exit: ExitInternal, Message: te.Message}
		}
		return Unavailable("%s", te.Message)
	}
	return &Error{Exit: ExitInternal, Message: err.Error()}
}

// issueNumber is an issue number argument.
func issueNumber(c *Ctx, text string) (int, error) {
	n, err := strconv.Atoi(text)
	if err != nil || n < 0 || strings.TrimSpace(text) != text || strings.HasPrefix(text, "+") {
		return 0, Usage("%s: issue must be a number, got %q", c.Path, text)
	}
	return n, nil
}

// ---- provider ---------------------------------------------------------------

func issuesProvider(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 0, 0, "issues provider takes no arguments"); err != nil {
			return Result{}, err
		}
		_, dir, env, err := issuesScope(c)
		if err != nil {
			return Result{}, err
		}
		p := issues.Provider(c.Context(), env, dir)
		return Result{Data: jsonObj("provider", p), Text: p}, nil
	}
}

// ---- label ------------------------------------------------------------------

func issuesLabel(fs *flag.FlagSet) RunFunc {
	add := fs.String("add", "", "label to add")
	remove := fs.String("remove", "", "label to remove")
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 1, 1, "issues label takes one issue number"); err != nil {
			return Result{}, err
		}
		given := givenFlags(fs)
		if given["add"] == given["remove"] {
			return Result{}, Usage("%s: pass exactly one of --add and --remove", c.Path)
		}
		action, name := "add", *add
		if given["remove"] {
			action, name = "remove", *remove
		}
		if name == "" {
			return Result{}, Usage("%s: --%s needs a label name", c.Path, action)
		}
		number, err := issueNumber(c, args[0])
		if err != nil {
			return Result{}, err
		}
		root, dir, env, err := issuesScope(c)
		if err != nil {
			return Result{}, err
		}
		auto, _ := config.Value(config.Load(rotatree.Config(root)), "issues.autoCreateLabel")
		changed, err := issues.Label(c.Context(), env, dir, number, name, action == "add", auto != false && auto != nil)
		if err != nil {
			return Result{}, issuesErr(err)
		}
		return Result{Data: jsonObj("issue", number, "label", name, "action", action, "changed", changed),
			Text: fmt.Sprintf("%s %s on #%d", action, name, number)}, nil
	}
}

// ---- imported ---------------------------------------------------------------

func issuesImported(fs *flag.FlagSet) RunFunc {
	forRepo := fs.String("for-repo", "", "only entries of this Repos: name")
	openOnly := fs.Bool("open-only", false, "drop issues that are closed upstream")
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 0, 0, "issues imported takes no positional arguments"); err != nil {
			return Result{}, err
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		entries := backlog.ScanImported(root, *forRepo)
		if *openOnly {
			ctx := c.Context()
			env := issues.Env{Config: config.Load(rotatree.Config(root)), Opts: c.deps().TrackerOptions}
			paths := repos.Paths(root)
			var kept []backlog.Imported
			for _, e := range entries {
				dir := root
				if p, ok := paths[e.Repo]; ok && e.Repo != "" {
					dir = p
				}
				if issues.StillOpen(ctx, env, e.Provider, dir, e.Issue) {
					kept = append(kept, e)
				}
			}
			entries = kept
		}
		rows := []any{}
		var lines []string
		for _, e := range entries {
			rows = append(rows, jsonObj("provider", e.Provider, "repo", nullStr(e.Repo), "issue", e.Issue,
				"itemId", e.ItemID, "status", e.Status))
			repo := ""
			if e.Repo != "" {
				repo = e.Repo
			}
			lines = append(lines, fmt.Sprintf("%s %s#%d %s %s", e.Provider, repo, e.Issue, e.ItemID, e.Status))
		}
		return Result{Data: jsonObj("entries", rows), Text: strings.Join(lines, "\n")}, nil
	}
}

// ---- close ------------------------------------------------------------------

func issuesClose(fs *flag.FlagSet) RunFunc {
	commit := fs.String("commit", "", "the commit that shipped the issue")
	item := fs.String("item", "", "backlog item ID named in the comment")
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 1, 1, "issues close takes one issue number"); err != nil {
			return Result{}, err
		}
		if *commit == "" {
			return Result{}, Usage("%s: --commit is required", c.Path)
		}
		number, err := issueNumber(c, args[0])
		if err != nil {
			return Result{}, err
		}
		_, dir, env, err := issuesScope(c)
		if err != nil {
			return Result{}, err
		}
		changed, err := issues.Close(c.Context(), env, dir, number, *commit, *item)
		if err != nil {
			return Result{}, issuesErr(err)
		}
		text := fmt.Sprintf("closed #%d", number)
		if !changed {
			text = fmt.Sprintf("#%d already closed", number)
		}
		return Result{Data: jsonObj("issue", number, "commit", *commit, "changed", changed), Text: text}, nil
	}
}

// ---- migrate issues -----------------------------------------------------------

func migrateIssues(fs *flag.FlagSet) RunFunc {
	apply := fs.Bool("apply", false, "do the work; without it only report the plan")
	limit := fs.String("limit", "", "create at most this many items this run")
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 0, 0, "migrate issues takes no positional arguments"); err != nil {
			return Result{}, err
		}
		lim := -1
		if givenFlags(fs)["limit"] {
			n, err := strconv.Atoi(*limit)
			if *limit == "" || err != nil || n < 0 || strings.HasPrefix(*limit, "+") {
				return Result{}, Usage("%s: --limit must be a number", c.Path)
			}
			lim = n
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		cfg := config.Load(rotatree.Config(root))
		// Notices are kept until the run's outcome is known: a failure answers
		// with its error alone, so they go to stderr only.
		var notices []string
		opts := migrate.Options{Root: root, Apply: *apply, Limit: lim, Cfg: cfg, Ctx: c.Context(),
			Sleep: c.deps().MigrateSleep, Warn: func(s string) { notices = append(notices, s) },
			NoteLimit: os.Getenv("ROTA_NOTE_LIMIT"),
			Tracker:   func() (migrate.Tracker, error) { return c.deps().MigrateTracker(c.Context(), root, cfg) }}
		res, err := migrate.Run(opts)
		if err != nil {
			for _, n := range notices {
				fmt.Fprintf(c.Stderr, "%s: warning: %s\n", c.Path, n)
			}
			return migrateIssuesFail(res, err)
		}
		for _, n := range notices {
			c.Warn("%s", n)
		}
		if !*apply {
			c.Warn("preview only; pass --apply")
		}
		ops := []any{}
		for _, o := range res.Ops {
			ops = append(ops, jsonObj("action", o.Action, "text", o.Text))
		}
		data := jsonObj("applied", *apply, "operations", ops, "map", res.Map, "migrated", res.Migrated,
			"total", res.Total, "changed", res.Changed)
		return Result{Data: data, Text: strings.Join(res.Lines, "\n")}, nil
	}
}

// migrateIssuesFail maps a stopped migration onto the exit table. A tracker
// failure carries no failure data, so its message ends with the progress the
// map kept.
func migrateIssuesFail(res *migrate.Result, err error) (Result, error) {
	var te *tracker.Error
	switch {
	case errors.Is(err, migrate.ErrNothingToMigrate):
		return Result{}, Resolution("%s", strings.TrimPrefix(err.Error(), migrate.ErrNothingToMigrate.Error()+": ")+" (nothing to migrate)")
	case errors.Is(err, migrate.ErrUmbrellaMigrate):
		return Result{Data: jsonObj("blockedBy", "umbrella", "changed", false)},
			Refused("%s", err.Error()).WithHint("run rota migrate issues inside each sub-repo")
	case errors.Is(err, migrate.ErrBadMap):
		return Result{}, &Error{Exit: ExitInternal, Message: err.Error()}
	case errors.As(err, &te):
		progress := fmt.Sprintf("%d of %d migrated", res.Migrated, res.Total)
		switch te.Kind {
		case tracker.KindRateLimited:
			return Result{}, Retry("%s", progress)
		case tracker.KindInternal:
			return Result{}, &Error{Exit: ExitInternal, Message: te.Message + "; " + progress}
		}
		return Result{}, Unavailable("%s; %s", te.Message, progress)
	case errors.Is(err, backlog.ErrInvalid):
		return Result{}, Usage("%s", err.Error())
	}
	return Result{}, &Error{Exit: ExitInternal, Message: err.Error()}
}
