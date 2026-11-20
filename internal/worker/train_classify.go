package worker

import (
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/landing"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/gotestout"
	"github.com/l4ci/rota/internal/land"
	"github.com/l4ci/rota/internal/shlex"
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
func (t *trainRun) classifyRed(p trainPlan, res *landing.TrainResult, k int, failed VerifyResult, narrow *narrowPlan) (redClass, error) {
	if k <= 1 { // the first member alone is the red prefix bisect ran
		return redClass{}, nil
	}
	if p.onCI {
		res.Notes = append(res.Notes, "TRAIN-UNCLASSIFIED — test.fullWhere is ci and the alone-runs run only locally; the culprit is the first member whose prefix is red, not checked alone")
		return redClass{}, nil
	}
	e, ctx, scratch := t.e, t.ctx, p.scratch
	cfg := p.verifyCfg()
	cmds, swapped := rerunCommands(failed, cfg.rerunTemplate, "", cfg.goCheck, false)
	var confirm []string
	if swapped { // a -run selection passing alone is no flake until the full command does
		confirm = failed.Failed
	}
	if narrow != nil {
		cmds, confirm = narrow.cmds, narrow.full
	}
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
			if _, err := land.LandLocal(land.LocalPorts{Git: run, Recover: land.RecoveryGit(ctx, e.Git, scratch)},
				land.LocalRequest{Head: p.heads[i], Base: "scratch tree", Message: land.TrainSubject(m.Branch, t.o.Base)}); err != nil {
				if errors.As(err, new(*land.PostMergeError)) {
					return false, false, err
				}
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
	// A narrowed pass proves less, so the pair run (the whole prefix when
	// last is 1) is confirmed by the full commands too.
	if last > 1 || confirm != nil {
		if _, code := e.git(scratch, "checkout", "-q", "-f", "--detach", p.tips[k]); code != 0 {
			return redClass{}, fmt.Errorf("git checkout %s failed in the scratch tree", p.tips[k])
		}
		t.phase("classify: base + %d member(s), %d command(s)", k, len(cmds))
		var red bool
		var err error
		if last > 1 {
			red, err = t.rerunConfirmed(scratch, cmds, confirm)
		} else {
			red, err = t.rerunIn(scratch, confirm)
		}
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
	tests, _ := gotestout.FailingTests(out)
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

// gateCheck is one red check in a gate log. The log contract a project's gate
// keeps (test/gate.sh does): "FAIL <check> (exit N)" per red check, and right
// under it "RETRIED-CHECK <check>" when the gate re-ran that check alone and it
// stayed red, and one "FAILED-CHECK <id>" per piece of it that test.rerunCheck
// can run alone ({check} is the id).
type gateCheck struct {
	name    string
	retried bool
	ids     []string
}

var (
	gateFailedLine = regexp.MustCompile(`^FAIL (\S+) \(exit \d+\)`)
	gateOtherRed   = regexp.MustCompile(`^FAIL (?:leak:|worktree guard|\S+ \(no pass line\))`)
	gateRetriedRed = regexp.MustCompile(`^RETRIED-CHECK (\S+)$`)
	gateIDRed      = regexp.MustCompile(`^FAILED-CHECK (\S+)$`)
)

// gateChecks reads the red checks out of one command's output; nil when it is
// no such log.
func gateChecks(out string) []gateCheck {
	var checks []gateCheck
	for _, line := range strings.Split(out, "\n") {
		if m := gateFailedLine.FindStringSubmatch(line); m != nil {
			checks = append(checks, gateCheck{name: m[1]})
		} else if gateOtherRed.MatchString(line) {
			checks = append(checks, gateCheck{name: "other"})
		} else if len(checks) > 0 {
			last := &checks[len(checks)-1]
			if m := gateRetriedRed.FindStringSubmatch(line); m != nil && m[1] == last.name {
				last.retried = true
			} else if m := gateIDRed.FindStringSubmatch(line); m != nil {
				last.ids = append(last.ids, m[1])
			}
		}
	}
	return checks
}

// rerunPlan is what an alone-run executes for a failed run.
type rerunPlan struct {
	cmds []string
	// origin maps each command to the failed command it stands for.
	origin map[string]string
	// narrowed: a command was swapped for a smaller one, so a pass proves less
	// than the failed command would.
	narrowed bool
	// skipped: a check the gate already re-ran alone and found red again was
	// left out. It is still red: nothing the rest does may call the train a flake.
	skipped bool
}

// rerunCommands is the commands of rerunSet.
func rerunCommands(failed VerifyResult, template, check, goCheck string, skipRetried bool) (cmds []string, narrowed bool) {
	r := rerunSet(failed, template, check, goCheck, skipRetried)
	return r.cmds, r.narrowed
}

// rerunSet is what an alone-run executes for the failed run: per failed
// command, the test.vacuousCommand template with only its failing tests when
// its output names them, else the command itself. With check (test.rerunCheck)
// set it narrows further (#3992): each id the gate log reported runs alone
// through check, every swap counts as narrowing, and skipRetried drops checks
// the gate already re-ran in isolation. Without check the gate log is not
// read and nothing is skipped, but a -run swap still happens and still counts
// as narrowing, so its pass is confirmed by a full re-run.
func rerunSet(failed VerifyResult, template, check, goCheck string, skipRetried bool) rerunPlan {
	r := rerunPlan{origin: map[string]string{}}
	narrowing := check != ""
	skipRetried = skipRetried && narrowing
	var from string
	add := func(c string) {
		if !slices.Contains(r.cmds, c) {
			r.cmds = append(r.cmds, c)
			r.origin[c] = from
		}
	}
	tests := func(out, c string) {
		if t, ok := testsCommand(out, template); ok {
			add(t)
			r.narrowed = true // a swapped command proves less, key or no key
		} else {
			add(c)
		}
	}
	for _, c := range failed.Failed {
		from = c
		out := logSection(failed.Log, c)
		checks := gateChecks(out)
		if !narrowing || len(checks) == 0 {
			tests(out, c)
			continue
		}
		var alone []string
		whole := false
		// Go output is command-wide: it supports a -run selection only when
		// the configured Go check is the only one left for it. Two
		// unselectable checks keep the command.
		open := 0
		for _, ck := range checks {
			if !(skipRetried && ck.retried) && !(len(ck.ids) > 0 && strings.Contains(check, "{check}")) {
				open++
			}
		}
		for _, ck := range checks {
			switch {
			case skipRetried && ck.retried:
				r.skipped = true
			case len(ck.ids) > 0 && strings.Contains(check, "{check}"):
				for _, id := range ck.ids {
					alone = append(alone, strings.ReplaceAll(check, "{check}", shlex.QuoteAlways(id)))
				}
			default:
				if t, ok := testsCommand(out, template); ok && open == 1 && ck.name == goCheck {
					add(t)
					r.narrowed = true
				} else {
					whole = true
				}
			}
		}
		if whole {
			add(c)
			continue
		}
		for _, a := range alone {
			add(a)
			r.narrowed = true
		}
	}
	return r
}

// narrowPlan is a narrowed diagnosis: cmds are the checks that failed, run
// alone; full are the commands of the failed run, which a green narrowed run
// must still pass before it counts for more than a diagnostic.
type narrowPlan struct {
	cmds, full []string
	origin     map[string]string // narrowed command -> the failed command it stands for
}

// narrowing reports how a red tier narrows to its failing checks, ok false
// when test.rerunCheck is unset, the tier runs on CI or nothing narrows.
func (t *trainRun) narrowing(p trainPlan, failed VerifyResult) (narrowPlan, bool) {
	cfg := p.verifyCfg()
	if p.onCI || cfg.rerunCheck == "" {
		return narrowPlan{}, false
	}
	r := rerunSet(failed, cfg.rerunTemplate, cfg.rerunCheck, cfg.goCheck, false)
	return narrowPlan{cmds: r.cmds, full: failed.Failed, origin: r.origin}, r.narrowed && len(r.cmds) > 0
}

// rerunConfirmed runs cmds in dir and reports whether any failed. When full is
// set, cmds were narrowed to single checks: a green is then confirmed by one
// run of the full commands, because a check that passes alone and fails with
// the rest is an interaction or an ordering bug, not a flake (#3992).
func (t *trainRun) rerunConfirmed(dir string, cmds, full []string) (bool, error) {
	red, err := t.rerunIn(dir, cmds)
	if err != nil || red || full == nil {
		return red, err
	}
	t.phase("rerun: the narrowed check passed; one full re-run before calling it a flake")
	return t.rerunIn(dir, full)
}

// bisectNarrowed is bisectTrain with each prefix run narrowed to the failing
// checks, then one full run of the members that may land. A second failure in
// another check shows only there: the bisect then restarts on the full tier,
// so it is reported and attributed the way an unnarrowed bisect would (#3992).
func (t *trainRun) bisectNarrowed(p trainPlan, res *landing.TrainResult, tier string, run func(int) (VerifyResult, error), failed VerifyResult, np narrowPlan) (int, bool, error) {
	e, ctx, scratch := t.e, t.ctx, p.scratch
	narrow := func(k int) (VerifyResult, error) {
		t.phase("diagnose %s: base + %d member(s), %d failing command(s) only", tier, k, len(np.cmds))
		began := time.Now()
		r, err := e.runGateVerify(ctx, np.cmds, scratch, p.in.ledger)
		res.DiagnoseTime += time.Since(began)
		if err == nil && ctx.Err() != nil && !r.OK() {
			res.Log = r.LogPath
			return r, ctx.Err()
		}
		// A narrowed run fails as the command it narrowed, so the bisect
		// sees the original failure and not a new one.
		var failedAs []string
		for _, c := range r.Failed {
			if o := np.origin[c]; o != "" && !slices.Contains(failedAs, o) {
				failedAs = append(failedAs, o)
			}
		}
		if len(failedAs) > 0 {
			r.Failed = failedAs
		}
		if err == nil {
			for _, f := range r.Flaky {
				res.Flaky = append(res.Flaky, f)
				res.Notes = append(res.Notes, flakeNote(f))
			}
		}
		return r, err
	}
	classify := func(k int, f VerifyResult) (redClass, error) { return t.classifyRed(p, res, k, f, &np) }
	snap := cloneTrainResult(*res)
	passing, stop, err := e.bisectTrain(scratch, res, p.tips, tier, narrow, classify, failed, t.o.LandGreen)
	if err != nil || stop || passing == 0 {
		return passing, stop, err
	}
	if _, code := e.git(scratch, "checkout", "-q", "-f", "--detach", p.tips[passing]); code != 0 {
		return 0, true, fmt.Errorf("git checkout %s failed in the scratch tree", p.tips[passing])
	}
	r, err := run(passing)
	if err != nil {
		return 0, true, err
	}
	if r.OK() {
		return passing, false, nil
	}
	t.phase("the full %s run of the first %d member(s) fails where the narrowed one passed; bisecting the full tier", tier, passing)
	// The new failure is what this bisect attributes. Original keeps the first
	// failure's evidence, but would mark the new one diagnostic-only, so it
	// steps aside for the walk and returns after.
	spent, orig := res.DiagnoseTime, snap.Original
	*res = snap
	res.DiagnoseTime, res.Original = spent, nil
	plain := func(k int, f VerifyResult) (redClass, error) { return t.classifyRed(p, res, k, f, nil) }
	passing, stop, err = e.bisectTrain(scratch, res, p.tips, tier, run, plain, r, t.o.LandGreen)
	if orig != nil {
		res.Original = orig
	}
	return passing, stop, err
}

// testsCommand expands template for the tests a command's output names.
func testsCommand(out, template string) (string, bool) {
	if !strings.Contains(template, "{tests}") || strings.Contains(template, "{files}") {
		return "", false
	}
	failing, ok := gotestout.FailingTests(out)
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
