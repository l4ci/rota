package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/shlex"
)

// vacuousConfig is read from the base, never from the candidate's config.
type vacuousConfig struct {
	enabled bool
	pattern *regexp.Regexp
	command string
	err     error
}

func parseVacuousConfig(cfg any) vacuousConfig {
	var c vacuousConfig
	c.enabled, c.err = config.Bool(cfg, "test.vacuousCheck")
	if c.err != nil || !c.enabled {
		return c
	}
	pattern := config.String(cfg, "test.filePattern")
	if pattern == "" {
		c.err = fmt.Errorf("test.filePattern must be a non-empty regular expression")
		return c
	}
	c.pattern, c.err = regexp.Compile(pattern)
	if c.err != nil {
		c.err = fmt.Errorf("test.filePattern: %w", c.err)
		return c
	}
	c.command = config.String(cfg, "test.vacuousCommand")
	if strings.TrimSpace(c.command) == "" || (!strings.Contains(c.command, "{files}") && !strings.Contains(c.command, "{packages}")) {
		c.err = fmt.Errorf("test.vacuousCommand must select tests with {files} or {packages}")
	}
	return c
}

type vacuousResult struct{ verdict, detail, note string }

// checkVacuous runs selected tests twice in a disposable copy of after. Only
// non-test files changed by this merge are restored from before. In a train,
// before is the preceding prefix, so earlier members' changes stay intact.
func (e Env) checkVacuous(ctx context.Context, root, before, after string, c vacuousConfig) (vacuousResult, error) {
	if c.err != nil {
		return vacuousResult{}, c.err
	}
	if !c.enabled {
		return vacuousResult{}, nil
	}
	diff, code := e.runGit(ctx, root, "diff", "--name-only", "--no-renames", "-z", before, after)
	if code != 0 {
		return vacuousResult{}, fmt.Errorf("could not diff the vacuous-test merge: %s", diff)
	}
	var tests, production []string
	for _, file := range strings.Split(diff, "\x00") {
		if file == "" {
			continue
		}
		if strings.HasPrefix(file, "docs/") || strings.HasPrefix(file, "skills/") || strings.HasSuffix(file, ".md") {
			production = append(production, file)
			continue
		}
		if c.pattern.MatchString(file) {
			// Deleted tests have nothing to run; retain every other changed test file.
			out, code := e.runGit(ctx, root, "--literal-pathspecs", "ls-tree", "-z", "--name-only", after, "--", file)
			if code != 0 {
				return vacuousResult{}, fmt.Errorf("could not inspect changed test %s: %s", file, out)
			}
			if out != "" {
				tests = append(tests, file)
			}
		} else {
			production = append(production, file)
		}
	}
	if len(tests) == 0 {
		return vacuousResult{note: "VACUOUS-SKIP — no added or changed test files (or docs/skills only)"}, nil
	}
	if len(production) == 0 {
		return vacuousResult{note: "VACUOUS-SKIP — no non-test changes to revert: " + strings.Join(tests, ", ")}, nil
	}
	dir, cleanup, err := e.scratchTree(ctx, root, after, "rota-train-vacuous-")
	if err != nil {
		return vacuousResult{}, err
	}
	defer cleanup()
	command, err := vacuousCommand(dir, tests, c.command)
	if err != nil {
		return vacuousResult{}, err
	}
	if command == "" {
		return vacuousResult{note: "VACUOUS-SKIP — no runnable Go tests in: " + strings.Join(tests, ", ")}, nil
	}
	names := strings.Join(tests, ", ")
	out, code := e.Shell(ctx, dir, command)
	if ctx.Err() != nil {
		return vacuousResult{}, ctx.Err()
	}
	if code != 0 {
		return vacuousResult{verdict: GateVerifyFailed, detail: fmt.Sprintf("selected tests fail on the merge: %s\n%s", names, indentTail(out, 20))}, nil
	}
	goDefault := c.command == config.String(nil, "test.vacuousCommand")
	if goDefault && !vacuousGoTestsRan(out) {
		return vacuousResult{note: "VACUOUS-SKIP — inconclusive: no selected Go tests completed without skipping on the merge: " + names}, nil
	}
	// restore handles additions, deletions, modes and renames (diff uses no-renames).
	args := append([]string{"--literal-pathspecs", "restore", "--source=" + before, "--staged", "--worktree", "--"}, production...)
	if len(production) > 0 {
		if out, code := e.runGit(ctx, dir, args...); code != 0 {
			return vacuousResult{}, fmt.Errorf("could not revert non-test changes: %s", out)
		}
	}
	out, code = e.Shell(ctx, dir, command)
	if ctx.Err() != nil {
		return vacuousResult{}, ctx.Err()
	}
	if code < 0 || code >= 126 {
		return vacuousResult{}, fmt.Errorf("counterfactual test command could not complete (exit %d): %s", code, indentTail(out, 20))
	}
	if code == 0 {
		if goDefault && !vacuousGoTestsRan(out) {
			return vacuousResult{note: "VACUOUS-SKIP — inconclusive: no selected Go tests completed without skipping with non-test changes reverted: " + names}, nil
		}
		return vacuousResult{verdict: GateVacuousTests, detail: "tests still pass with non-test changes reverted: " + names}, nil
	}
	return vacuousResult{note: "VACUOUS-PASS — tests fail with non-test changes reverted: " + names}, nil
}

