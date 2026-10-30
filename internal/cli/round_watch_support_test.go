package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/proc"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/roundwatch"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// swForge is the round's Forge for the support watch: issues with labels and
// comments, open PRs with heads, a write-access list, and a count of reads.
type swForge struct {
	issues  map[int]*tracker.Issue
	prs     []tracker.PR
	heads   map[int]string
	writers map[string]bool
	gets    []string
	lists   [][]string
	// conflict is the PR numbers the forge reports as not mergeable; mergeErr
	// makes the mergeability read fail; merges counts the reads.
	conflict map[int]bool
	mergeErr bool
	merges   int
}

func (f *swForge) PRMergeable(_ context.Context, n int) (tracker.Mergeability, error) {
	f.merges++
	if f.mergeErr {
		return tracker.Mergeability{}, fmt.Errorf("forge down")
	}
	if f.conflict[n] {
		return tracker.Mergeability{State: tracker.MergeConflict}, nil
	}
	return tracker.Mergeability{State: tracker.MergeClean}, nil
}

func (f *swForge) List(_ context.Context, fl tracker.ListFilter) ([]tracker.Issue, error) {
	f.lists = append(f.lists, fl.Labels)
	var out []tracker.Issue
	for n := 1; n < 100; n++ {
		if is, ok := f.issues[n]; ok && (len(fl.Labels) == 0 || slices.Contains(is.Labels, fl.Labels[0])) {
			out = append(out, *is)
		}
	}
	return out, nil
}
func (f *swForge) Get(_ context.Context, k tracker.IssueKey, _ bool) (tracker.Issue, error) {
	f.gets = append(f.gets, k.String())
	n, _ := k.Number()
	return *f.issues[n], nil
}
func (f *swForge) OpenPRs(context.Context) ([]tracker.PR, error)           { return f.prs, nil }
func (f *swForge) MergedPRs(context.Context, string) ([]tracker.PR, error) { return nil, nil }
func (f *swForge) PRState(context.Context, int) (string, error)            { return "open", nil }
func (f *swForge) PRView(_ context.Context, n int) (tracker.PRInfo, error) {
	return tracker.PRInfo{State: "OPEN", HeadSHA: f.heads[n]}, nil
}

var swClose = regexp.MustCompile(`#(\d+)`)

func (f *swForge) ClosedNumbers(body string) []int {
	var out []int
	for _, m := range swClose.FindAllStringSubmatch(body, -1) {
		var n int
		fmt.Sscan(m[1], &n)
		out = append(out, n)
	}
	return out
}
func (f *swForge) AddLabels(context.Context, tracker.IssueKey, []string, bool) error { return nil }
func (f *swForge) RemoveLabels(context.Context, tracker.IssueKey, []string) error    { return nil }
func (f *swForge) CanPush(_ context.Context, login string) (bool, error) {
	return f.writers[login], nil
}

const swLabel = "machine:mac"

func (f *swForge) issue(n int, labels ...string) *tracker.Issue {
	if f.issues == nil {
		f.issues = map[int]*tracker.Issue{}
	}
	is := &tracker.Issue{Key: tracker.NumKey(n), Labels: labels, State: "open"}
	f.issues[n] = is
	return is
}

func (f *swForge) comment(n int, id, author, body string) {
	is := f.issues[n]
	is.Comments = append(is.Comments, tracker.Comment{ID: id, Author: author, Body: body})
}

func swWatch(t *testing.T, f *swForge, role string, workers string) func() map[string]string {
	t.Helper()
	return swWatchGit(t, f, role, workers, git.Exec)
}

func swWatchGit(t *testing.T, f *swForge, role string, workers string, g git.Runner) func() map[string]string {
	t.Helper()
	root := gitRepo(t)
	write(t, filepath.Join(root, ".rota", "config.json"), `{"issues":{"source":"github"},"round":{"support":"mac"}}`)
	if workers != "" {
		write(t, filepath.Join(root, ".rota", "workers.json"), workers)
	}
	deps := testDeps()
	deps.RoundEnv = func(context.Context, string) round.Env {
		return round.Env{Worker: round.FromWorkerEnv(worker.Env{}), Git: g, Base: "main", HostName: "solo", Forge: f, SupportLabel: swLabel,
			Round: roundcfg.Settings{Support: "mac", Role: role}}
	}
	c := &Ctx{Deps: deps, ctx: context.Background()}
	return func() map[string]string { return watchForge(context.Background(), c, root) }
}

