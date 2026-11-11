package worker

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/land"
	"github.com/l4ci/rota/internal/testledger"
)

// redClass is what classifyRed found out about a red prefix. The zero value
// keeps its last member the culprit.
type redClass struct {
	// interaction holds the indexes of the members whose merge together makes
	// the red, ascending.
	interaction []int
	// unreproduced: the member passes alone and the failing tests passed on a
	// rerun of the red prefix too, so the red is a flake and nobody is blamed.
	unreproduced bool
}

// classifyRed decides whether the red prefix of k members that bisect found is
// the fault of its last member, an interaction or a red no rerun reproduces
// (#3095). It reruns only the failing tests of failed, first on the base plus
// member k alone, then on the base plus each earlier member and member k, then
// on the red prefix itself. Member k stays the culprit when it is red alone, or
// when it was not checked alone (the first member, a CI train, a member that
// does not merge without the ones before it).
//
// The in-tree retry already caught flakes, so a rerun is not retried.
func (t *trainRun) classifyRed(p trainPlan, res *TrainResult, k int, failed VerifyResult) (redClass, error) {
	if k <= 1 { // the first member alone is the red prefix bisect ran
		return redClass{}, nil
	}
	if p.onCI {
		res.Notes = append(res.Notes, "TRAIN-UNCLASSIFIED — test.fullWhere is ci and the alone-runs run only locally; the culprit is the first member whose prefix is red, not checked alone")
		return redClass{}, nil
	}
	e, ctx, scratch := t.e, t.ctx, p.scratch
	cmds := rerunCommands(failed, p.verifyCfg().rerunTemplate)
	began := time.Now()
	defer func() {
		res.DiagnoseTime += time.Since(began)
		e.git(scratch, "checkout", "-q", "-f", "--detach", p.tips[k])
	}()
	// rerun reports whether the failing tests fail on the base plus members
	// (merged in order); merged is false when the members do not merge cleanly
	// without the ones between them.
	rerun := func(members ...int) (red, merged bool, err error) {
		if _, code := e.git(scratch, "checkout", "-q", "-f", "--detach", p.tips[0]); code != 0 {
			return false, false, fmt.Errorf("git checkout %s failed in the scratch tree", p.tips[0])
		}
		labels := []string{}
		for _, i := range members {
			m := res.Members[i]
			run := func(args ...string) (git.Result, error) { return e.Git(ctx, scratch, args...) }
			if err := land.MergeLocal(run, p.heads[i], land.TrainSubject(m.Branch, t.o.Base), land.RecoveryGit(ctx, e.Git, scratch)); err != nil {
				if ctx.Err() != nil {
					return false, false, ctx.Err()
				}
				return false, false, nil
			}
			labels = append(labels, m.Target)
		}
		t.phase("classify: base + %s, %d command(s)", strings.Join(labels, " + "), len(cmds))
		red, err = t.rerunIn(scratch, cmds)
		return red, true, err
	}
	last := k - 1
	red, merged, err := rerun(last)
	if err != nil || !merged || red {
		if err == nil && !merged {
			res.Notes = append(res.Notes, fmt.Sprintf("TRAIN-UNCLASSIFIED %s — does not merge on the base without the members before it, so it was not checked alone", res.Members[last].Target))
		}
		return redClass{}, err
	}
	for j := 0; j < last; j++ {
		red, merged, err := rerun(j, last)
		if err != nil {
			return redClass{}, err
		}
		if merged && red {
			return redClass{interaction: []int{j, last}}, nil
		}
	}
	// No pair is red: the red needs three or more members, or does not
	// reproduce. With one earlier member the pair was the red prefix itself.
	if last > 1 {
		if _, code := e.git(scratch, "checkout", "-q", "-f", "--detach", p.tips[k]); code != 0 {
			return redClass{}, fmt.Errorf("git checkout %s failed in the scratch tree", p.tips[k])
		}
		t.phase("classify: base + %d member(s), %d command(s)", k, len(cmds))
		red, err := t.rerunIn(scratch, cmds)
		if err != nil {
			return redClass{}, err
		}
		if red {
			all := make([]int, k)
			for i := range all {
				all[i] = i
			}
			return redClass{interaction: all}, nil
		}
	}
	for _, c := range failed.Failed {
		res.Notes = append(res.Notes, unreproducedNote(c, logSection(failed.Log, c)))
	}
	return redClass{unreproduced: true}, nil
}

// unreproducedNote names a failed command's tests the way a FLAKY note does,
// for a red that passed on every rerun.
func unreproducedNote(cmd, out string) string {
	tests, _ := testledger.FailingTests(out)
	named := strings.Join(tests, ", ")
	if named == "" {
		named = "(none named)"
	}
	return fmt.Sprintf("FLAKY %s — tests: %s; red on the train and its in-tree retry, green on every rerun (alone and the red prefix); no member blamed", cmd, named)
}

// rerunIn runs cmds in dir and reports whether any failed. A cancel is an
// error, never a finding.
func (t *trainRun) rerunIn(dir string, cmds []string) (bool, error) {
	red := false
	for _, c := range cmds {
		if _, code := t.e.Shell(t.ctx, dir, c); code != 0 {
			red = true
		}
		if t.ctx.Err() != nil {
			return false, t.ctx.Err()
		}
		if red {
			return true, nil
		}
	}
	return false, nil
}

// failPackage matches go test's per-package FAIL line: "FAIL\t<pkg>\t0.01s".
var failPackage = regexp.MustCompile(`(?m)^FAIL\t(\S+)\t`)

// rerunCommands is what an alone-run executes for the failed run: per failed
// command, the template with only its failing tests when its output names
// them (and their packages when the template needs those), else the command
// itself.
func rerunCommands(failed VerifyResult, template string) []string {
	var out []string
	for _, c := range failed.Failed {
		cmd := c
		if t, ok := testsCommand(logSection(failed.Log, c), template); ok {
			cmd = t
		}
		if !slices.Contains(out, cmd) {
			out = append(out, cmd)
		}
	}
	return out
}

// testsCommand expands template for the tests a command's output names.
func testsCommand(out, template string) (string, bool) {
	if !strings.Contains(template, "{tests}") || strings.Contains(template, "{files}") {
		return "", false
	}
	failing, ok := testledger.FailingTests(out)
	if !ok {
		return "", false
	}
	var names, packages []string
	for _, n := range failing {
		top, _, _ := strings.Cut(n, "/") // -run splits on '/': select the top-level test
		if !slices.Contains(names, top) {
			names = append(names, top)
		}
	}
	for _, m := range failPackage.FindAllStringSubmatch(out, -1) {
		if !slices.Contains(packages, m[1]) {
			packages = append(packages, m[1])
		}
	}
	if strings.Contains(template, "{packages}") && len(packages) == 0 {
		return "", false
	}
	return expandTestTemplate(template, nil, packages, names), true
}

// logSection is the output runVerifyCmds logged for cmd ("== <cmd>" up to the
// next command's header; a retry's output stays in its section).
func logSection(log, cmd string) string {
	head := "== " + cmd + "\n"
	start := strings.Index(log, head)
	if start < 0 {
		return ""
	}
	rest := log[start+len(head):]
	for off := 0; ; {
		i := strings.Index(rest[off:], "\n== ")
		if i < 0 {
			return rest
		}
		at := off + i + 1
		if !strings.HasPrefix(rest[at:], "== RETRY ") {
			return rest[:at]
		}
		off = at
	}
}
