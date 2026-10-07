package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/worker"
)

func testCommands() *Command {
	return &Command{Name: "test", Summary: "run a config test tier", Subs: []*Command{
		{Name: "run", Summary: "run test.fast, test.full or test.e2e in order, stopping at the first failure", Verb: testRun},
		{Name: "ledger", Summary: "the exclusion ledger of known-red tests", Subs: []*Command{
			{Name: "check", Summary: "report expired and malformed entries in .rota/test-ledger.json", Verb: noFlags(testLedgerCheck)},
		}},
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
		isolate, err := config.Bool(config.Load(rotatree.Config(root)), "test.isolate")
		if err != nil {
			return Result{}, Resolution("%v", err)
		}
		if isolate {
			// A command may not reach the live round, ssh-agent or the
			// developer's home (#388); the temp root goes with the run.
			tmp, err := os.MkdirTemp("", "rota-test-isolate-")
			if err != nil {
				return Result{}, err
			}
			defer os.RemoveAll(tmp)
			environ, err := worker.IsolatedEnviron(os.Environ(), tmp)
			if err != nil {
				return Result{}, err
			}
			env.Shell = worker.IsolatedShell(environ)
		}
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

// remoteBase prefers origin/<base> over a local base it is strictly ahead of,
// so a lagging local branch does not pull upstream commits into {files}. It
// returns base when there is no such remote ref or local is not behind it.
func remoteBase(ctx context.Context, repo git.Repo, base string) string {
	remote := "origin/" + base
	if found, err := repo.Verify(ctx, "refs/remotes/"+remote); err != nil || !found {
		return base
	}
	// Exit 0 when base is an ancestor of remote; equal tips are not "ahead".
	anc, err := repo.Run(ctx, "merge-base", "--is-ancestor", base, remote)
	if err != nil || anc.ExitCode != 0 {
		return base
	}
	same, err := repo.Run(ctx, "rev-parse", base, remote)
	if err != nil || same.ExitCode != 0 {
		return base
	}
	if shas := strings.Fields(same.Stdout); len(shas) == 2 && shas[0] == shas[1] {
		return base
	}
	return remote
}

// changedFiles lists the files that differ between the merge-base of base and
// HEAD and the working tree (committed and uncommitted changes, plus untracked
// files git does not ignore), minus deleted ones.
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
		base = remoteBase(ctx, repo, b)
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
	untracked, err := repo.Run(ctx, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, gitErr(err)
	}
	if untracked.ExitCode != 0 {
		return nil, Failed("git ls-files failed: %s", strings.TrimSpace(untracked.Stderr))
	}
	var files []string
	seen := map[string]bool{}
	for _, f := range strings.Split(out.Stdout+"\x00"+untracked.Stdout, "\x00") {
		if f != "" && !seen[f] {
			seen[f] = true
			files = append(files, f)
		}
	}
	return files, nil
}
