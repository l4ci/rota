package cli

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/connections"
	"github.com/l4ci/rota/internal/jsonx"
)

// fakeKeychain is the secret store tests use in place of the OS keychain.
type fakeKeychain struct {
	vals map[string]string
	fail error // Set fails with it, as a headless Linux box does
}

func (*fakeKeychain) Name() string { return connections.StoreKeychain }
func (k *fakeKeychain) Get(_ context.Context, n string) (string, bool, error) {
	v, ok := k.vals[n]
	return v, ok, nil
}
func (k *fakeKeychain) Set(_ context.Context, n, s string) error {
	if k.fail != nil {
		return k.fail
	}
	k.vals[n] = s
	return nil
}
func (k *fakeKeychain) Delete(_ context.Context, n string) error { delete(k.vals, n); return nil }

type fakeTest struct {
	err  error
	seen string // the secret the last read used
}

func (f *fakeTest) Test(_ context.Context, _ connections.Connection, secret string) error {
	f.seen = secret
	return f.err
}

// connectEnv is a temp global config dir (XDG) and Deps with a fake keychain,
// a fake jira tester and an empty environment: no test touches the real ones.
type connectEnv struct {
	t      *testing.T
	d      *Deps
	kc     *fakeKeychain
	tester *fakeTest
	env    map[string]string
	xdg    string
}

func newConnectEnv(t *testing.T) *connectEnv {
	t.Helper()
	x := projectsXDG(t)
	wd, _ := os.Getwd() // -C changes the process directory; put it back
	t.Cleanup(func() { os.Chdir(wd) })
	e := &connectEnv{t: t, d: testDeps(), kc: &fakeKeychain{vals: map[string]string{}}, tester: &fakeTest{}, env: map[string]string{}, xdg: x}
	e.d.Keychain = func() connections.Store { return e.kc }
	e.d.ConnTesters = map[string]connections.Tester{connections.KindJira: e.tester}
	real := e.d.Getenv
	e.d.Getenv = func(k string) string {
		if strings.HasPrefix(k, "ROTA_") && strings.HasSuffix(k, "_TOKEN") {
			return e.env[k]
		}
		return real(k)
	}
	return e
}

func (e *connectEnv) run(stdin string, args ...string) (int, *jsonx.Object, string) {
	e.t.Helper()
	var so, se bytes.Buffer
	code := mainWith(e.d, append(args, "--json"), strings.NewReader(stdin), &so, &se)
	v, err := jsonx.Decode(so.Bytes())
	if err != nil {
		e.t.Fatalf("%v: %v\n%s", args, err, so.String())
	}
	return code, v.(*jsonx.Object), se.String()
}

func connData(env *jsonx.Object) *jsonx.Object { return initData(env) }

func (e *connectEnv) addWork(extra ...string) {
	e.t.Helper()
	args := append([]string{"connect", "add", "work", "--kind", "jira", "--site", "https://acme.atlassian.net", "--user", "me@acme.com", "--token-stdin"}, extra...)
	if code, env, _ := e.run("s3cret-token\n", args...); code != 0 {
		e.t.Fatalf("add failed: %v", env)
	}
}

func TestConnectAddListNeverPrintsSecret(t *testing.T) {
	e := newConnectEnv(t)
	e.addWork()
	var so, se bytes.Buffer
	mainWith(e.d, []string{"connect", "list"}, strings.NewReader(""), &so, &se)
	if strings.Contains(so.String()+se.String(), "s3cret-token") {
		t.Fatalf("list printed the secret: %s", so.String())
	}
	code, env, _ := e.run("", "connect", "list")
	rows, _ := connData(env).Get("connections")
	items, _ := rows.([]any)
	if code != 0 || len(items) != 1 {
		t.Fatalf("%d %v", code, env)
	}
	row := items[0].(*jsonx.Object)
	if jsonx.Str(row, "name") != "work" || jsonx.Str(row, "kind") != "jira" || jsonx.Str(row, "store") != "keychain" {
		t.Errorf("%v", row)
	}
	raw, _ := os.ReadFile(filepath.Join(e.xdg, "rota", "connections.json"))
	if strings.Contains(string(raw), "s3cret-token") {
		t.Errorf("connections.json holds the secret: %s", raw)
	}
}

