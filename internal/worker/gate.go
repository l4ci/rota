package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/config"
	gatepath "github.com/l4ci/rota/internal/gate"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/tracker"
)

// Merge gate for one worker slot's branch/PR into the cycle branch: the port
// of bin/hv-worker-gate, with the #39 fixes.
//
// Worker-owned branches buy git-native integration and bring back the failure
// class that per-branch verification structurally cannot catch: two workers
// each verify honestly, both branches are green, the merge is textually clean,
// and the cycle branch breaks (a symbol one worker widens while another adds a
// fresh call to it). So this is not a `gh pr merge` wrapper. Per slot, in order:
//
//  1. FRESHNESS: is the cycle branch an ancestor of the worker branch? If not,
//     the worker never merged what landed since it branched and its green is
//     stale. That alone is no refusal (#31): when the merge is clean and the
//     two sides changed no file in common the gate merges it itself and
//     verifies the merged tree; a conflict or a shared file bounces it (see
//     staleReason), and the CLI counts bounces per item. With a recorded PR and an origin remote this is judged
//     on the PUSHED refs after a fetch (origin/<base> vs origin/<branch>),
//     because the PR merges what was pushed, not what sits in a local worktree.
//     The PR's head branch, head SHA and target branch must match the verified
//     ones, so a re-pushed branch or a stacked PR cannot slip through. A git
//     exit code above 1 is the check itself breaking (check-broke), never
//     reported as stale.
//  2. MERGE: through the forge when the slot recorded a PR (pinned to the
//     verified head SHA; glab with auto-merge off, since a scheduled merge is
//     not a merge), else a local `git merge`. A recorded PR with no origin
//     remote is refused, never merged locally. A PR merge is confirmed: its
//     merge commit must be an ancestor of origin/<base> and of the local tree
//     before RE-VERIFY runs.
//  3. RE-VERIFY: on the MERGED tree, never on the branch. This is the only step
//     that catches the two shapes above. Verification commands come from
//     refactor.verifyCommands; when that is empty the gate reports
//     verifySkipped rather than inventing a check it cannot perform.
//
// Between 1 and 2 sits a PROVENANCE check: the PR body's `## Approvals`
// section is cross-checked against the slot's relays[] log. A relay cited as
// the maintainer, a relay round cited that was never sent, or no section at all
// while relays exist is a provenance-fail. Nothing merges on a failed citation.

// Gate verdicts. fresh (with CheckOnly) and pass succeed; the rest are exit 1.
const (
	GateFresh          = "fresh"
	GatePass           = "pass"
	GateStale          = "stale"
	GatePRMismatch     = "pr-mismatch"
	GateProvenanceFail = "provenance-fail"
	GateNotMerged      = "not-merged"
	GateNotOnBase      = "not-on-base"
	GateVerifyFailed   = "verify-failed"
	GateMergedRemotely = "merged-remotely"
	GateMergeFailed    = "merge-failed"
	GateCheckBroke     = "check-broke"
	// GateApprovalRequired: the merge-approval gate (B1) refused before the
	// merge; the CLI exits 4 with it.
	GateApprovalRequired = "approval-required"
)

// GateOpts are the flags of `rota worker gate`.
type GateOpts struct {
	Slot      string
	Base      string
	CheckOnly bool
	NoVerify  bool
	// Approve is the merge-approval gate (B1), run after provenance and right
	// before the merge, never under CheckOnly. files lists the paths the merge
	// changes. A non-nil error stops the gate with verdict approval-required
	// and is returned as is.
	Approve func(files func() ([]string, error)) error
}

// GateResult is the gate's answer. Err is the message for a non-success
// verdict, Hint what to do about it.
type GateResult struct {
	Slot          string
	Verdict       string
	Base          string
	Branch        string
	SHA           string
	PR            string
	Verified      []string
	VerifySkipped bool
	Changed       bool
	Err           string
	Hint          string
	Notes         []string // PROVENANCE-SKIP and NO-VERIFY lines for stderr
}

// OK reports a successful verdict.
func (r GateResult) OK() bool { return r.Verdict == GateFresh || r.Verdict == GatePass }

