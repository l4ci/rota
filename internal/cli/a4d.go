package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/issues"
	"github.com/l4ci/rota/internal/repos"
	"github.com/l4ci/rota/internal/tracker"
)

// The A4 `rota issues list|label|imported|close|provider` and `rota migrate
// issues` verbs. Shapes, flags and exits are the verb contract's
// (docs/design/5.0-verb-contract.md); the old helpers named in each `old:`
// line are the behaviour to match. These verbs exec gh or glab, always
// through internal/tracker.

func a4dCommands() []*Command {
	return []*Command{
		{Name: "issues", Summary: "upstream issues on GitHub or GitLab", Subs: []*Command{
			{Name: "list", Summary: "open upstream issues", Repo: true, Verb: a4dList},
			{Name: "label", Summary: "add or remove a label on an upstream issue", Repo: true, Verb: a4dLabel},
			{Name: "imported", Summary: "backlog items that point at upstream issues", Verb: a4dImported},
			{Name: "close", Summary: "close an upstream issue naming the shipping commit", Repo: true, Verb: a4dClose},
			{Name: "provider", Summary: "github, gitlab or unknown for the origin remote", Repo: true, Verb: a4dProvider},
		}},
	}
}

// a4dScope is the project root, the issues config and the directory the
// forge CLI runs in (scope S): --repo's sub-repo, else the registered
// sub-repo the working directory is in, else the root.
func a4dScope(c *Ctx) (root, dir string, env issues.Env, err error) {
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
	env = issues.Env{Settings: tracker.SettingsFromConfig(config.Load(filepath.Join(root, ".rota", "config.json"))), Opts: trackerOptions}
	return
}

// a4dErr maps the issues package's failures onto the exit table.
func a4dErr(err error) error {
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

// a4dNumber is an issue number argument.
func a4dNumber(c *Ctx, text string) (int, error) {
	n, err := strconv.Atoi(text)
	if err != nil || n < 0 || strings.TrimSpace(text) != text || strings.HasPrefix(text, "+") {
		return 0, Usage("%s: issue must be a number, got %q", c.Path, text)
	}
	return n, nil
}

// ---- provider ---------------------------------------------------------------

func a4dProvider(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if err := a4Args(c, args, 0, 0, "issues provider takes no arguments"); err != nil {
			return Result{}, err
		}
		_, dir, env, err := a4dScope(c)
		if err != nil {
			return Result{}, err
		}
		p := issues.Provider(c.Context(), env, dir)
		return Result{Data: a4Obj("provider", p), Text: p}, nil
	}
}

// ---- list -------------------------------------------------------------------

func a4dList(fs *flag.FlagSet) RunFunc {
	mine := fs.Bool("mine", false, "only issues assigned to you")
	label := fs.String("label", "", "only issues with this label")
	limit := fs.String("limit", "30", "most issues to fetch")
	return func(c *Ctx, args []string) (Result, error) {
		if err := a4Args(c, args, 0, 0, "issues list takes no positional arguments"); err != nil {
			return Result{}, err
		}
		given := a4Given(fs)
		if given["label"] && *label == "" {
			return Result{}, Usage("%s: --label needs a name", c.Path)
		}
		n, err := strconv.Atoi(*limit)
		if err != nil || n < 1 || strings.HasPrefix(*limit, "+") {
			return Result{}, Usage("%s: --limit must be a positive number", c.Path)
		}
		_, dir, env, err := a4dScope(c)
		if err != nil {
			return Result{}, err
		}
		list, err := issues.List(c.Context(), env, dir, issues.ListOpts{Mine: *mine, Label: *label, Limit: n})
		if err != nil {
			return Result{}, a4dErr(err)
		}
		rows := []any{}
		var lines []string
		for _, is := range list {
			rows = append(rows, a4Obj("number", is.Number, "title", is.Title, "body", is.Body,
				"labels", is.Labels, "url", is.URL, "author", is.Author))
			line := fmt.Sprintf("#%v %v", is.Number, is.Title)
			if len(is.Labels) > 0 {
				var ls []string
				for _, l := range is.Labels {
					ls = append(ls, fmt.Sprint(l))
				}
				line += " [" + strings.Join(ls, ", ") + "]"
			}
			lines = append(lines, line)
		}
		return Result{Data: a4Obj("issues", rows), Text: strings.Join(lines, "\n")}, nil
	}
}

// ---- label ------------------------------------------------------------------

func a4dLabel(fs *flag.FlagSet) RunFunc {
	add := fs.String("add", "", "label to add")
	remove := fs.String("remove", "", "label to remove")
	return func(c *Ctx, args []string) (Result, error) {
		if err := a4Args(c, args, 1, 1, "issues label takes one issue number"); err != nil {
			return Result{}, err
		}
		given := a4Given(fs)
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
		number, err := a4dNumber(c, args[0])
		if err != nil {
			return Result{}, err
		}
		root, dir, env, err := a4dScope(c)
		if err != nil {
			return Result{}, err
		}
		auto, _ := config.Value(config.Load(filepath.Join(root, ".rota", "config.json")), "issues.autoCreateLabel")
		changed, err := issues.Label(c.Context(), env, dir, number, name, action == "add", auto != false && auto != nil)
		if err != nil {
			return Result{}, a4dErr(err)
		}
		return Result{Data: a4Obj("issue", number, "label", name, "action", action, "changed", changed),
			Text: fmt.Sprintf("%s %s on #%d", action, name, number)}, nil
	}
}