func TestConnectAddFallsBackToFile0600AndListSaysSo(t *testing.T) {
	e := newConnectEnv(t)
	e.kc.fail = connections.ErrUnavailable
	e.addWork()
	_, env, _ := e.run("", "connect", "list")
	rows, _ := connData(env).Get("connections")
	if got := jsonx.Str(rows.([]any)[0].(*jsonx.Object), "store"); got != "file" {
		t.Errorf("store %q", got)
	}
	fi, err := os.Stat(filepath.Join(e.xdg, "rota", "secrets.json"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("%v %v", fi, err)
	}
	// the fallback is reported on add, never silent
	_, env, _ = e.run("tok2", "connect", "add", "work", "--kind", "jira", "--site", "https://acme.atlassian.net", "--token-stdin", "--replace")
	if st := jsonx.Str(connData(env), "store"); st != "file" || jsonx.Str(connData(env), "note") == "" {
		t.Errorf("%v", connData(env))
	}
}

func TestConnectEnvOverridesStoredSecretInTest(t *testing.T) {
	e := newConnectEnv(t)
	e.addWork()
	e.env["ROTA_WORK_TOKEN"] = "ci-token"
	code, env, _ := e.run("", "connect", "test", "work")
	if code != 0 || e.tester.seen != "ci-token" || jsonx.Str(connData(env), "store") != "env" {
		t.Fatalf("%d seen=%q %v", code, e.tester.seen, connData(env))
	}
	delete(e.env, "ROTA_WORK_TOKEN")
	e.run("", "connect", "test", "work")
	if e.tester.seen != "s3cret-token" {
		t.Errorf("stored secret not used: %q", e.tester.seen)
	}
}

func TestConnectTestFailureAndUnknown(t *testing.T) {
	e := newConnectEnv(t)
	if code, _, _ := e.run("", "connect", "test", "nope"); code == 0 {
		t.Error("unknown connection passed")
	}
	e.addWork()
	e.tester.err = errors.New("401 unauthorized")
	code, env, _ := e.run("", "connect", "test", "work")
	if code == 0 || !strings.Contains(jsonx.Str(connData(env), "detail"), "401") {
		t.Errorf("%d %v", code, connData(env))
	}
}

func TestConnectAddRefusesDuplicateWithoutReplace(t *testing.T) {
	e := newConnectEnv(t)
	e.addWork()
	code, _, _ := e.run("x", "connect", "add", "work", "--kind", "jira", "--site", "https://a.example", "--token-stdin")
	if code == 0 {
		t.Error("overwrote an existing connection")
	}
	if code, _, _ := e.run("", "connect", "add", "w2", "--kind", "jira", "--site", "https://a.example", "--token-stdin"); code == 0 {
		t.Error("an empty token was accepted")
	}
}

func TestConnectRemoveClearsSecret(t *testing.T) {
	e := newConnectEnv(t)
	e.addWork()
	if code, env, _ := e.run("", "connect", "remove", "work"); code != 0 || connData(env) == nil {
		t.Fatalf("%v", env)
	}
	if len(e.kc.vals) != 0 {
		t.Errorf("secret left in keychain: %v", e.kc.vals)
	}
	if _, env, _ := e.run("", "connect", "list"); true {
		rows, _ := connData(env).Get("connections")
		if len(rows.([]any)) != 0 {
			t.Errorf("%v", rows)
		}
	}
}

// doctorWithConnection runs `rota doctor` in a project that names a connection.
func doctorWithConnection(t *testing.T, e *connectEnv, name string) (bool, map[string]any) {
	t.Helper()
	doctorFakes(t, map[string]string{"git": `case "$1" in remote) exit 2;; esac; exit 0`})
	e.d.DoctorPath = os.Getenv("ROTA_TEST_DOCTOR_PATH")
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	os.MkdirAll(filepath.Join(dir, ".rota"), 0o755)
	cfg := `{"issues":{"connection":"` + name + `"}}`
	os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(cfg), 0o644)
	var so, se bytes.Buffer
	mainWith(e.d, []string{"-C", dir, "doctor", "--json"}, strings.NewReader(""), &so, &se)
	v, err := jsonx.Decode(so.Bytes())
	if err != nil {
		t.Fatalf("%v: %s", err, so.String())
	}
	d := connData(v.(*jsonx.Object))
	checks, _ := d.Get("checks")
	for _, c := range checks.([]any) {
		if o := c.(*jsonx.Object); jsonx.Str(o, "name") == "connection" {
			ok, _ := d.Get("ok")
			m := map[string]any{"status": jsonx.Str(o, "status"), "detail": jsonx.Str(o, "detail"), "hint": jsonx.Str(o, "hint")}
			return ok == true, m
		}
	}
	t.Fatalf("no connection check in %s", so.String())
	return false, nil
}

