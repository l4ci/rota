package worker

import (
	"context"
	"errors"
	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/usagelimit"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/reviewpolicy"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
)

type fakeIssueGet struct {
	is  tracker.Issue
	err error
}

func (f fakeIssueGet) LabelMatcher() tracker.LabelMatcher { return tracker.LabelMatcher{} }

func (f fakeIssueGet) Get(context.Context, tracker.IssueKey, bool) (tracker.Issue, error) {
	return f.is, f.err
}

func depthRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gittest.Init(t, dir, "main")
	gittest.Run(t, dir, "commit", "-q", "--allow-empty", "-m", "base")
	gittest.Run(t, dir, "checkout", "-q", "-b", "feat")
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, dir, "add", "x.txt")
	gittest.Run(t, dir, "commit", "-q", "-m", "change")
	return dir
}

func TestResolveReviewDepthSignals(t *testing.T) {
	dir := depthRepo(t)
	pol, err := config.ParseReviewPolicy(nil)
	if err != nil {
		t.Fatal(err)
	}
	pol.LightBelow = 100
	src := ReviewSources{Policy: pol, Git: git.Exec, Dir: dir, Span: "main...feat"}

	got, err := ResolveReviewDepth(context.Background(), src)
	if err != nil || got.Depth != config.DepthLight || got.Signal != reviewpolicy.SignalSize {
		t.Fatalf("size: %+v %v", got, err)
	}

	src.PRBody = func() (string, error) { return "Door: one-way. Blast radius: schema.", nil }
	if got, err = ResolveReviewDepth(context.Background(), src); err != nil || got.Depth != config.DepthFull || got.Signal != reviewpolicy.SignalDoor {
		t.Fatalf("door: %+v %v", got, err)
	}

	src.PRBody = nil
	src.Issue = tracker.NumKey(5)
	src.Issues = fakeIssueGet{is: tracker.Issue{Body: "## Files\n\n- y.txt\n"}}
	if got, err = ResolveReviewDepth(context.Background(), src); err != nil || got.Signal != reviewpolicy.SignalScopeDrift || !strings.Contains(got.Why, "x.txt") {
		t.Fatalf("drift: %+v %v", got, err)
	}

	src.Issues = fakeIssueGet{err: errors.New("boom")}
	var de *ReviewDepthError
	if _, err = ResolveReviewDepth(context.Background(), src); !errors.As(err, &de) || de.Stage != "issue" {
		t.Fatalf("unreadable issue must fail, not guess: %v", err)
	}
}

// A file moved out of a protected directory still counts as touching it.
func TestResolveReviewDepthSeesBothSidesOfARename(t *testing.T) {
	dir := depthRepo(t)
	gittest.Run(t, dir, "checkout", "-q", "main")
	gittest.Run(t, dir, "checkout", "-q", "-b", "mv")
	os.MkdirAll(filepath.Join(dir, "migrations"), 0o755)
	os.WriteFile(filepath.Join(dir, "migrations", "001.sql"), []byte("create table t (id int);\n"), 0o644)
	gittest.Run(t, dir, "add", "migrations")
	gittest.Run(t, dir, "commit", "-q", "-m", "add")
	gittest.Run(t, dir, "checkout", "-q", "-b", "mv2")
	gittest.Run(t, dir, "mv", "migrations/001.sql", "moved.sql")
	gittest.Run(t, dir, "commit", "-q", "-m", "move")
	pol, err := config.ParseReviewPolicy(nil)
	if err != nil {
		t.Fatal(err)
	}
	pol.LightBelow = 1000
	pol.Paths = map[string]config.ReviewDepth{"migrations/**": config.DepthFull}
	got, err := ResolveReviewDepth(context.Background(), ReviewSources{Policy: pol, Git: git.Exec, Dir: dir, Span: "mv...mv2"})
	if err != nil || got.Signal != reviewpolicy.SignalPath {
		t.Fatalf("rename out of migrations/: %+v %v", got, err)
	}
}