type prInfo struct{ head, sha, base, state, merge string }

// GateTarget resolves the argument of `rota worker gate`: a slot name, or a PR
// (`#N`, `N` or its URL) resolving to the queued record of a PR whose slot
// moved on, else to a slot recording that PR. A queued record stands in for
// the slot: it carries the branch, pr and relays the gate reads. queued says
// which one it is.
//
// A slot that records no PR while a record queued from it exists is refused:
// the habitual `gate <slot>` would otherwise merge the slot's NEW branch.
func (r Registry) GateTarget(arg string) (s *jsonx.Object, queued bool, err error) {
	if s = r.Slot(arg); s != nil {
		if Str(s, "pr") == "" {
			for _, q := range r.PRs() {
				if Str(q, "from") == arg {
					return nil, false, &Error{Exit: ExitUsage,
						Message: fmt.Sprintf("slot %s records no PR, but its PR %s (%s) waits in review", arg, Str(q, "pr"), Str(q, "branch")),
						Hint:    fmt.Sprintf("gate the PR in review with `rota worker gate %s`", trailingNumber(Str(q, "pr")))}
				}
			}
		}
		return s, false, nil
	}
	if n, ok := PRRefNumber(arg); ok {
		if q := r.QueuedPR(arg); q != nil {
			return q, true, nil
		}
		for _, sl := range r.Slots() {
			if m, ok := PRRefNumber(Str(sl, "pr")); ok && m == n {
				return sl, false, nil
			}
		}
		return nil, false, fail(ExitResolution, fmt.Sprintf("no PR in review or slot records PR #%d", n))
	}
	return nil, false, fail(ExitResolution, fmt.Sprintf("slot '%s' is not in the pool", arg))
}

// Gate runs the merge gate for a slot or a queued PR (see GateTarget). A
// passing gate of a queued PR drops its record.
func (e Env) Gate(ctx context.Context, root string, o GateOpts) (GateResult, error) {
	e = e.withDefaults()
	res := GateResult{Slot: o.Slot, Base: o.Base}
	reg := LoadRegistry(root)
	if !reg.Exists {
		return res, fail(ExitResolution, "no worker pool — run rota worker pool init first")
	}
	s, queued, err := reg.GateTarget(o.Slot)
	if err != nil {
		return res, err
	}
	res, err = e.gate(ctx, root, o, res, reg, s)
	if err == nil && queued && res.Verdict == GatePass {
		if err := RemoveQueuedPR(root, Str(s, "pr")); err != nil {
			return res, err
		}
	}
	return res, err
}

