package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/roundwatch"
	"github.com/l4ci/rota/internal/tracker"
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

const swLabel = "site:support"

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
	root := gitRepo(t)
	write(t, filepath.Join(root, ".rota", "config.json"), `{"issues":{"source":"github"},"round":{"support":"mac"}}`)
	if workers != "" {
		write(t, filepath.Join(root, ".rota", "workers.json"), workers)
	}
	deps := testDeps()
	deps.RoundEnv = func(context.Context, string) round.Env {
		return round.Env{Git: git.Exec, Base: "main", HostName: "solo", Forge: f, SupportLabel: swLabel,
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
	f.comment(7, "14", "ann", "<!-- rota:comment note -->\n<!-- rota:from support -->\nneed a decision")
	if got := supportKeys(pass())[roundwatch.SupportKey("7", "comment")]; got != "ann#14" {
		t.Fatalf("stamped note by the support machine: key = %q", got)
	}
	f.comment(7, "15", "ann", "<!-- rota:comment note -->\n<!-- rota:from main -->\nmain talking")
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
	f.comment(31, "5", "ann", "<!-- rota:comment note -->\n<!-- rota:from main -->\nuse the other API")
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
		return round.Env{Git: git.Exec, Base: "main", HostName: "solo", Forge: f, SupportLabel: swLabel}
	}
	c := &Ctx{Deps: deps, ctx: context.Background()}
	if k := supportKeys(watchForge(context.Background(), c, root)); len(k) != 0 || len(f.gets) != 0 || f.labelLists() != 0 {
		t.Fatalf("keys %v gets %v lists %v", k, f.gets, f.lists)
	}
}

// The seen file keeps what the orchestrator was woken for across watches.
func TestWatchSeenPersistsAcrossWatches(t *testing.T) {
	cd := t.TempDir()
	seen, mark := watchSeen(cd)
	if len(seen()) != 0 {
		t.Fatalf("fresh seen = %v", seen())
	}
	mark(map[string]string{"support/7/pr-head": "aaa"})
	mark(map[string]string{"support/8/comment": "ann#2"})
	seen2, _ := watchSeen(cd)
	if got := seen2(); got["support/7/pr-head"] != "aaa" || got["support/8/comment"] != "ann#2" || len(got) != 2 {
		t.Fatalf("seen = %v", got)
	}
	if _, err := os.Stat(filepath.Join(cd, "rota")); err != nil {
		t.Errorf("seen file not under the rota state dir: %v", err)
	}
}