func supportKeys(m map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		if strings.HasPrefix(k, "support/") {
			out[k] = v
		}
	}
	return out
}

// #1827 AC-3: main's watch reports a support PR opening and its head moving,
// for an offered issue.
func TestWatchWakesOnSupportPRHead(t *testing.T) {
	f := &swForge{heads: map[int]string{9: "aaa111"}, writers: map[string]bool{"ann": true}}
	f.issue(7, swLabel)
	f.prs = []tracker.PR{{Number: 9, Branch: "mac-ben/7-export", Body: "Closes #7", URL: "https://github.com/o/r/pull/9"}}
	pass := swWatch(t, f, roundcfg.RoleMain, "")
	first := supportKeys(pass())
	if first[roundwatch.SupportKey("7", "pr-head")] != "aaa111" || first[roundwatch.SupportKey("7", "pr-opened")] != "#9" {
		t.Fatalf("first pass = %v", first)
	}
	f.heads[9] = "bbb222"
	second := supportKeys(pass())
	var got []string
	for _, ch := range roundwatch.Diff(first, second) {
		got = append(got, ch.Key+"|"+ch.From+"|"+ch.To)
	}
	if want := []string{"support/7/pr-head|aaa111|bbb222"}; !slices.Equal(got, want) {
		t.Fatalf("changes = %q, want %q", got, want)
	}
}

// #1918 AC-1: a support PR whose issue lost the support label still wakes the
// watch on a push.
func TestWatchWakesOnSupportPRHeadWithoutLabel(t *testing.T) {
	f := &swForge{heads: map[int]string{9: "aaa111"}, writers: map[string]bool{"ann": true}}
	f.issue(7, "type:bug")
	f.prs = []tracker.PR{{Number: 9, Branch: "mac-ben/7-export", Body: "Closes #7"}, {Number: 10, Branch: "ben/8-other", Body: "Closes #8"}}
	pass := swWatch(t, f, roundcfg.RoleMain, "")
	first := supportKeys(pass())
	if first[roundwatch.SupportKey("7", "pr-head")] != "aaa111" || first[roundwatch.SupportKey("7", "pr-opened")] != "#9" || len(first) != 2 {
		t.Fatalf("first pass = %v", first)
	}
	f.heads[9] = "bbb222"
	if got := supportKeys(pass())[roundwatch.SupportKey("7", "pr-head")]; got != "bbb222" {
		t.Fatalf("moved head = %q", got)
	}
}

func lsRemote(out string) git.Runner {
	return func(_ context.Context, _ string, args ...string) (proc.Result, error) {
		if len(args) > 0 && args[0] == "ls-remote" {
			return proc.Result{Stdout: out}, nil
		}
		return proc.Result{}, nil
	}
}

// #1918 AC-2: a pushed support branch with no PR yet is named by the watch,
// and stops being a branch entry once it has a PR.
func TestWatchWakesOnSupportBranchWithoutPR(t *testing.T) {
	f := &swForge{heads: map[int]string{}, writers: map[string]bool{"ann": true}}
	out := "ccc333\trefs/heads/mac-ben/7-export\nddd444\trefs/heads/mac-nia/8-other\n"
	pass := swWatchGit(t, f, roundcfg.RoleMain, "", lsRemote(out))
	first := supportKeys(pass())
	key := roundwatch.SupportKey("mac-ben~7-export", "branch-head")
	if first[key] != "ccc333" || len(first) != 2 {
		t.Fatalf("first pass = %v", first)
	}
	f.prs = []tracker.PR{{Number: 9, Branch: "mac-ben/7-export", Body: "Closes #7"}}
	f.heads[9] = "ccc333"
	second := supportKeys(pass())
	if _, ok := second[key]; ok || second[roundwatch.SupportKey("7", "pr-opened")] != "#9" {
		t.Fatalf("branch with a PR must be reported as the PR: %v", second)
	}
}