func (e Env) gate(ctx context.Context, root string, o GateOpts, res GateResult, reg Registry, s *jsonx.Object) (GateResult, error) {
	branch, pr := Str(s, "branch"), Str(s, "pr")
	res.Branch, res.PR = branch, pr
	cfg := config.Load(filepath.Join(root, ".rota", "config.json"))
	settings := tracker.SettingsFromConfig(cfg)

	g := &gate{e: e, ctx: ctx, root: root, res: &res, o: o, slot: s, reg: reg, branch: branch, pr: pr}
	g.provider = e.detectProvider(root, pr)
	g.cli = e.Forge(g.provider, root, settings.RetryWait)
	g.cliName = "gh"
	if g.provider == "gitlab" {
		g.cliName = "glab"
	}

	// A PR merges what was PUSHED, so with a PR and an origin remote the gate
	// reads origin/* refs. Without a PR the local merge takes the local branch,
	// and local refs are the right ones. A recorded PR with no origin remote is
	// refused: a local merge would leave the PR open and bypass its review, CI
	// and branch protection while reporting MERGED.
	if pr != "" {
		g.prNum = trailingNumber(pr)
		if _, code := e.git(root, "remote", "get-url", "origin"); code != 0 {
			return g.broke(fmt.Sprintf("slot %s has PR %s but this repo has no 'origin' remote; refusing a local merge that would bypass the PR", o.Slot, pr))
		}
		if g.prNum == "" {
			return g.broke(fmt.Sprintf("cannot read a PR number from '%s'", pr))
		}
		g.remote = true
	}
	if _, code := e.git(root, "rev-parse", "--verify", "--quiet", o.Base); code != 0 {
		return res, fail(ExitResolution, fmt.Sprintf("base branch '%s' does not exist", o.Base))
	}
	if g.remote {
		if _, code := e.git(root, "fetch", "origin", "-q"); code != 0 {
			return g.broke("git fetch origin failed")
		}
		g.headRef, g.baseRef = "origin/"+branch, "origin/"+o.Base
		if _, code := e.git(root, "rev-parse", "--verify", "--quiet", g.headRef+"^{commit}"); code != 0 {
			return g.broke(fmt.Sprintf("%s does not exist — has the worker pushed %s?", g.headRef, branch))
		}
		if _, code := e.git(root, "rev-parse", "--verify", "--quiet", g.baseRef+"^{commit}"); code != 0 {
			return g.broke(g.baseRef + " does not exist")
		}
	} else {
		g.headRef, g.baseRef = branch, o.Base
		if _, code := e.git(root, "rev-parse", "--verify", "--quiet", branch); code != 0 {
			return res, fail(ExitResolution, fmt.Sprintf("worker branch '%s' does not exist", branch))
		}
	}

	// 1. Freshness. Three-way on the exit code: `&& FRESH || STALE` would call
	// a check that itself errors STALE.
	switch _, code := e.git(root, "merge-base", "--is-ancestor", g.baseRef, g.headRef); code {
	case 0:
	case 1:
		behind, c := e.git(root, "rev-list", "--count", g.headRef+".."+g.baseRef)
		if c != 0 {
			behind = "?"
		}
		why, brokeMsg := g.staleReason(cfg)
		if brokeMsg != "" {
			return g.broke(brokeMsg)
		}
		if why != "" {
			return g.verdict(GateStale, fmt.Sprintf("STALE %s %s — %s commit(s) landed on %s since it branched; %s", o.Slot, branch, behind, o.Base, why),
				fmt.Sprintf("bounce: tell slot %s to `git merge %s`, resolve and re-verify, then re-gate", o.Slot, o.Base)), nil
		}
		res.Notes = append(res.Notes, fmt.Sprintf("STALE-MERGE %s — %s commit(s) landed on %s since %s branched; none touch its files and the merge is clean, merging as is", o.Slot, behind, o.Base, branch))
	default:
		return g.broke(fmt.Sprintf("git merge-base --is-ancestor %s %s exited %d", g.baseRef, g.headRef, code))
	}

	if g.remote {
		// The PR must be the thing that was just verified: open, aimed at the
		// gate's base (a PR stacked on another worker's branch merges THERE, not
		// here), and headed by the pushed commit.
		var code int
		if g.verified, code = e.git(root, "rev-parse", g.headRef); code != 0 || g.verified == "" {
			return g.broke(fmt.Sprintf("git rev-parse %s failed (exit %d)", g.headRef, code))
		}
		info, ok := g.prInfo()
		if !ok {
			return g.broke(fmt.Sprintf("could not read PR %s from %s", g.prNum, g.provider))
		}
		mismatch := func(msg string) (GateResult, error) {
			return g.verdict(GatePRMismatch, "error: "+msg, ""), nil
		}
		switch {
		case info.state != "OPEN":
			return mismatch(fmt.Sprintf("PR %s is %s, not open", g.prNum, info.state))
		case info.base != o.Base:
			return mismatch(fmt.Sprintf("PR %s targets '%s', the gate's base is '%s' — stacked PR?", g.prNum, info.base, o.Base))
		case info.head != branch:
			return mismatch(fmt.Sprintf("PR %s is headed by '%s', slot %s verified '%s'", g.prNum, info.head, o.Slot, branch))
		case info.sha != g.verified:
			return mismatch(fmt.Sprintf("PR %s head is %s, the verified %s is %s — pushed since?", g.prNum, info.sha, g.headRef, g.verified))
		}
	}

	failMsg, brokeMsg := g.checkProvenance()
	if brokeMsg != "" {
		return g.broke(brokeMsg)
	}
	if failMsg != "" {
		return g.verdict(GateProvenanceFail, failMsg, ""), nil
	}
	if o.CheckOnly {
		// the checked tip: origin/<branch> when a PR is recorded
		res.SHA, _ = e.git(root, "rev-parse", "--short=7", g.headRef)
		res.Verdict = GateFresh
		return res, nil
	}

	// 2. Merge.
	cur, _ := e.git(root, "rev-parse", "--abbrev-ref", "HEAD")
	if cur != o.Base {
		return res, fail(ExitResolution, fmt.Sprintf("gate must run with %s checked out (currently on %s)", o.Base, cur))
	}
	if o.Approve != nil {
		files := func() ([]string, error) {
			out, code := e.git(root, "diff", "--name-only", g.baseRef+"..."+g.headRef)
			if code != 0 {
				return nil, fmt.Errorf("git diff --name-only %s...%s exited %d", g.baseRef, g.headRef, code)
			}
			var list []string
			for _, l := range strings.Split(out, "\n") {
				if l != "" {
					list = append(list, l)
				}
			}
			return list, nil
		}
		if err := o.Approve(files); err != nil {
			res.Verdict = GateApprovalRequired
			return res, err
		}
	}
	if g.remote {
		if r, done := g.mergeRemote(); done {
			return r, nil
		}
	} else {
		out, errb, code, gerr := e.Git(e.context(), root, "merge", "--no-ff", "-m", fmt.Sprintf("merge: %s into %s", branch, o.Base), branch)
		if gerr != nil {
			code, errb = 127, gerr.Error()
		}
		if code != 0 {
			e.git(root, "merge", "--abort")
			// Only a real conflict is called one. Anything else (no committer
			// identity, a hook, a locked index) is reported with git's own words,
			// so it is not mistaken for work to resolve with the slot.
			if strings.Contains(out+errb, "CONFLICT") {
				return g.verdict(GateMergeFailed, fmt.Sprintf("error: merge of %s into %s conflicted — resolve with the slot that owns the context", branch, o.Base), ""), nil
			}
			return g.verdict(GateMergeFailed, fmt.Sprintf("error: merge of %s into %s failed (exit %d): %s", branch, o.Base, code, strings.TrimSpace(errb+" "+out)), ""), nil
		}
	}
	res.Changed = true
	res.SHA, _ = e.git(root, "rev-parse", "--short=7", "HEAD")

	if o.NoVerify {
		res.Verdict, res.VerifySkipped = GatePass, true
		return res, nil
	}

	// 3. Re-verify on the merged tree.
	var cmds []string
	if v, ok := config.Lookup(cfg, "refactor.verifyCommands"); ok {
		if list, ok := v.([]any); ok {
			for _, c := range list {
				if t := fmt.Sprint(c); strings.TrimSpace(t) != "" {
					cmds = append(cmds, t)
				}
			}
		}
	}
	if len(cmds) == 0 {
		res.Verdict, res.VerifySkipped = GatePass, true
		res.Notes = append(res.Notes, fmt.Sprintf("NO-VERIFY %s — refactor.verifyCommands is empty; merged tree was NOT gated by a command.", o.Slot),
			"set refactor.verifyCommands via rota config set to make this gate real")
		return res, nil
	}
	// Output is kept so a failure can be diagnosed: the tail goes to the
	// message, the whole log stays on disk when anything failed.
	logf, err := os.CreateTemp("", "rota-gate-verify-")
	if err != nil {
		return res, err
	}
	logf.Close()
	failed := false
	for _, c := range cmds {
		out, code := e.Shell(ctx, root, c)
		appendFile(logf.Name(), "== "+c+"\n"+out)
		if code == 0 {
			res.Verified = append(res.Verified, c)
		} else {
			res.Notes = append(res.Notes, "verify FAILED: "+c)
			failed = true
		}
	}
	if failed {
		b, _ := os.ReadFile(logf.Name())
		res.Verdict = GateVerifyFailed
		res.Err = fmt.Sprintf("GATE-FAIL %s — merged tree does not pass verification at %s\nlast lines of the verify output (full log: %s):\n%s",
			o.Slot, res.SHA, logf.Name(), indentTail(string(b), 20))
		res.Hint = fmt.Sprintf("fix forward on %s; the owning slot has usually moved on", o.Base)
		return res, nil
	}
	os.Remove(logf.Name())
	res.Verdict = GatePass
	return res, nil
}