// A successful package exit alone proves nothing: build constraints can exclude
// every selected test, and run events can all end in skips. Require a completed,
// non-skipped test (including examples and fuzz seeds) before judging vacuity.
func vacuousGoTestsRan(out string) bool {
	for _, line := range strings.Split(out, "\n") {
		var event struct{ Action, Test string }
		if json.Unmarshal([]byte(line), &event) == nil && event.Test != "" && event.Action == "pass" {
			return true
		}
	}
	return false
}

// The Go default selects test entry points in the changed files, with caching
// disabled. Other languages use {files}; every expansion is shell quoted.
func vacuousCommand(dir string, files []string, template string) (string, error) {
	quote := func(values []string) string {
		out := make([]string, len(values))
		for i, value := range values {
			out[i] = shlex.QuoteAlways(value)
		}
		return strings.Join(out, " ")
	}
	packages, names := []string{}, []string{}
	if strings.Contains(template, "{packages}") || strings.Contains(template, "{tests}") {
		for _, file := range files {
			f, err := parser.ParseFile(token.NewFileSet(), path.Join(dir, file), nil, 0)
			if err != nil {
				return "", fmt.Errorf("read Go tests in %s: %w", file, err)
			}
			pkg := "./" + path.Dir(file)
			if !slices.Contains(packages, pkg) {
				packages = append(packages, pkg)
			}
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv != nil {
					continue
				}
				name := fn.Name.Name
				if strings.HasPrefix(name, "Test") && name != "TestMain" || strings.HasPrefix(name, "Example") || strings.HasPrefix(name, "Fuzz") {
					if !slices.Contains(names, name) {
						names = append(names, name)
					}
				}
			}
		}
		if len(names) == 0 {
			return "", nil
		}
	}
	re := "^(" + strings.Join(names, "|") + ")$"
	return strings.NewReplacer("{files}", quote(files), "{packages}", quote(packages), "{tests}", shlex.QuoteAlways(re)).Replace(template), nil
}

func (g *gate) vacuousRun(before, after string) (bool, error) {
	r, err := g.e.checkVacuous(g.ctx, g.root, before, after, g.in.cfg.vacuous)
	if err != nil {
		return g.broke(err.Error())
	}
	if r.note != "" {
		g.res.Notes = append(g.res.Notes, r.note)
	}
	if r.verdict == "" {
		return false, nil
	}
	hint := "fix the selected tests, then re-gate"
	if r.verdict == GateVacuousTests {
		hint = "add an assertion that fails with non-test changes reverted, then re-gate; for refactors or tests-only work, disable test.vacuousCheck in the base configuration or use --no-verify"
	}
	g.verdict(r.verdict, r.detail+"; nothing landed", hint)
	return true, nil
}

// vacuousStage checks each train member independently of later members and
// never reuses the full-tier cache. A failure names the owning member.
func (t *trainRun) vacuousStage() (bool, error) {
	if t.o.NoVerify {
		return false, nil
	}
	for i := 0; i < t.passing; i++ {
		r, err := t.e.checkVacuous(t.ctx, t.root, t.tips[i], t.tips[i+1], t.in.cfg.vacuous)
		if err != nil {
			return t.broke(err.Error())
		}
		if r.note != "" {
			t.res.Notes = append(t.res.Notes, t.res.Members[i].Target+": "+r.note)
		}
		if r.verdict == "" {
			continue
		}
		for j := range t.res.Members {
			t.res.Members[j].Culprit = false
		}
		t.res.Members[i].Culprit = true
		t.res.Culprit = t.res.Members[i].Target
		t.res.Verdict, t.res.Err = r.verdict, r.detail+"; nothing landed"
		t.res.Hint = "fix the selected tests, then re-run the train"
		if r.verdict == GateVacuousTests {
			t.res.Hint = "add an assertion that fails with non-test changes reverted, then re-run the train; for refactors or tests-only work, disable test.vacuousCheck in the base configuration or use --no-verify"
		}
		// Unlike tier bisection this check has not verified the earlier prefix on
		// its own. Refuse the whole train, including under --land-green.
		return true, nil
	}
	return false, nil
}
