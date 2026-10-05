package cli

import (
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/gate"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/tracker"
)

// trackerOptions are applied to every forge CLI the verbs build; tests use
// them to swap in a fake executor.
var trackerOptions []tracker.Option

// trackerCommands is the `rota tracker` group (#52).
func trackerCommands() *Command {
	return &Command{Name: "tracker", Summary: "gh/glab passthrough and upstream issues", Subs: []*Command{
		{Name: "call", Summary: "run gh or glab with list limits and rate-limit handling", Repo: true, Verb: trCall},
		{Name: "suggest-upstream", Summary: "file a rota issue from a learning", Verb: trSuggest},
	}}
}

// trackerSettings reads the issues.* config of the project, or the defaults
// outside one.
func trackerSettings(c *Ctx) tracker.Settings {
	root, err := c.Root()
	if err != nil {
		return tracker.SettingsFromConfig(nil)
	}
	return tracker.SettingsFromConfig(config.Load(filepath.Join(root, ".rota", "config.json")))
}

// trackerErr maps a tracker failure onto the exit table through its kind.
func trackerErr(err error) *Error {
	var e *tracker.Error
	if errors.As(err, &e) {
		return newErr(e.Kind.Exit(), "", "%s", e.Message)
	}
	return newErr(ExitInternal, "", "%v", err)
}

func trCall(fs *flag.FlagSet) RunFunc {
	provider := fs.String("provider", "auto", "auto, github or gitlab")
	return func(c *Ctx, args []string) (Result, error) {
		switch *provider {
		case "auto", "github", "gitlab":
		default:
			return Result{}, Usage("--provider must be auto, github or gitlab, not %q", *provider)
		}
		if len(args) == 0 {
			return Result{}, Usage("no CLI arguments; usage: rota tracker call [--provider auto|github|gitlab] -- <cli-arg>...")
		}
		dir, err := c.RepoPath()
		if err != nil {
			return Result{}, err
		}
		ctx := c.Context()
		cl, err := tracker.NewCLI(ctx, trackerSettings(c), *provider, dir, trackerOptions...)
		if err != nil {
			return Result{}, trackerErr(err)
		}
		res, err := cl.Run(ctx, args, c.Stdin)
		if err != nil {
			return Result{}, trackerErr(err)
		}
		data := jsonx.NewObject()
		data.Set("provider", cl.Provider)
		data.Set("exitCode", res.ExitCode)
		data.Set("stdout", string(res.Stdout))
		data.Set("stderr", string(res.Stderr))
		if !c.JSON {
			// Passthrough: the CLI's output goes out unchanged.
			c.Stdout.Write(res.Stdout)
			c.Stderr.Write(res.Stderr)
		}
		if res.ExitCode != 0 {
			cli := "gh"
			if cl.Provider == "gitlab" {
				cli = "glab"
			}
			return Result{Data: data}, Failed("%s exited %d", cli, res.ExitCode)
		}
		return Result{Data: data}, nil
	}
}

func trSuggest(fs *flag.FlagSet) RunFunc {
	title := fs.String("title", "", "issue title")
	bodyFile := fs.String("body-file", "", "issue body: a path, or - for stdin")
	upstream := fs.String("upstream-repo", "", "owner/repo (default $ROTA_UPSTREAM_REPO, else l4ci/rota)")
	confirm := confirmFlags(fs)
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) > 0 {
			return Result{}, Usage("unexpected argument %q", args[0])
		}
		conf, err := confirm()
		if err != nil {
			return Result{}, err
		}
		if *title == "" {
			return Result{}, Usage("--title is required")
		}
		if *bodyFile == "" {
			return Result{}, Usage("--body-file is required")
		}
		var raw []byte
		if *bodyFile == "-" {
			raw, err = io.ReadAll(c.Stdin)
		} else {
			raw, err = os.ReadFile(*bodyFile)
		}
		if err != nil {
			return Result{}, Usage("--body-file %s: %v", *bodyFile, unwrapPathErr(err))
		}
		// The old helper read the body with $(cat), which drops trailing newlines.
		body := strings.TrimRight(string(raw), "\n")
		repo := *upstream
		if repo == "" {
			repo = os.Getenv("ROTA_UPSTREAM_REPO")
		}
		if repo == "" {
			repo = "l4ci/rota"
		}
		manual := "file it by hand at https://github.com/" + repo + "/issues/new"
		if res, err := clearGate(c, gate.PublicFiling, repo+": "+*title, conf, nil, nil); err != nil {
			return res, err
		}

		ctx := c.Context()
		cl, err := tracker.NewCLI(ctx, trackerSettings(c), "github", "", trackerOptions...)
		if err != nil {
			return Result{}, trackerErr(err).WithHint(manual)
		}
		if r, err := cl.Run(ctx, []string{"auth", "status"}, nil); err != nil || r.ExitCode != 0 {
			return Result{}, Unavailable("gh is not available or not authenticated").WithHint(manual)
		}
		r, err := cl.Run(ctx, []string{"issue", "create", "-R", repo, "-t", *title, "-F", "-"}, strings.NewReader(body))
		if err != nil {
			return Result{}, trackerErr(err).WithHint(manual)
		}
		if r.ExitCode != 0 {
			return Result{}, Unavailable("gh issue create failed: %s", strings.TrimSpace(string(r.Stderr))).WithHint(manual)
		}
		url := strings.TrimRight(string(r.Stdout), "\n")
		number, err := strconv.Atoi(url[strings.LastIndex(url, "/")+1:])
		if err != nil {
			return Result{}, Unavailable("gh issue create printed no issue URL: %q", url)
		}
		data := jsonx.NewObject()
		data.Set("url", url)
		data.Set("number", number)
		data.Set("upstreamRepo", repo)
		data.Set("changed", true)
		return Result{Data: data, Text: url}, nil
	}
}