// Git quotes a non-ASCII name in newline output; the -z list must match as is.
func TestResolveReviewDepthUnquotedPaths(t *testing.T) {
	dir := depthRepo(t)
	os.MkdirAll(filepath.Join(dir, "migrations"), 0o755)
	os.WriteFile(filepath.Join(dir, "migrations", "ä\"x.sql"), []byte("x\n"), 0o644)
	for _, args := range [][]string{{"add", "migrations"}, {"commit", "-q", "-m", "q"}} {
		gittest.Run(t, dir, args...)
	}
	pol, _ := config.ParseReviewPolicy(nil)
	pol.LightBelow = 1000
	pol.Paths = map[string]config.ReviewDepth{"migrations/**": config.DepthFull}
	got, err := ResolveReviewDepth(context.Background(), ReviewSources{Policy: pol, Git: git.Exec, Dir: dir, Span: "main...feat"})
	if err != nil || got.Signal != reviewpolicy.SignalPath || !strings.Contains(got.Why, "ä\"x.sql") {
		t.Fatalf("quoted name: %+v %v", got, err)
	}
}

func TestDoorNote(t *testing.T) {
	below := config.ReviewPolicy{Default: config.DepthLight}
	full := config.ReviewPolicy{Default: config.DepthFull}
	light := ReviewDepth{Decision: reviewpolicy.Decision{Depth: config.DepthLight}}
	labelNone := ReviewDepth{Decision: reviewpolicy.Decision{Depth: config.DepthNone, Signal: reviewpolicy.SignalLabel}}
	for name, c := range map[string]struct {
		p                config.ReviewPolicy
		res              ReviewDepth
		prFound, noForge bool
		want             string
	}{
		"no pr":       {below, light, false, false, "; no PR yet, so no Door line was read"},
		"no forge":    {below, light, false, true, "; no forge to read, so no Door line was read"},
		"pr read":     {below, light, true, false, ""},
		"full":        {below, ReviewDepth{Decision: reviewpolicy.Decision{Depth: config.DepthFull}}, false, false, ""},
		"label none":  {below, labelNone, false, false, ""},
		"never below": {full, light, false, false, ""},
	} {
		if got := DoorNote(c.p, c.res, c.prFound, c.noForge); got != c.want {
			t.Errorf("%s: DoorNote = %q, want %q", name, got, c.want)
		}
	}
}

// byKeyIssues serves a different issue per key.
type byKeyIssues map[tracker.IssueKey]tracker.Issue

func (m byKeyIssues) LabelMatcher() tracker.LabelMatcher { return tracker.LabelMatcher{} }

func (m byKeyIssues) Get(_ context.Context, k tracker.IssueKey, _ bool) (tracker.Issue, error) {
	is, ok := m[k]
	if !ok {
		return tracker.Issue{}, errors.New("no issue " + k.Ref())
	}
	return is, nil
}

