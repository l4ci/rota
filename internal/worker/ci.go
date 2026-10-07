package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/tracker"
)

// CI-backed verification (#376). With test.fullWhere "ci" the full tier runs on
// the project's CI instead of here: the gate and the train push the tree they
// would have verified to origin as rota/ci/<name> and wait for the checks the
// forge reports on that commit (GitHub check runs and statuses, GitLab
// pipelines). Most projects already run CI on every push, so a local full run
// duplicates it.
//
// The project's CI must run on pushes to rota/ci/** branches. Nothing here can
// prove that ahead of time, so the first push is the preflight: no check on
// the pushed commit within the start window is verdict ci-not-run, before
// anything lands.

// CI verdicts, all exit 1 with nothing landed: CI did not start on the pushed
// commit, did not finish within test.ciTimeoutMinutes, or would have run a CI
// definition the merge itself changes.
const (
	GateCINotRun        = "ci-not-run"
	GateVerifyTimeout   = "verify-timeout"
	GateCIConfigChanged = "ci-config-changed"
)

// ciConfigPaths define a project's CI: GitHub workflows and local actions,
// GitLab's default pipeline file and its usual include directory. CI runs the
// definition in the pushed tree, so a merge that changes one would choose its
// own verification, which the gate never lets a branch do (see Gate). A
// pipeline file kept elsewhere is not recognised.
var ciConfigPaths = []string{".github/workflows/", ".github/actions/", ".gitlab-ci.yml", ".gitlab/ci/"}

// ciConfigChanges is the files among changed that define CI.
func ciConfigChanges(changed []string) []string {
	var hit []string
	for _, f := range changed {
		for _, p := range ciConfigPaths {
			if f == p || (strings.HasSuffix(p, "/") && strings.HasPrefix(f, p)) {
				hit = append(hit, f)
				break
			}
		}
	}
	return hit
}

// ciDiffFiles lists every path the merge of head onto base touches, for
// ciConfigChanges: NUL-separated so no path comes back quoted, and without
// rename detection so a workflow moved away counts by its old path too.
func (e gateEnv) ciDiffFiles(root, base, head string) ([]string, error) {
	out, code := e.runGit(root, "diff", "--no-renames", "--name-only", "-z", base+"..."+head)
	if code != 0 {
		return nil, fmt.Errorf("git diff --name-only %s...%s exited %d", base, head, code)
	}
	var files []string
	for _, f := range strings.Split(out, "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}

// ciConfigRefusal is the ci-config-changed message and hint for who, or ""
// when no file in changed defines CI.
func ciConfigRefusal(who string, changed []string) (msg, hint string) {
	hit := ciConfigChanges(changed)
	if len(hit) == 0 {
		return "", ""
	}
	return fmt.Sprintf("CI-CONFIG %s — the merge changes the CI definition (%s), so under test.fullWhere ci it would choose its own verification; nothing landed", who, strings.Join(hit, ", ")),
		"review the CI change, then land it with test.fullWhere local or by hand"
}

// ciRefPrefix is where verification commits are pushed; the docs tell projects
// to run CI on it.
const ciRefPrefix = "rota/ci/"

// Where the full tier runs: test.fullWhere.
const (
	WhereLocal = "local"
	WhereCI    = "ci"
)

// FullWhere reads test.fullWhere: local (the default) or ci. Any other value
// is an error, so a typo never falls back to a local run silently, and so is
// ci with no test.ciChecks: CI mode only knows a run is green by the checks
// it names.
func FullWhere(cfg any) (string, error) {
	switch w := config.String(cfg, "test.fullWhere"); w {
	case WhereLocal:
		return w, nil
	case WhereCI:
		if len(ciChecks(cfg)) == 0 {
			return "", fail(exitcode.ExitInternal, "test.fullWhere is ci, but test.ciChecks names no check to wait for").
				WithHint(`list the CI checks that must pass, e.g. rota config set test.ciChecks '["test"]'`)
		}
		return w, nil
	default:
		v, _ := config.Lookup(cfg, "test.fullWhere")
		return "", fail(exitcode.ExitInternal, fmt.Sprintf("test.fullWhere must be local or ci (got %v)", v))
	}
}

// ciChecks is test.ciChecks: the names of the CI checks that must all pass.
func ciChecks(cfg any) []string { return commandList(cfg, "test.ciChecks") }

// ciSettings is how long a CI wait lasts: the poll interval, how long the
// first check may take to appear, and how long the checks may take to finish.
type ciSettings struct {
	poll, start, timeout time.Duration
}

// ciSettingsFrom reads test.ciTimeoutMinutes, and ROTA_CI_POLL,
// ROTA_CI_START_WAIT and ROTA_CI_TIMEOUT (seconds) over the built-in values.
func ciSettingsFrom(cfg any, getenv func(string) string) (ciSettings, error) {
	mins, err := config.Int(cfg, "test.ciTimeoutMinutes", 1, 24*60)
	if err != nil {
		return ciSettings{}, err
	}
	s := ciSettings{poll: 20 * time.Second, start: 5 * time.Minute, timeout: time.Duration(mins) * time.Minute}
	for env, d := range map[string]*time.Duration{"ROTA_CI_POLL": &s.poll, "ROTA_CI_START_WAIT": &s.start, "ROTA_CI_TIMEOUT": &s.timeout} {
		if v := getenv(env); v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
				*d = time.Duration(f * float64(time.Second))
			}
		}
	}
	return s, nil
}

