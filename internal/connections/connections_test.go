package connections

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/proc"
)

// memStore is a fake secret store: nothing here reaches a keychain.
type memStore struct {
	name    string
	vals    map[string]string
	failSet error
	failGet error
}

func newMem(name string) *memStore { return &memStore{name: name, vals: map[string]string{}} }

func (m *memStore) Name() string { return m.name }
func (m *memStore) Get(_ context.Context, n string) (string, bool, error) {
	if m.failGet != nil {
		return "", false, m.failGet
	}
	v, ok := m.vals[n]
	return v, ok, nil
}
func (m *memStore) Set(_ context.Context, n, s string) error {
	if m.failSet != nil {
		return m.failSet
	}
	m.vals[n] = s
	return nil
}
func (m *memStore) Delete(_ context.Context, n string) error { delete(m.vals, n); return nil }

type fakeTester struct{ err error }

func (f fakeTester) Test(context.Context, Connection, string) error { return f.err }

var jira = Connection{Name: "work", Kind: KindJira, Site: "https://acme.atlassian.net", User: "me@acme.com"}

func TestAddListRemoveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := Add(dir, jira, false); err != nil {
		t.Fatal(err)
	}
	if err := Add(dir, jira, false); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate add: %v", err)
	}
	if err := Add(dir, Connection{Name: "lin", Kind: KindLinear}, false); err != nil {
		t.Fatal(err)
	}
	cs, err := Load(dir)
	if err != nil || len(cs) != 2 || cs[0].Name != "lin" || cs[1] != jira {
		t.Fatalf("%+v %v", cs, err)
	}
	if gone, _ := Remove(dir, "work"); !gone {
		t.Error("remove existing reported absent")
	}
	if gone, _ := Remove(dir, "work"); gone {
		t.Error("remove twice reported present")
	}
}

func TestAddValidates(t *testing.T) {
	for _, c := range []Connection{
		{Name: "bad name", Kind: KindJira, Site: "https://x"},
		{Name: "a", Kind: "github"},
		{Name: "a", Kind: KindJira, Site: "acme"},
		{Name: "a", Kind: KindLinear, Site: "api.linear.app"},
		{Name: "a", Kind: KindLinear, Deployment: DeploymentCloud},
		{Name: "a", Kind: KindItsaplan},
		{Name: "a", Kind: KindItsaplan, Site: "plan.example.com"},
		{Name: "a", Kind: KindItsaplan, Site: "https://plan.example.com", Deployment: DeploymentCloud},
	} {
		if err := Add(t.TempDir(), c, false); err == nil {
			t.Errorf("accepted %+v", c)
		}
	}
}

func TestLoadRefusesCorruptFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(Path(dir), []byte("{nope"), 0o600)
	if _, err := Load(dir); err == nil {
		t.Fatal("corrupt file loaded")
	}
	if err := Add(dir, jira, false); err == nil {
		t.Fatal("add overwrote a corrupt file")
	}
}

func TestEnvVar(t *testing.T) {
	if got := EnvVar("my-jira.1"); got != "ROTA_MY_JIRA_1_TOKEN" {
		t.Error(got)
	}
}

func TestSaveUsesKeychainWhenAvailable(t *testing.T) {
	kc, file := newMem("keychain"), newMem("file")
	file.vals["work"] = "stale"
	s := &Secrets{Keychain: kc, File: file}
	got, err := s.Save(context.Background(), "work", "tok")
	if err != nil || got.Store != StoreKeychain || got.Note != "" {
		t.Fatalf("%+v %v", got, err)
	}
	if kc.vals["work"] != "tok" || len(file.vals) != 0 {
		t.Errorf("kc=%v file=%v", kc.vals, file.vals)
	}
}

func TestSaveFallsBackToFileAndSaysSo(t *testing.T) {
	kc := newMem("keychain")
	kc.failSet = ErrUnavailable
	file := newMem("file")
	got, err := (&Secrets{Keychain: kc, File: file}).Save(context.Background(), "work", "tok")
	if err != nil || got.Store != StoreFile || !strings.Contains(got.Note, "keychain") {
		t.Fatalf("%+v %v", got, err)
	}
	got, _ = (&Secrets{File: file}).Save(context.Background(), "work", "tok")
	if got.Store != StoreFile || got.Note == "" {
		t.Errorf("no keychain: %+v", got)
	}
}