// A bundle takes the highest depth any bundled issue requires: a `none` label
// on one issue must not win over another issue's default.
func TestReviewDepthBundleTakesHighest(t *testing.T) {
	dir := depthRepo(t)
	pol := config.ReviewPolicy{Default: config.DepthLight, Labels: map[string]config.ReviewDepth{"risk:high": config.DepthFull, "partial-slice": config.DepthNone}}
	lead, member := tracker.NumKey(5), tracker.NumKey(6)
	src := ReviewSources{Policy: pol, Git: git.Exec, Dir: dir, Span: "main...feat", Issue: lead, Members: []tracker.IssueKey{member}}

	src.Issues = byKeyIssues{lead: {Body: "## Files\n\n- x.txt\n"}, member: {Labels: []string{"risk:high"}, Body: "## Files\n\n- x.txt\n"}}
	got, err := ResolveReviewDepth(context.Background(), src)
	if err != nil || got.Depth != config.DepthFull || !slices.Contains(got.Labels, "risk:high") {
		t.Fatalf("member risk:high: %+v %v", got, err)
	}

	src.Issues = byKeyIssues{lead: {Labels: []string{"partial-slice"}, Body: "## Files\n\n- x.txt\n"}, member: {Body: "## Files\n\n- x.txt\n"}}
	if got, err = ResolveReviewDepth(context.Background(), src); err != nil || got.Depth != config.DepthLight {
		t.Fatalf("none lead, default member: %+v %v", got, err)
	}

	// a file named by any bundled issue is not scope drift
	src.Issues = byKeyIssues{lead: {Body: "## Files\n\n- y.txt\n"}, member: {Body: "## Files\n\n- x.txt\n"}}
	if got, err = ResolveReviewDepth(context.Background(), src); err != nil || got.Signal == reviewpolicy.SignalScopeDrift {
		t.Fatalf("member's file is drift: %+v %v", got, err)
	}

	src.Issues = byKeyIssues{lead: {}}
	var de *ReviewDepthError
	if _, err = ResolveReviewDepth(context.Background(), src); !errors.As(err, &de) || de.Stage != "issue" || !strings.Contains(de.Msg, member.Ref()) {
		t.Fatalf("unreadable member must fail naming it: %v", err)
	}
}

// The cross-harness requirement (#1612) resolves from the trigger labels, the
// review:<h> and review:none labels, the author's harness and round.harnesses.
func TestReviewDepthCross(t *testing.T) {
	on := roundcfg.CrossReview{Labels: []string{"size:Major", "risk:high"}, Harnesses: []string{"claude", "codex"}}
	codexDefault := roundcfg.CrossReview{Labels: on.Labels, Harnesses: []string{"codex", "claude"}, DefaultKind: "codex"}
	for _, c := range []struct {
		name   string
		cfg    roundcfg.CrossReview
		labels []string
		author string
		cross  string
		why    string
	}{
		{"major, claude author", on, []string{"size:Major"}, "claude", "codex", "label size:Major"},
		{"risk:high, codex author", on, []string{"risk:high"}, "codex", "claude", "other than the author's codex"},
		{"unknown author takes the default kind", on, []string{"size:Major"}, "", "codex", "author's claude"},
		{"harness label names the author", on, []string{"size:Major", "harness:codex"}, "", "claude", "author's codex"},
		{"project default names the author", codexDefault, []string{"size:Major"}, "", "claude", "author's codex"},
		{"label overrides project default", codexDefault, []string{"size:Major", "harness:claude"}, "", "codex", "author's claude"},
		{"slot overrides label and project", codexDefault, []string{"size:Major", "harness:codex"}, "claude", "codex", "author's claude"},
		{"review label picks", on, []string{"size:Major", "review:claude"}, "claude", "claude", "label review:claude"},
		{"review:none skips", on, []string{"size:Major", "review:none"}, "claude", "none", "label review:none"},
		{"review:none beats review:codex", on, []string{"size:Major", "review:codex", "review:none"}, "claude", "none", "label review:none"},
		{"no other harness listed", roundcfg.CrossReview{Labels: on.Labels, Harnesses: []string{"claude"}}, []string{"size:Major"}, "claude", "none", "no reviewer harness other than the author's claude"},
		{"opencode is no reviewer", roundcfg.CrossReview{Labels: on.Labels, Harnesses: []string{"claude", "opencode"}}, []string{"size:Major"}, "claude", "none", "no reviewer harness"},
		{"minor has no requirement", on, []string{"size:Minor"}, "claude", "", ""},
		{"review label alone does not trigger", on, []string{"review:codex"}, "claude", "none", "no label in review.crossLabels"},
		{"an unknown review label is ignored", on, []string{"review:later"}, "claude", "", ""},
		{"unset harnesses turns it off", roundcfg.CrossReview{Labels: on.Labels}, []string{"size:Major"}, "claude", "", ""},
		{"empty trigger list turns it off", roundcfg.CrossReview{Harnesses: on.Harnesses}, []string{"size:Major"}, "claude", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := depthRepo(t)
			pol, err := config.ParseReviewPolicy(nil)
			if err != nil {
				t.Fatal(err)
			}
			src := ReviewSources{Policy: pol, Git: git.Exec, Dir: dir, Span: "main...feat", Cross: c.cfg, Author: c.author,
				Issue: tracker.NumKey(5), Issues: fakeIssueGet{is: tracker.Issue{Labels: c.labels}}}
			got, err := ResolveReviewDepth(context.Background(), src)
			if err != nil {
				t.Fatal(err)
			}
			if got.Cross != c.cross || !strings.Contains(got.CrossWhy, c.why) {
				t.Errorf("cross = %q (%q), want %q (%q)", got.Cross, got.CrossWhy, c.cross, c.why)
			}
		})
	}
}