func TestDoctorChecksProjectConnection(t *testing.T) {
	e := newConnectEnv(t)
	ok, c := doctorWithConnection(t, e, "work")
	if c["status"] != "fail" || c["hint"] == "" || ok {
		t.Errorf("unknown connection: %v", c)
	}
	e.addWork()
	e.kc.vals = map[string]string{} // defined, but no secret
	if _, c = doctorWithConnection(t, e, "work"); c["status"] != "fail" || c["hint"] == "" {
		t.Errorf("no secret: %v", c)
	}
	e.kc.vals["work"] = "tok"
	e.tester.err = errors.New("403")
	if _, c = doctorWithConnection(t, e, "work"); c["status"] != "fail" || !strings.Contains(c["detail"].(string), "403") {
		t.Errorf("failed read: %v", c)
	}
	e.tester.err = nil
	if _, c = doctorWithConnection(t, e, "work"); c["status"] != "pass" {
		t.Errorf("healthy: %v", c)
	}
}

// AC-5: no secret lands under .rota/, and no config key is a place to put one.
func TestNoSecretUnderDotRota(t *testing.T) {
	e := newConnectEnv(t)
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	if code, _, _ := e.run("", "-C", dir, "init", "--no-blocks"); code != 0 {
		t.Fatal("init failed")
	}
	e.addWork()
	if e.kc.vals["work"] != "s3cret-token" {
		t.Fatalf("the known token was not stored: %v", e.kc.vals)
	}
	e.run("", "-C", dir, "config", "set", "issues.connection", "work")
	e.run("", "-C", dir, "config", "set", "issues.jira.project", "ROTA")
	os.WriteFile(filepath.Join(dir, ".rota", "config.local.json"), []byte(`{"issues":{"connection":"work"}}`), 0o644)
	e.run("", "-C", dir, "connect", "test", "work")
	files, sawConn := 0, false
	err := filepath.WalkDir(filepath.Join(dir, ".rota"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		files++
		b, _ := os.ReadFile(p)
		if strings.Contains(string(b), "s3cret-token") {
			t.Errorf("%s holds a secret", p)
		}
		if strings.HasSuffix(p, "config.json") && strings.Contains(string(b), `"connection": "work"`) {
			sawConn = true
		}
		return nil
	})
	if err != nil || files == 0 || !sawConn {
		t.Fatalf("walk saw %d files, connection name in config: %v, err %v", files, sawConn, err)
	}
	for _, k := range config.Keys {
		n := strings.ToLower(k.Name)
		for _, bad := range []string{"token", "secret", "password", "apikey"} {
			if strings.Contains(n, bad) {
				t.Errorf("config key %s can hold a secret in .rota/", k.Name)
			}
		}
	}
}

func TestConnectAddTokenFlagIsGone(t *testing.T) {
	e := newConnectEnv(t)
	if code, _, _ := e.run("", "connect", "add", "w", "--kind", "jira", "--site", "https://a.example", "--token", "x"); code == 0 {
		t.Error("--token still accepted")
	}
}

func TestConnectAddReadsStdinWithoutFlag(t *testing.T) {
	e := newConnectEnv(t)
	if code, _, _ := e.run("tok\n", "connect", "add", "w", "--kind", "jira", "--site", "https://a.example"); code != 0 || e.kc.vals["w"] != "tok" {
		t.Errorf("%d %v", code, e.kc.vals)
	}
}

func TestConnectAddFailedSaveLeavesNoRecord(t *testing.T) {
	e := newConnectEnv(t)
	e.kc.fail = connections.ErrUnavailable
	// the file store cannot be written: its dir path is a regular file
	os.MkdirAll(e.xdg, 0o755)
	os.WriteFile(filepath.Join(e.xdg, "rota"), []byte("file, not dir"), 0o644)
	if code, _, _ := e.run("tok", "connect", "add", "w", "--kind", "jira", "--site", "https://a.example", "--token-stdin"); code == 0 {
		t.Fatal("add succeeded with nowhere to store the secret")
	}
	os.Remove(filepath.Join(e.xdg, "rota"))
	_, env, _ := e.run("", "connect", "list")
	if rows, _ := connData(env).Get("connections"); len(rows.([]any)) != 0 {
		t.Errorf("a record without a secret was left: %v", rows)
	}
}