// ---- imported ---------------------------------------------------------------

func a4dImported(fs *flag.FlagSet) RunFunc {
	forRepo := fs.String("for-repo", "", "only entries of this Repos: name")
	openOnly := fs.Bool("open-only", false, "drop issues that are closed upstream")
	return func(c *Ctx, args []string) (Result, error) {
		if err := a4Args(c, args, 0, 0, "issues imported takes no positional arguments"); err != nil {
			return Result{}, err
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		entries := backlog.ScanImported(root, *forRepo)
		if *openOnly {
			ctx := c.Context()
			env := issues.Env{Settings: tracker.SettingsFromConfig(config.Load(filepath.Join(root, ".rota", "config.json"))), Opts: trackerOptions}
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
			rows = append(rows, a4Obj("provider", e.Provider, "repo", nullStr(e.Repo), "issue", e.Issue,
				"itemId", e.ItemID, "status", e.Status))
			repo := ""
			if e.Repo != "" {
				repo = e.Repo
			}
			lines = append(lines, fmt.Sprintf("%s %s#%d %s %s", e.Provider, repo, e.Issue, e.ItemID, e.Status))
		}
		return Result{Data: a4Obj("entries", rows), Text: strings.Join(lines, "\n")}, nil
	}
}

// ---- close ------------------------------------------------------------------

func a4dClose(fs *flag.FlagSet) RunFunc {
	commit := fs.String("commit", "", "the commit that shipped the issue")
	item := fs.String("item", "", "backlog item ID named in the comment")
	return func(c *Ctx, args []string) (Result, error) {
		if err := a4Args(c, args, 1, 1, "issues close takes one issue number"); err != nil {
			return Result{}, err
		}
		if *commit == "" {
			return Result{}, Usage("%s: --commit is required", c.Path)
		}
		number, err := a4dNumber(c, args[0])
		if err != nil {
			return Result{}, err
		}
		_, dir, env, err := a4dScope(c)
		if err != nil {
			return Result{}, err
		}
		changed, err := issues.Close(c.Context(), env, dir, number, *commit, *item)
		if err != nil {
			return Result{}, a4dErr(err)
		}
		text := fmt.Sprintf("closed #%d", number)
		if !changed {
			text = fmt.Sprintf("#%d already closed", number)
		}
		return Result{Data: a4Obj("issue", number, "commit", *commit, "changed", changed), Text: text}, nil
	}
}

// ---- migrate issues -----------------------------------------------------------

// migrateSleep and migrateTracker are seams: tests pin the pace and the forge.
var (
	migrateSleep   func(d time.Duration)
	migrateTracker = func(ctx context.Context, root string, cfg any) (backlog.MigrateTracker, error) {
		return tracker.New(ctx, tracker.SettingsFromConfig(cfg), "", root, trackerOptions...)
	}
)

func a4dMigrateIssues(fs *flag.FlagSet) RunFunc {
	apply := fs.Bool("apply", false, "do the work; without it only report the plan")
	limit := fs.String("limit", "", "create at most this many items this run")
	return func(c *Ctx, args []string) (Result, error) {
		if err := a4Args(c, args, 0, 0, "migrate issues takes no positional arguments"); err != nil {
			return Result{}, err
		}
		lim := -1
		if a4Given(fs)["limit"] {
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
		cfg := config.Load(filepath.Join(root, ".rota", "config.json"))
		// Notices are kept until the run's outcome is known: a failure answers
		// with its error alone, so they go to stderr only.
		var notices []string
		opts := backlog.MigrateOptions{Root: root, Apply: *apply, Limit: lim, Cfg: cfg, Ctx: c.Context(),
			Sleep: migrateSleep, Warn: func(s string) { notices = append(notices, s) },
			Tracker: func() (backlog.MigrateTracker, error) { return migrateTracker(c.Context(), root, cfg) }}
		res, err := backlog.MigrateIssues(opts)
		if err != nil {
			for _, n := range notices {
				fmt.Fprintf(c.Stderr, "%s: warning: %s\n", c.Path, n)
			}
			return a4dMigrateFail(res, err)
		}
		for _, n := range notices {
			c.Warn("%s", n)
		}
		if !*apply {
			c.Warn("preview only; pass --apply")
		}
		ops := []any{}
		for _, o := range res.Ops {
			ops = append(ops, a4Obj("action", o.Action, "text", o.Text))
		}
		data := a4Obj("applied", *apply, "operations", ops, "map", res.Map, "migrated", res.Migrated,
			"total", res.Total, "changed", res.Changed)
		return Result{Data: data, Text: strings.Join(res.Lines, "\n")}, nil
	}
}

// a4dMigrateFail maps a stopped migration onto the exit table. A tracker
// failure carries no failure data, so its message ends with the progress the
// map kept.
func a4dMigrateFail(res *backlog.MigrateResult, err error) (Result, error) {
	var te *tracker.Error
	switch {
	case errors.Is(err, backlog.ErrNothingToMigrate):
		return Result{}, Resolution("%s", strings.TrimPrefix(err.Error(), backlog.ErrNothingToMigrate.Error()+": ")+" (nothing to migrate)")
	case errors.Is(err, backlog.ErrUmbrellaMigrate):
		return Result{Data: a4Obj("blockedBy", "umbrella", "changed", false)},
			Refused("%s", err.Error()).WithHint("run rota migrate issues inside each sub-repo")
	case errors.Is(err, backlog.ErrBadMap):
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
