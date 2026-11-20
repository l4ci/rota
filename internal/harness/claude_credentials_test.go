package harness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/proc"
)

func TestCredentialsProblem(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"usable", `{"claudeAiOauth":{"accessToken":"t"}}`, ""},
		{"empty object", `{}`, "no access token"},
		{"empty token", `{"claudeAiOauth":{"accessToken":""}}`, "no access token"},
		{"not json", `nope`, "unreadable credentials"},
	} {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(tc.body), 0o600)
		if got := (Credentials{}).Problem(dir); got != tc.want {
			t.Errorf("%s: Credentials.Problem = %q, want %q", tc.name, got, tc.want)
		}
		tok, why := newClaudeFetch(QuotaOptions{}).token(dir)
		if (tok == "") != (tc.want != "") || (tc.want != "" && why != tc.want) {
			t.Errorf("%s: token = %q, %q disagrees with Credentials.Problem %q", tc.name, tok, why, tc.want)
		}
	}
	if got := (Credentials{}).Problem(t.TempDir()); got != "no credentials file" {
		t.Errorf("missing file: %q", got)
	}
}

// fakeKeychain is a Credentials for goos whose `security` runner answers from
// entries keyed by service name (exit 44 for a miss); reads counts the calls.
func fakeKeychain(goos string, entries map[string]string) (c Credentials, reads *int) {
	reads = new(int)
	return Credentials{GOOS: goos, Run: func(_ context.Context, cmd proc.Cmd) (proc.Result, error) {
		*reads++
		for i, a := range cmd.Args {
			if a == "-s" {
				if v, ok := entries[cmd.Args[i+1]]; ok {
					return proc.Result{Stdout: v + "\n"}, nil
				}
			}
		}
		return proc.Result{ExitCode: 44}, nil
	}}, reads
}

func tokenOf(c Credentials, dir string) (string, string) {
	return newClaudeFetch(QuotaOptions{Login: c}).token(dir)
}

func TestKeychainService(t *testing.T) {
	home, _ := os.UserHomeDir()
	if got := keychainService(filepath.Join(home, ".claude"), home); got != "Claude Code-credentials" {
		t.Errorf("default dir = %q, want no suffix", got)
	}
	// sha256("/acct/alpha") = 5c3a1b3b...; the suffix is its first 8 hex chars.
	want := "Claude Code-credentials-" + sha256Hex8("/acct/alpha")
	if got := keychainService("/acct/alpha", home); got != want || len(got) != len("Claude Code-credentials-")+8 {
		t.Errorf("custom dir = %q, want %q", got, want)
	}
}

func sha256Hex8(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}

func TestKeychainOnlyCredentialsAreUsableOnDarwin(t *testing.T) {
	dir := t.TempDir()
	kc, _ := fakeKeychain("darwin", map[string]string{keychainService(dir, ""): `{"claudeAiOauth":{"accessToken":"kc"}}`})
	if got := kc.Problem(dir); got != "" {
		t.Errorf("Problem = %q, want usable", got)
	}
	if tok, why := tokenOf(kc, dir); tok != "kc" || why != "" {
		t.Errorf("token = %q, %q", tok, why)
	}
}

func TestExpiredFileYieldsToKeychain(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"old","expiresAt":1000}}`), 0o600)
	kc, _ := fakeKeychain("darwin", map[string]string{keychainService(dir, ""): `{"claudeAiOauth":{"accessToken":"kc"}}`})
	if tok, why := tokenOf(kc, dir); tok != "kc" || why != "" {
		t.Errorf("token = %q, %q, want the Keychain token", tok, why)
	}
}

func TestExpiredFileWithoutKeychainStaysExpired(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"old","expiresAt":1000}}`), 0o600)
	kc, _ := fakeKeychain("darwin", nil)
	if tok, why := tokenOf(kc, dir); tok != "" || why != "token expired" {
		t.Errorf("token = %q, %q", tok, why)
	}
	// No Keychain at all: the same expiry, with no network.
	if tok, why := tokenOf(Credentials{}, dir); tok != "" || why != "token expired" {
		t.Errorf("token = %q, %q", tok, why)
	}
}