// staleReason says why a branch behind the base must go back to its worker:
// "" when the merge is clean and the two sides changed no file in common, so
// the gate merges it itself and verifies the merged tree. A conflict needs the
// worker's context. A shared file changed on both sides can merge textually
// clean and still break (one side widens a symbol, the other adds a call), and
// that breakage would land on the base before RE-VERIFY sees it. Files matching
// round.sharedPaths are ignored, as the readiness overlap check ignores them.
// brokeMsg is set when a git check itself fails.
func (g *gate) staleReason(cfg any) (why, brokeMsg string) {
	e, root := g.e, g.root
	if _, code := e.git(root, "merge-base", "--is-ancestor", g.headRef, g.baseRef); code == 0 {
		return fmt.Sprintf("its work is already on %s, nothing to merge", g.o.Base), ""
	}
	switch out, code := e.git(root, "merge-tree", "--write-tree", "--no-messages", g.baseRef, g.headRef); code {
	case 0:
	case 1:
		return "the merge conflicts", ""
	default:
		return "", fmt.Sprintf("git merge-tree %s %s exited %d: %s", g.baseRef, g.headRef, code, out)
	}
	mb, code := e.git(root, "merge-base", g.baseRef, g.headRef)
	if code != 0 || mb == "" {
		return "", fmt.Sprintf("git merge-base %s %s exited %d", g.baseRef, g.headRef, code)
	}
	changed := func(ref string) (map[string]bool, bool) {
		out, code := e.git(root, "diff", "--name-only", "--no-renames", mb, ref)
		if code != 0 {
			return nil, false
		}
		set := map[string]bool{}
		for _, l := range strings.Split(out, "\n") {
			if l != "" {
				set[l] = true
			}
		}
		return set, true
	}
	onBase, ok1 := changed(g.baseRef)
	onHead, ok2 := changed(g.headRef)
	if !ok1 || !ok2 {
		return "", fmt.Sprintf("git diff --name-only against %s failed", mb)
	}
	var shared []string
	if v, ok := config.Lookup(cfg, "round.sharedPaths"); ok {
		if list, ok := v.([]any); ok {
			for _, p := range list {
				shared = append(shared, fmt.Sprint(p))
			}
		}
	}
	var both []string
	for f := range onHead {
		if !onBase[f] {
			continue
		}
		skip := false
		for _, p := range shared {
			if gatepath.MatchPath(p, f) {
				skip = true
			}
		}
		if !skip {
			both = append(both, f)
		}
	}
	if len(both) == 0 {
		return "", ""
	}
	sort.Strings(both)
	return "both sides changed " + strings.Join(both, ", "), ""
}

