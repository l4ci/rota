package harness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/l4ci/rota/internal/connections"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/proc"
)

// This code NEVER refreshes or writes credentials: an expired token reports
// unknown. Refreshing would mutate state a live Claude Code session owns.

// Credentials reads an account's login. GOOS and Run are the platform seams:
// on darwin the login Keychain is consulted through Run (the one `security`
// read, connections.SecurityRead), elsewhere never. The zero value reads files
// only. Now is the clock the expiry check reads; nil means none, so no token
// reads as expired.
type Credentials struct {
	GOOS string
	Run  proc.Runner
	Now  func() time.Time
	// Home is whose ~/.claude is the unsuffixed Keychain service; nil means
	// none is. SystemCredentials takes it from its caller; nothing defaults it.
	Home func() (string, error)
}

// SystemCredentials is Credentials on this machine, running tools through run,
// reading the home through home and the time through now.
func SystemCredentials(run proc.Runner, home func() (string, error), now func() time.Time) Credentials {
	return Credentials{GOOS: runtime.GOOS, Run: run, Home: home, Now: now}
}

// Problem reports why the account whose config dir is dir (already expanded)
// has no usable credentials: no file (or, on macOS, Keychain entry),
// unreadable, or no access token. "" means usable. An expired token is not a
// problem: Claude Code refreshes it itself, and this code never does. The clock
// only decides whether an expired file yields to the Keychain first; with a nil
// Now (the zero time) nothing is expired, so the file is read as is.
func (c Credentials) Problem(dir string) string {
	var now time.Time
	if c.Now != nil {
		now = c.Now()
	}
	_, _, why := c.read(dir, now)
	return why
}

const keychainBase = "Claude Code-credentials"

// keychainService is the generic-password service name Claude Code files the
// OAuth credentials under for config dir dir (already expanded): the default
// ~/.claude has no suffix, any other dir carries the first 8 hex chars of
// sha256(dir).
func keychainService(dir, home string) string {
	if home != "" && dir == filepath.Join(home, ".claude") {
		return keychainBase
	}
	sum := sha256.Sum256([]byte(dir))
	return keychainBase + "-" + hex.EncodeToString(sum[:])[:8]
}

// readCredentials returns the OAuth blob, access token and a reason when there
// is none. The file comes first. On darwin the login Keychain is consulted only
// when the file is missing, unusable or expired: Claude Code moved its
// credentials there, so a leftover file can be stale while the Keychain entry
// is current. Without a usable Keychain entry the file's own result stands.
func (c Credentials) read(dir string, now time.Time) (*jsonx.Object, string, string) {
	oauth, tok, why := readCredentialsFile(dir)
	if why == "" && !expired(oauth, now) {
		return oauth, tok, ""
	}
	if c.GOOS == "darwin" && c.Run != nil {
		home := ""
		if c.Home != nil {
			home, _ = c.Home()
		}
		b, ok, err := connections.SecurityRead(context.Background(), c.Run, keychainService(dir, home), "")
		if err == nil && ok {
			if o, t, w := parseCredentials([]byte(b)); w == "" {
				return o, t, ""
			}
		}
		if why == "no credentials file" {
			why = "no credentials file or Keychain entry"
			if err != nil {
				why = "no credentials file, Keychain unavailable: " + err.Error()
			}
		}
	}
	return oauth, tok, why
}

func readCredentialsFile(dir string) (*jsonx.Object, string, string) {
	b, err := os.ReadFile(filepath.Join(dir, ".credentials.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", "no credentials file"
		}
		return nil, "", "unreadable credentials"
	}
	return parseCredentials(b)
}

// expired reports whether the blob's expiresAt is at or before now.
func expired(oauth *jsonx.Object, now time.Time) bool {
	exp, ok := oauth.Get("expiresAt")
	if !ok || !jsonx.Truthy(exp) {
		return false
	}
	n, ok := exp.(json.Number)
	if !ok {
		return false
	}
	ms, err := n.Int64()
	return err == nil && !time.UnixMilli(ms).After(now)
}

func parseCredentials(b []byte) (*jsonx.Object, string, string) {
	v, err := jsonx.Decode(b)
	blob, ok := v.(*jsonx.Object)
	if err != nil || !ok {
		return nil, "", "unreadable credentials"
	}
	ov, _ := blob.Get("claudeAiOauth")
	oauth, _ := ov.(*jsonx.Object)
	if oauth == nil {
		return nil, "", "no access token"
	}
	tok := jsonx.Str(oauth, "accessToken")
	if tok == "" {
		return nil, "", "no access token"
	}
	return oauth, tok, ""
}
