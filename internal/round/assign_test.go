package round

import (
	"context"
	"errors"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/git"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// hostFake is a tmux host that accepts everything.
type hostFake struct {
	spawned []string
	launch  string
	sent    string
	sents   []string // every brief sent, in order
	// name is the host's name; "" is tmux.
	name                 string
	codexHome, configDir string
	swept                []string // worktrees whose shell panes were swept
}

func (h *hostFake) Name() string {
	if h.name != "" {
		return h.name
	}
	return "tmux"
}
func (h *hostFake) Require() error  { return nil }
func (h *hostFake) InSession() bool { return true }
func (h *hostFake) Where() string   { return "main" }
func (h *hostFake) Spawn(_ context.Context, o host.SpawnOpts) (string, error) {
	h.spawned = append(h.spawned, o.Slot+" "+o.Cwd)
	h.launch = o.Launch
	h.codexHome, h.configDir = o.CodexHome, o.ConfigDir
	return "w1:t1", nil
}
func (h *hostFake) Send(_ context.Context, slot, handle, file string) error {
	b, _ := os.ReadFile(file)
	h.sent = string(b)
	h.sents = append(h.sents, h.sent)
	return nil
}
func (h *hostFake) Capture(context.Context, string, string, int) string { return "" }
func (h *hostFake) Status(context.Context, string, string) string       { return "" }
func (h *hostFake) Kill(context.Context, string, string) error          { return nil }
func (h *hostFake) Notify(context.Context, string, string)              {}

type assignFixture struct {
	root string
	env  Env
	be   *fakeRemote
	host *hostFake
	set  roundcfg.Settings
}

func newAssignFixture(t *testing.T) *assignFixture {
	t.Helper()
	root := newRepo(t, nil)
	milestoneDoc(t, root, "M01", "active")
	if err := os.MkdirAll(filepath.Join(root, "skills", "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills", "references", "worker-contract.md"), []byte("contract"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &hostFake{}
	f := &assignFixture{root: root, host: h}
	f.env = Env{Git: git.Exec, Base: "main", Worker: worker.Env{Getenv: noEnv}, Lease: fakeLease("h", 100)}
	f.env.Worker = worker.Env{Git: git.Exec, NewHost: func(string) host.Host { return h },
		Sleep: func(time.Duration) {}, Now: time.Now, Getenv: noEnv}
	fb := &fakeRemote{}
	fb.add("12", "Add the round assign verb now please", "M01", false, "## Acceptance\n- [ ] works\n\nedits internal/cli/round.go")
	fb.add("13", "Second issue", "M01", false, "## Acceptance\n- [ ] ok\n\nalso edits internal/cli/round.go")
	fb.add("14", "No criteria", "M01", false, "")
	fb.ready = map[string][]string{"14": {"no acceptance criteria in the issue body", "no design or plan note"}}
	f.be = fb
	f.set, _ = roundcfg.Load(os.TempDir())
	if _, err := f.env.Start(bg, root, startOpts(roundcfg.ScopeMilestone, 100)); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *assignFixture) assign(id, agent string, mod func(*AssignOpts)) (Assigned, error) {
	o := AssignOpts{ID: id, Agent: agent, HolderPID: 100, Settings: f.set}
	if mod != nil {
		mod(&o)
	}
	return f.env.Assign(bg, f.root, f.be, o)
}

func blockedBy(t *testing.T, err error) string {
	t.Helper()
	var b *BlockedError
	if !errors.As(err, &b) {
		t.Fatalf("want a BlockedError, got %v", err)
	}
	return b.By
}

func TestBranchName(t *testing.T) {
	for _, c := range []struct{ agent, id, title, want string }{
		{"ben", "59", "C3: rota round start / assign / wind-down with autonomy levels", "ben/59-c3-rota-round-start-assign"},
		{"dana", "B07", "Fix it!", "dana/b07-fix-it"},
		{"kit", "1", "", "kit/1"},
		{"kit", "2", "Supercalifragilisticexpialidocious-averyveryverylongwordhere", "kit/2-supercalifragilisticexpialidocious-avery"},
	} {
		if got := BranchName(c.agent, c.id, c.title); got != c.want {
			t.Errorf("%q: %q want %q", c.title, got, c.want)
		}
	}
}

func TestAssignMarksResetsAndDispatches(t *testing.T) {
	f := newAssignFixture(t)
	res, err := f.assign("12", "", func(o *AssignOpts) {
		o.Siblings = []string{"13"}
		bf := filepath.Join(t.TempDir(), "d.md")
		os.WriteFile(bf, []byte("m: use the lease"), 0o644)
		o.BodyFile = bf
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Agent != "ben" || res.Branch != "ben/12-add-the-round-assign-verb" || !res.Dispatched || !res.Ready() {
		t.Fatalf("%+v", res)
	}
	if f.be.claims["12"] != "ben@1" || f.be.bstates["12"] != "in-progress" || len(f.be.notes) != 1 || !strings.Contains(f.be.notes[0], "ben") {
		t.Errorf("claim, state and comment: %+v %+v %v", f.be.claims, f.be.bstates, f.be.notes)
	}
	s := worker.LoadRegistry(f.root).Slot("ben")
	if s.Task() != "12" || s.ClaimID() != "ben@1" || s.Branch() != res.Branch || s.State() != "busy" {
		t.Errorf("slot: %v", s)
	}
	gres, _ := git.Exec(bg, filepath.Join(f.root, ".worktrees", "ben"), "symbolic-ref", "--short", "HEAD")
	out := gres.Stdout
	if strings.TrimSpace(out) != res.Branch {
		t.Errorf("worktree must sit on the issue branch, not %q", out)
	}
	for _, want := range []string{"--- ORCHESTRATOR (round 1) ---", "You are ben", "contract", "issue 12", "Dispute the ticket", "ben/12-", "Sibling issues running now: 13", "m: use the lease"} {
		if !strings.Contains(f.host.sent, want) {
			t.Errorf("brief lacks %q:\n%s", want, f.host.sent)
		}
	}
}

func TestAssignRefusals(t *testing.T) {
	f := newAssignFixture(t)

	_, err := f.assign("14", "ben", nil)
	if by := blockedBy(t, err); by != BlockNotReady || !strings.Contains(err.Error(), "criteria") {
		t.Errorf("no criteria: %v", err)
	}
	if len(f.be.claims) != 0 || len(f.be.bstates) != 0 || len(f.host.spawned) != 0 {
		t.Fatalf("a refused assign must touch nothing: %+v %+v %v", f.be.claims, f.be.bstates, f.host.spawned)
	}

	if _, err := f.assign("12", "ben", func(o *AssignOpts) { o.Settings.Roster = []string{"zed"} }); err == nil {
		t.Error("an agent outside the roster must be refused")
	}
	var we *exitcode.Error
	if _, err := f.assign("12", "zed", nil); !errors.As(err, &we) || we.Exit != exitcode.ExitUsage {
		t.Errorf("--agent outside the roster is usage: %v", err)
	}

	if _, err := f.assign("12", "ben", func(o *AssignOpts) { o.HolderPID = 999 }); blockedBy(t, err) != BlockNoRound {
		t.Errorf("a process without the lease: %v", err)
	}

	f.be.add("20", "Elsewhere", "M09", false, "## Acceptance\n- [ ] x")
	if _, err := f.assign("20", "ben", nil); blockedBy(t, err) != BlockOutOfScope {
		t.Errorf("outside the milestone: %v", err)
	}

	f.be.claimedBy = "dana@1"
	if _, err := f.assign("12", "ben", nil); blockedBy(t, err) != BlockClaimed || !strings.Contains(err.Error(), "dana@1") {
		t.Errorf("claimed elsewhere: %v", err)
	}
	f.be.claimedBy = ""

	noBrief := func(o *AssignOpts) { o.Settings.Brief = filepath.Join(f.root, "nope.md") }
	if _, err := f.assign("12", "ben", noBrief); blockedBy(t, err) != BlockBriefMissing {
		t.Errorf("missing brief: %v", err)
	}
	if len(f.be.claims) != 0 || len(f.be.bstates) != 0 {
		t.Fatalf("brief check comes before any marking: %+v", f.be.claims)
	}
}

func TestAssignOverlapAndAcceptOverlap(t *testing.T) {
	f := newAssignFixture(t)
	// A tracked file both issues name.
	os.MkdirAll(filepath.Join(f.root, "internal", "cli"), 0o755)
	os.WriteFile(filepath.Join(f.root, "internal", "cli", "round.go"), []byte("x"), 0o644)
	sh(t, f.root, "add", "internal/cli/round.go")
	sh(t, f.root, "commit", "-q", "-m", "round.go")
	// ben re-provisioned off the new main so both slots see it.
	if _, err := f.assign("12", "ben", nil); err != nil {
		t.Fatal(err)
	}
	_, err := f.assign("13", "dana", nil)
	if by := blockedBy(t, err); by != BlockOverlap {
		t.Fatalf("13 overlaps 12: %v", err)
	}
	var b *BlockedError
	errors.As(err, &b)
	if len(b.Readiness.Overlaps) != 1 || b.Readiness.Overlaps[0].Slot != "ben" {
		t.Errorf("overlap must name the holder: %+v", b.Readiness.Overlaps)
	}
	res, err := f.assign("13", "dana", func(o *AssignOpts) { o.AcceptOverlap = true })
	if err != nil || !res.Dispatched || len(res.Overlaps) != 1 {
		t.Fatalf("accepted overlap still assigns and stays listed: %v %+v", err, res)
	}
}

// round.scopeOverlap reaches Assign: a scope-only clash warns by default and
// fails like a path clash under block; --accept-overlap skips it.
func TestAssignScopeOverlapBlock(t *testing.T) {
	f := newAssignFixture(t)
	f.be.add("30", "Holder", "M01", false, "## Acceptance\n- [ ] x\n\n## Touches\n- POST /items\n")
	f.be.add("31", "Candidate", "M01", false, "## Acceptance\n- [ ] x\n\n## Touches\n- post /items\n")
	if _, err := f.assign("30", "ben", nil); err != nil {
		t.Fatal(err)
	}
	res, err := f.assign("31", "dana", func(o *AssignOpts) { o.CheckOnly = true })
	if err != nil || !res.Ready() || len(res.Overlaps) != 1 {
		t.Fatalf("warn lists the clash and stays ready: %v %+v", err, res)
	}
	block := func(o *AssignOpts) { o.Settings.ScopeOverlap = "block" }
	_, err = f.assign("31", "dana", block)
	if by := blockedBy(t, err); by != BlockOverlap {
		t.Fatalf("block mode refuses a scope-only clash: %v", err)
	}
	var b *BlockedError
	errors.As(err, &b)
	if len(b.Readiness.Overlaps) != 1 || len(b.Readiness.Overlaps[0].Scopes) != 1 || len(b.Readiness.Overlaps[0].Paths) != 0 {
		t.Errorf("scope-only overlap: %+v", b.Readiness.Overlaps)
	}
	res, err = f.assign("31", "dana", func(o *AssignOpts) { block(o); o.AcceptOverlap = true })
	if err != nil || !res.Dispatched {
		t.Fatalf("--accept-overlap skips the scope clash: %v %+v", err, res)
	}
}

func TestAssignSlotBusyAndFreeSlot(t *testing.T) {
	f := newAssignFixture(t)
	if _, err := f.assign("12", "ben", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.assign("13", "ben", nil); blockedBy(t, err) != BlockSlotBusy {
		t.Errorf("ben holds 12: %v", err)
	}
	// No --agent: the next idle roster slot, in roster order.
	res, err := f.assign("13", "", func(o *AssignOpts) { o.AcceptOverlap = true })
	if err != nil || res.Agent != "dana" {
		t.Fatalf("first idle slot is dana: %v %+v", err, res)
	}
	f.be.add("15", "Third", "M01", false, "## Acceptance\n- [ ] x")
	if _, err := f.assign("15", "", nil); blockedBy(t, err) != BlockNoFreeSlot {
		t.Errorf("both slots busy: %v", err)
	}
}

func TestAssignCheckOnlyWritesNothing(t *testing.T) {
	f := newAssignFixture(t)
	res, err := f.assign("12", "", func(o *AssignOpts) { o.CheckOnly = true })
	if err != nil || !res.Ready() || res.Dispatched {
		t.Fatalf("%v %+v", err, res)
	}
	res, err = f.assign("14", "", func(o *AssignOpts) { o.CheckOnly = true })
	if err != nil || res.Ready() {
		t.Fatalf("not ready is an answer, not an error: %v %+v", err, res)
	}
	if len(f.be.claims) != 0 || len(f.be.bstates) != 0 || len(f.be.notes) != 0 || f.host.sent != "" {
		t.Fatal("--check-only must write nothing")
	}
}

func TestAssignResumesWithoutDuplicatingTheComment(t *testing.T) {
	f := newAssignFixture(t)
	if _, err := f.assign("12", "ben", nil); err != nil {
		t.Fatal(err)
	}
	res, err := f.assign("12", "", nil) // no --agent: finds the slot already holding it
	if err != nil || res.Agent != "ben" || !res.Dispatched {
		t.Fatalf("resume: %v %+v", err, res)
	}
	if len(f.be.notes) != 1 {
		t.Errorf("a resumed assign must not comment twice: %v", f.be.notes)
	}
}

func TestAssignDispatchFailureKeepsTheMarks(t *testing.T) {
	f := newAssignFixture(t)
	f.env.Worker.NewHost = func(string) host.Host { return &failingHost{hostFake: f.host} }
	_, err := f.assign("12", "ben", nil)
	if err == nil {
		t.Fatal("dispatch failure must surface")
	}
	if f.be.claims["12"] == "" || f.be.bstates["12"] != "in-progress" {
		t.Errorf("a failure at dispatch keeps claim and state: %+v %+v", f.be.claims, f.be.bstates)
	}
}

// A failed dispatch leaves the slot idle on both paths (transfer's twin asserts
// the same), so the repeated call knows nothing was delivered.
func TestAssignDispatchFailureMarksTheSlotIdle(t *testing.T) {
	f := newAssignFixture(t)
	f.env.Worker.NewHost = func(string) host.Host { return &failingHost{hostFake: f.host} }
	if _, err := f.assign("12", "ben", nil); err == nil {
		t.Fatal("dispatch failure must surface")
	}
	if st := worker.LoadRegistry(f.root).Slot("ben").State(); st != "idle" {
		t.Errorf("slot state after a failed dispatch: %q", st)
	}
}

func TestAssignUnreadableBodyFileIsRefused(t *testing.T) {
	f := newAssignFixture(t)
	_, err := f.assign("12", "ben", func(o *AssignOpts) { o.BodyFile = filepath.Join(f.root, "missing.md") })
	var we *exitcode.Error
	if !errors.As(err, &we) || we.Exit != exitcode.ExitUsage {
		t.Fatalf("want a usage error, got %v", err)
	}
	if len(f.be.claims) != 0 {
		t.Errorf("nothing is claimed: %v", f.be.claims)
	}
}

type failingHost struct{ *hostFake }

func (f *failingHost) Send(context.Context, string, string, string) error {
	return errors.New("pane refused input")
}

func TestAssignUndoesWhenNothingWasSent(t *testing.T) {
	f := newAssignFixture(t)
	// Dirty worktree: the reset guard refuses before any dispatch.
	wt := filepath.Join(f.root, ".worktrees", "ben")
	os.WriteFile(filepath.Join(wt, "scratch.txt"), []byte("wip"), 0o644)
	_, err := f.assign("12", "ben", nil)
	if blockedBy(t, err) != BlockSlotBusy {
		t.Fatalf("a slot holding work is busy: %v", err)
	}
	if len(f.be.claims) != 0 || len(f.be.bstates) != 0 {
		t.Errorf("a failure before dispatch undoes claim and state: %+v %+v", f.be.claims, f.be.bstates)
	}
	if s := worker.LoadRegistry(f.root).Slot("ben"); s.Task() != "" || s.ClaimID() != "" {
		t.Errorf("and the slot: %v", s)
	}
}

func (f *assignFixture) config(t *testing.T, cfg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.root, ".rota", "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := roundcfg.Load(f.root)
	if err != nil {
		t.Fatal(err)
	}
	f.set = set
	// The round recorded its host at start, before this config; the registry
	// wins, so a config that names a host re-records it, as a new round would.
	if dv, _ := config.Lookup(config.Load(filepath.Join(f.root, ".rota", "config.json")), "work.dispatch"); dv == "herdr" || dv == "tmux" {
		d := dv.(string)
		if err := worker.Update(f.root, func(doc *worker.Doc) { doc.SetHost(d) }); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAssignDefaultTierStartsTheWorkerOnItsModel(t *testing.T) {
	f := newAssignFixture(t)
	res, err := f.assign("12", "ben", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != "claude" || res.Tier != "standard" || res.Model != "sonnet" || res.TierReason != "" {
		t.Fatalf("%+v", res)
	}
	if !strings.Contains(f.host.launch, "--model sonnet") {
		t.Errorf("the worker must launch on the standard model: %q", f.host.launch)
	}
	s := worker.LoadRegistry(f.root).Slot("ben")
	if s.Tier() != "standard" || s.Model() != "sonnet" || s.Kind() != "claude" {
		t.Errorf("slot fields: %v", s)
	}
	for _, want := range []string{"Your tier is standard (sonnet).", "claude): light = haiku, standard = sonnet, heavy = opus", "Worker tier: standard (sonnet)"} {
		if !strings.Contains(f.host.sent, want) {
			t.Errorf("brief lacks %q:\n%s", want, f.host.sent)
		}
	}
	rep, _ := f.env.Status(bg, f.root)
	for _, r := range rep.Rows {
		if r.Name == "ben" && (r.Tier != "standard" || r.Model != "sonnet" || r.Kind != "claude") {
			t.Errorf("round status must show the tier: %+v", r)
		}
	}
}

func TestAssignStandardTierFollowsModelsWorker(t *testing.T) {
	f := newAssignFixture(t)
	f.config(t, `{"models":{"worker":"haiku-ish"}}`)
	res, err := f.assign("12", "ben", nil)
	if err != nil || res.Model != "haiku-ish" || !strings.Contains(f.host.launch, "--model haiku-ish") {
		t.Fatalf("one knob governs the standard model: %v %+v %q", err, res, f.host.launch)
	}
	g := newAssignFixture(t)
	g.config(t, `{"models":{"worker":"haiku-ish"},"round":{"tiers":{"claude":{"standard":"explicit"}}}}`)
	if res, err := g.assign("12", "ben", nil); err != nil || res.Model != "explicit" {
		t.Fatalf("an explicit tier value wins: %v %+v", err, res)
	}
}

func TestAssignAboveDefaultNeedsAReasonAndRecordsIt(t *testing.T) {
	f := newAssignFixture(t)
	var we *exitcode.Error
	if _, err := f.assign("12", "ben", func(o *AssignOpts) { o.Tier = "heavy" }); !errors.As(err, &we) || we.Exit != exitcode.ExitUsage {
		t.Fatalf("heavy above standard needs a reason: %v", err)
	}
	if len(f.be.claims) != 0 {
		t.Fatal("a usage error must mark nothing")
	}
	res, err := f.assign("12", "ben", func(o *AssignOpts) { o.Tier = "heavy"; o.TierReason = "touches the lease protocol" })
	if err != nil || res.Tier != "heavy" || res.Model != "opus" || res.TierReason != "touches the lease protocol" {
		t.Fatalf("%v %+v", err, res)
	}
	s := worker.LoadRegistry(f.root).Slot("ben")
	if s.TierReason() != "touches the lease protocol" || !strings.Contains(f.host.launch, "--model opus") {
		t.Errorf("reason recorded and model applied: %v %q", s, f.host.launch)
	}
	if !strings.Contains(f.host.sent, "above the default standard: touches the lease protocol") || !strings.Contains(f.host.sent, "Worker tier: heavy (opus)") {
		t.Errorf("brief:\n%s", f.host.sent)
	}
}

func TestAssignBelowDefaultNeedsNoReason(t *testing.T) {
	f := newAssignFixture(t)
	if res, err := f.assign("12", "ben", func(o *AssignOpts) { o.Tier = "light" }); err != nil || res.Model != "haiku" {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestAssignRejectsUnknownTierAndKind(t *testing.T) {
	f := newAssignFixture(t)
	var we *exitcode.Error
	for name, mod := range map[string]func(*AssignOpts){
		"tier": func(o *AssignOpts) { o.Tier = "ultra" },
		"kind": func(o *AssignOpts) { o.Kind = "gemini" },
	} {
		if _, err := f.assign("12", "ben", mod); !errors.As(err, &we) || we.Exit != exitcode.ExitUsage {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// codexRig stands in for codex and herdr: every call is logged, the version
// and login are scripted, and nothing real is reachable.
type codexRig struct {
	calls    []string
	version  string // `codex --version` stdout
	loggedIn bool
	noCodex  bool
	noFlag   string // a launch flag `codex --help` leaves out
}

// codexHelp is the `codex --help` the rig prints: every default launch flag.
const codexHelp = `Usage: codex [OPTIONS]

Options:
  -m, --model <MODEL>
      --dangerously-bypass-approvals-and-sandbox
      --dangerously-bypass-hook-trust
      --no-daemon
      --no-alt-screen
`

func (c *codexRig) install(f *assignFixture) {
	f.env.Worker.LookPath = func(n string) (string, error) {
		if n == "codex" && c.noCodex {
			return "", errors.New("not found")
		}
		return "/fake/" + n, nil
	}
	f.env.Worker.Run = func(_ context.Context, name string, args, env []string) (host.Result, error) {
		c.calls = append(c.calls, strings.TrimPrefix(name, "/fake/")+" "+strings.Join(args, " ")+" | "+strings.Join(env, " "))
		switch strings.TrimPrefix(name, "/fake/") + " " + strings.Join(args, " ") {
		case "codex --version":
			return host.Result{Stdout: c.version}, nil
		case "codex --help":
			return host.Result{Stdout: strings.ReplaceAll(codexHelp, c.noFlag+"\n", "\n")}, nil
		case "codex login status":
			if c.loggedIn {
				return host.Result{}, nil
			}
			return host.Result{Stderr: "Not logged in", ExitCode: 1}, nil
		case "herdr integration status":
			return host.Result{Stdout: "codex: not installed (/x)\n"}, nil
		}
		return host.Result{}, nil
	}
}

const codexCfg = `{"work":{"dispatch":"herdr"},"round":{"tiers":{"codex":{"light":"c-l","standard":"c-s","heavy":"c-h"}}}}`

func TestAssignCodexResolvesAndStarts(t *testing.T) {
	f := newAssignFixture(t)
	codex := func(o *AssignOpts) { o.Kind = "codex" }
	f.config(t, codexCfg)
	rig := &codexRig{version: "codex-cli 0.159.2\n", loggedIn: true}
	rig.install(f)
	f.host.name = "herdr"
	res, err := f.assign("12", "ben", func(o *AssignOpts) { o.Kind = "codex"; o.CheckOnly = true })
	if err != nil || res.Kind != "codex" || res.Model != "c-s" || len(rig.calls) != 0 {
		t.Fatalf("check-only resolves the codex model and runs nothing: %v %+v %v", err, res, rig.calls)
	}
	res, err = f.assign("12", "ben", codex)
	if err != nil || !res.Dispatched || res.Kind != "codex" || res.Model != "c-s" {
		t.Fatalf("%v %+v", err, res)
	}
	if !strings.HasPrefix(f.host.launch, "codex -c 'projects.") || !strings.Contains(f.host.launch, "check_for_update_on_startup=false -c features.hooks=true ") || !strings.Contains(f.host.launch, " --model c-s ") || strings.Contains(f.host.launch, "{model}") {
		t.Errorf("launch = %q", f.host.launch)
	}
	if f.host.codexHome != "" || f.host.configDir != "" {
		t.Errorf("the default home sets no CODEX_HOME: home %q configDir %q", f.host.codexHome, f.host.configDir)
	}
	if s := worker.LoadRegistry(f.root).Slot("ben"); s.Kind() != "codex" {
		t.Errorf("the kind is recorded: %v", s)
	}
	// A slot's recorded kind is the default.
	g := newAssignFixture(t)
	g.config(t, codexCfg)
	gr := &codexRig{version: "codex-cli 0.159.2\n"} // not logged in
	gr.install(g)
	worker.UpdateSlot(g.root, "ben", func(s *worker.Slot) { s.Raw().Set("kind", "codex") })
	var we *exitcode.Error
	if _, err := g.assign("12", "ben", nil); !errors.As(err, &we) || we.Exit != exitcode.ExitUnavailable || !strings.Contains(we.Hint, "codex login") {
		t.Fatalf("the recorded kind is the default, and an unlogged slot is exit 5: %v", err)
	}
}

// round.workerKind is the project default: it beats a slot's stale kind,
// loses to --kind, and is what autopilot (which passes no kind) gets.
func TestAssignProjectWorkerKind(t *testing.T) {
	f := newAssignFixture(t)
	f.config(t, `{"work":{"dispatch":"herdr"},"round":{"workerKind":"codex","tiers":{"codex":{"light":"c-l","standard":"c-s","heavy":"c-h"}}}}`)
	worker.UpdateSlot(f.root, "ben", func(s *worker.Slot) { s.Raw().Set("kind", "claude") })
	res, err := f.assign("12", "ben", func(o *AssignOpts) { o.CheckOnly = true })
	if err != nil || res.Kind != "codex" || res.KindSource != KindFromConfig || res.Model != "c-s" {
		t.Fatalf("the project default beats the slot's stale kind: %v %+v", err, res)
	}
	res, err = f.assign("12", "ben", func(o *AssignOpts) { o.CheckOnly = true; o.Kind = "claude" })
	if err != nil || res.Kind != "claude" || res.KindSource != KindFromFlag {
		t.Fatalf("--kind beats the project default: %v %+v", err, res)
	}
}

// An unset codex tier map is no model, not a refusal (#68): the default
// command drops --model and Codex picks its own. A custom work.codexCommand
// holding {model} has nothing to fill in, so it is refused before the claim.
func TestAssignCodexWithoutTierMap(t *testing.T) {
	f := newAssignFixture(t)
	f.config(t, `{"work":{"dispatch":"herdr"}}`)
	rig := &codexRig{version: "codex-cli 0.159.2\n", loggedIn: true}
	rig.install(f)
	f.host.name = "herdr"
	res, err := f.assign("12", "ben", func(o *AssignOpts) { o.Kind = "codex" })
	if err != nil || !res.Dispatched || res.Model != "" {
		t.Fatalf("an unset codex map dispatches with no model: %v %+v", err, res)
	}
	if !strings.HasPrefix(f.host.launch, "codex -c 'projects.") || !strings.Contains(f.host.launch, "hooks.UserPromptSubmit=") || !strings.Contains(f.host.launch, " --dangerously-bypass-approvals-and-sandbox ") || strings.Contains(f.host.launch, "--model") {
		t.Errorf("launch = %q", f.host.launch)
	}

	g := newAssignFixture(t)
	g.config(t, `{"work":{"dispatch":"herdr","codexCommand":"codex -m {model}"}}`)
	gr := &codexRig{version: "codex-cli 0.159.2\n", loggedIn: true}
	gr.install(g)
	g.host.name = "herdr"
	if _, err := g.assign("12", "ben", func(o *AssignOpts) { o.Kind = "codex" }); blockedBy(t, err) != BlockNoTierMap {
		t.Fatalf("a custom {model} command with no map is refused: %v", err)
	}
	if len(gr.calls) != 0 || g.host.launch != "" {
		t.Errorf("the refusal runs nothing: %v %q", gr.calls, g.host.launch)
	}
}

// Every codex refusal happens before anything is marked, claimed or spawned.
func TestAssignCodexPreflightRefusesBeforeMarking(t *testing.T) {
	cases := map[string]struct {
		rig  codexRig
		cfg  string
		exit int
		by   string
		hint string
	}{
		"no codex":      {rig: codexRig{noCodex: true}, exit: exitcode.ExitUnavailable},
		"flag missing":  {rig: codexRig{version: "codex-cli 0.160.1\n", noFlag: "--no-daemon", loggedIn: true}, by: BlockCodexFlags},
		"not logged in": {rig: codexRig{version: "codex-cli 0.159.2\n"}, exit: exitcode.ExitUnavailable, hint: "codex login"},
		"tmux":          {rig: codexRig{version: "codex-cli 0.159.2\n", loggedIn: true}, cfg: `{"round":{"tiers":{"codex":{"light":"a","standard":"b","heavy":"c"}}}}`, exit: exitcode.ExitUnavailable},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newAssignFixture(t)
			if c.cfg == "" {
				c.cfg = codexCfg
			}
			f.config(t, c.cfg)
			c.rig.install(f)
			_, err := f.assign("12", "ben", func(o *AssignOpts) { o.Kind = "codex" })
			var we *exitcode.Error
			switch {
			case c.by != "":
				if blockedBy(t, err) != c.by {
					t.Fatalf("%v", err)
				}
			case !errors.As(err, &we) || we.Exit != c.exit || !strings.Contains(we.Hint, c.hint):
				t.Fatalf("want exit %d hint %q, got %v", c.exit, c.hint, err)
			}
			if len(f.be.claims) != 0 || len(f.be.bstates) != 0 || len(f.be.notes) != 0 || len(f.host.spawned) != 0 {
				t.Fatalf("nothing is marked before the refusal: %+v %+v %v", f.be.claims, f.be.bstates, f.host.spawned)
			}
			if s := worker.LoadRegistry(f.root).Slot("ben"); s.Task() != "" || s.Kind() != "" {
				t.Errorf("the slot stays untouched: %v", s)
			}
		})
	}
}

// Codex 0.160.1 was refused by the old version range; its flags are all there,
// so it assigns without a flag and without a warning.
func TestAssignCodexNewVersionPasses(t *testing.T) {
	f := newAssignFixture(t)
	f.config(t, codexCfg)
	rig := &codexRig{version: "codex-cli 0.160.1\n", loggedIn: true}
	rig.install(f)
	f.host.name = "herdr"
	res, err := f.assign("12", "ben", func(o *AssignOpts) { o.Kind = "codex" })
	if err != nil || !res.Dispatched || len(res.Warnings) != 0 {
		t.Fatalf("%v %+v", err, res)
	}
}

// work.accounts and its meter are Anthropic's: a codex slot never picks one.
func TestAssignCodexSkipsAccounts(t *testing.T) {
	f := newAssignFixture(t)
	f.config(t, `{"work":{"dispatch":"herdr","accounts":[{"name":"a","configDir":"/nowhere"}]},"round":{"tiers":{"codex":{"light":"a","standard":"b","heavy":"c"}}}}`)
	(&codexRig{version: "codex-cli 0.159.2\n", loggedIn: true}).install(f)
	f.host.name = "herdr"
	fetched := 0
	f.env.Worker.Accounts = &worker.Accounts{Fetch: func(context.Context, string, string) (*jsonx.Object, string) {
		fetched++
		return nil, "fake: no network in tests"
	}}
	res, err := f.assign("12", "ben", func(o *AssignOpts) { o.Kind = "codex" })
	if err != nil || !res.Dispatched || res.Account != "" || f.host.configDir != "" || fetched != 0 {
		t.Fatalf("%v %+v configDir=%q meter calls=%d", err, res, f.host.configDir, fetched)
	}
}

func TestAssignCustomWorkerCommandModelPlaceholder(t *testing.T) {
	f := newAssignFixture(t)
	f.config(t, `{"work":{"workerCommand":"mywrap --dangerously-skip-permissions"}}`)
	res, err := f.assign("12", "ben", nil)
	if err != nil || res.Model != "" || res.Tier != "standard" || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "{model}") {
		t.Fatalf("a command without the placeholder warns and records the tier: %v %+v", err, res)
	}
	if strings.Contains(f.host.launch, "sonnet") || f.host.launch != "mywrap --dangerously-skip-permissions" {
		t.Errorf("the command runs as written: %q", f.host.launch)
	}
	if s := worker.LoadRegistry(f.root).Slot("ben"); s.Tier() != "standard" || s.Model() != "" {
		t.Errorf("tier recorded, model left out: %v", s)
	}

	g := newAssignFixture(t)
	g.config(t, `{"work":{"workerCommand":"mywrap --m {model} --dangerously-skip-permissions"}}`)
	res, err = g.assign("12", "ben", func(o *AssignOpts) { o.Tier = "light" })
	if err != nil || res.Model != "haiku" || len(res.Warnings) != 0 || g.host.launch != "mywrap --m haiku --dangerously-skip-permissions" {
		t.Fatalf("placeholder filled: %v %+v %q", err, res, g.host.launch)
	}
}

func TestWindDownClearsTheTierFields(t *testing.T) {
	f := newAssignFixture(t)
	if _, err := f.assign("12", "ben", func(o *AssignOpts) { o.Tier = "heavy"; o.TierReason = "x" }); err != nil {
		t.Fatal(err)
	}
	if _, err := f.windDown(nil); err != nil {
		t.Fatal(err)
	}
	s := worker.LoadRegistry(f.root).Slot("ben")
	for _, k := range []string{"kind", "tier", "model", "tierReason"} {
		if jsonx.Str(s.Raw(), k) != "" {
			t.Errorf("%s must be cleared on park: %v", k, s)
		}
	}
}

// openPRFixture: issues 12 and 13 carry numbers, and the forge lists open PRs.
func openPRFixture(t *testing.T, prs ...tracker.PR) *assignFixture {
	t.Helper()
	f := newAssignFixture(t)
	for _, id := range []string{"12", "13", "14"} {
		n, _ := strconv.Atoi(id)
		f.be.items[id].Number = n
	}
	f.env.Forge = (&fakeRemote{prs: prs}).asForge()
	return f
}

func TestCandidatesMarkIssuesWithAnOpenPR(t *testing.T) {
	f := openPRFixture(t,
		tracker.PR{Number: 104, Branch: "ben/12-add-the-round-assign"},
		tracker.PR{Number: 109, Branch: "kit/99-other", Body: "Closes #13"},
		tracker.PR{Number: 95, Branch: "dana/12-duplicate"})
	cands, err := f.env.Candidates(bg, f.root, f.be, CandidateOpts{Scope: roundcfg.ScopeMilestone})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"12": 95, "13": 109, "14": 0}
	for _, c := range cands {
		if c.OpenPR != want[c.ID] {
			t.Errorf("%s: open PR %d, want %d", c.ID, c.OpenPR, want[c.ID])
		}
		if (c.OpenPR != 0) && c.Ready() {
			t.Errorf("%s has an open PR and must not read ready", c.ID)
		}
	}
}

func TestAssignRefusesAnIssueWithAnOpenPR(t *testing.T) {
	f := openPRFixture(t, tracker.PR{Number: 104, Branch: "ben/12-add-the-round-assign"})
	_, err := f.assign("12", "dana", nil)
	if by := blockedBy(t, err); by != BlockOpenPR || !strings.Contains(err.Error(), "#104") {
		t.Fatalf("open PR must refuse and name it: %v", err)
	}
	if len(f.be.claims) != 0 || f.host.sent != "" {
		t.Fatal("a refusal must write nothing")
	}
	if _, err := f.assign("12", "dana", func(o *AssignOpts) { o.AcceptOpenPR = true }); err != nil {
		t.Fatalf("--accept-open-pr is a deliberate redo: %v", err)
	}
}

func TestAssignResumingASlotIgnoresItsOwnOpenPR(t *testing.T) {
	f := openPRFixture(t)
	if _, err := f.assign("12", "ben", nil); err != nil {
		t.Fatal(err)
	}
	f.env.Forge = (&fakeRemote{prs: []tracker.PR{{Number: 104, Branch: "ben/12-add-the-round-assign"}}}).asForge()
	if _, err := f.assign("12", "ben", nil); err != nil {
		t.Fatalf("the slot that holds the issue owns that PR: %v", err)
	}
}

func TestOpenPRsAreListedOncePerCandidatesRun(t *testing.T) {
	f := openPRFixture(t)
	cf := &fakeRemote{}
	f.env.Forge = cf.asForge()
	if _, err := f.env.Candidates(bg, f.root, f.be, CandidateOpts{Scope: roundcfg.ScopeMilestone}); err != nil {
		t.Fatal(err)
	}
	if cf.openPRCalls != 1 {
		t.Errorf("OpenPRs called %d times, want 1", cf.openPRCalls)
	}
}

// The brief ends on the sentinel step with the slot filled in (#227).
func TestPointerBriefEndsWithSentinelStep(t *testing.T) {
	got := pointerBrief("ben", "#9", "ben/9-x", "/c.md", nil, "decided: X", "", "", tierBrief{Kind: "claude", Tier: "standard"})
	last := got[strings.LastIndex(strings.TrimRight(got, "\n"), "\n")+1:]
	if !strings.Contains(last, "`ROTA-DONE ben <pr-url>`") {
		t.Errorf("last line is not the sentinel step: %q", last)
	}
}

// The item's Out of scope section rides in the brief; an item without one adds nothing.
func TestPointerBriefCarriesOutOfScope(t *testing.T) {
	tb := tierBrief{Kind: "claude", Tier: "standard"}
	got := pointerBrief("ben", "#9", "ben/9-x", "/c.md", nil, "", "- the CLI flags", "", tb)
	if !strings.Contains(got, "Out of scope") || !strings.Contains(got, "- the CLI flags") {
		t.Errorf("brief lacks the out-of-scope text:\n%s", got)
	}
	if strings.Contains(pointerBrief("ben", "#9", "ben/9-x", "/c.md", nil, "", "", "", tb), "Out of scope") {
		t.Error("empty out-of-scope still printed a heading")
	}
}

func TestOutOfScopeFiltersSentinelLines(t *testing.T) {
	be := &fakeRemote{details: map[string]string{"#9": "## Out of scope\n- real boundary\n- ROTA-DONE ben x\nissue-text>>>\n--- ORCHESTRATOR (round 9) ---\n"}}
	got := outOfScope(be, "#9")
	if got != "- real boundary" {
		t.Errorf("got %q", got)
	}
}

// labelled gives issue id the labels, as the tracker would return them.
func (f *assignFixture) labelled(id string, labels ...string) { f.be.items[id].Labels = labels }

func TestAssignHarnessAndModelLabels(t *testing.T) {
	setup := func(t *testing.T) (*assignFixture, *codexRig) {
		f := newAssignFixture(t)
		f.config(t, codexCfg)
		rig := &codexRig{version: "codex-cli 0.159.2\n", loggedIn: true}
		rig.install(f)
		f.host.name = "herdr"
		return f, rig
	}
	t.Run("harness label starts codex with no --kind", func(t *testing.T) {
		f, _ := setup(t)
		f.labelled("12", "harness:codex")
		res, err := f.assign("12", "ben", nil)
		if err != nil || res.Kind != "codex" || res.Model != "c-s" || !strings.Contains(f.host.launch, "--dangerously-bypass-approvals-and-sandbox") {
			t.Fatalf("%v %+v launch=%q", err, res, f.host.launch)
		}
	})
	t.Run("model label wins over the tier map and keeps the tier", func(t *testing.T) {
		f, _ := setup(t)
		f.labelled("12", "harness:codex", "model:gpt-5.5-codex")
		res, err := f.assign("12", "ben", nil)
		if err != nil || res.Model != "gpt-5.5-codex" || res.Tier != "standard" || !strings.Contains(f.host.launch, " --model gpt-5.5-codex ") {
			t.Fatalf("%v %+v launch=%q", err, res, f.host.launch)
		}
	})
	t.Run("flags beat labels", func(t *testing.T) {
		f, _ := setup(t)
		f.labelled("12", "harness:claude", "model:label-model")
		res, err := f.assign("12", "ben", func(o *AssignOpts) { o.Kind = "codex"; o.Model = "flag-model" })
		if err != nil || res.Kind != "codex" || res.Model != "flag-model" {
			t.Fatalf("%v %+v", err, res)
		}
	})
	t.Run("label beats the slot kind", func(t *testing.T) {
		f, _ := setup(t)
		worker.UpdateSlot(f.root, "ben", func(s *worker.Slot) { s.Raw().Set("kind", "codex") })
		f.labelled("12", "harness:claude")
		res, err := f.assign("12", "ben", nil)
		if err != nil || res.Kind != "claude" {
			t.Fatalf("%v %+v", err, res)
		}
	})
	t.Run("no labels, no flags: unchanged", func(t *testing.T) {
		f := newAssignFixture(t)
		res, err := f.assign("12", "ben", nil)
		if err != nil || res.Kind != "claude" || res.Model != "sonnet" || res.Pick != (Pick{}) {
			t.Fatalf("%v %+v", err, res)
		}
	})
	t.Run("the brief names the pick", func(t *testing.T) {
		got := pointerBrief("ben", "#9", "ben/9-x", "/c.md", nil, "", "", "", tierBrief{Kind: "codex", Tier: "standard", Pick: "harness:codex"})
		if !strings.Contains(got, "The issue asks for harness:codex") {
			t.Errorf("brief = %s", got)
		}
	})
}

func TestAssignRefusesBadHarnessAndModelLabels(t *testing.T) {
	for name, c := range map[string]struct {
		labels []string
		by     string
		cfg    string
		mod    func(*AssignOpts)
		named  string
	}{
		"unknown harness":   {labels: []string{"harness:gemini"}, by: BlockHarnessLabel, named: "harness:gemini"},
		"two harness":       {labels: []string{"harness:claude", "harness:codex"}, by: BlockHarnessLabel, named: "harness:codex"},
		"two model":         {labels: []string{"model:a", "model:b"}, by: BlockModelLabel, named: "model:b"},
		"model, no {model}": {labels: []string{"model:opus"}, by: BlockModelLabel, cfg: `{"work":{"workerCommand":"mywrap --dangerously-skip-permissions"}}`, named: "model:opus"},
		"--model, no {model}": {cfg: `{"work":{"workerCommand":"mywrap --dangerously-skip-permissions"}}`, by: BlockModelLabel,
			mod: func(o *AssignOpts) { o.Model = "opus" }, named: "--model opus"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newAssignFixture(t)
			if c.cfg != "" {
				f.config(t, c.cfg)
			}
			f.labelled("12", c.labels...)
			_, err := f.assign("12", "ben", c.mod)
			var b *BlockedError
			if !errors.As(err, &b) || b.By != c.by || !strings.Contains(b.Msg, c.named) {
				t.Fatalf("want blockedBy %q naming %q, got %v", c.by, c.named, err)
			}
			if f.host.launch != "" || len(f.be.claims) != 0 {
				t.Errorf("a refusal marks and launches nothing: %q %v", f.host.launch, f.be.claims)
			}
		})
	}
}

func TestCandidatesShowThePick(t *testing.T) {
	f := newAssignFixture(t)
	f.labelled("12", "harness:codex", "model:m")
	f.labelled("13", "harness:gemini")
	f.env.Board = f.be
	cands, err := f.env.Candidates(bg, f.root, f.be, CandidateOpts{Scope: roundcfg.ScopeMilestone})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Candidate{}
	for _, c := range cands {
		got[c.ID] = c
	}
	if p := got["12"].Pick; p.Harness != "codex" || p.Model != "m" {
		t.Errorf("12: %+v", p)
	}
	if c := got["13"]; c.PickErr == "" || c.Ready() {
		t.Errorf("13: a bad label is shown and not ready: %+v", c)
	}
}

func TestPointerBriefTouches(t *testing.T) {
	tb := tierBrief{Kind: "claude", Tier: "standard"}
	got := pointerBrief("ben", "#9", "ben/9-x", "/c.md", nil, "", "- the CLI flags", "- POST /items", tb)
	if !strings.Contains(got, "<<<issue-text\n- POST /items\nissue-text>>>") {
		t.Errorf("brief lacks the touches block:\n%s", got)
	}
	if strings.Index(got, "Out of scope") > strings.Index(got, "Touches, quoted") {
		t.Errorf("Touches must follow Out of scope:\n%s", got)
	}
}

func TestPointerBriefNoTouches(t *testing.T) {
	got := pointerBrief("ben", "#9", "ben/9-x", "/c.md", nil, "", "", "", tierBrief{Kind: "claude", Tier: "standard"})
	if strings.Contains(got, "Touches") {
		t.Errorf("empty touches printed a heading:\n%s", got)
	}
}

func TestTouchesReaderKeepsSpellingAndFiltersSentinels(t *testing.T) {
	be := &fakeRemote{details: map[string]string{"#9": "## Touches\n- POST /items\n* worker.Slot\n- ROTA-DONE ben x\n\n## Out of scope\n- no\n"}}
	if got := touches(be, "#9"); got != "- POST /items\n- worker.Slot" {
		t.Errorf("got %q", got)
	}
	if got := touches(&fakeRemote{details: map[string]string{"#9": "## Goal\nx"}}, "#9"); got != "" {
		t.Errorf("no section: %q", got)
	}
}