func appendFile(path, text string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(text)
}

func indentTail(text string, n int) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return "    " + strings.Join(lines, "\n    ")
}

func trailingNumber(s string) string {
	m := regexp.MustCompile(`[0-9]+$`).FindString(s)
	return m
}

// detectProvider reads the provider from the PR URL (a bare number carries
// none), then origin, and falls back to github, which is what this gate always
// assumed.
func (e Env) detectProvider(root, pr string) string {
	switch {
	case strings.Contains(pr, "gitlab") || strings.Contains(pr, "/-/merge_requests/"):
		return "gitlab"
	case strings.Contains(pr, "github") || strings.Contains(pr, "/pull/"):
		return "github"
	}
	url, _ := e.git(root, "remote", "get-url", "origin")
	if tracker.ProviderFromURL(url) == "gitlab" {
		return "gitlab"
	}
	return "github"
}

type gate struct {
	e        Env
	ctx      context.Context
	root     string
	res      *GateResult
	o        GateOpts
	slot     *jsonx.Object
	reg      Registry
	branch   string
	pr       string
	prNum    string
	provider string
	cli      *tracker.CLI
	cliName  string
	remote   bool
	headRef  string
	baseRef  string
	verified string
}

// verdict records a non-success verdict.
func (g *gate) verdict(v, msg, hint string) GateResult {
	g.res.Verdict, g.res.Err, g.res.Hint = v, msg, hint
	return *g.res
}