// ciVerifier runs the full tier on CI for one gate or train: it is set up
// once, before anything merges, so a missing remote or forge is refused first.
type ciVerifier struct {
	e      gateEnv
	root   string
	forge  Forge
	set    ciSettings
	checks []string // test.ciChecks
}

// newCIVerifier checks what a CI run needs: an origin to push to and a forge
// to read checks from. brokeMsg says what is missing.
func (e gateEnv) newCIVerifier(root string, forge Forge, cfg any) (v *ciVerifier, brokeMsg string) {
	if _, code := e.runGit(root, "remote", "get-url", "origin"); code != 0 {
		return nil, "test.fullWhere is ci, but this repo has no 'origin' remote to push the merge result to"
	}
	set, err := ciSettingsFrom(cfg, e.getenv)
	if err != nil {
		return nil, err.Error()
	}
	return &ciVerifier{e: e, root: root, forge: forge, set: set, checks: ciChecks(cfg)}, ""
}

var ciNameUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// verify pushes sha to origin as rota/ci/<name>, waits for its checks and
// deletes the branch again. err is a git or forge failure, not a red check.
//
// Green is every check in test.ciChecks finished with success (all of them,
// when several share a name) and no check on the commit failed, holding for
// two polls in a row. Unlisted checks still running do not hold it up; any
// failed check ends the run red at once, and so does a listed check that
// finished without testing anything.
func (v *ciVerifier) verify(sha, name string) (VerifyResult, error) {
	ref := ciRefPrefix + strings.Trim(ciNameUnsafe.ReplaceAllString(name, "-"), "-")
	res := VerifyResult{CI: true, Ref: ref, SHA: sha}
	if out, code := v.e.runGit(v.root, "push", "-q", "--force", "origin", sha+":refs/heads/"+ref); code != 0 {
		return res, fmt.Errorf("git push of %s to origin %s failed (exit %d): %s", short(sha), ref, code, strings.TrimSpace(out))
	}
	defer v.e.cleanupGit(v.root, "push", "-q", "origin", "--delete", ref)
	start := v.e.now()
	green := "" // the checks of the last green poll
	for {
		checks, err := v.forge.CommitChecks(v.e.ctx, sha)
		if err != nil {
			return res, fmt.Errorf("could not read the CI checks of %s: %w", short(sha), err)
		}
		elapsed := v.e.now().Sub(start)
		pending, failed := 0, false
		state := map[string]string{} // per name: the worst state seen
		rank := map[string]int{tracker.CheckSuccess: 1, tracker.CheckPending: 2, tracker.CheckSkipped: 3, tracker.CheckFailure: 4}
		var sig []string
		for _, c := range checks {
			sig = append(sig, c.Name)
			switch c.State {
			case tracker.CheckSuccess, tracker.CheckSkipped:
			case tracker.CheckFailure:
				failed = true
			default:
				pending++
			}
			st := c.State
			if rank[st] == 0 {
				st = tracker.CheckPending
			}
			if rank[st] > rank[state[c.Name]] {
				state[c.Name] = st
			}
		}
		var missing, waiting, untested []string
		for _, n := range v.checks {
			switch state[n] {
			case "":
				missing = append(missing, n)
			case tracker.CheckPending:
				waiting = append(waiting, n)
			case tracker.CheckSkipped:
				untested = append(untested, n)
			}
		}
		if !failed && len(untested) == 0 && len(missing) == 0 && len(waiting) == 0 {
			// A check can appear after the others finished (a later workflow,
			// a job created only when it starts), so green must hold for two
			// polls in a row with the same checks.
			if s := strings.Join(sig, "\n"); s != green {
				green = s
				v.e.sleep(v.set.poll)
				continue
			}
			res.Log = checkLog(checks)
			res.Verified = append([]string(nil), v.checks...)
			return res, nil
		}
		green = ""
		switch {
		case failed || len(untested) > 0:
			// One red check is the verdict; the rest need not finish.
			res.Log = checkLog(checks)
			for _, c := range checks {
				if c.State == tracker.CheckFailure {
					res.Failed = append(res.Failed, c.Name)
				}
			}
			for _, n := range untested {
				res.Failed = append(res.Failed, n+" skipped")
				res.Log += "listed check " + n + " skipped: it tested nothing\n"
			}
			return res, nil
		case len(missing) > 0 && pending == 0 && elapsed >= v.set.start:
			// Nothing on the commit is still running, so a missing check is
			// not waiting behind another job: it will never come.
			res.NotRun, res.Missing = true, missing
			return res, nil
		case len(checks) > 0 && elapsed >= v.set.timeout:
			res.TimedOut = true
			res.Log = fmt.Sprintf("%d of %d check(s) still pending", pending, len(checks))
			if len(waiting) > 0 {
				res.Log += "; listed check(s) not finished: " + strings.Join(waiting, ", ")
			}
			if len(missing) > 0 {
				res.Log += "; listed check(s) not started: " + strings.Join(missing, ", ")
			}
			res.Log += "\n"
			return res, nil
		}
		v.e.sleep(v.set.poll)
	}
}

