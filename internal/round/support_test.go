package round

import (
	"context"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/rotatree"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/worker"
)

// Machine pins stay visible on both machines, with an actionable refusal.
func TestCandidatesPinnedElsewhereShown(t *testing.T) {
	root := newRepo(t, nil)
	be := &fakeRemote{}
	for _, id := range []string{"1", "2", "3", "4", "5"} {
		be.add(id, "work", "", false, "")
	}
	be.items["2"].Labels = []string{"machine:mac"}
	be.items["3"].Labels = []string{"machine:main"}
	be.items["4"].Labels = []string{"machine:other", "machine:mac"}
	be.items["5"].Labels = []string{"machine:other", "machine:main"}
	e := Env{Git: git.Exec, Base: "main", Worker: wk(worker.Env{Getenv: noEnv, Config: diskConfig}), Lease: fakeLease("h", 100)}
	for _, tc := range []struct {
		role    string
		blocked []string
	}{
		{roundcfg.RoleMain, []string{"2", "4"}},
		{roundcfg.RoleSupport, []string{"1", "3", "5"}},
	} {
		t.Run(tc.role, func(t *testing.T) {
			cs, err := e.RoundCandidates(bg, root, be, roundcfg.Settings{Scope: roundcfg.ScopeOpen, Support: "mac", Role: tc.role}, "")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(ids(cs), []string{"1", "2", "3", "4", "5"}) {
				t.Fatalf("candidates = %v; pinned issues must remain visible", ids(cs))
			}
			for _, c := range cs {
				wantBlocked := slices.Contains(tc.blocked, c.ID)
				if (c.BlockedBy == BlockSite) != wantBlocked {
					t.Errorf("%s blockedBy = %q, want site = %v", c.ID, c.BlockedBy, wantBlocked)
				}
				if !wantBlocked && !c.Ready() {
					t.Errorf("local or unpinned issue %s must be ready: %+v", c.ID, c)
				}
				if wantBlocked {
					detail := fmt.Sprint(c.Checks)
					if !strings.Contains(detail, "machine:") {
						t.Errorf("missing machine pin in refusal: %s", detail)
					}
					if c.Ready() {
						t.Errorf("pinned elsewhere issue %s is ready", c.ID)
					}
				}
			}
		})
	}
}