// #1918 AC-3: no head change, no change.
func TestWatchSupportBranchQuiet(t *testing.T) {
	f := &swForge{heads: map[int]string{}, writers: map[string]bool{"ann": true}}
	pass := swWatchGit(t, f, roundcfg.RoleMain, "", lsRemote("ccc333\trefs/heads/mac-ben/7-export\n"))
	if ch := roundwatch.Diff(supportKeys(pass()), supportKeys(pass())); len(ch) != 0 {
		t.Fatalf("changes = %v", ch)
	}
}

// A stalled origin read is bounded, produces no branch news, and does not
// prevent the next poll from reporting a push.
func TestWatchSupportBranchTimeout(t *testing.T) {
	f := &swForge{}
	stall := false
	head := "aaa111"
	runGit := func(ctx context.Context, _ string, args ...string) (proc.Result, error) {
		if len(args) == 0 || args[0] != "ls-remote" {
			return proc.Result{}, nil
		}
		if stall {
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > 5*time.Second {
				t.Fatal("support branch read must have a deadline within five seconds")
			}
			<-ctx.Done()
			return proc.Result{Stdout: "partial\trefs/heads/mac-ben/7-export\n"}, ctx.Err()
		}
		return proc.Result{Stdout: head + "\trefs/heads/mac-ben/7-export\n"}, nil
	}
	pass := swWatchGit(t, f, roundcfg.RoleMain, "", runGit)
	seen := supportKeys(pass())
	const key = "support/mac-ben~7-export/branch-head"
	if seen[key] != "aaa111" || len(seen) != 1 {
		t.Fatalf("successful snapshot = %v", seen)
	}
	stall = true
	timedOut := supportKeys(pass())
	if len(timedOut) != 0 {
		t.Fatalf("timed-out read must omit partial branch output: %v", timedOut)
	}
	// Exercise the watch's saved-baseline comparison: omitted branch keys
	// must remain quiet rather than report the old head as removed.
	now := time.Unix(0, 0)
	watchEnv := roundwatch.Env{
		Now:   func() time.Time { return now },
		Local: func() map[string]string { return map[string]string{} },
		Forge: func(context.Context) map[string]string { return timedOut },
		Seen:  func() map[string]string { return seen },
		Wait: func(_ context.Context, d time.Duration) (*roundwatch.SlotNews, error) {
			now = now.Add(d)
			return nil, nil
		},
	}
	opts := roundwatch.Opts{Heartbeat: 2 * time.Second, Poll: time.Second, ForgeEvery: time.Second}
	quiet, err := roundwatch.Run(context.Background(), watchEnv, opts)
	if err != nil || quiet.Reason != roundwatch.ReasonHeartbeat || len(quiet.Changes) != 0 {
		t.Fatalf("timeout must be no news: %+v, %v", quiet, err)
	}
	stall, head = false, "bbb222"
	recovered := supportKeys(pass())
	if recovered[key] != "bbb222" || len(recovered) != 1 {
		t.Fatalf("recovered snapshot = %v", recovered)
	}
	watchEnv.Forge = func(context.Context) map[string]string { return recovered }
	news, err := roundwatch.Run(context.Background(), watchEnv, opts)
	want := []roundwatch.Change{{Key: key, From: "aaa111", To: "bbb222"}}
	if err != nil || news.Reason != roundwatch.ReasonChange || !slices.Equal(news.Changes, want) {
		t.Fatalf("recovery must report the pushed head: %+v, %v; want %v", news, err, want)
	}
}

