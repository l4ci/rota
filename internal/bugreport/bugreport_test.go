package bugreport

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

func project(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestAddLoadRoundTrip(t *testing.T) {
	root := project(t)
	got, err := Add(root, Record{Source: SourceAuto, Verb: "worker done", Exit: 70, Error: "boom"}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "bug-1" || got.Status != StatusOpen || got.TS != "2026-10-21T12:00:00Z" || got.Count != 1 {
		t.Fatalf("added %+v", got)
	}
	second, err := Add(root, Record{Source: SourceAgent, Summary: "a wrong refusal"}, t0)
	if err != nil || second.ID != "bug-2" {
		t.Fatalf("second %+v %v", second, err)
	}
	all, err := Load(root)
	if err != nil || len(all) != 2 || all[0].Verb != "worker done" || all[1].Summary != "a wrong refusal" {
		t.Fatalf("loaded %+v %v", all, err)
	}
	if _, err := os.Stat(Path(root)); err != nil || !strings.HasSuffix(Path(root), filepath.Join(".rota", File)) {
		t.Errorf("log path %q: %v", Path(root), err)
	}
}

func TestLoadMissingLogIsEmpty(t *testing.T) {
	all, err := Load(project(t))
	if err != nil || len(all) != 0 {
		t.Fatalf("%+v %v", all, err)
	}
}

func TestAddCountsAnOpenRepeat(t *testing.T) {
	root := project(t)
	r := Record{Source: SourceAuto, Verb: "gate", Exit: 70, Error: "x"}
	Add(root, r, t0)
	again, err := Add(root, r, t0)
	if err != nil || again.ID != "bug-1" || again.Count != 2 {
		t.Fatalf("repeat %+v %v", again, err)
	}
	all, _ := Load(root)
	if len(all) != 1 {
		t.Fatalf("repeat added a record: %+v", all)
	}
}

func TestOpenSkipsFiledAndDismissed(t *testing.T) {
	root := project(t)
	Add(root, Record{Source: SourceAgent, Summary: "a"}, t0)
	Add(root, Record{Source: SourceAgent, Summary: "b"}, t0)
	Add(root, Record{Source: SourceAgent, Summary: "c"}, t0)
	if _, err := Update(root, "bug-1", func(r *Record) { r.Status = StatusFiled; r.Issue = "https://x/1" }); err != nil {
		t.Fatal(err)
	}
	Update(root, "bug-2", func(r *Record) { r.Status = StatusDismissed })
	open, err := Open(root)
	if err != nil || len(open) != 1 || open[0].ID != "bug-3" {
		t.Fatalf("open %+v %v", open, err)
	}
	if _, err := Update(root, "bug-9", func(*Record) {}); err == nil {
		t.Error("updating an unknown id succeeded")
	}
}

func TestRedact(t *testing.T) {
	env := Env{Home: "/home/vo", Hostname: "vo-laptop", Repo: "acme-shop", Remote: "git@github.com:acme/acme-shop.git"}
	for _, c := range []struct{ name, in, gone string }{
		{"github token", "auth ghp_abcdefghijklmnopqrstuvwxyz0123456789 failed", "ghp_abcdefghijklmnopqrstuvwxyz0123456789"},
		{"fine-grained token", "github_pat_11ABCDEFG0abcdefghijkl_abcdefghijklmnopqrstuvwxyz", "github_pat_11ABCDEFG0"},
		{"gitlab token", "glpat-abcdefghij0123456789", "glpat-abcdefghij0123456789"},
		{"bearer", "Authorization: Bearer abc.def-123", "abc.def-123"},
		{"key value", `{"api_key": "s3cr3tvalue"}`, "s3cr3tvalue"},
		{"aws key", "id AKIAIOSFODNN7EXAMPLE", "AKIAIOSFODNN7EXAMPLE"},
		{"email", "mail me at volker@example.com now", "volker@example.com"},
		{"home path", "open /home/vo/dev/acme/.rota/x.json", "/home/vo"},
		{"other home", "open /Users/jane/work/x", "jane"},
		{"hostname", "on vo-laptop the lock", "vo-laptop"},
		{"repo name", "repo acme-shop is red", "acme-shop"},
		{"remote", "origin git@github.com:acme/acme-shop.git", "acme/acme-shop"},
		{"url userinfo", "https://bob:hunter2@git.example.com/x", "hunter2"},
		{"userinfo token only", "https://ghp_realtoken123@github.com/x", "ghp_realtoken123"},
		{"linear key", "key lin_api_AbCdEf0123456789xyz", "lin_api_AbCdEf0123456789xyz"},
		{"atlassian token", "tok ATATT3xFfGF0abcDEF_123=45", "ATATT3xFfGF0abcDEF_123"},
	} {
		got := Redact(c.in, env)
		if strings.Contains(got, c.gone) || got == c.in {
			t.Errorf("%s: %q still holds %q", c.name, got, c.gone)
		}
	}
}

func TestRedactKeepsPlainText(t *testing.T) {
	in := "rota worker done exited 70: unexpected state in l4ci/rota"
	if got := Redact(in, Env{Repo: "rota"}); got != in {
		t.Errorf("changed plain text: %q", got)
	}
}

func TestDraftHasTemplateSectionsAndEnvironment(t *testing.T) {
	r := Record{ID: "bug-1", Source: SourceAuto, Verb: "worker done", Args: []string{"w1", "--attempt", "ghp_abcdefghijklmnopqrstuvwxyz0123456789"}, Exit: 70, Error: "open /home/vo/x: boom", Count: 2}
	title, body := Draft(r, Environment{Version: "1.2.3", Commit: "abc1234", OS: "linux", Arch: "amd64", Harness: "claude", Model: "sonnet"}, Env{Home: "/home/vo"})
	if !strings.Contains(title, "worker done") || strings.Contains(title, "/home/vo") {
		t.Errorf("title %q", title)
	}
	for _, want := range []string{"## Summary", "## What happened", "## Expected", "## Repro", "## Environment", "## Redaction note",
		"1.2.3", "linux", "amd64", "claude", "sonnet", "exit 70"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
	for _, leak := range []string{"ghp_abcdef", "/home/vo"} {
		if strings.Contains(body, leak) {
			t.Errorf("body leaks %q:\n%s", leak, body)
		}
	}
}

func TestDraftUnknownHarnessAndModel(t *testing.T) {
	_, body := Draft(Record{Source: SourceAgent, Summary: "wrong refusal"}, Environment{Version: "dev", OS: "linux", Arch: "arm64"}, Env{})
	if strings.Count(body, "unknown") < 2 {
		t.Errorf("missing harness/model not marked unknown:\n%s", body)
	}
}

func TestDraftTitleIsRedactedBeforeItIsTruncated(t *testing.T) {
	secret := "ghp_abcdefghijklmnopqrstuvwxyz0123456789"
	// the secret straddles the 90-character cut: a cut-then-redact leaves a prefix
	r := Record{Source: SourceAgent, Verb: "x", Summary: strings.Repeat("a", 60) + " " + secret}
	title, _ := Draft(r, Environment{}, Env{})
	if strings.Contains(title, "ghp_") {
		t.Errorf("title keeps a token fragment: %q", title)
	}
}

func TestAddRedactsWhatItStores(t *testing.T) {
	root := project(t)
	got, err := Add(root, Record{Source: SourceAuto, Verb: "x", Args: []string{"--token", "ghp_abcdefghijklmnopqrstuvwxyz0123456789"}, Error: "bad key sk-abcdefghijklmnopqrstuv"}, t0)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(Path(root))
	if strings.Contains(string(raw), "ghp_abc") || strings.Contains(string(raw), "sk-abcdef") || strings.Contains(got.Error, "sk-abc") {
		t.Errorf("log keeps secrets: %s", raw)
	}
	if fi, _ := os.Stat(Path(root)); fi.Mode().Perm() != 0o600 {
		t.Errorf("log mode %v, want 0600", fi.Mode().Perm())
	}
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.email=a@b", "-c", "user.name=n"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

// A worker's worktree has its own .rota/ (tracked), yet its records must land
// where the main checkout's wind-down reads them.
func TestWorktreeRecordsLandInTheMainLog(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	main := filepath.Join(base, "main")
	os.MkdirAll(filepath.Join(main, ".rota"), 0o755)
	gitIn(t, main, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(main, ".rota", "config.json"), []byte("{}"), 0o644)
	gitIn(t, main, "add", ".rota/config.json")
	gitIn(t, main, "commit", "-q", "-m", "i")
	wt := filepath.Join(base, "wt")
	gitIn(t, main, "worktree", "add", "-q", "-b", "w", wt)
	if _, err := Add(wt, Record{Source: SourceAgent, Summary: "from a worker"}, t0); err != nil {
		t.Fatal(err)
	}
	open, err := Open(main)
	if err != nil || len(open) != 1 || open[0].Summary != "from a worker" {
		t.Fatalf("main sees %+v %v", open, err)
	}
}

func TestRedactGaps(t *testing.T) {
	env := Env{Home: "/home/vo", Hostname: "box", Repo: "checkout-dir", Remote: "git@gitlab.acme.internal:acme-org/Shop-App.git"}
	for _, c := range []struct{ name, in, gone string }{
		{"https form of the remote", "cloned https://gitlab.acme.internal/acme-org/Shop-App failed", "acme-org"},
		{"owner alone", "owner acme-org denied", "acme-org"},
		{"repo name, other case", "repo shop-app is red", "shop-app"},
		{"remote host", "dial gitlab.acme.internal:8443 refused", "gitlab.acme.internal"},
		{"unknown url host", "GET https://ci.corp.example.net/api failed", "ci.corp.example.net"},
		{"local fqdn", "on box.lan.example the lock", "lan.example"},
		{"basic auth", "Authorization: Basic dXNlcjpwYXNz", "dXNlcjpwYXNz"},
		{"token scheme", "Authorization: token 0123456789abcdef0123456789abcdef01234567", "0123456789abcdef"},
		{"userinfo then host", "GET https://bot@ci.corp.example.net/x", "ci.corp.example.net"},
		{"userinfo pass then host", "dial http://user:pass@proxy.acme.corp/", "proxy.acme.corp"},
		{"tld-like host with port", "dial db.corp.sh:5432 refused", "db.corp.sh"},
		{"ipv4 with port", "dial 10.1.2.3:8080 refused", "10.1.2.3"},
		{"flag value", "rota x --token plainvalue1 --force", "plainvalue1"},
		{"flag equals quoted", `rota x --password="two words"`, "two words"},
		{"env style", "GH_TOKEN plainvalue2 rota x", "plainvalue2"},
		{"aws secret", "aws_secret_access_key = wJalrXUtnFEMI/K7MDENG", "wJalrXUtnFEMI"},
		{"quoted value", `{"secret": "has space inside"}`, "has space inside"},
		{"pem", "-----BEGIN RSA PRIVATE KEY-----\nMIIEabc\n-----END RSA PRIVATE KEY-----", "MIIEabc"},
		{"npm token", "npm_abcdefghijklmnopqrstuvwxyz0123456789", "npm_abcdef"},
		{"google key", "AIzaSyA-abcdefghijklmnopqrstuvwxyz012345", "AIzaSyA"},
		{"root home", "open /root/.config/x", "/root"},
		{"windows home", `open C:\Users\jane\x`, "jane"},
	} {
		got := Redact(c.in, env)
		if strings.Contains(got, c.gone) {
			t.Errorf("%s: %q still holds %q", c.name, got, c.gone)
		}
	}
}

func TestRedactKeepsFileLocations(t *testing.T) {
	env := Env{Home: "/home/vo", Hostname: "box"}
	in := "panic: runtime error: index out of range [3] with length 3\n\n" +
		"goroutine 1 [running]:\n" +
		"main.run(...)\n\t/src/rota/internal/cli/hook.go:242 +0x1d\n" +
		"main.main()\n\tmain.go:12 +0x25\n" +
		"read .rota/backlog.md:12, scripts/run.sh:7 and v0.12.3:100"
	if got := Redact(in, env); got != in {
		t.Errorf("file:line locations changed:\n%q\nwant\n%q", got, in)
	}
}

func TestRedactKeepsUpstreamAndPublicNames(t *testing.T) {
	env := Env{Remote: "git@github.com:l4ci/rota.git"}
	in := "see https://github.com/l4ci/rota/issues/1 and github.com"
	if got := Redact(in, env); got != in {
		t.Errorf("changed %q", got)
	}
}

func TestAddRedactsSpaceSeparatedFlagArgs(t *testing.T) {
	root := project(t)
	Add(root, Record{Source: SourceAuto, Verb: "x", Exit: 70, Args: []string{"--token", "plainvalue", "--force", "GH_TOKEN", "other"}, Error: "e"}, t0)
	raw, _ := os.ReadFile(Path(root))
	if strings.Contains(string(raw), "plainvalue") {
		t.Errorf("log keeps the value: %s", raw)
	}
	_, body := Draft(Record{Verb: "x", Args: []string{"--token", "plainvalue"}}, Environment{}, Env{})
	if strings.Contains(body, "plainvalue") {
		t.Errorf("draft keeps the value:\n%s", body)
	}
}
