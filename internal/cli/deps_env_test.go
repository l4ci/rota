package cli

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/connections"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// TestFakeGitReachesWorkerAndRoundEnv pins the one Git source: a faked
// Deps.Git is the runner of the worker env and of the round env's worker.
func TestFakeGitReachesWorkerAndRoundEnv(t *testing.T) {
	d := testDeps()
	var calls int
	d.Git = func(context.Context, string, ...string) (git.Result, error) {
		calls++
		return git.Result{}, nil
	}
	envs := map[string]func(context.Context, string, ...string) (git.Result, error){
		"workerEnv":    d.workerEnv().Git,
		"baseRoundEnv": d.baseRoundEnv().Git,
		"round worker": d.baseRoundEnv().Worker.Git,
	}
	for name, run := range envs {
		before := calls
		if run == nil {
			t.Errorf("%s: Git is nil", name)
			continue
		}
		run(context.Background(), ".", "status")
		if calls != before+1 {
			t.Errorf("%s: did not reach the faked Deps.Git", name)
		}
	}
}

// TestRoundEnvAccountsAndGit pins what RoundEnv hands round verbs: Accounts nil
// (quota and account picking are opt-in, so round start keeps its path), and
// the faked Deps.Git reachable through Worker.Git; accountedRoundEnv opts in.
func TestRoundEnvAccountsAndGit(t *testing.T) {
	d := testDeps()
	var calls int
	d.Git = func(context.Context, string, ...string) (git.Result, error) {
		calls++
		return git.Result{}, nil
	}
	root := t.TempDir()
	env := d.RoundEnv(context.Background(), root)
	if env.Worker.Accounts != nil || env.AccountPicking {
		t.Error("RoundEnv: account picking must stay off")
	}
	if env.Worker.Git == nil {
		t.Fatal("RoundEnv: Worker.Git is nil")
	}
	env.Worker.Git(context.Background(), ".", "status")
	if calls == 0 {
		t.Error("RoundEnv: Worker.Git did not reach the faked Deps.Git")
	}
	c := &Ctx{Deps: d}
	if got := accountedRoundEnv(c, context.Background(), root); !got.AccountPicking || got.Worker.Accounts == nil {
		t.Error("accountedRoundEnv: account picking is not on")
	}
	// The verbs that build RoundEnv plus workerEnvCtx (adopt, review relay,
	// winddown) get the meter reader but never pick.
	plain := d.RoundEnv(context.Background(), root)
	plain.Worker = workerEnvCtx(c, context.Background())
	if plain.AccountPicking {
		t.Error("RoundEnv + workerEnvCtx: account picking must stay off")
	}
}

// Account picking is chosen where the env is built (accountedRoundEnv): no verb
// sets the flag or Worker.Accounts on a round env itself, and the accounting
// verbs (assign, tick, architecture, return, status's quota read) go through
// the constructor. Only defaultRoundEnv may switch it off and only
// accountedRoundEnv may call WithAccounts.
func TestAccountPickingChosenOnlyInTheConstructor(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"round_assign.go": true, "round_tick.go": true, "round_architecture.go": true, "round_return.go": true, "round_status.go": true}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		for _, fn := range []string{"defaultRoundEnv", "accountedRoundEnv"} {
			src = cutFunc(t, f, src, fn)
		}
		for _, bad := range []string{".Worker.Accounts =", "AccountPicking =", ".WithAccounts("} {
			if strings.Contains(src, bad) {
				t.Errorf("%s uses %q outside defaultRoundEnv/accountedRoundEnv; use accountedRoundEnv", f, bad)
			}
		}
		if want[f] && !strings.Contains(string(b), "accountedRoundEnv(") {
			t.Errorf("%s must build its env with accountedRoundEnv", f)
		}
		if !want[f] && strings.Contains(src, "accountedRoundEnv(") {
			t.Errorf("%s uses accountedRoundEnv but is not an accounting verb", f)
		}
	}
}

// cutFunc removes the top-level function name from src.
func cutFunc(t *testing.T, file, src, name string) string {
	t.Helper()
	fs := token.NewFileSet()
	af, err := parser.ParseFile(fs, file, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range af.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Name.Name == name {
			return src[:fs.Position(fd.Pos()).Offset] + src[fs.Position(fd.End()).Offset:]
		}
	}
	return src
}

// TestDepsConfigReachesEveryConfigReader pins the one config source: a swapped
// Deps.Config is what the worker env, escalation env, knowledge store and host
// decision read, so none of them goes to disk.
func TestDepsConfigReachesEveryConfigReader(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	var reads int
	sentinel := new(int)
	d := testDeps()
	d.Getwd = func() (string, error) { return root, nil }
	d.Config = func(string) any { reads++; return sentinel }

	for name, cfg := range map[string]func(string) any{
		"workerEnv":     d.workerEnv().Config,
		"escalationEnv": d.escalationEnv().Config,
	} {
		before := reads
		if cfg == nil || cfg(root) != sentinel || reads != before+1 {
			t.Errorf("%s: Config did not reach Deps.Config", name)
		}
	}
	st, _, err := knStore(&Ctx{Deps: d})
	if err != nil {
		t.Fatal(err)
	}
	if before := reads; st.Config == nil || st.Config(root) != sentinel || reads != before+1 {
		t.Error("knStore: Config did not reach Deps.Config")
	}
	before := reads
	d.resolveHost(root)
	if reads != before+1 {
		t.Error("resolveHost did not read Deps.Config")
	}
}

// One clock reaches every credential read: the account picker, slot health,
// doctor and the status screen all take Deps.Now, not the wall clock.
func TestCredentialReadsUseDepsClock(t *testing.T) {
	d := testDeps()
	pinNow(d, "2031-05-06T07:08:09Z")
	want := d.Now()
	for name, c := range map[string]worker.Credentials{
		"credentials":   d.credentials(),
		"round env":     d.baseRoundEnv().Login,
		"worker accts":  d.WorkerAccounts().Login,
		"gate-free env": d.RoundEnv(context.Background(), t.TempDir()).Login,
	} {
		if c.Now == nil || !c.Now().Equal(want) {
			t.Errorf("%s: Now does not read Deps.Now", name)
		}
	}
}

// The gate's issue tracker and the bounce store's comment tracker are built
// under the worker env's context, as its forge is, so an interrupt reaches the
// connection lookup they start with.
func TestGateIssueTrackersCarryEnvCtx(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var seen []error
	d := testDeps()
	d.TrackerOptions = []tracker.Option{tracker.WithConnections(func(c context.Context, _ string) (connections.Connection, string, error) {
		seen = append(seen, c.Err())
		return connections.Connection{}, "", c.Err()
	})}
	d.WorkerEnv = func() worker.Env { return worker.Env{Ctx: ctx} }
	e := d.workerEnv()
	s := tracker.Settings{Source: "jira", Connection: "c", JiraProject: "P"}
	e.Issues(t.TempDir(), s)
	e.IssueComments(t.TempDir(), s)
	if len(seen) != 2 {
		t.Fatalf("connection lookups = %d, want one per tracker", len(seen))
	}
	for i, err := range seen {
		if err == nil {
			t.Errorf("lookup %d ran under a live context, want the cancelled Env.Ctx", i)
		}
	}
}