// In a bundle, a member that needs a cross review makes the branch need one.
func TestReviewDepthCrossBundle(t *testing.T) {
	dir := depthRepo(t)
	pol, _ := config.ParseReviewPolicy(nil)
	src := ReviewSources{Policy: pol, Git: git.Exec, Dir: dir, Span: "main...feat", Author: "claude",
		Cross: roundcfg.CrossReview{Labels: []string{"size:Major"}, Harnesses: []string{"claude", "codex"}},
		Issue: tracker.NumKey(5), Members: []tracker.IssueKey{tracker.NumKey(6)},
		Issues: issueMap{5: {Labels: []string{"size:Minor"}}, 6: {Labels: []string{"size:Major"}}}}
	got, err := ResolveReviewDepth(context.Background(), src)
	if err != nil || got.Cross != harness.Codex {
		t.Fatalf("cross = %q (%v)", got.Cross, err)
	}
}

type issueMap map[int]tracker.Issue

func (m issueMap) LabelMatcher() tracker.LabelMatcher { return tracker.LabelMatcher{} }

func (m issueMap) Get(_ context.Context, key tracker.IssueKey, _ bool) (tracker.Issue, error) {
	n, _ := strconv.Atoi(strings.TrimPrefix(key.Ref(), "#"))
	return m[n], nil
}

// review.lenses (#2064) triggers a lens by a changed path or an issue label,
// reports why, and triggers none at depth none.
func TestReviewDepthLenses(t *testing.T) {
	lenses := config.Lenses{
		"security": {Paths: []string{"*.txt"}, Labels: []string{"risk:security"}},
		"ux":       {Labels: []string{"ux"}},
		"perf":     {Paths: []string{"internal/hot/**"}},
	}
	for _, c := range []struct {
		name   string
		lenses config.Lenses
		labels []string
		policy string
		want   []config.LensHit
	}{
		{"path triggers", lenses, nil, "", []config.LensHit{{Name: "security", Why: "path x.txt"}}},
		{"label triggers beside a path", lenses, []string{"ux"}, "", []config.LensHit{{Name: "security", Why: "path x.txt"}, {Name: "ux", Why: "label ux"}}},
		{"no match lists none", config.Lenses{"perf": lenses["perf"]}, []string{"p1"}, "", nil},
		{"empty config lists none", nil, []string{"ux"}, "", nil},
		{"depth none lists none", lenses, []string{"ux"}, "none", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := depthRepo(t)
			pol, err := config.ParseReviewPolicy(nil)
			if err != nil {
				t.Fatal(err)
			}
			if c.policy != "" {
				pol.Default = config.DepthNone
			}
			src := ReviewSources{Policy: pol, Git: git.Exec, Dir: dir, Span: "main...feat", Lenses: c.lenses,
				Issue: tracker.NumKey(5), Issues: fakeIssueGet{is: tracker.Issue{Labels: c.labels}}}
			got, err := ResolveReviewDepth(context.Background(), src)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got.Lenses, c.want) {
				t.Errorf("lenses = %v, want %v", got.Lenses, c.want)
			}
		})
	}
}