func TestResolveEnvOverridesStored(t *testing.T) {
	file := newMem("file")
	file.vals["work"] = "stored"
	env := map[string]string{"ROTA_WORK_TOKEN": "from-env"}
	s := &Secrets{Getenv: func(k string) string { return env[k] }, File: file}
	v, store, _ := s.Resolve(context.Background(), "work")
	if v != "from-env" || store != StoreEnv {
		t.Errorf("%q %q", v, store)
	}
	delete(env, "ROTA_WORK_TOKEN")
	v, store, _ = s.Resolve(context.Background(), "work")
	if v != "stored" || store != StoreFile {
		t.Errorf("%q %q", v, store)
	}
}

func TestResolveSurvivesBrokenKeychainWhenFileHasIt(t *testing.T) {
	kc, file := newMem("keychain"), newMem("file")
	kc.failGet = errors.New("dbus down")
	file.vals["work"] = "tok"
	v, store, err := (&Secrets{Keychain: kc, File: file}).Resolve(context.Background(), "work")
	if err != nil || v != "tok" || store != StoreFile {
		t.Errorf("%q %q %v", v, store, err)
	}
	delete(file.vals, "work")
	if _, _, err := (&Secrets{Keychain: kc, File: file}).Resolve(context.Background(), "work"); err == nil {
		t.Error("a broken keychain with no file secret must surface its error")
	}
}

func TestFileStoreIs0600(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rota")
	fs := FileStore(dir)
	if err := fs.Set(context.Background(), "work", "tok"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "secrets.json"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("%v %v", fi, err)
	}
	if v, ok, _ := fs.Get(context.Background(), "work"); !ok || v != "tok" {
		t.Errorf("%q %v", v, ok)
	}
	fs.Delete(context.Background(), "work")
	if _, ok, _ := fs.Get(context.Background(), "work"); ok {
		t.Error("delete left the secret")
	}
}

func TestCheckStates(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	file := newMem("file")
	sec := &Secrets{File: file}
	ok := map[string]Tester{KindJira: fakeTester{}}

	if d := Check(ctx, dir, "work", sec, ok); d.State != StateUnknown || d.Hint == "" {
		t.Errorf("unknown: %+v", d)
	}
	Add(dir, jira, false)
	if d := Check(ctx, dir, "work", sec, ok); d.State != StateNoSecret || d.Hint == "" {
		t.Errorf("nosecret: %+v", d)
	}
	file.vals["work"] = "tok"
	if d := Check(ctx, dir, "work", sec, ok); d.State != StateOK || d.Store != StoreFile {
		t.Errorf("ok: %+v", d)
	}
	if d := Check(ctx, dir, "work", sec, nil); d.State != StateUnsupported {
		t.Errorf("unsupported: %+v", d)
	}
	bad := map[string]Tester{KindJira: fakeTester{errors.New("401")}}
	if d := Check(ctx, dir, "work", sec, bad); d.State != StateFailed || !strings.Contains(d.Detail, "401") || d.Hint == "" {
		t.Errorf("failed: %+v", d)
	}
}

func TestOSKeychainCommands(t *testing.T) {
	var got []proc.Cmd
	out := proc.Result{Stdout: "sekret\n"}
	run := func(_ context.Context, c proc.Cmd) (proc.Result, error) { got = append(got, c); return out, nil }
	ctx := context.Background()

	if OSKeychain(run, "windows") != nil {
		t.Error("windows has no keychain support")
	}
	lin := OSKeychain(run, "linux")
	if v, ok, err := lin.Get(ctx, "work"); err != nil || !ok || v != "sekret" {
		t.Errorf("%q %v %v", v, ok, err)
	}
	lin.Set(ctx, "work", "tok")
	if c := got[1]; c.Name != "secret-tool" || string(c.Stdin) != "tok" || strings.Contains(strings.Join(c.Args, " "), "tok") {
		t.Errorf("linux set must pass the secret on stdin only: %+v", c)
	}
	mac := OSKeychain(run, "darwin")
	mac.Get(ctx, "work")
	if c := got[2]; c.Name != "security" || c.Args[0] != "find-generic-password" {
		t.Errorf("%+v", c)
	}
}

