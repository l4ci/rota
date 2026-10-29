package cli

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/connections"
	"github.com/l4ci/rota/internal/doctor"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/proc"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// TestFakeGitReachesWorkerAndRoundEnv pins the one Git source: a faked
// Deps.Git is the runner of the worker env and of the round env.
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
// (quota and account picking are opt-in, so round start keeps its path);
// accountedRoundEnv opts in.
func TestRoundEnvAccountsAndGit(t *testing.T) {
	d := testDeps()
	root := t.TempDir()
	env := d.RoundEnv(context.Background(), root)
	if env.Worker.Accounts() != nil || env.AccountPicking {
		t.Error("RoundEnv: account picking must stay off")
	}
	c := &Ctx{Deps: d}
	if got := accountedRoundEnv(c, context.Background(), root); !got.AccountPicking || got.Worker.Accounts() == nil {
		t.Error("accountedRoundEnv: account picking is not on")
	}
	// The verbs that build RoundEnv plus workerEnvCtx (adopt, review relay,
	// winddown) get the meter reader but never pick.
	plain := d.RoundEnv(context.Background(), root)
	plain.Worker = round.FromWorkerEnv(workerEnvCtx(c, context.Background()))
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
// under the context workerEnvCtx binds, as its forge is, so an interrupt reaches
// the connection lookup they start with. The hook supplies no context: the
// production binding route alone has to carry it.
func TestGateIssueTrackersCarryEnvCtx(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var seen []error
	d := testDeps()
	d.TrackerOptions = []tracker.Option{tracker.WithConnections(func(c context.Context, _ string) (connections.Connection, string, error) {
		seen = append(seen, c.Err())
		return connections.Connection{}, "", c.Err()
	})}
	e := workerEnvCtx(&Ctx{Deps: d}, ctx)
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

// The call-level half: a tracker the worker env built under a live context
// still returns the context error from a read and a write once Env.Ctx is
// cancelled, and neither reaches the site.
func TestGateIssueTrackerCallsAbortOnEnvCtx(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	d := testDeps()
	d.TrackerOptions = []tracker.Option{tracker.WithConnections(func(context.Context, string) (connections.Connection, string, error) {
		return connections.Connection{Name: "c", Kind: connections.KindJira, Site: srv.URL, User: "u@example.com", Deployment: connections.DeploymentDataCenter}, "secret", nil
	})}
	e := workerEnvCtx(&Ctx{Deps: d}, ctx)
	s := tracker.Settings{Source: "jira", Connection: "c", JiraProject: "P"}
	issues, err := e.Issues(t.TempDir(), s)
	if err != nil {
		t.Fatal(err)
	}
	comments, err := e.IssueComments(t.TempDir(), s)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := issues.Get(e.Ctx, "P-1", false); !canceled(err) {
		t.Errorf("Issues.Get after cancel = %v, want the context error", err)
	}
	if _, err := comments.AddComment(e.Ctx, "P-1", "body"); !canceled(err) {
		t.Errorf("IssueComments.AddComment after cancel = %v, want the context error", err)
	}
	if hits != 0 {
		t.Errorf("site was reached %d times after cancel, want 0", hits)
	}
}

// the rest client formats the transport error without wrapping it, so the
// context error is matched by its text.
func canceled(err error) bool {
	return err != nil && strings.Contains(err.Error(), context.Canceled.Error())
}

// Doctor's account check reads credentials with the Deps clock: a file token
// that expired at the pinned time falls back to the Keychain, one that has
// not expired stays put.
func TestDoctorAccountsUseDepsClock(t *testing.T) {
	const expiresAt = "2031-05-06T07:00:00Z"
	exp, _ := time.Parse(time.RFC3339, expiresAt)
	for name, tc := range map[string]struct {
		now      string
		keychain int
	}{
		"after expiry":  {"2031-05-06T08:00:00Z", 1},
		"before expiry": {"2031-05-06T06:00:00Z", 0},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			blob := fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"file","expiresAt":%d}}`, exp.UnixMilli())
			if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(blob), 0o600); err != nil {
				t.Fatal(err)
			}
			d := testDeps()
			// Keep this account-only check outside the developer project: doctor
			// must not discover its tracker or scan its remote claims.
			d.Getwd = func() (string, error) { return dir, nil }
			pinNow(d, tc.now)
			in := doctorInput(context.Background(), d)
			// only the accounts check matters: no host env, no tool lookups.
			in.OrchestratorConfigDir = ""
			in.Exec = func(context.Context, string, []string, []string, string) (doctor.Result, error) {
				return doctor.Result{}, nil
			}
			in.Look = func(string) (string, bool) { return "", false }
			in.Accounts = []config.Account{{Kind: config.KindClaude, Name: "a", Dir: dir}}
			in.Credentials.GOOS = "darwin"
			var asked int
			in.Credentials.Run = func(context.Context, proc.Cmd) (proc.Result, error) {
				asked++
				return proc.Result{Stdout: `{"claudeAiOauth":{"accessToken":"kc"}}`}, nil
			}
			var got *doctor.Check
			for _, c := range doctor.Run(context.Background(), in).Checks {
				if c.Name == "accounts" {
					got = &c
				}
			}
			if got == nil || got.Status != doctor.Pass {
				t.Fatalf("accounts check = %+v, want a pass", got)
			}
			if asked != tc.keychain {
				t.Errorf("Keychain reads = %d at %s, want %d", asked, tc.now, tc.keychain)
			}
		})
	}
}

// The worker env's context is bound inside workerEnvFor, before the factories
// capture it. An assignment to Env.Ctx anywhere else in the CLI (as
// Deps.WorkerPoll once did) rebinds only the field and leaves the issue,
// comment and forge factories on Background. Poll resolves its forge through
// one of them, so a cancelled invocation could still start that lookup live.
func TestNoWorkerEnvCtxAssignAfterConstruction(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range af.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name == "workerEnvFor" {
				continue
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				as, ok := n.(*ast.AssignStmt)
				if !ok {
					return true
				}
				for _, lhs := range as.Lhs {
					if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == "Ctx" {
						t.Errorf("%s assigns .Ctx after construction: bind it through Deps.workerEnvFor", fset.Position(as.Pos()))
					}
				}
				return true
			})
		}
	}
}