// In a bundle a lens triggers when any member issue's labels match.
func TestReviewDepthLensesBundle(t *testing.T) {
	dir := depthRepo(t)
	pol, _ := config.ParseReviewPolicy(nil)
	src := ReviewSources{Policy: pol, Git: git.Exec, Dir: dir, Span: "main...feat",
		Lenses: config.Lenses{"security": {Labels: []string{"risk:security"}}},
		Issue:  tracker.NumKey(5), Members: []tracker.IssueKey{tracker.NumKey(6)},
		Issues: issueMap{5: {Labels: []string{"p1"}}, 6: {Labels: []string{"risk:security"}}}}
	got, err := ResolveReviewDepth(context.Background(), src)
	want := []config.LensHit{{Name: "security", Why: "label risk:security"}}
	if err != nil || !slices.Equal(got.Lenses, want) {
		t.Fatalf("lenses = %v (%v), want %v", got.Lenses, err, want)
	}
}

type fakePools struct{ p CrossPools }

func (f fakePools) read(context.Context) (CrossPools, error) { return f.p, nil }

func crossSources(t *testing.T, cfg roundcfg.CrossReview, labels []string, author string, pools func(context.Context) (CrossPools, error)) ReviewSources {
	t.Helper()
	pol, err := config.ParseReviewPolicy(nil)
	if err != nil {
		t.Fatal(err)
	}
	return ReviewSources{Policy: pol, Git: git.Exec, Dir: depthRepo(t), Span: "main...feat", Cross: cfg, Author: author, Pools: pools,
		Issue: tracker.NumKey(5), Issues: fakeIssueGet{is: tracker.Issue{Labels: labels}}}
}

// review.accounts (#3315): the reviewer is the review account with the most
// headroom of another harness, the depth output names it, and a review:<h>
// label still picks the harness while review:none still skips.
func TestReviewDepthCrossReviewAccounts(t *testing.T) {
	cfg := roundcfg.CrossReview{Labels: []string{"size:Major"}, Harnesses: []string{"claude", "codex"},
		Review: []config.Account{{Kind: "codex", Name: "r1", Dir: "/r1"}, {Kind: "codex", Name: "r2", Dir: "/r2"}},
		Main:   []config.Account{{Kind: "claude", Name: "w", Dir: "/w"}, {Kind: "codex", Name: "wc", Dir: "/wc"}}}
	pools := fakePools{CrossPools{
		Review: []PoolEntry{pe("codex", "r1", 30, usagelimit.VerdictFree), pe("codex", "r2", 70, usagelimit.VerdictFree)},
		Main:   []PoolEntry{pe("claude", "w", 90, usagelimit.VerdictFree), pe("codex", "wc", 50, usagelimit.VerdictFree)},
	}}.read
	major := []string{"size:Major"}

	got, err := ResolveReviewDepth(context.Background(), crossSources(t, cfg, major, "claude", pools))
	if err != nil {
		t.Fatal(err)
	}
	if got.Cross != harness.Codex || got.CrossAccount == nil || got.CrossAccount.Name != "r2" || got.CrossAccount.Dir != "/r2" || got.CrossAccount.Fallback {
		t.Fatalf("review account: cross %q account %+v (%s), want codex r2 /r2", got.Cross, got.CrossAccount, got.CrossWhy)
	}
	if !strings.Contains(got.CrossWhy, "review.accounts") {
		t.Errorf("why %q does not name review.accounts", got.CrossWhy)
	}

	got, _ = ResolveReviewDepth(context.Background(), crossSources(t, cfg, []string{"size:Major", "review:claude"}, "claude", pools))
	if got.Cross != harness.Claude || got.CrossAccount == nil || got.CrossAccount.Name != "w" || !got.CrossAccount.Fallback {
		t.Errorf("label review:claude: cross %q account %+v, want the claude round account as a fallback (no claude review account)", got.Cross, got.CrossAccount)
	}
	got, _ = ResolveReviewDepth(context.Background(), crossSources(t, cfg, []string{"size:Major", "review:none"}, "claude", pools))
	if got.Cross != CrossNone || got.CrossAccount != nil {
		t.Errorf("review:none: cross %q account %+v, want none", got.Cross, got.CrossAccount)
	}
}