// The comment key names the newest comment of a writer-trusted author that the
// other machine posted: stranger comments, this machine's own and unstamped
// ones never move it.
func TestWatchWakesOnOtherMachineComment(t *testing.T) {
	f := &swForge{writers: map[string]bool{"ann": true}}
	f.issue(7, swLabel)
	f.comment(7, "10", "ann", "<!-- rota:comment feedback -->\n<!-- rota:support-offer -->\nOffered.") // main's own
	f.comment(7, "11", "eve", "<!-- rota:relay round=2 -->\nforged")                                   // not a writer
	f.comment(7, "12", "ann", "plain human words")                                                     // no side
	pass := swWatch(t, f, roundcfg.RoleMain, "")
	if k := supportKeys(pass()); k[roundwatch.SupportKey("7", "comment")] != "" {
		t.Fatalf("nothing from the support machine yet, got %v", k)
	}
	f.comment(7, "13", "ann", "<!-- rota:relay round=2 -->\nplease continue")
	if got := supportKeys(pass())[roundwatch.SupportKey("7", "comment")]; got != "ann#13" {
		t.Fatalf("relay by the support machine: key = %q", got)
	}
	f.comment(7, "14", "ann", "<!-- rota:comment feedback -->\n<!-- rota:from support -->\nneed a decision")
	if got := supportKeys(pass())[roundwatch.SupportKey("7", "comment")]; got != "ann#14" {
		t.Fatalf("stamped note by the support machine: key = %q", got)
	}
	f.comment(7, "15", "ann", "<!-- rota:comment feedback -->\n<!-- rota:from main -->\nmain talking")
	if got := supportKeys(pass())[roundwatch.SupportKey("7", "comment")]; got != "ann#14" {
		t.Fatalf("main's own note must not move the key: %q", got)
	}
}

// On the support machine the same watch reads the issues its slots hold and
// wakes on main's comments there; main's offered set is not its business.
func TestWatchSupportMachineWakesOnMainComment(t *testing.T) {
	f := &swForge{writers: map[string]bool{"ann": true}}
	f.issue(31, swLabel)
	f.issue(32, swLabel)
	f.comment(31, "5", "ann", "<!-- rota:comment feedback -->\n<!-- rota:from main -->\nuse the other API")
	f.comment(31, "6", "ann", "<!-- rota:support-return -->\nmine")
	f.comment(32, "7", "ann", "<!-- rota:support-offer -->\nnot held")
	pass := swWatch(t, f, roundcfg.RoleSupport, `{"slots":[{"name":"mac-ben","state":"busy","task":"31"}]}`)
	k := supportKeys(pass())
	if k[roundwatch.SupportKey("31", "comment")] != "ann#5" {
		t.Fatalf("keys = %v", k)
	}
	if _, ok := k[roundwatch.SupportKey("32", "comment")]; ok || slices.Contains(f.gets, "32") {
		t.Fatalf("an issue no slot holds was read: keys %v gets %v", k, f.gets)
	}
	if len(k) != 1 {
		t.Errorf("the support machine has no PR keys: %v", k)
	}
}

// Only offered-or-held issues are read, once per pass, and the backlog is not
// listed: the offered set is one label query.
func TestWatchSupportReadsAreBounded(t *testing.T) {
	f := &swForge{writers: map[string]bool{"ann": true}, heads: map[int]string{}}
	for n := 1; n <= 3; n++ {
		f.issue(n, swLabel)
	}
	for n := 4; n <= 40; n++ {
		f.issue(n, "type:feature")
	}
	pass := swWatch(t, f, roundcfg.RoleMain, "")
	pass()
	if want := []string{"1", "2", "3"}; !slices.Equal(f.gets, want) {
		t.Errorf("issues read = %v, want %v", f.gets, want)
	}
	if n := f.labelLists(); n != 1 {
		t.Errorf("label queries = %d, want 1 (lists %v)", n, f.lists)
	}
}

// labelLists counts the list calls filtered by the support label; the status
// read keeps its own, unfiltered ones.
func (f *swForge) labelLists() int {
	n := 0
	for _, l := range f.lists {
		if len(l) == 1 && l[0] == swLabel {
			n++
		}
	}
	return n
}

// Without round.support nothing is read and no key appears.
func TestWatchSupportOffWithoutRoundSupport(t *testing.T) {
	f := &swForge{writers: map[string]bool{"ann": true}}
	f.issue(7, swLabel)
	root := gitRepo(t)
	write(t, filepath.Join(root, ".rota", "config.json"), `{"issues":{"source":"github"}}`)
	deps := testDeps()
	deps.RoundEnv = func(context.Context, string) round.Env {
		return round.Env{Worker: round.FromWorkerEnv(worker.Env{}), Git: git.Exec, Base: "main", HostName: "solo", Forge: f, SupportLabel: swLabel}
	}
	c := &Ctx{Deps: deps, ctx: context.Background()}
	if k := supportKeys(watchForge(context.Background(), c, root)); len(k) != 0 || len(f.gets) != 0 || f.labelLists() != 0 || f.merges != 0 {
		t.Fatalf("keys %v gets %v lists %v", k, f.gets, f.lists)
	}
}

