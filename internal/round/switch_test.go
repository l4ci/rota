package round

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/worker"
)

// switchCfg is two Claude and two codex accounts, with a tier model for each.
const switchCfg = `{"round":{"dispatch":"herdr","harnesses":["claude","codex"],"accounts":[{"name":"a","configDir":"/cfg-a"},{"name":"b","configDir":"/cfg-b"},{"kind":"codex","name":"c1","dir":"/h1"},{"kind":"codex","name":"c2","dir":"/h2"}],"tiers":{"claude":{"light":"m-l","standard":"m-s","heavy":"m-h"},"codex":{"light":"c-l","standard":"c-s","heavy":"c-h"}}}}`

// orderHost logs kills and spawns in the order they happen.
type orderHost struct {
	*hostFake
	log *[]string
}

func (h *orderHost) Capabilities() host.Capabilities { return host.CapabilitiesOf(h) }
func (h *orderHost) Kill(_ context.Context, slot, _ string) error {
	*h.log = append(*h.log, "kill "+slot)
	return nil
}
func (h *orderHost) Spawn(ctx context.Context, o host.SpawnOpts) (string, error) {
	*h.log = append(*h.log, "spawn "+o.Slot)
	return h.hostFake.Spawn(ctx, o)
}

type switchFx struct {
	*assignFixture
	origin string
	used   map[string]float64
	log    []string
}