// A Credentials with no clock is a no-expiry read: an expired file token is
// taken as is and the Keychain is not consulted. The clock turns expiry on.
func TestNilNowReadsWithoutExpiry(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"old","expiresAt":1000}}`), 0o600)
	kc, reads := fakeKeychain("darwin", map[string]string{keychainService(dir, ""): `{"claudeAiOauth":{"accessToken":"kc"}}`})
	if got := kc.Problem(dir); got != "" || *reads != 0 {
		t.Errorf("no clock: Problem = %q, Keychain reads = %d, want usable with none", got, *reads)
	}
	kc.Now = func() time.Time { return time.UnixMilli(2000) }
	if got := kc.Problem(dir); got != "" || *reads != 1 {
		t.Errorf("with clock: Problem = %q, Keychain reads = %d, want one read for the expired file", got, *reads)
	}
}

func TestValidFileWinsAndSkipsKeychain(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"file"}}`), 0o600)
	kc, calls := fakeKeychain("darwin", map[string]string{keychainService(dir, ""): `{"claudeAiOauth":{"accessToken":"kc"}}`})
	if tok, why := tokenOf(kc, dir); tok != "file" || why != "" {
		t.Errorf("token = %q, %q, want the file token", tok, why)
	}
	if got := kc.Problem(dir); got != "" || *calls != 0 {
		t.Errorf("Problem = %q, keychain reads = %d, want usable with none", got, *calls)
	}
}

func TestMissingKeychainEntryNamesKeychain(t *testing.T) {
	dir := t.TempDir()
	kc, _ := fakeKeychain("darwin", nil)
	got := kc.Problem(dir)
	if !strings.Contains(got, "Keychain") {
		t.Errorf("Credentials.Problem = %q, want it to name the Keychain", got)
	}
	// A file still serves when the Keychain has nothing.
	os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"f"}}`), 0o600)
	if got := kc.Problem(dir); got != "" {
		t.Errorf("file fallback = %q, want usable", got)
	}
}

func TestKeychainIgnoredOffDarwin(t *testing.T) {
	dir := t.TempDir()
	kc, calls := fakeKeychain("linux", map[string]string{keychainService(dir, ""): `{"claudeAiOauth":{"accessToken":"kc"}}`})
	if got := kc.Problem(dir); got != "no credentials file" || *calls != 0 {
		t.Errorf("Problem = %q, want the file-only reason", got)
	}
}

func TestKeychainReadFailureIsNamedNotMissing(t *testing.T) {
	dir := t.TempDir()
	kc := Credentials{GOOS: "darwin", Run: func(context.Context, proc.Cmd) (proc.Result, error) {
		return proc.Result{ExitCode: 51, Stderr: "boom\n"}, nil
	}}
	got := kc.Problem(dir)
	if !strings.Contains(got, "Keychain unavailable") || !strings.Contains(got, "exit 51") {
		t.Errorf("Problem = %q, want the Keychain read failure named", got)
	}
	if strings.Contains(got, "no credentials file or Keychain entry") {
		t.Errorf("Problem = %q, must not claim a missing entry", got)
	}
}

// The Keychain service name treats Credentials.Home's ~/.claude as the
// default login: no suffix.
func TestCredentialsHomeSeamPicksDefaultKeychainService(t *testing.T) {
	kc, _ := fakeKeychain("darwin", map[string]string{"Claude Code-credentials": `{"claudeAiOauth":{"accessToken":"kc"}}`})
	kc.Home = func() (string, error) { return "/fake/home", nil }
	if got := kc.Problem("/fake/home/.claude"); got != "" {
		t.Errorf("Problem = %q, want usable through the default service", got)
	}
}
