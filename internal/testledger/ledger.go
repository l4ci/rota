// Package testledger is the exclusion ledger of known-red tests: the one
// sanctioned way to let the merge gate and the merge train pass over a failing
// test. Each entry in .rota/test-ledger.json carries an owner, a receipt (the
// issue or PR that tracks the fix) and an expiry; an expired entry fails the
// gate instead of silently excusing the test forever.
package testledger

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/rotatree"
)

// Entry is one excused test. Expires is the last instant the exclusion holds:
// the end of that day (UTC) for a date, the given instant for an RFC 3339 time.
type Entry struct {
	Test    string    `json:"test"`
	Owner   string    `json:"owner"`
	Receipt string    `json:"receipt"`
	Expires time.Time `json:"-"`
	// Raw is the expires string as written, for messages.
	Raw string `json:"expires"`
}

// Expired reports whether the exclusion no longer holds at now.
func (e Entry) Expired(now time.Time) bool { return now.After(e.Expires) }

// String names the entry the way a failure quotes it.
func (e Entry) String() string {
	return fmt.Sprintf("%s (owner %s, receipt %s, expired %s)", e.Test, e.Owner, e.Receipt, e.Raw)
}

// Ledger is the parsed ledger file. The zero value is an empty ledger.
type Ledger struct {
	Entries []Entry
}

// MalformedError is an entry that cannot be read; Index is its 1-based position.
type MalformedError struct {
	Index  int
	Test   string
	Reason string
}

func (e *MalformedError) Error() string {
	if e.Test != "" {
		return fmt.Sprintf("test ledger entry %d (%s): %s", e.Index, e.Test, e.Reason)
	}
	return fmt.Sprintf("test ledger entry %d: %s", e.Index, e.Reason)
}

// Load reads the ledger of the project at root. A missing file or an empty
// list is an empty ledger; a malformed file or entry is a *MalformedError
// (index 0 for the file itself).
func Load(root string) (Ledger, error) {
	data, err := os.ReadFile(rotatree.TestLedger(root))
	if errors.Is(err, os.ErrNotExist) {
		return Ledger{}, nil
	}
	if err != nil {
		return Ledger{}, err
	}
	return Parse(data)
}

// Parse reads ledger JSON: a list of {test, owner, receipt, expires}.
func Parse(data []byte) (Ledger, error) {
	if strings.TrimSpace(string(data)) == "" {
		return Ledger{}, nil
	}
	var raw []map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return Ledger{}, &MalformedError{Reason: "not a JSON list of entries: " + err.Error()}
	}
	var l Ledger
	for i, m := range raw {
		str := func(k string) string { s, _ := m[k].(string); return strings.TrimSpace(s) }
		e := Entry{Test: str("test"), Owner: str("owner"), Receipt: str("receipt"), Raw: str("expires")}
		for _, f := range []struct{ name, val string }{{"test", e.Test}, {"owner", e.Owner}, {"receipt", e.Receipt}, {"expires", e.Raw}} {
			if f.val == "" {
				return Ledger{}, &MalformedError{Index: i + 1, Test: e.Test, Reason: "missing " + f.name}
			}
		}
		var ok bool
		if e.Expires, ok = parseExpiry(e.Raw); !ok {
			return Ledger{}, &MalformedError{Index: i + 1, Test: e.Test, Reason: fmt.Sprintf("expires %q is not a YYYY-MM-DD date or RFC 3339 time", e.Raw)}
		}
		l.Entries = append(l.Entries, e)
	}
	return l, nil
}

func parseExpiry(s string) (time.Time, bool) {
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.Add(24*time.Hour - time.Nanosecond), true
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// Expired lists the entries past their expiry at now, in file order.
func (l Ledger) Expired(now time.Time) []Entry {
	var out []Entry
	for _, e := range l.Entries {
		if e.Expired(now) {
			out = append(out, e)
		}
	}
	return out
}

// Active lists the entries still excusing a test at now.
func (l Ledger) Active(now time.Time) []Entry {
	var out []Entry
	for _, e := range l.Entries {
		if !e.Expired(now) {
			out = append(out, e)
		}
	}
	return out
}

var (
	failLine = regexp.MustCompile(`(?m)^\s*--- FAIL: (\S+)`)
	// unnamed are failure shapes `go test` prints with no `--- FAIL:` line to
	// excuse: a package that did not build or set up, a panic, a timeout.
	unnamed = regexp.MustCompile(`(?m)\[build failed\]|\[setup failed\]|^panic: |^\s*panic: test timed out|^FAIL\s+\S+\s+\[`)
)

// FailingTests returns the tests a failed command's output names with go
// test's `--- FAIL: <name>` lines, deduplicated, in first-seen order. ok is
// false when the output also shows a failure it does not name (a build or
// setup failure, a panic, a timeout) or names none at all; such a run cannot
// be excused by the ledger.
func FailingTests(output string) (names []string, ok bool) {
	if unnamed.MatchString(output) {
		return nil, false
	}
	seen := map[string]bool{}
	for _, m := range failLine.FindAllStringSubmatch(output, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			names = append(names, m[1])
		}
	}
	return names, len(names) > 0
}

// Excuses reports whether the active entries cover every failing test in a
// failed command's output, and which entries did. A subtest (`Parent/case`) is
// covered by an entry for the test or for any parent of it. The excused list
// is empty (and covered false) when any failing test has no entry.
func (l Ledger) Excuses(output string, now time.Time) (excused []Entry, covered bool) {
	names, ok := FailingTests(output)
	if !ok {
		return nil, false
	}
	active := l.Active(now)
	used := map[int]bool{}
	for _, n := range names {
		hit := -1
		for i, e := range active {
			if n == e.Test || strings.HasPrefix(n, e.Test+"/") {
				hit = i
				break
			}
		}
		if hit < 0 {
			return nil, false
		}
		used[hit] = true
	}
	for i, e := range active {
		if used[i] {
			excused = append(excused, e)
		}
	}
	sort.SliceStable(excused, func(a, b int) bool { return excused[a].Test < excused[b].Test })
	return excused, true
}