// newSwitchFx is a round where ben holds #12 on Claude account a, with a
// pushed commit, and an origin to push to.
func newSwitchFx(t *testing.T, used map[string]float64, mod ...func(*AssignOpts)) *switchFx {
	t.Helper()
	// ben is assigned while a has the most headroom; the test's readings apply after.
	f := &switchFx{assignFixture: newAssignFixture(t), used: map[string]float64{"a": 1, "b": 50, "c1": 50, "c2": 50}}
	f.origin = filepath.Join(t.TempDir(), "origin.git")
	gittest.Run(t, f.root, "init", "-q", "--bare", f.origin)
	gittest.Quiet(t, f.origin)
	gittest.Run(t, f.root, "remote", "add", "origin", f.origin)
	gittest.Run(t, f.root, "push", "-q", "origin", "main")
	// Wired before the pool: wire rebuilds the worker, which drops its accounts.
	f.wire(func(w *worker.Env) {
		w.NewHost = func(host.Kind) host.Host { return &orderHost{hostFake: f.host, log: &f.log} }
	})
	f.pool(t, switchCfg, f.used)
	if _, err := f.assign("12", "ben", func(o *AssignOpts) {
		o.Kind = harness.Claude
		for _, m := range mod {
			m(o)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if s := f.slot("ben"); s.Account() != "a" {
		t.Fatalf("ben starts on %q, want a", s.Account())
	}
	clear(f.used)
	for k, v := range used {
		f.used[k] = v
	}
	f.write(t, "work.txt", "work")
	gittest.Run(t, f.wt(), "add", "work.txt")
	gittest.Run(t, f.wt(), "commit", "-q", "-m", "work\n\n"+worker.AttemptTrailer+": "+f.slot("ben").Attempt())
	gittest.Run(t, f.wt(), "push", "-q", "-u", "origin", "HEAD")
	f.log = nil // the assignment's own dispatch
	return f
}

func (f *switchFx) wt() string { return filepath.Join(f.root, ".worktrees", "ben") }

func (f *switchFx) slot(name string) *worker.Slot {
	return worker.LoadRegistryTolerant(f.root).Slot(name)
}

func (f *switchFx) write(t *testing.T, file, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.wt(), file), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// dirty leaves a modified tracked file, a staged new file and an untracked one.
func (f *switchFx) dirty(t *testing.T) {
	t.Helper()
	f.write(t, "work.txt", "changed")
	f.write(t, "staged.txt", "staged")
	gittest.Run(t, f.wt(), "add", "staged.txt")
	f.write(t, "untracked.txt", "new")
}

func (f *switchFx) switchSlot(mod func(*SwitchOpts)) (Switched, error) {
	o := SwitchOpts{Slot: "ben"}
	if mod != nil {
		mod(&o)
	}
	return f.env.Switch(bg, f.root, f.be, o)
}

func (f *switchFx) git(t *testing.T, args ...string) string { return gitIn(t, f.wt(), args...) }

// AC-1, AC-2, AC-3, AC-7, AC-8, AC-10: the dirty work becomes one pushed
// checkpoint commit stamped with the slot's attempt; the slot keeps its issue,
// claim, attempt and branch, moves to account b on the same harness, and gets a
// fresh session briefed to continue.
func TestSwitchCheckpointsAndRelaunchesOnAnotherAccount(t *testing.T) {
	f := newSwitchFx(t, map[string]float64{"a": 100, "b": 10, "c1": 5, "c2": 5})
	before := f.slot("ben")
	f.dirty(t)
	res, err := f.switchSlot(func(o *SwitchOpts) { o.Account = "b" })
	if err != nil {
		t.Fatal(err)
	}
	head := f.git(t, "rev-parse", "HEAD")
	if res.Checkpoint == "" || !strings.HasPrefix(head, res.Checkpoint) {
		t.Fatalf("checkpoint %q, HEAD %s", res.Checkpoint, head)
	}
	if got := f.git(t, "log", "-1", "--format=%s"); got != "wip: checkpoint before switch" {
		t.Errorf("subject %q", got)
	}
	if got := f.git(t, "log", "-1", "--format=%(trailers:key="+worker.AttemptTrailer+",valueonly)"); got != before.Attempt() {
		t.Errorf("attempt trailer %q, want %q", got, before.Attempt())
	}
	files := strings.Fields(f.git(t, "show", "--name-only", "--format=", "HEAD"))
	slices.Sort(files)
	if want := []string{"staged.txt", "untracked.txt", "work.txt"}; !slices.Equal(files, want) {
		t.Errorf("checkpoint holds %v, want %v", files, want)
	}
	if st := f.git(t, "status", "--porcelain"); st != "" {
		t.Errorf("worktree not clean after the checkpoint: %q", st)
	}
	if remote := gitIn(t, f.origin, "rev-parse", before.Branch()); remote != head {
		t.Errorf("origin has %s, want the checkpoint %s", remote, head)
	}

	after := f.slot("ben")
	if after.Task() != before.Task() || after.ClaimID() != before.ClaimID() || after.Attempt() != before.Attempt() ||
		after.AttemptBase() != before.AttemptBase() || after.Branch() != before.Branch() {
		t.Errorf("binding moved: before %s/%s/%s/%s after %s/%s/%s/%s", before.Task(), before.ClaimID(), before.Attempt(), before.Branch(),
			after.Task(), after.ClaimID(), after.Attempt(), after.Branch())
	}
	if after.Account() != "b" || after.Kind() != harness.Claude {
		t.Errorf("slot on %s/%q, want claude/b", after.Kind(), after.Account())
	}
	if res.FromAccount != "a" || res.ToAccount != "b" || res.ToKind != harness.Claude || !res.Dispatched || !res.Changed {
		t.Errorf("result %+v", res)
	}
	if !slices.Equal(f.log, []string{"kill ben", "spawn ben"}) {
		t.Errorf("host calls %v, want the old session killed before the new one starts", f.log)
	}
	if !slices.Contains(f.host.env, "CLAUDE_CONFIG_DIR=/cfg-b") {
		t.Errorf("launch env %v, want account b's config dir", f.host.env)
	}
	brief := f.host.sent
	for _, want := range []string{res.Checkpoint, "git log origin/main..HEAD", "git revert --no-edit " + res.Checkpoint, "issue 12"} {
		if !strings.Contains(brief, want) {
			t.Errorf("brief lacks %q:\n%s", want, brief)
		}
	}
	gate, _ := f.ws.Gate(bg, f.root, worker.GateOpts{Slot: "ben", Base: "main", CheckOnly: true})
	if gate.Verdict == worker.GateStaleAttempt {
		t.Errorf("the gate fences the slot's own checkpoint: %+v", gate)
	}
}

// AC-4: --kind moves the slot to the other harness: its launch and account are
// codex's, and the Claude account it ran under is gone from the slot.
func TestSwitchKindMovesTheSlotToTheOtherHarness(t *testing.T) {
	f := newSwitchFx(t, map[string]float64{"a": 100, "b": 100, "c1": 50, "c2": 10})
	res, err := f.switchSlot(func(o *SwitchOpts) { o.Kind = harness.Codex })
	if err != nil {
		t.Fatal(err)
	}
	s := f.slot("ben")
	if s.Kind() != harness.Codex || s.HomeAccount() != "c2" || s.Account() != "" || s.Model() != "c-s" {
		t.Errorf("slot kind %q home %q claude %q model %q", s.Kind(), s.HomeAccount(), s.Account(), s.Model())
	}
	if res.Checkpoint != "" {
		t.Errorf("a clean worktree made checkpoint %s", res.Checkpoint)
	}
	for _, kv := range f.host.env {
		if strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR=") {
			t.Errorf("codex launch carries %s", kv)
		}
	}
	if !slices.Contains(f.host.env, "CODEX_HOME=/h2") {
		t.Errorf("launch env %v, want CODEX_HOME=/h2", f.host.env)
	}
}

// AC-10: without --kind the harness stays, even when the other one has more
// headroom; the current account is never picked again.
func TestSwitchKeepsTheHarnessUnlessAsked(t *testing.T) {
	f := newSwitchFx(t, map[string]float64{"a": 100, "b": 60, "c1": 1, "c2": 1})
	res, err := f.switchSlot(nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.ToKind != harness.Claude || res.ToAccount != "b" {
		t.Fatalf("switched to %s/%s, want claude/b", res.ToKind, res.ToAccount)
	}
}

// AC-5: a target account at its limit is refused before anything is touched.
func TestSwitchRefusesALimitedAccount(t *testing.T) {
	f := newSwitchFx(t, map[string]float64{"a": 100, "b": 100, "c1": 5, "c2": 5})
	f.dirty(t)
	head, status := f.git(t, "rev-parse", "HEAD"), f.git(t, "status", "--porcelain")
	for name, mod := range map[string]func(*SwitchOpts){
		"named":  func(o *SwitchOpts) { o.Account = "b" },
		"picked": nil,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := f.switchSlot(mod)
			want := blockedby.Limited
			if mod == nil {
				want = blockedby.Quota
			}
			if by := blockedBy(t, err); by != want {
				t.Fatalf("blockedBy %q, want %q: %v", by, want, err)
			}
			if f.git(t, "rev-parse", "HEAD") != head || f.git(t, "status", "--porcelain") != status || len(f.log) != 0 {
				t.Errorf("a refused switch touched the slot: log %v", f.log)
			}
			if s := f.slot("ben"); s.Account() != "a" {
				t.Errorf("account %q", s.Account())
			}
		})
	}
}

// AC-6: a push that fails takes the checkpoint back: HEAD, index and worktree
// are as they were, and nothing is dispatched.
func TestSwitchFailedPushLeavesTheWorktreeAsFound(t *testing.T) {
	f := newSwitchFx(t, map[string]float64{"a": 100, "b": 10})
	f.dirty(t)
	head, status := f.git(t, "rev-parse", "HEAD"), f.git(t, "status", "--porcelain")
	gittest.Run(t, f.root, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))
	res, err := f.switchSlot(func(o *SwitchOpts) { o.Account = "b" })
	if err == nil {
		t.Fatal("a failed push must fail the switch")
	}
	if res.Changed || res.Checkpoint != "" {
		t.Errorf("result %+v", res)
	}
	if got := f.git(t, "rev-parse", "HEAD"); got != head {
		t.Errorf("HEAD %s, want %s", got, head)
	}
	if got := f.git(t, "status", "--porcelain"); got != status {
		t.Errorf("status %q, want %q", got, status)
	}
	if len(f.log) != 0 || f.slot("ben").Account() != "a" {
		t.Errorf("log %v account %q", f.log, f.slot("ben").Account())
	}
}

