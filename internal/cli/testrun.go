package cli

import (
	"flag"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/worker"
)

func testCommands() *Command {
	return &Command{Name: "test", Summary: "run a config test tier", Subs: []*Command{
		{Name: "run", Summary: "run test.fast, test.full or test.e2e in order, stopping at the first failure", Verb: testRun},
	}}
}

// testRun is `rota test run <tier> [--base <ref>]`.
func testRun(fs *flag.FlagSet) RunFunc {
	base := fs.String("base", "", "ref {files} is diffed against (default: the base branch)")
	return func(c *Ctx, args []string) (Result, error) {
		tier, err := oneArg(args, "tier")
		if err != nil {
			return Result{}, err
		}
		switch tier {
		case "fast", "full", "e2e":
		default:
			return Result{}, Usage("unknown tier %q: want fast, full or e2e", tier)
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		cmds := worker.TierCommands(root, tier)
		if tier == "full" {
			cmds = worker.GateCommands(root) // the gate's lookup, legacy key included
		}
		if len(cmds) == 0 {
			return Result{}, Resolution("test.%s is empty", tier).WithHint(fmt.Sprintf("set it with: rota config set test.%s '[\"<command>\"]'", tier))
		}
		ctx, stop := workerContext()
		defer stop()
		if anyContains(cmds, filesPlaceholder) {
			files, err := changedFiles(c, root, *base)
			if err != nil {
				return Result{}, err
			}
			quoted := make([]string, len(files))
			for i, f := range files {
				quoted[i] = shellQuote(f)
			}
			list := strings.Join(quoted, " ")
			for i, cmd := range cmds {
				cmds[i] = strings.ReplaceAll(cmd, filesPlaceholder, list)
			}
		}
		env := workerEnvCtx(c, ctx)
		var ran, passed, failed []string
		var lines []string
		logPath := ""
		for _, cmd := range cmds {
			// One command per call: RunVerify runs every command it is given,
			// and a tier stops at the first failure.
			res, err := env.RunVerify(ctx, []string{cmd}, root)
			if err != nil {
				return Result{}, err
			}
			ran = append(ran, cmd)
			if res.OK() {
				passed = append(passed, cmd)
				lines = append(lines, "pass: "+cmd)
				continue
			}
			failed = append(failed, cmd)
			logPath = res.LogPath
			lines = append(lines, "FAIL: "+cmd, "log: "+res.LogPath)
			break
		}
		d := jsonx.NewObject()
		d.Set("tier", tier)
		d.Set("commands", strList(ran))
		d.Set("verified", strList(passed))
		d.Set("failed", strList(failed))
		if logPath != "" {
			d.Set("logPath", logPath)
		}
		res := Result{Data: d, Text: strings.Join(lines, "\n")}
		if len(failed) > 0 {
			return res, Failed("test.%s failed: %s", tier, failed[0])
		}
		return res, nil
	}
}

const filesPlaceholder = "{files}"

func anyContains(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// shellQuote wraps s in single quotes, escaping embedded single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// changedFiles lists the files that differ between the merge-base of base and
// HEAD and the working tree (committed and uncommitted changes), minus deleted ones.
func changedFiles(c *Ctx, root, base string) ([]string, error) {
	ctx := c.Context()
	repo := git.Repo{Dir: root}
	if base == "" {
		b, ok, err := resolveBase(ctx, root)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, Resolution("could not determine base branch for {files}").WithHint("pass --base <ref>")
		}
		base = b
	}
	mb, err := repo.Run(ctx, "merge-base", base, "HEAD")
	if err != nil {
		return nil, gitErr(err)
	}
	if mb.ExitCode != 0 {
		return nil, Resolution("no merge base between %s and HEAD", base)
	}
	out, err := repo.Run(ctx, "diff", "--name-only", "-z", "--diff-filter=d", strings.TrimSpace(mb.Stdout))
	if err != nil {
		return nil, gitErr(err)
	}
	if out.ExitCode != 0 {
		return nil, Failed("git diff failed: %s", strings.TrimSpace(out.Stderr))
	}
	var files []string
	for _, f := range strings.Split(out.Stdout, "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}
