// Package gotestout reads `go test` output: which tests it names as failed
// and whether the run is the kind a caller may reason about by name at all.
package gotestout

import (
	"regexp"
	"strings"
)

var (
	failLine = regexp.MustCompile(`(?m)^\s*--- FAIL: (\S+)`)
	// unnamed are failure shapes `go test` prints with no `--- FAIL:` line to
	// excuse: a package that did not build or set up, a panic, a timeout.
	unnamed = regexp.MustCompile(`(?m)\[build failed\]|\[setup failed\]|^panic: |^\s*panic: test timed out|^FAIL\s+\S+\s+\[`)
)

// endsInFail reports that the last line is go test's own `FAIL` summary, so a
// later step of the same command (`go test ./...; go vet ./...`) that failed
// with output of its own is not mistaken for the excused tests.
func endsInFail(output string) bool {
	lines := strings.Split(strings.TrimRight(output, " \t\r\n"), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	return last == "FAIL" || strings.HasPrefix(last, "FAIL\t") || strings.HasPrefix(last, "FAIL ")
}

// FailingTests returns the tests a failed command's output names with go
// test's `--- FAIL: <name>` lines, deduplicated, in first-seen order. ok is
// false when the output does not end in go test's `FAIL` line, also shows a failure it does not name (a build or
// setup failure, a panic, a timeout) or names none at all; such a run cannot
// be excused by the ledger.
func FailingTests(output string) (names []string, ok bool) {
	if unnamed.MatchString(output) || !endsInFail(output) {
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