// checkLog lists checks one per line, for a verdict to quote.
func checkLog(checks []tracker.CheckRun) string {
	var b strings.Builder
	for _, c := range checks {
		b.WriteString(c.State + " " + c.Name)
		if c.URL != "" {
			b.WriteString(" " + c.URL)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// stopVerdict is the verdict a CI run that gave no answer ends with: ci-not-run
// or verify-timeout, with its message and hint. "" for a run that answered.
func (r VerifyResult) stopVerdict(who string) (verdict, msg, hint string) {
	switch {
	case r.NotRun:
		return GateCINotRun, fmt.Sprintf("CI-NOT-RUN %s — listed CI check(s) never ran on %s (%s): %s; nothing landed", who, r.Ref, short(r.SHA), strings.Join(r.Missing, ", ")),
			"make the project's CI run on pushes to " + ciRefPrefix + "** branches and check that test.ciChecks matches its check names, or set test.fullWhere to local"
	case r.TimedOut:
		return GateVerifyTimeout, fmt.Sprintf("VERIFY-TIMEOUT %s — CI on %s (%s) did not finish in time: %s; nothing landed", who, r.Ref, short(r.SHA), strings.TrimSpace(r.Log)),
			"re-run when CI has caught up, or raise test.ciTimeoutMinutes"
	}
	return "", "", ""
}

// detail is the part of a failed run a verdict message quotes: the local log's
// last lines, or the CI checks and where to read them.
func (r VerifyResult) detail() string {
	if r.Cached {
		return "cached verdict (no log kept) — failed: " + strings.Join(r.Failed, ", ")
	}
	if r.CI {
		return fmt.Sprintf("CI checks on %s (%s):\n%s", r.Ref, short(r.SHA), indentTail(r.Log, 20))
	}
	return fmt.Sprintf("last lines of the verify output (full log: %s):\n%s", r.LogPath, indentTail(r.Log, 20))
}

// scratchTree checks out sha detached in a temporary worktree of root. The
// caller runs cleanup.
func (e gateEnv) scratchTree(root, sha string) (dir string, cleanup func(), err error) {
	tmp, err := os.MkdirTemp("", "rota-ci-")
	if err != nil {
		return "", nil, err
	}
	dir = filepath.Join(tmp, "tree")
	if out, code := e.runGit(root, "worktree", "add", "--detach", dir, sha); code != 0 {
		os.RemoveAll(tmp)
		return "", nil, fmt.Errorf("could not create the scratch worktree: %s", out)
	}
	return dir, func() {
		e.cleanupGit(root, "worktree", "remove", "--force", dir)
		os.RemoveAll(tmp)
		e.cleanupGit(root, "worktree", "prune")
	}, nil
}

// cleanupTimeout bounds one cleanup git call.
const cleanupTimeout = 30 * time.Second

// cleanupGit runs git for cleanup on a fresh context, so a cancelled or
// interrupted run still deletes its CI branch and scratch worktree.
func (e gateEnv) cleanupGit(dir string, args ...string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(e.ctx), cleanupTimeout)
	defer cancel()
	e.ctx = ctx
	e.runGit(dir, args...)
}

// fullTier is the train's seam for the full tier: verify runs it on the tree
// checked out in dir, with test.full here or on CI under test.fullWhere ci.
// brokeMsg refuses the run before anything merges; err is a bad config.
// onCI says the tier runs on CI.
func (e Env) fullTier(ctx context.Context, root, name string) (verify func(dir string) (VerifyResult, error), onCI bool, brokeMsg string, err error) {
	cfg := config.Load(rotatree.Config(root))
	where, err := FullWhere(cfg)
	if err != nil {
		return nil, false, "", err
	}
	if where == WhereLocal {
		return func(dir string) (VerifyResult, error) { return e.Verify(ctx, root, dir) }, false, "", nil
	}
	ge := e.gateEnv()
	ge.ctx = ctx
	provider := ge.detectProvider(root, "")
	forge, ferr := ge.forge(provider, root, cfg)
	if ferr != nil {
		return nil, true, fmt.Sprintf("cannot reach the %s forge to read CI checks: %v", provider, ferr), nil
	}
	ci, msg := ge.newCIVerifier(root, forge, cfg)
	if msg != "" {
		return nil, true, msg, nil
	}
	return func(dir string) (VerifyResult, error) {
		sha, code := ge.runGit(dir, "rev-parse", "HEAD")
		if code != 0 {
			return VerifyResult{}, fmt.Errorf("git rev-parse HEAD exited %d in %s", code, dir)
		}
		return ci.verify(sha, name)
	}, true, "", nil
}