func TestOSKeychainNotFoundAndUnavailable(t *testing.T) {
	ctx := context.Background()
	missing := func(context.Context, proc.Cmd) (proc.Result, error) {
		return proc.Result{}, exec.ErrNotFound
	}
	if _, _, err := OSKeychain(missing, "linux").Get(ctx, "w"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("missing tool: %v", err)
	}
	notFound := func(context.Context, proc.Cmd) (proc.Result, error) {
		return proc.Result{ExitCode: 1}, nil
	}
	if _, ok, err := OSKeychain(notFound, "linux").Get(ctx, "w"); ok || err != nil {
		t.Errorf("empty exit 1 is a missing item: %v %v", ok, err)
	}
	noBus := func(context.Context, proc.Cmd) (proc.Result, error) {
		return proc.Result{ExitCode: 1, Stderr: "Cannot autolaunch D-Bus"}, nil
	}
	if _, _, err := OSKeychain(noBus, "linux").Get(ctx, "w"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("no session bus: %v", err)
	}
}

func TestAddRejectsEnvVarCollision(t *testing.T) {
	dir := t.TempDir()
	if err := Add(dir, Connection{Name: "a-b", Kind: KindLinear}, false); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a_b", "A-B"} {
		if err := Add(dir, Connection{Name: n, Kind: KindLinear}, false); !errors.Is(err, ErrEnvCollision) {
			t.Errorf("%s: %v", n, err)
		}
	}
	if err := Add(dir, Connection{Name: "a-b", Kind: KindLinear}, true); err != nil {
		t.Errorf("replacing the same name collided with itself: %v", err)
	}
}

// failDelKeychain is a keychain whose Set and Delete fail while Get still
// returns an old secret: the case where a stale entry would shadow the file.
type failDelKeychain struct{ *memStore }

func (f failDelKeychain) Delete(context.Context, string) error { return errors.New("locked") }

func TestSaveFallbackNeverLeavesStaleKeychainSecret(t *testing.T) {
	ctx := context.Background()
	kc := newMem("keychain")
	kc.vals["work"] = "old"
	kc.failSet = ErrUnavailable
	file := newMem("file")
	if _, err := (&Secrets{Keychain: failDelKeychain{kc}, File: file}).Save(ctx, "work", "new"); err == nil {
		t.Fatal("a stale keychain entry survived a fallback save")
	}
	// when the delete works, the file holds the new secret and resolve returns it
	s := &Secrets{Keychain: kc, File: file}
	got, err := s.Save(ctx, "work", "new")
	if err != nil || got.Store != StoreFile {
		t.Fatalf("%+v %v", got, err)
	}
	if v, store, _ := s.Resolve(ctx, "work"); v != "new" || store != StoreFile {
		t.Errorf("shadowed: %q %q", v, store)
	}
}

// keychainSim is a proc.Runner standing in for macOS `security` and records argv.
type keychainSim struct {
	calls []proc.Cmd
	item  string
}

