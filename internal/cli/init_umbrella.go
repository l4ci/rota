package cli

import (
	"errors"
	"flag"
	"os"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/initproj"
	"github.com/l4ci/rota/internal/rotatree"
)

// seedProject is the base seeding `rota init` does; `init umbrella` runs it
// first (the old init skill always ran hv-bootstrap before hv-umbrella-init).
func seedProject(root string) error { _, err := initproj.Init(root); return err }

func initUmbrella(fs *flag.FlagSet) RunFunc {
	repos := fs.String("repos", "", "comma-separated sub-repo names to register (empty: none)")
	all := fs.Bool("all", false, "register every immediate git child")
	list := fs.Bool("list", false, "only list the candidates; write nothing")
	return func(c *Ctx, args []string) (Result, error) {
		if err := knNoArgs(args); err != nil {
			return Result{}, err
		}
		reposGiven := false
		fs.Visit(func(f *flag.Flag) { reposGiven = reposGiven || f.Name == "repos" })
		root, err := os.Getwd()
		if err != nil {
			return Result{}, err
		}
		if *list {
			if reposGiven || *all {
				return Result{}, Usage("--list takes neither --repos nor --all")
			}
			l, err := initproj.List(root)
			if err != nil {
				return Result{}, Resolution("%v", unwrapPathErr(err))
			}
			return Result{Data: knObj("root", root, "candidates", strSlice(l.Candidates), "isGitRepo", l.IsGitRepo),
				Text: "candidates: " + listOrNone(l.Candidates)}, nil
		}
		if reposGiven == *all {
			return Result{}, Usage("give exactly one of --repos <csv> or --all")
		}
		var names []string
		if reposGiven && *repos != "" {
			names = strings.Split(*repos, ",")
		}
		var seed func() error
		if seedBase := c.deps().SeedBase; seedBase != nil {
			seed = func() error { return seedBase(root) }
		}
		res, err := initproj.Umbrella(root, initproj.UmbrellaOptions{All: *all, Names: names}, seed)
		if err != nil {
			if errors.Is(err, initproj.ErrNoCandidates) {
				return Result{}, Resolution("%v", err)
			}
			return Result{}, &Error{Exit: ExitInternal, Message: err.Error()}
		}
		for _, w := range res.Warnings {
			c.Warn("%s", w)
		}
		cfg, err := initConfig(c, root, false)
		if err != nil {
			return Result{}, err
		}
		enabled := false
		if len(res.Registered) > 0 {
			if cur, _ := config.Lookup(config.Load(rotatree.Config(root)), "umbrella.enabled"); cur != true {
				if _, err := config.Set(root, "umbrella.enabled", "true"); err != nil {
					return Result{}, err
				}
				enabled = true
			}
		}
		data := knObj("root", root, "created", strSlice(res.Created), "registered", strSlice(res.Registered),
			"umbrellaIsGitRepo", res.IsGitRepo, "changed", res.Changed || cfg.changed() || enabled)
		text := []string{"registered: " + listOrNone(res.Registered)}
		cfg.report(data, &text)
		data.Set("umbrellaEnabled", enabled)
		if enabled {
			text = append(text, "set umbrella.enabled: true")
		}
		return Result{Data: data, Text: strings.Join(text, "\n")}, nil
	}
}

func listOrNone(l []string) string {
	if len(l) == 0 {
		return "none"
	}
	return strings.Join(l, ", ")
}