func TestReviewDepthCrossFallsBackAndReportsUnavailable(t *testing.T) {
	cfg := roundcfg.CrossReview{Labels: []string{"size:Major"}, Harnesses: []string{"claude", "codex"},
		Review: []config.Account{{Kind: "codex", Name: "r1", Dir: "/r1"}}}
	major := []string{"size:Major"}
	cooling := PoolEntry{Kind: "codex", Name: "r1", Meter: usagelimit.Meter{Name: "r1", ConfigDir: "/r1", Verdict: usagelimit.VerdictCooling}}

	pools := fakePools{CrossPools{Review: []PoolEntry{cooling}, Main: []PoolEntry{pe("codex", "wc", 40, usagelimit.VerdictFree), pe("claude", "w", 99, usagelimit.VerdictFree)}}}.read
	got, _ := ResolveReviewDepth(context.Background(), crossSources(t, cfg, major, "claude", pools))
	if got.Cross != harness.Codex || got.CrossAccount == nil || got.CrossAccount.Name != "wc" || !got.CrossAccount.Fallback {
		t.Fatalf("fallback: cross %q account %+v, want codex wc as a fallback", got.Cross, got.CrossAccount)
	}
	if !strings.Contains(got.CrossWhy, "fell back") || !strings.Contains(got.CrossWhy, "r1") || !strings.Contains(got.CrossWhy, "cooling") {
		t.Errorf("why %q must say it fell back from r1 and why", got.CrossWhy)
	}

	none := fakePools{CrossPools{Review: []PoolEntry{cooling}, Main: []PoolEntry{pe("claude", "w", 99, usagelimit.VerdictFree)}}}.read
	got, _ = ResolveReviewDepth(context.Background(), crossSources(t, cfg, major, "claude", none))
	// /rota-review keys on this form ("; unavailable:"): pin it so the skill and the verb cannot drift.
	if !strings.Contains(got.CrossWhy, "; unavailable: no account of ") {
		t.Errorf("why %q lost the `; unavailable:` form the review skill keys on", got.CrossWhy)
	}
	if got.Cross != CrossNone || !strings.Contains(got.CrossWhy, "unavailable") {
		t.Errorf("unavailable: cross %q (%s), want none with an unavailable reason, never the author's harness", got.Cross, got.CrossWhy)
	}
}

// Without metered pools (the gate) the harness comes from configuration and
// any non-author review harness satisfies the requirement.
func TestReviewDepthCrossStaticViewAcceptsEveryEligibleHarness(t *testing.T) {
	cfg := roundcfg.CrossReview{Labels: []string{"size:Major"}, Harnesses: []string{"claude", "codex"},
		Review: []config.Account{{Kind: "codex", Name: "r1", Dir: "/r1"}}}
	got, err := ResolveReviewDepth(context.Background(), crossSources(t, cfg, []string{"size:Major"}, "claude", nil))
	if err != nil {
		t.Fatal(err)
	}
	if got.Cross != harness.Codex || !slices.Equal(got.CrossAccepts, []string{harness.Codex}) {
		t.Errorf("static: cross %q accepts %v, want codex only", got.Cross, got.CrossAccepts)
	}
	// review.accounts alone, with no round.harnesses, still asks for a review.
	only := roundcfg.CrossReview{Labels: cfg.Labels, Review: cfg.Review}
	got, _ = ResolveReviewDepth(context.Background(), crossSources(t, only, []string{"size:Major"}, "claude", nil))
	if got.Cross != harness.Codex {
		t.Errorf("review.accounts without round.harnesses: cross %q, want codex", got.Cross)
	}
}