func (*swForge) PRSetBase(context.Context, int, string) error { return nil }

const swQueued = `{"slots":[{"name":"mac-ben","state":"busy","task":"40","pr":"https://github.com/o/r/pull/50"}],` +
	`"prs":[{"issue":"31","pr":"https://github.com/o/r/pull/5","branch":"mac-nia/31-x","from":"mac-nia"}]}`

// #1911 AC-1: main's comment on the issue of a queued (review-list) PR wakes
// the support machine, though no slot holds the issue any more.
func TestWatchSupportMachineWakesOnQueuedPRComment(t *testing.T) {
	f := &swForge{writers: map[string]bool{"ann": true}}
	f.issue(31, swLabel)
	f.issue(40, swLabel)
	f.comment(31, "5", "ann", "<!-- rota:comment feedback -->\n<!-- rota:from main -->\nREVIEW BOUNCE for PR 5")
	pass := swWatch(t, f, roundcfg.RoleSupport, swQueued)
	if got := supportKeys(pass())[roundwatch.SupportKey("31", "comment")]; got != "ann#5" {
		t.Fatalf("queued PR's issue comment key = %q", got)
	}
}

// #1911 AC-2: an open support PR, held or queued, that turns CONFLICTING wakes
// once per head; a mergeable PR stays quiet.
func TestWatchSupportMachineWakesOnConflict(t *testing.T) {
	f := &swForge{writers: map[string]bool{"ann": true}, heads: map[int]string{5: "aaa111", 50: "bbb222"}}
	f.issue(31, swLabel)
	f.issue(40, swLabel)
	pass := swWatch(t, f, roundcfg.RoleSupport, swQueued)
	first := supportKeys(pass())
	if len(first) != 0 {
		t.Fatalf("mergeable PRs must stay quiet: %v", first)
	}
	f.conflict = map[int]bool{5: true, 50: true}
	second := supportKeys(pass())
	if second[roundwatch.SupportKey("31", "conflict")] != "aaa111" || second[roundwatch.SupportKey("40", "conflict")] != "bbb222" {
		t.Fatalf("conflicting PRs not reported: %v", second)
	}
	third := supportKeys(pass())
	if ch := roundwatch.Diff(second, third); len(ch) != 0 {
		t.Fatalf("same head woke again: %v", ch)
	}
	f.heads[5] = "ccc333"
	var got []string
	for _, ch := range roundwatch.Diff(third, supportKeys(pass())) {
		got = append(got, ch.Key+"|"+ch.From+"|"+ch.To)
	}
	if want := []string{"support/31/conflict|aaa111|ccc333"}; !slices.Equal(got, want) {
		t.Fatalf("new head on a conflicting PR: %q, want %q", got, want)
	}
}

// #1911 AC-3: a failed mergeability read is no news; main's watch does not
// read mergeability at all.
func TestWatchSupportConflictReadFailureAndMain(t *testing.T) {
	f := &swForge{writers: map[string]bool{"ann": true}, heads: map[int]string{5: "aaa111", 50: "bbb222"}, conflict: map[int]bool{5: true}, mergeErr: true}
	f.issue(31, swLabel)
	f.issue(40, swLabel)
	pass := swWatch(t, f, roundcfg.RoleSupport, swQueued)
	if k := supportKeys(pass()); len(k) != 0 {
		t.Fatalf("failed read must be no news: %v", k)
	}
	g := &swForge{writers: map[string]bool{"ann": true}, heads: map[int]string{}, conflict: map[int]bool{5: true}}
	g.issue(31, swLabel)
	swWatch(t, g, roundcfg.RoleMain, swQueued)()
	if g.merges != 0 {
		t.Fatalf("main read mergeability %d times", g.merges)
	}
}
