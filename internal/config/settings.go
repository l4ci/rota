package config

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// MaxInt is the upper bound of every unbounded integer setting.
const MaxInt = 1 << 30

// Int reads an integer key and checks it is within min..max. A missing key
// gives its schema default. The error message is what a verb prints for exit 70.
func Int(cfg any, key string, min, max int) (int, error) {
	v, err := Value(cfg, key)
	if err != nil {
		return 0, err
	}
	n, ok := v.(interface{ Int64() (int64, error) })
	if !ok {
		return 0, fmt.Errorf("%s must be an integer (got %v)", key, v)
	}
	i, err := n.Int64()
	if err != nil || i < int64(min) || i > int64(max) {
		return 0, fmt.Errorf("%s must be an integer from %d to %d (got %v)", key, min, max, v)
	}
	return int(i), nil
}

// IntOr is Int for an advisory key a reader must not fail on: a value outside
// min..max, or one that is not an integer, gives the schema default.
func IntOr(cfg any, key string, min, max int) int {
	if n, err := Int(cfg, key, min, max); err == nil {
		return n
	}
	k, _ := SchemaKey(key)
	n, _ := Default(k).(interface{ Int64() (int64, error) })
	i, _ := n.Int64()
	return int(i)
}

// Bool reads a boolean key. A missing key gives its schema default.
func Bool(cfg any, key string) (bool, error) {
	v, err := Value(cfg, key)
	if err != nil {
		return false, err
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("%s must be true or false (got %v)", key, v)
	}
	return b, nil
}

// String returns a string key, or its schema default when unset. A value that
// is not a string gives "". An unknown key is a coding error and panics, so a
// typo cannot read as unset; TestReadKeysExistInSchema catches literal ones.
func String(cfg any, key string) string {
	v, err := Value(cfg, key)
	if err != nil {
		panic(err)
	}
	s, _ := v.(string)
	return s
}

// badRefComponent matches what git's ref-component rules forbid in a name: "..",
// "@{", a bare "@", a trailing dot or lock suffix, an empty path segment, a segment
// starting with ".". This validates a name; it builds no file.
var badRefComponent = regexp.MustCompile(`\.\.|@\{|^@$|\.$|\.lock$|^/|/$|//|/\.`)

// ValidGitRemote refuses a value that is not a safe git remote name: empty,
// a leading "-" (git would read it as an option), whitespace, control
// characters, or what git's ref-component rules forbid.
func ValidGitRemote(name string) error {
	switch {
	case name == "":
		return errors.New("a remote name cannot be empty")
	case strings.HasPrefix(name, "-"):
		return errors.New("a remote name cannot start with '-': git would read it as an option")
	}
	for _, r := range name {
		if unicode.IsSpace(r) || unicode.IsControl(r) || strings.ContainsRune("~^:?*[\\", r) {
			return fmt.Errorf("a remote name cannot contain %q", r)
		}
	}
	if badRefComponent.MatchString(name) {
		return errors.New("a remote name must be a valid git ref component")
	}
	return nil
}

// GitRemote is the git remote round workers fetch from and push to: git.remote,
// origin when unset. A set value that is not a valid remote name is an error:
// no git runs on it.
func GitRemote(cfg any) (string, error) {
	raw := String(cfg, GitRemoteKey)
	if strings.TrimSpace(raw) == "" {
		return "origin", nil
	}
	if err := ValidGitRemote(raw); err != nil {
		return "", fmt.Errorf("%w for %s: %v (rota config set %s origin)", ErrBadValue, GitRemoteKey, err, GitRemoteKey)
	}
	return raw, nil
}

// StringIfSet returns a string key as written, "" when it is absent or not a
// string. Unlike String it ignores the schema default, for keys where "unset"
// must be told apart from the default.
func StringIfSet(cfg any, key string) string {
	v, _ := Lookup(cfg, key)
	s, _ := v.(string)
	return s
}

// Keys shared by more than one concern. Each is read here so its range is
// defined once.

// EscalateIssue is orchestrator.escalateIssue as text: an issue number, or a
// key like PROJ-123 on a key-based tracker. "" means unset (0 or empty). A
// string is not checked against the key grammar here; tracker.ParseKey does.
func EscalateIssue(cfg any) (string, error) {
	const key = "orchestrator.escalateIssue"
	v, err := Value(cfg, key)
	if err != nil {
		return "", err
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s), nil
	}
	n, err := Int(cfg, key, 0, MaxInt)
	if err != nil || n == 0 {
		return "", err
	}
	return strconv.Itoa(n), nil
}

// HandoffMaxAgeSeconds is how old a handoff file may be and still be delivered
// to a fresh session (the D1 hook and keepalive share it).
const HandoffMaxAgeSeconds = 900

// SwitchOnUsage is orchestrator.switchOnUsage.
func SwitchOnUsage(cfg any) (bool, error) {
	return Bool(cfg, "orchestrator.switchOnUsage")
}

// UsageThreshold is orchestrator.usageThreshold, a percent.
func UsageThreshold(cfg any) (int, error) {
	return Int(cfg, "orchestrator.usageThreshold", 1, 100)
}

// FallbackSleepSeconds is the hold when a limit message names no reset time
// (limits and keepalive share it).
const FallbackSleepSeconds = 1800

// StringOrDefault returns a string key as written, or its schema default when
// it is absent, null or empty. For keys whose empty string means "use the
// default", such as round.tiers.claude.standard. Looking up a schema default
// for an unknown key panics, as in String.
func StringOrDefault(cfg any, key string) string {
	if s := StringIfSet(cfg, key); s != "" {
		return s
	}
	return String(nil, key)
}