// Default-config upgrades must not turn legacy support offers into free work.
func TestCandidatesLegacySupportBlocked(t *testing.T) {
	root := newRepo(t, nil)
	be := &fakeRemote{}
	be.add("1", "work", "", false, "")
	be.items["1"].Labels = []string{"site:support"}
	e := Env{Git: git.Exec, Base: "main", Worker: wk(worker.Env{Getenv: noEnv, Config: diskConfig})}
	cs, err := e.RoundCandidates(bg, root, be, roundcfg.Settings{Scope: roundcfg.ScopeOpen, Support: "mac", Role: roundcfg.RoleMain}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || cs[0].Ready() || cs[0].BlockedBy != BlockSite {
		t.Fatalf("legacy support issue must stay blocked until migration: %+v", cs)
	}
	if why := fmt.Sprint(cs[0].Checks); !strings.Contains(why, "rota migrate labels --apply") {
		t.Fatalf("missing migration warning: %s", why)
	}
}

func TestAssignRefusesPinnedElsewhere(t *testing.T) {
	for _, bundle := range []bool{false, true} {
		t.Run(fmt.Sprint(bundle), func(t *testing.T) {
			f := newAssignFixture(t)
			f.env.Round.Support = "mac"
			id := "12"
			if bundle {
				id = "13"
			}
			f.be.items[id].Labels = []string{"machine:mac"}
			_, err := f.assign("12", "", func(o *AssignOpts) {
				if bundle {
					o.Bundle = []string{"13"}
				}
			})
			var refusal *BlockedError
			if !errors.As(err, &refusal) || refusal.By != BlockSite {
				t.Fatalf("assign err = %v, want blockedBy site", err)
			}
			if !strings.Contains(err.Error(), "machine:mac") || !strings.Contains(err.Error(), "--withdraw") {
				t.Errorf("refusal = %v", err)
			}
			if len(f.be.claims) != 0 {
				t.Errorf("refused assign claims: %v", f.be.claims)
			}
		})
	}
}

func TestAssignRefusesLegacySupport(t *testing.T) {
	for _, label := range []string{"site:support", "remote"} {
		for _, bundle := range []bool{false, true} {
			t.Run(fmt.Sprint(label, bundle), func(t *testing.T) {
				f := newAssignFixture(t)
				f.env.Round.Support = "mac"
				if label == "remote" {
					f.env.Round.LegacySupportLabel = "remote"
				}
				id := "12"
				if bundle {
					id = "13"
				}
				f.be.items[id].Labels = []string{label}
				_, err := f.assign("12", "", func(o *AssignOpts) {
					if bundle {
						o.Bundle = []string{"13"}
					}
				})
				var refusal *BlockedError
				if !errors.As(err, &refusal) || refusal.By != BlockSite || !strings.Contains(err.Error(), "rota migrate labels --apply") {
					t.Fatalf("assign err = %v, want migration refusal", err)
				}
				if len(f.be.claims) != 0 {
					t.Errorf("refused assign claims: %v", f.be.claims)
				}
			})
		}
	}
}

// supportFixture is newAssignFixture on a support machine: settings loaded
// from config with round.role support, and an origin the base is fetched from.
func supportFixture(t *testing.T, cfg string) (*assignFixture, string) {
	t.Helper()
	f := newAssignFixture(t)
	origin := filepath.Join(gitTemp(t), "origin.git")
	sh(t, f.root, "clone", "-q", "--bare", f.root, origin)
	sh(t, f.root, "remote", "add", "origin", origin)
	sh(t, f.root, "fetch", "-q", "origin")
	// The resume merge commits as the user, as a worker's commits do.
	sh(t, f.root, "config", "user.email", "t@t")
	sh(t, f.root, "config", "user.name", "t")
	writeFile(t, filepath.Join(f.root, ".rota", "config.json"), cfg)
	writeFile(t, filepath.Join(f.root, ".rota", "config.local.json"), `{"round":{"role":"support"}}`)
	set, err := roundcfg.FromConfig(config.Load(rotatree.Config(f.root)))
	if err != nil {
		t.Fatal(err)
	}
	f.set = set
	f.env.Round = set
	if _, err := f.env.Start(bg, f.root, StartOpts{Scope: roundcfg.ScopeMilestone, HolderPID: 100, Settings: set, DefaultNum: 2, Dispatch: "tmux"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"12", "13"} {
		f.be.items[id].Labels = []string{"machine:mac"}
	}
	return f, origin
}

func gitTemp(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "rota-origin-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// pushToOrigin moves origin's main one commit ahead of the local main, as a
// merge on the main machine would.
func pushToOrigin(t *testing.T, origin, file, body string) string {
	t.Helper()
	clone := filepath.Join(gitTemp(t), "c")
	sh(t, filepath.Dir(clone), "clone", "-q", origin, clone)
	sh(t, clone, "config", "user.email", "t@t")
	sh(t, clone, "config", "user.name", "t")
	writeFile(t, filepath.Join(clone, file), body)
	sh(t, clone, "add", file)
	sh(t, clone, "commit", "-q", "-m", "landed on main elsewhere")
	sh(t, clone, "push", "-q", "origin", "HEAD:main")
	res, _ := git.Exec(bg, clone, "rev-parse", "HEAD")
	return strings.TrimSpace(res.Stdout)
}

// #1598 AC-4/AC-9: a support assign names the slot, branch and claim with the
// machine prefix and cuts the branch from a freshly fetched origin/main.
func TestSupportAssignPrefixesAndCutsFromFreshOrigin(t *testing.T) {
	f, origin := supportFixture(t, `{"issues":{"source":"github"},"round":{"support":"mac"}}`)
	landed := pushToOrigin(t, origin, "landed", "x")
	res, err := f.assign("12", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Agent != "mac-ben" || !strings.HasPrefix(res.Branch, "mac-ben/12-") || f.be.claims["12"] != "mac-ben@1" {
		t.Fatalf("agent %q branch %q claim %q", res.Agent, res.Branch, f.be.claims["12"])
	}
	wt := filepath.Join(f.root, ".worktrees", "mac-ben")
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("support worktree: %v", err)
	}
	if r, _ := git.Exec(bg, wt, "merge-base", "--is-ancestor", landed, "HEAD"); r.ExitCode != 0 {
		t.Error("the branch was cut from the stale local main, not origin/main")
	}
}

// #1598 AC-3: support assigns only labelled issues; main, with the split on,
// leaves them alone.
func TestSupportAssignRefusesTheOtherSite(t *testing.T) {
	f, _ := supportFixture(t, `{"issues":{"source":"github"},"round":{"support":"mac"}}`)
	f.be.items["13"].Labels = nil
	if _, err := f.assign("13", "", nil); blockedBy(t, err) != BlockSite {
		t.Errorf("support assign of an unlabelled issue: %v", err)
	}
	if len(f.be.claims) != 0 {
		t.Errorf("a refused assign claims nothing: %v", f.be.claims)
	}

	m := newAssignFixture(t)
	m.env.Round.Support = "mac"
	m.be.items["12"].Labels = []string{"machine:mac"}
	if _, err := m.assign("12", "", nil); blockedBy(t, err) != BlockSite || !strings.Contains(err.Error(), "--withdraw") {
		t.Errorf("main assign of a support issue: %v", err)
	}
}

// #1598 T4: a fetch that fails refuses before anything is marked, and never
// cuts from the stale local base.
func TestSupportAssignFetchFailureRefuses(t *testing.T) {
	f, _ := supportFixture(t, `{"issues":{"source":"github"},"round":{"support":"mac"}}`)
	sh(t, f.root, "remote", "set-url", "origin", filepath.Join(gitTemp(t), "gone.git"))
	if _, err := f.assign("12", "", nil); blockedBy(t, err) != BlockFetch {
		t.Fatalf("fetch failure: %v", err)
	}
	if len(f.be.claims) != 0 || len(f.host.spawned) != 0 {
		t.Errorf("nothing marked or sent: %v %v", f.be.claims, f.host.spawned)
	}
	if s := worker.LoadRegistryTolerant(f.root).Slot("mac-ben"); s.Branch() != "park/mac-ben" {
		t.Errorf("slot moved off its park branch: %s", s.Branch())
	}
}

// #1598 T4: a resumed support branch merges the fresh origin/main before the
// brief goes out; a conflict refuses and leaves the branch as it was.
func TestSupportResumeMergesOriginOrRefusesOnConflict(t *testing.T) {
	f, origin := supportFixture(t, `{"issues":{"source":"github"},"round":{"support":"mac"}}`)
	res, err := f.assign("12", "mac-ben", nil)
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(f.root, ".worktrees", "mac-ben")
	writeFile(t, filepath.Join(wt, "work"), "mine")
	sh(t, wt, "add", "work")
	sh(t, wt, "commit", "-q", "-m", "worker commit")

	landed := pushToOrigin(t, origin, "other", "theirs")
	if _, err := f.assign("12", "mac-ben", nil); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if r, _ := git.Exec(bg, wt, "merge-base", "--is-ancestor", landed, "HEAD"); r.ExitCode != 0 {
		t.Error("the resumed branch did not merge origin/main")
	}
	if r, _ := git.Exec(bg, wt, "log", "-1", "--format=%s", "HEAD^2"); r.ExitCode != 0 {
		t.Error("want a merge commit on the resumed branch")
	}

	before, _ := git.Exec(bg, wt, "rev-parse", "HEAD")
	pushToOrigin(t, origin, "work", "theirs")
	sent := len(f.host.sents)
	if _, err := f.assign("12", "mac-ben", nil); blockedBy(t, err) != BlockConflict {
		t.Fatalf("conflicting resume: %v", err)
	}
	after, _ := git.Exec(bg, wt, "rev-parse", "HEAD")
	if before.Stdout != after.Stdout {
		t.Error("a refused merge moved the branch")
	}
	if st, _ := git.Exec(bg, wt, "status", "--porcelain"); strings.TrimSpace(st.Stdout) != "" {
		t.Errorf("a refused merge left the worktree dirty:\n%s", st.Stdout)
	}
	if cur, _ := git.Exec(bg, wt, "symbolic-ref", "--short", "HEAD"); strings.TrimSpace(cur.Stdout) != res.Branch {
		t.Errorf("worktree on %q", cur.Stdout)
	}
	if len(f.host.sents) != sent {
		t.Error("a conflicting resume was dispatched")
	}
}

// #1598 slice B review: a dirty worktree refuses the resume before any merge
// runs, as dirty and never as a conflict, and leaves the branch and the change
// where they were.
func TestSupportResumeRefusesDirtyWorktree(t *testing.T) {
	f, origin := supportFixture(t, `{"issues":{"source":"github"},"round":{"support":"mac"}}`)
	if _, err := f.assign("12", "mac-ben", nil); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(f.root, ".worktrees", "mac-ben")
	writeFile(t, filepath.Join(wt, "work"), "mine")
	sh(t, wt, "add", "work")
	sh(t, wt, "commit", "-q", "-m", "worker commit")
	writeFile(t, filepath.Join(wt, "work"), "uncommitted")
	pushToOrigin(t, origin, "other", "theirs")
	before, _ := git.Exec(bg, wt, "rev-parse", "HEAD")
	sent := len(f.host.sents)
	if _, err := f.assign("12", "mac-ben", nil); blockedBy(t, err) != BlockDirty {
		t.Fatalf("dirty resume: %v", err)
	}
	after, _ := git.Exec(bg, wt, "rev-parse", "HEAD")
	if before.Stdout != after.Stdout {
		t.Error("a dirty resume merged origin/main")
	}
	if b, _ := os.ReadFile(filepath.Join(wt, "work")); string(b) != "uncommitted" {
		t.Errorf("the uncommitted change is gone: %q", b)
	}
	if len(f.host.sents) != sent {
		t.Error("a dirty resume was dispatched")
	}
}

// #1598 slice B review: every --bundle member must be on this machine's side
// of the split, not only the lead.
func TestSupportAssignRefusesOffSiteBundleMember(t *testing.T) {
	f, _ := supportFixture(t, `{"issues":{"source":"github"},"round":{"support":"mac"}}`)
	f.be.items["13"].Labels = nil
	_, err := f.assign("12", "", func(o *AssignOpts) { o.Bundle = []string{"13"} })
	if blockedBy(t, err) != BlockSite || !strings.Contains(err.Error(), "13") {
		t.Fatalf("support bundle with an unlabelled member: %v", err)
	}
	if len(f.be.claims) != 0 {
		t.Errorf("a refused assign claims nothing: %v", f.be.claims)
	}
}

// #1598 AC-2: main offers issues to the support side by label and withdraws
// them; an issue a main slot holds is not offered, and one a support claim
// holds is not withdrawn.
func TestOfferSupport(t *testing.T) {
	f := newAssignFixture(t)
	f.env.Forge = f.be.asForge()
	f.env.Round = f.set
	f.env.Round.Support = "mac"
	offered, err := f.env.OfferSupport(bg, f.root, f.be, []string{"12", "13"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(offered, []string{"12", "13"}) || !slices.Contains(f.be.labels[12], "machine:mac") || !slices.Contains(f.be.labels[13], "machine:mac") {
		t.Fatalf("offered %v, labels %v", offered, f.be.labels)
	}

	f.be.claims = map[string]string{"14": "ben@1"}
	if _, err := f.env.OfferSupport(bg, f.root, f.be, []string{"14"}, false); blockedBy(t, err) != BlockClaimed {
		t.Errorf("offer of an issue main holds: %v", err)
	}

	f.be.claims["12"] = "mac-ben@1"
	if _, err := f.env.OfferSupport(bg, f.root, f.be, []string{"13", "12"}, true); blockedBy(t, err) != BlockClaimed {
		t.Errorf("withdraw of an issue a support claim holds: %v", err)
	}
	if len(f.be.removed) != 0 {
		t.Errorf("a refused withdraw removes nothing: %v", f.be.removed)
	}
	if got, err := f.env.OfferSupport(bg, f.root, f.be, []string{"13"}, true); err != nil || !reflect.DeepEqual(got, []string{"13"}) || !reflect.DeepEqual(f.be.removed, []int{13}) {
		t.Errorf("withdraw: %v %v removed %v", got, err, f.be.removed)
	}

	f.env.Round.Support = ""
	if _, err := f.env.OfferSupport(bg, f.root, f.be, []string{"13"}, false); blockedBy(t, err) != BlockConfig {
		t.Errorf("offer with round.support unset: %v", err)
	}
}

// The overview's support projection: labelled issues are waiting until a claim
// from the support machine appears, and an unreadable claim never reads as
// waiting.
func TestSupportQueueGroupsWaitingActiveAndUnknown(t *testing.T) {
	be := &fakeRemote{}
	label := "machine:mac"
	for _, id := range []string{"1", "2", "3", "4"} {
		be.add(id, "work "+id, "", false, "")
	}
	for _, id := range []string{"1", "2", "3"} {
		be.items[id].Labels = []string{label}
	}
	be.claims = map[string]string{"2": "mac-ben", "3": "main-kit"}
	be.gone = map[string]bool{}
	e := Env{SupportLabel: label, Worker: wk(worker.Env{})}
	got, err := e.SupportQueue(bg, be, "mac")
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, s := range got {
		state := "waiting"
		switch {
		case s.Unknown:
			state = "unknown"
		case s.Active():
			state = "active " + s.Claim
		}
		lines = append(lines, s.ID+" "+state)
	}
	want := []string{"1 waiting", "2 active mac-ben", "3 waiting"}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("queue %v, want %v (a main claim is not support activity; unlabelled 4 is absent)", lines, want)
	}
	if got, _ := e.SupportQueue(bg, be, ""); got != nil {
		t.Errorf("round.support empty must list nothing: %v", got)
	}
	be.gone["1"] = true
	got, _ = e.SupportQueue(bg, be, "mac")
	if len(got) == 0 || !got[0].Unknown || got[0].Active() {
		t.Errorf("an unreadable claim must be unknown, not waiting: %+v", got)
	}
}

// A cancelled pass stops reading claims: the screen that asked for the queue
// is gone, so no further tracker call is made for it (#1873 AC-4).
func TestSupportQueueStopsWhenCancelled(t *testing.T) {
	label := "machine:mac"
	be := &countingStatus{fakeRemote: &fakeRemote{}}
	for _, id := range []string{"1", "2", "3"} {
		be.add(id, "work "+id, "", false, "")
		be.items[id].Labels = []string{label}
	}
	be.claims, be.gone = map[string]string{}, map[string]bool{}
	e := Env{SupportLabel: label}

	if _, err := e.SupportQueue(bg, be, "mac"); err != nil || be.statuses != 3 {
		t.Fatalf("a live pass reads one claim per labelled issue: err %v, %d reads", err, be.statuses)
	}
	be.statuses = 0
	ctx, cancel := context.WithCancel(bg)
	cancel()
	if _, err := e.SupportQueue(ctx, be, "mac"); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled pass must report the cancellation, got %v", err)
	}
	if be.statuses != 0 {
		t.Errorf("%d claim reads after cancellation", be.statuses)
	}
}

type countingStatus struct {
	*fakeRemote
	statuses int
}

func (c *countingStatus) Status(id string) (*backlog.Status, error) {
	c.statuses++
	return c.fakeRemote.Status(id)
}

// #1827 AC-1: the support machine hands an issue back with a reason comment
// and loses the label; main and a held issue are refused.
func TestReturnSupport(t *testing.T) {
	f := newAssignFixture(t)
	f.env.Forge = f.be.asForge()
	f.env.Round = f.set
	f.env.Round.Support = "mac"
	f.env.Round.Role = roundcfg.RoleSupport
	label := "machine:mac"
	f.be.items["12"].Labels = []string{label}
	f.be.items["13"].Labels = []string{label}

	f.env.Round.Role = roundcfg.RoleMain
	if _, err := f.env.ReturnSupport(bg, f.root, f.be, "12", "why"); blockedBy(t, err) != BlockRole {
		t.Errorf("return on main: %v", err)
	}
	f.env.Round.Role = roundcfg.RoleSupport

	if err := worker.RegisterExternal(f.root, "ext-1", "mac-ben/12-x", "", "main", "13", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.env.ReturnSupport(bg, f.root, f.be, "13", "why"); blockedBy(t, err) != BlockClaimed {
		t.Errorf("return of a held issue: %v", err)
	}
	if len(f.be.removed) != 0 || len(f.be.comments["13"]) != 0 {
		t.Errorf("a refused return writes nothing: %v %v", f.be.removed, f.be.comments)
	}
	f.env.Round.Support = ""
	if _, err := f.env.ReturnSupport(bg, f.root, f.be, "12", "why"); blockedBy(t, err) != BlockConfig {
		t.Errorf("return with round.support unset: %v", err)
	}
	f.env.Round.Support = "mac"

	got, err := f.env.ReturnSupport(bg, f.root, f.be, "12", "premise is wrong")
	if err != nil || got.ID != "12" || !got.Changed || got.Comment == "" {
		t.Fatalf("return: %+v %v", got, err)
	}
	cs := f.be.comments["12"]
	if len(cs) != 1 || !strings.HasPrefix(cs[0], SupportReturnMarker+"\n") || !strings.Contains(cs[0], "premise is wrong") || f.be.kinds["12"][0] != "feedback" {
		t.Errorf("comment %q kinds %v", cs, f.be.kinds["12"])
	}
	if slices.Contains(f.be.items["12"].Labels, label) || !reflect.DeepEqual(f.be.removed, []int{12}) {
		t.Errorf("label not removed: %v %v", f.be.items["12"].Labels, f.be.removed)
	}

	// An issue main already has back is a no-op.
	again, err := f.env.ReturnSupport(bg, f.root, f.be, "12", "again")
	if err != nil || again.Changed || len(f.be.comments["12"]) != 1 {
		t.Errorf("second return: %+v %v %q", again, err, f.be.comments["12"])
	}
}

// #1827 AC-4: an offer leaves a support-offer comment on each issue it newly
// labels, which the support side's gate reads as the assignment approval.
func TestOfferSupportPostsComment(t *testing.T) {
	f := newAssignFixture(t)
	f.env.Forge = f.be.asForge()
	f.env.Round = f.set
	f.env.Round.Support = "mac"
	if _, err := f.env.OfferSupport(bg, f.root, f.be, []string{"12", "13"}, false); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"12", "13"} {
		c := f.be.comments[id]
		if len(c) != 1 || !strings.Contains(c[0], "<!-- rota:support-offer -->") {
			t.Errorf("%s comments = %v", id, c)
		}
	}
	// Offering again changes nothing and posts nothing; a withdraw posts none.
	if _, err := f.env.OfferSupport(bg, f.root, f.be, []string{"12"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.env.OfferSupport(bg, f.root, f.be, []string{"13"}, true); err != nil {
		t.Fatal(err)
	}
	if n, m := len(f.be.comments["12"]), len(f.be.comments["13"]); n != 1 || m != 1 {
		t.Errorf("comments after re-offer and withdraw: %d %d", n, m)
	}
}

func TestOfferRefusesIssuePinnedElsewhere(t *testing.T) {
	for _, pin := range []string{"machine:main", "machine:other"} {
		t.Run(pin, func(t *testing.T) {
			f := newAssignFixture(t)
			f.env.Forge = f.be.asForge()
			f.env.Round = f.set
			f.env.Round.Support = "mac"
			f.be.items["13"].Labels = []string{pin}
			_, err := f.env.OfferSupport(bg, f.root, f.be, []string{"12", "13"}, false)
			var refusal *BlockedError
			if !errors.As(err, &refusal) || refusal.By != BlockSite {
				t.Fatalf("offer err = %v, want blockedBy site", err)
			}
			if !strings.Contains(err.Error(), pin) || !strings.Contains(err.Error(), "remove") {
				t.Errorf("refusal = %v", err)
			}
			if len(f.be.labels) != 0 || len(f.be.removed) != 0 || len(f.be.comments) != 0 {
				t.Errorf("refused offer wrote: %v %v %v", f.be.labels, f.be.removed, f.be.comments)
			}
		})
	}
}