func TestConnectAddRefusesEnvCollision(t *testing.T) {
	e := newConnectEnv(t)
	e.run("t", "connect", "add", "a-b", "--kind", "linear", "--token-stdin")
	if code, _, _ := e.run("t", "connect", "add", "a_b", "--kind", "linear", "--token-stdin"); code == 0 {
		t.Error("a_b collided with a-b but was accepted")
	}
	if _, ok := e.kc.vals["a_b"]; ok {
		t.Error("the refused connection left a secret behind")
	}
}

func TestConnectRemoveClearsFileStore(t *testing.T) {
	e := newConnectEnv(t)
	e.kc.fail = connections.ErrUnavailable
	e.addWork()
	e.run("", "connect", "remove", "work")
	raw, _ := os.ReadFile(filepath.Join(e.xdg, "rota", "secrets.json"))
	if strings.Contains(string(raw), "s3cret-token") {
		t.Errorf("secret left in secrets.json: %s", raw)
	}
}

type failDeleteKeychain struct{ fakeKeychain }

func (*failDeleteKeychain) Delete(context.Context, string) error { return errors.New("locked") }

func TestConnectRemoveReportsFailedDelete(t *testing.T) {
	e := newConnectEnv(t)
	e.addWork()
	e.d.Keychain = func() connections.Store { return &failDeleteKeychain{fakeKeychain{vals: map[string]string{}}} }
	if code, _, errOut := e.run("", "connect", "remove", "work"); code == 0 || !strings.Contains(errOut, "locked") {
		t.Errorf("%d %q", code, errOut)
	}
}

type errGetKeychain struct{ fakeKeychain }

func (*errGetKeychain) Get(context.Context, string) (string, bool, error) {
	return "", false, errors.New("dbus down")
}

func TestConnectListReportsStoreError(t *testing.T) {
	e := newConnectEnv(t)
	e.addWork()
	e.d.Keychain = func() connections.Store { return &errGetKeychain{fakeKeychain{vals: map[string]string{}}} }
	_, env, _ := e.run("", "connect", "list")
	rows, _ := connData(env).Get("connections")
	row := rows.([]any)[0].(*jsonx.Object)
	if jsonx.Str(row, "store") != "error" || jsonx.Str(row, "storeError") != "dbus down" {
		t.Errorf("%v", row)
	}
}

func TestDoctorWarnsWhenKindHasNoTestRead(t *testing.T) {
	e := newConnectEnv(t)
	e.addWork()
	e.d.ConnTesters = nil
	ok, c := doctorWithConnection(t, e, "work")
	if c["status"] != "warn" || !ok {
		t.Errorf("%v ok=%v", c, ok)
	}
	if code, _, _ := e.run("", "connect", "test", "work"); code != ExitUnavailable {
		t.Errorf("connect test exit %d", code)
	}
}

func TestConnectAddRejectsControlCharsInToken(t *testing.T) {
	e := newConnectEnv(t)
	for _, tok := range []string{"abc\ndelete-generic-password -s rota", "abc\rdef", "ab\tc", "ab\x00c"} {
		if code, _, _ := e.run(tok, "connect", "add", "w", "--kind", "jira", "--site", "https://a.example", "--token-stdin"); code != ExitUsage {
			t.Errorf("%q: exit %d", tok, code)
		}
	}
	if len(e.kc.vals) != 0 {
		t.Errorf("a rejected token was stored: %v", e.kc.vals)
	}
	if _, env, _ := e.run("", "connect", "list"); true {
		if rows, _ := connData(env).Get("connections"); len(rows.([]any)) != 0 {
			t.Errorf("record left: %v", rows)
		}
	}
}

func TestConnectAddReportsFailedRollback(t *testing.T) {
	e := newConnectEnv(t)
	kc := &failDeleteKeychain{fakeKeychain{vals: map[string]string{}}}
	e.d.Keychain = func() connections.Store { return kc }
	e.run("t", "connect", "add", "a-b", "--kind", "linear", "--token-stdin")
	// a_b collides on its env var: the record is refused after its secret was saved
	code, _, errOut := e.run("t", "connect", "add", "a_b", "--kind", "linear", "--token-stdin")
	if code == 0 || !strings.Contains(errOut, "rolling back the secret also failed") || !strings.Contains(errOut, "locked") {
		t.Errorf("%d %q", code, errOut)
	}
}