func (g *gate) broke(msg string) (GateResult, error) {
	return g.verdict(GateCheckBroke, fmt.Sprintf("CHECK-BROKE %s — %s", g.o.Slot, msg), ""), nil
}

// call makes one forge CLI call with an empty stdin.
func (g *gate) call(args ...string) (tracker.Result, error) {
	return g.cli.Run(g.ctx, args, nil)
}

func (g *gate) prInfo() (prInfo, bool) {
	if g.provider == "gitlab" {
		r, err := g.call("api", "projects/:fullpath/merge_requests/"+g.prNum)
		if err != nil || r.ExitCode != 0 {
			return prInfo{}, false
		}
		var d struct {
			Source, Sha, Target, State, Merge, Squash string
		}
		var raw map[string]any
		if json.Unmarshal(r.Stdout, &raw) != nil {
			return prInfo{}, false
		}
		str := func(k string) string { s, _ := raw[k].(string); return s }
		d.Source, d.Sha, d.Target = str("source_branch"), str("sha"), str("target_branch")
		d.State, d.Merge, d.Squash = str("state"), str("merge_commit_sha"), str("squash_commit_sha")
		if d.Source == "" && d.Sha == "" {
			return prInfo{}, false
		}
		st := "CLOSED"
		switch d.State {
		case "opened":
			st = "OPEN"
		case "merged":
			st = "MERGED"
		}
		m := d.Merge
		if m == "" {
			m = d.Squash
		}
		return prInfo{d.Source, d.Sha, d.Target, st, m}, true
	}
	r, err := g.call("pr", "view", g.prNum, "--json", "headRefName,headRefOid,baseRefName,state,mergeCommit")
	if err != nil || r.ExitCode != 0 {
		return prInfo{}, false
	}
	var d struct {
		HeadRefName string `json:"headRefName"`
		HeadRefOid  string `json:"headRefOid"`
		BaseRefName string `json:"baseRefName"`
		State       string `json:"state"`
		MergeCommit *struct {
			Oid string `json:"oid"`
		} `json:"mergeCommit"`
	}
	if json.Unmarshal(r.Stdout, &d) != nil || d.HeadRefName == "" {
		return prInfo{}, false
	}
	m := ""
	if d.MergeCommit != nil {
		m = d.MergeCommit.Oid
	}
	return prInfo{d.HeadRefName, d.HeadRefOid, d.BaseRefName, d.State, m}, true
}

// prBody is the PR/MR description. An error means it could not be read.
func (g *gate) prBody() (string, error) {
	if g.provider == "gitlab" {
		r, err := g.call("api", "projects/:fullpath/merge_requests/"+g.prNum)
		if err != nil {
			return "", err
		}
		if r.ExitCode != 0 {
			return "", fmt.Errorf("%s exited %d", g.cliName, r.ExitCode)
		}
		var d struct {
			Description string `json:"description"`
		}
		if err := json.Unmarshal(r.Stdout, &d); err != nil {
			return "", err
		}
		return d.Description, nil
	}
	r, err := g.call("pr", "view", g.pr, "--json", "body", "-q", ".body")
	if err != nil {
		return "", err
	}
	if r.ExitCode != 0 {
		return "", fmt.Errorf("%s exited %d", g.cliName, r.ExitCode)
	}
	// gh prints the jq result followed by a newline; only that one goes.
	return strings.TrimSuffix(string(r.Stdout), "\n"), nil
}

var (
	reApprovalsHead = regexp.MustCompile(`(?i)^##\s+Approvals\s*$`)
	reH2            = regexp.MustCompile(`^##(\s|$)`)
	reSpaces        = regexp.MustCompile(`\s+`)
	reRelayCite     = regexp.MustCompile(`orchestrator relay(?:\s+round\s+(\d+))?`)
)