func (k *keychainSim) run(_ context.Context, c proc.Cmd) (proc.Result, error) {
	k.calls = append(k.calls, c)
	switch {
	case len(c.Args) > 0 && c.Args[0] == "-i":
		in := string(c.Stdin)
		if i := strings.Index(in, "-w \""); i >= 0 {
			q := strings.TrimSuffix(strings.TrimSpace(in[i+4:]), `"`)
			k.item = strings.NewReplacer(`\"`, `"`, `\\`, `\`).Replace(q)
		}
		return proc.Result{}, nil
	case len(c.Args) > 0 && c.Args[0] == "find-generic-password":
		if k.item == "" {
			return proc.Result{ExitCode: 44}, nil
		}
		return proc.Result{Stdout: k.item + "\n"}, nil
	case len(c.Args) > 0 && c.Args[0] == "delete-generic-password":
		k.item = ""
	}
	return proc.Result{}, nil
}

func TestDarwinSetKeepsSecretOutOfArgv(t *testing.T) {
	sim := &keychainSim{}
	kc := OSKeychain(sim.run, "darwin")
	if err := kc.Set(context.Background(), "work", `s"3\cret`); err != nil {
		t.Fatal(err)
	}
	for _, c := range sim.calls {
		if strings.Contains(strings.Join(c.Args, " "), "cret") {
			t.Errorf("secret in argv: %v", c.Args)
		}
	}
	first := sim.calls[0]
	if first.Name != "security" || first.Args[0] != "-i" || !strings.HasPrefix(string(first.Stdin), "add-generic-password -U -s \"rota\" -a \"work\" -w ") {
		t.Errorf("%+v", first)
	}
	if !strings.Contains(string(first.Stdin), `"s\"3\\cret"`) {
		t.Errorf("secret not quoted on stdin: %q", first.Stdin)
	}
}

func TestDarwinSetFailsWhenItemWasNotStored(t *testing.T) {
	run := func(context.Context, proc.Cmd) (proc.Result, error) { return proc.Result{ExitCode: 44}, nil }
	if err := OSKeychain(run, "darwin").Set(context.Background(), "work", "tok"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("%v", err)
	}
}

func TestKeychainDeleteArgv(t *testing.T) {
	var got []proc.Cmd
	run := func(_ context.Context, c proc.Cmd) (proc.Result, error) {
		got = append(got, c)
		return proc.Result{}, nil
	}
	OSKeychain(run, "darwin").Delete(context.Background(), "work")
	OSKeychain(run, "linux").Delete(context.Background(), "work")
	if got[0].Name != "security" || strings.Join(got[0].Args, " ") != "delete-generic-password -s rota -a work" {
		t.Errorf("%+v", got[0])
	}
	if got[1].Name != "secret-tool" || strings.Join(got[1].Args, " ") != "clear service rota account work" {
		t.Errorf("%+v", got[1])
	}
	failing := func(context.Context, proc.Cmd) (proc.Result, error) {
		return proc.Result{ExitCode: 1, Stderr: "boom"}, nil
	}
	if err := OSKeychain(failing, "linux").Delete(context.Background(), "work"); err == nil {
		t.Error("a failing delete was swallowed")
	}
}

func TestFileStoreLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	fs := FileStore(dir)
	fs.Set(context.Background(), "a", "1")
	fs.Set(context.Background(), "b", "2")
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("leftover %s", e.Name())
		}
	}
}

// A jira connection says whether it is Cloud or Data Center; only an
// *.atlassian.net site defaults to cloud, and Cloud needs the account email.
func TestJiraDeployment(t *testing.T) {
	for _, tc := range []struct {
		c    Connection
		want string // "" means refused
	}{
		{Connection{Name: "a", Kind: KindJira, Site: "https://acme.atlassian.net", User: "me@acme.com"}, DeploymentCloud},
		{Connection{Name: "a", Kind: KindJira, Site: "https://ACME.Atlassian.net/", User: "me@acme.com"}, DeploymentCloud},
		{Connection{Name: "a", Kind: KindJira, Site: "https://jira.acme.com", Deployment: DeploymentDataCenter}, DeploymentDataCenter},
		{Connection{Name: "a", Kind: KindJira, Site: "https://jira.acme.com", User: "me", Deployment: DeploymentCloud}, DeploymentCloud},
		{Connection{Name: "a", Kind: KindJira, Site: "https://jira.acme.com", User: "me@acme.com"}, ""},
		{Connection{Name: "a", Kind: KindJira, Site: "https://evil-atlassian.net.example.com", User: "me"}, ""},
		{Connection{Name: "a", Kind: KindJira, Site: "https://acme.atlassian.net"}, ""},
		{Connection{Name: "a", Kind: KindJira, Site: "https://jira.acme.com", Deployment: "server"}, ""},
		{Connection{Name: "a", Kind: KindLinear, Deployment: DeploymentCloud}, ""},
	} {
		c := tc.c.WithDefaults()
		err := c.Validate()
		switch {
		case tc.want == "" && err == nil:
			t.Errorf("%+v: accepted as %q", tc.c, c.Deployment)
		case tc.want != "" && (err != nil || c.Deployment != tc.want):
			t.Errorf("%+v: %q, %v; want %q", tc.c, c.Deployment, err, tc.want)
		}
	}
	dir := t.TempDir()
	dc := Connection{Name: "dc", Kind: KindJira, Site: "https://jira.acme.com", Deployment: DeploymentDataCenter}
	if err := Add(dir, dc, false); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := Get(dir, "dc"); got != dc {
		t.Errorf("stored %+v, want %+v", got, dc)
	}
}