func TestCrossSatisfiedBy(t *testing.T) {
	d := ReviewDepth{Cross: "codex"}
	if !d.CrossSatisfiedBy("codex") || d.CrossSatisfiedBy("claude") {
		t.Error("a label-fixed harness must match exactly")
	}
	d.CrossAccepts = []string{"codex", "claude"}
	if !d.CrossSatisfiedBy("claude") || d.CrossSatisfiedBy("opencode") {
		t.Error("an account-picked harness accepts every eligible one, no other")
	}
}

func TestStaticCrossPoolsLeaveOutReviewOnlyHarnessesFromTheFallback(t *testing.T) {
	c := roundcfg.CrossReview{Labels: []string{"size:Major"}, Harnesses: []string{"claude", "codex"},
		Review: []config.Account{{Kind: "codex", Name: "r1", Dir: "/r1"}},
		Main:   []config.Account{{Kind: "claude", Name: "w", Dir: "/w"}}}
	p := StaticCrossPools(c)
	if len(p.Review) != 1 || p.Review[0].Name != "r1" || len(p.Main) != 1 || p.Main[0].Name != "w" {
		t.Fatalf("pools %+v, want review r1 and main w only", p)
	}
}

// No issue source (or key) leaves the body unread: the depth says drift went
// unchecked, and an issue read with an empty body does not (#3555).
func TestResolveReviewDepthUnknownIssueBody(t *testing.T) {
	dir := depthRepo(t)
	pol, err := config.ParseReviewPolicy(nil)
	if err != nil {
		t.Fatal(err)
	}
	pol.LightBelow = 100
	src := ReviewSources{Policy: pol, Git: git.Exec, Dir: dir, Span: "main...feat"}

	got, err := ResolveReviewDepth(context.Background(), src)
	if err != nil || got.DriftUnchecked == "" || !strings.Contains(got.Why, "scope drift was not checked") {
		t.Fatalf("unread body: %+v %v", got, err)
	}

	src.Issue, src.Issues = tracker.NumKey(5), fakeIssueGet{is: tracker.Issue{Body: ""}}
	if got, err = ResolveReviewDepth(context.Background(), src); err != nil || got.DriftUnchecked != "" {
		t.Fatalf("empty body read: %+v %v", got, err)
	}
}

func TestReviewDepthTrackerLabelCase(t *testing.T) {
	for _, provider := range []string{"github", "linear", "itsaplan", "gitlab", "jira"} {
		t.Run(provider, func(t *testing.T) {
			src := ReviewSources{Policy: config.ReviewPolicy{Default: config.DepthLight, Labels: map[string]config.ReviewDepth{"risk:high": config.DepthFull}},
				Git: git.Exec, Dir: depthRepo(t), Span: "main...feat", Issue: tracker.NumKey(5),
				Issues: caseReviewIssues{fakeIssueGet: fakeIssueGet{is: tracker.Issue{Labels: []string{"Risk:High", "Area:Security", "Review:Codex"}}}, provider: provider},
				Lenses: config.Lenses{"security": {Labels: []string{"area:security"}}},
				Cross:  roundcfg.CrossReview{Labels: []string{"risk:high"}, Harnesses: []string{"claude", "codex"}}, Author: "claude"}
			got, err := ResolveReviewDepth(context.Background(), src)
			if err != nil {
				t.Fatal(err)
			}
			fold := provider != "gitlab" && provider != "jira"
			if (got.Depth == config.DepthFull) != fold || (len(got.Lenses) == 1) != fold || (got.Cross == "codex") != fold {
				t.Fatalf("review = %+v, want tracker case folding=%v", got, fold)
			}
		})
	}
}

type caseReviewIssues struct {
	fakeIssueGet
	provider string
}

func (f caseReviewIssues) LabelMatcher() tracker.LabelMatcher {
	return (tracker.IssueActor{Source: f.provider}).LabelMatcher()
}
