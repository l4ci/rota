// Package gotestout reads test-runner output: which tests `go test` names as
// failed and whether the run is the kind a caller may reason about by name at
// all, plus the same question for the JS runners (Playwright, Vitest, node --test).
package gotestout

import (
	"regexp"
	"strings"
)

var (
	failLine = regexp.MustCompile(`(?m)^\s*--- FAIL: (\S+)`)
	// unnamed are failure shapes `go test` prints with no `--- FAIL:` line to
	// excuse: a package that did not build or set up, a panic, a timeout.
	// lockTrailer is the line `rota test lock` prints after its child failed.
	// It is not another step's output, so it does not hide go test's own FAIL.
	lockTrailer = regexp.MustCompile(`^rota test lock: .+ exited \d+$`)
	unnamed     = regexp.MustCompile(`(?m)\[build failed\]|\[setup failed\]|^panic: |^\s*panic: test timed out|^FAIL\s+\S+\s+\[`)
)

// endsInFail reports that the last line is go test's own `FAIL` summary, so a
// later step of the same command (`go test ./...; go vet ./...`) that failed
// with output of its own is not mistaken for the excused tests.
func endsInFail(output string) bool {
	lines := strings.Split(strings.TrimRight(output, " \t\r\n"), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if lockTrailer.MatchString(last) && len(lines) > 1 {
		last = strings.TrimSpace(lines[len(lines)-2])
	}
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

var (
	// playwrightFail is a list-reporter failure heading: `  1) [project] › file:1:1 › title ────`; the separator run is not part of the name.
	playwrightFail    = regexp.MustCompile(`(?m)^\s*\d+\) (\S.*?)[\s─]*$`)
	playwrightSummary = regexp.MustCompile(`(?m)^\s*[1-9]\d* failed\s*$`)
	// vitestFail is ` FAIL  file > name`; the summary is `Tests  1 failed | 3 passed`.
	vitestFail    = regexp.MustCompile(`(?m)^\s*FAIL\s+(\S.*?)\s*$`)
	vitestSummary = regexp.MustCompile(`(?m)^\s*Tests\s+[1-9]\d* failed`)
	// nodeFail covers node --test's TAP (`not ok 1 - name`) and spec (`✖ name (1ms)`)
	// reporters (the spec reporter's `✖ failing tests:` header is not a test); the summary is `# fail 1` or `ℹ fail 1`.
	nodeFail    = regexp.MustCompile(`(?m)^\s*(?:not ok \d+ - (\S.*?)|✖ (\S.*?)(?: \(\d[\d.]*ms\))?)\s*$`)
	nodeSummary = regexp.MustCompile(`(?m)^(?:#|ℹ) fail [1-9]\d*\s*$`)
)

// JSFailures returns the tests a failed JS run's output names, for the runners
// whose output rota recognises: Playwright's list reporter, Vitest's default
// reporter and node --test (TAP and spec). A runner counts only when it names a
// failure and also prints its own failed summary, so a numbered step list or a
// bare "1 failed" is not mistaken for a test failure. ok is false for anything
// else, go test output included.
func JSFailures(output string) (names []string, ok bool) {
	seen := map[string]bool{}
	add := func(re *regexp.Regexp, summary *regexp.Regexp) {
		if !summary.MatchString(output) {
			return
		}
		for _, m := range re.FindAllStringSubmatch(output, -1) {
			for _, n := range m[1:] {
				if n != "" && n != "failing tests:" && !seen[n] {
					seen[n] = true
					names = append(names, n)
				}
			}
		}
	}
	add(playwrightFail, playwrightSummary)
	add(vitestFail, vitestSummary)
	add(nodeFail, nodeSummary)
	return names, len(names) > 0
}