func norm(s string) string {
	return strings.TrimSpace(reSpaces.ReplaceAllString(strings.ToLower(s), " "))
}

// checkProvenance cross-checks the PR body's approvals against the slot's
// relay log. It returns the PROVENANCE-FAIL message, or a check-broke message
// when the PR body cannot be read; both "" for pass or skip.
//
// Only a missing forge CLI may skip. Reading the body failing for any other
// reason (not authenticated, rate limited, PR gone, unparseable reply) used
// to be a skip too, in the Python gate as well, so the gate merged without
// having looked at the approvals. That fails open; here it is check-broke.
func (g *gate) checkProvenance() (failMsg, brokeMsg string) {
	if g.pr == "" {
		g.res.Notes = append(g.res.Notes, fmt.Sprintf("PROVENANCE-SKIP %s — no recorded PR (or no %s) to read approvals from", g.o.Slot, g.cliName))
		return "", ""
	}
	body, err := g.prBody()
	if err != nil {
		if tracker.IsKind(err, tracker.KindUnavailable) && strings.Contains(err.Error(), "is not installed") {
			g.res.Notes = append(g.res.Notes, fmt.Sprintf("PROVENANCE-SKIP %s — no %s to read approvals from", g.o.Slot, g.cliName))
			return "", ""
		}
		return "", fmt.Sprintf("could not read the body of %s to check its approvals: %v", g.pr, err)
	}
	var relays []*jsonx.Object
	if v, _ := g.slot.Get("relays"); v != nil {
		if l, ok := v.([]any); ok {
			for _, r := range l {
				if o, ok := r.(*jsonx.Object); ok {
					relays = append(relays, o)
				}
			}
		}
	}
	section, found := approvalsSection(body)
	if !found {
		if len(relays) > 0 {
			return fmt.Sprintf("PROVENANCE-FAIL %s: %d relay(s) logged but the PR body has no ## Approvals section", g.o.Slot, len(relays)), ""
		}
		return "", ""
	}
	rounds := map[int]bool{}
	for _, r := range relays {
		if v, ok := r.Get("round"); ok {
			if n, ok := v.(json.Number); ok {
				if i, err := strconv.Atoi(n.String()); err == nil {
					rounds[i] = true
				}
			}
		}
	}
	var problems []string
	for _, line := range splitLines(section) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		low := norm(line)
		if m := reRelayCite.FindStringSubmatch(low); m != nil {
			if n := m[1]; n != "" {
				i, _ := strconv.Atoi(n)
				if !rounds[i] {
					problems = append(problems, fmt.Sprintf("cites an orchestrator relay for round %s but none is logged: %s", n, strings.TrimSpace(line)))
				}
			} else if len(relays) == 0 {
				problems = append(problems, fmt.Sprintf("cites an orchestrator relay but none is logged: %s", strings.TrimSpace(line)))
			}
		} else if strings.Contains(low, "maintainer") {
			for _, r := range relays {
				summary := norm(Str(r, "summary"))
				if len([]rune(summary)) >= 12 && (strings.Contains(low, summary) || strings.Contains(summary, low)) {
					rv, _ := r.Get("round")
					problems = append(problems, fmt.Sprintf("cites the maintainer for text the orchestrator relayed (round %v): %s", rv, strings.TrimSpace(line)))
					break
				}
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Sprintf("PROVENANCE-FAIL %s: %s", g.o.Slot, strings.Join(problems, "; ")), ""
	}
	return "", ""
}

// approvalsSection is the body of the `## Approvals` section: the lines after
// the heading up to the next `## ` heading.
func approvalsSection(body string) (string, bool) {
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		if !reApprovalsHead.MatchString(l) {
			continue
		}
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			if reH2.MatchString(lines[j]) {
				end = j
				break
			}
		}
		return strings.Join(lines[i+1:end], "\n"), true
	}
	return "", false
}