// AC-6: a gate on the slot refuses the switch with nothing touched.
func TestSwitchRefusedWhileAGateRuns(t *testing.T) {
	f := newSwitchFx(t, map[string]float64{"a": 100, "b": 10})
	f.dirty(t)
	status := f.git(t, "status", "--porcelain")
	holdLandLock(t, f.root, func() {
		_, err := f.switchSlot(func(o *SwitchOpts) { o.Account = "b" })
		if by := blockedBy(t, err); by != worker.GateRunning {
			t.Errorf("blockedBy %q: %v", by, err)
		}
	})
	if f.git(t, "status", "--porcelain") != status || len(f.log) != 0 {
		t.Errorf("a refused switch touched the slot")
	}
}

// AC-9: a bundle lead switches with its members still bound, and a recorded PR
// stays the slot's.
func TestSwitchKeepsTheBundleAndThePR(t *testing.T) {
	f := newSwitchFx(t, map[string]float64{"a": 100, "b": 10}, func(o *AssignOpts) { o.Bundle = []string{"13"} })
	const pr = "https://github.com/o/r/pull/7"
	if _, err := worker.UpdateSlot(f.root, "ben", func(s *worker.Slot) { s.SetPR(pr) }); err != nil {
		t.Fatal(err)
	}
	claim := f.be.claims["13"]
	if _, err := f.switchSlot(func(o *SwitchOpts) { o.Account = "b" }); err != nil {
		t.Fatal(err)
	}
	s := f.slot("ben")
	if !slices.Equal(s.Bundle(), []string{"13"}) || f.be.claims["13"] != claim {
		t.Errorf("bundle %v claim %q, want [13] and %q", s.Bundle(), f.be.claims["13"], claim)
	}
	if s.PR() != pr {
		t.Errorf("PR %q, want %q", s.PR(), pr)
	}
}

// A slot that holds nothing has nothing to switch.
func TestSwitchRefusesAnIdleSlot(t *testing.T) {
	f := newSwitchFx(t, map[string]float64{"a": 100, "b": 10})
	_, err := f.env.Switch(bg, f.root, f.be, SwitchOpts{Slot: "dana", Account: "b"})
	if by := blockedBy(t, err); by != blockedby.NoSlotHolds {
		t.Fatalf("blockedBy %q: %v", by, err)
	}
}