// mergeRemote merges through the forge and confirms it landed. done is true
// when it ends the gate with a verdict.
func (g *gate) mergeRemote() (GateResult, bool) {
	e, o := g.e, g.o
	// Pinned to the verified SHA so a push after the check is refused. glab
	// schedules an auto-merge while a pipeline runs unless told not to, and a
	// scheduled merge reports success and merges nothing.
	var args []string
	if g.provider == "gitlab" {
		args = []string{"mr", "merge", g.prNum, "-y", "--auto-merge=false", "--sha", g.verified}
	} else {
		args = []string{"pr", "merge", g.prNum, "--merge", "--match-head-commit", g.verified}
	}
	r, err := g.call(args...)
	if err != nil || r.ExitCode != 0 {
		out := ""
		code := 0
		if err != nil {
			out, code = err.Error(), 1
		} else {
			out, code = string(r.Stdout)+string(r.Stderr), r.ExitCode
		}
		return g.verdict(GateMergeFailed, fmt.Sprintf("error: %s merge failed for %s (exit %d):\n%s", g.cliName, g.pr, code, tailLines(out, 20)), ""), true
	}

	// Confirm it landed: the tracker's word is not enough. The merge commit can
	// take a moment to appear. State not merged is its own verdict (a scheduled
	// auto-merge reports success and merges nothing). A fast-forward or rebase
	// merge has no merge commit on either provider: state merged with an empty
	// one falls back to the pinned verified SHA, which must then be on the base.
	wait := 2 * time.Second
	if v := e.Getenv("ROTA_GATE_SHA_WAIT"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			wait = time.Duration(f * float64(time.Second))
		}
	}
	var sha, state string
	for i := 0; i < 5; i++ {
		info, ok := g.prInfo()
		if !ok {
			res, _ := g.broke(fmt.Sprintf("could not re-read PR %s after merging", g.prNum))
			return res, true
		}
		state, sha = info.state, info.merge
		if state == "MERGED" && sha != "" {
			break
		}
		if state != "MERGED" {
			e.Sleep(wait)
		}
	}
	if state != "MERGED" {
		return g.verdict(GateNotMerged, fmt.Sprintf("NOT-MERGED %s — PR %s is %s after the merge call; nothing is on %s", o.Slot, g.prNum, state, o.Base), ""), true
	}
	if sha == "" {
		sha = g.verified
	}
	if _, code := e.git(g.root, "fetch", "origin", "-q"); code != 0 {
		res, _ := g.broke("git fetch origin failed after merging")
		return res, true
	}
	switch _, code := e.git(g.root, "merge-base", "--is-ancestor", sha, g.baseRef); code {
	case 0:
	case 1:
		return g.verdict(GateNotOnBase, fmt.Sprintf("NOT-ON-BASE %s — merge commit %s is not an ancestor of %s (merged into another branch?)", o.Slot, sha, g.baseRef), ""), true
	default:
		res, _ := g.broke(fmt.Sprintf("git merge-base --is-ancestor %s %s exited %d", sha, g.baseRef, code))
		return res, true
	}
	// Past this point the PR IS merged: report local trouble distinctly so
	// nobody retries an already-merged PR.
	g.res.Changed = true
	if len(sha) >= 7 {
		g.res.SHA = sha[:7] // the merge that landed on origin, for the merged-remotely verdicts
	}
	if _, code := e.git(g.root, "merge", "--ff-only", g.baseRef); code != 0 {
		return g.verdict(GateMergedRemotely, fmt.Sprintf("MERGED-REMOTELY %s — PR %s is on %s but local %s could not fast-forward (diverged); do not re-merge, reconcile %s by hand", o.Slot, g.prNum, g.baseRef, o.Base, o.Base), ""), true
	}
	if _, code := e.git(g.root, "merge-base", "--is-ancestor", sha, "HEAD"); code != 0 {
		return g.verdict(GateMergedRemotely, fmt.Sprintf("MERGED-REMOTELY %s — PR %s is on %s but %s is not in the local %s; do not re-merge, reconcile %s by hand", o.Slot, g.prNum, g.baseRef, sha, o.Base, o.Base), ""), true
	}
	return GateResult{}, false
}

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
