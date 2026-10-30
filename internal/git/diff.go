package git

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// The helpers below take a Runner, not a Repo, so a layer that fakes git (the
// gate, the round env) keeps its seam. Repo's methods run them on its runner.

// ExitError is a git call that ran and exited non-zero. Callers map it to
// their own exit class and message; Stderr is what git said.
type ExitError struct {
	Args     string
	ExitCode int
	Stderr   string
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("git %s exited %d", e.Args, e.ExitCode)
}

// RemoteRef returns origin/<base> and true when that remote-tracking branch
// resolves in dir. A failure to run git counts as absent.
func RemoteRef(ctx context.Context, run Runner, dir, base string) (string, bool) {
	if base == "" {
		return "", false
	}
	ref := "origin/" + base
	res, err := run(ctx, dir, "rev-parse", "--verify", "-q", ref)
	if err != nil || res.ExitCode != 0 {
		return "", false
	}
	return ref, true
}

// RemoteBase is the ref to measure work against: origin/<base> when it exists,
// else base itself. A strictly-ahead variant lives with {files} in
// internal/cli/testrun.go, which must not pull upstream commits into a list.
func RemoteBase(ctx context.Context, run Runner, dir, base string) string {
	if ref, ok := RemoteRef(ctx, run, dir, base); ok {
		return ref
	}
	return base
}

// ChangedPaths lists the paths `git diff --name-only <span>` reports, one per
// line, trimmed, blanks dropped. A non-zero exit is an error.
func ChangedPaths(ctx context.Context, run Runner, dir, span string) ([]string, error) {
	res, err := run(ctx, dir, "diff", "--name-only", span)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, &ExitError{"diff --name-only " + span, res.ExitCode, res.Stderr}
	}
	var paths []string
	for _, l := range strings.Split(res.Stdout, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			paths = append(paths, l)
		}
	}
	return paths, nil
}

// DiffFiles lists every path `git diff <span>` touches, both names of a
// rename (--no-renames), so a file moved out of a protected directory still
// counts as touching it. -z keeps git from C-quoting unusual names, which no
// glob would match. opts go before the span (e.g. --diff-filter=d to leave out
// deleted files). A non-zero exit is an error.
func DiffFiles(ctx context.Context, run Runner, dir string, optsAndSpan ...string) ([]string, error) {
	span := optsAndSpan[len(optsAndSpan)-1]
	res, err := run(ctx, dir, append([]string{"diff", "--name-only", "-z", "--no-renames"}, optsAndSpan...)...)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, &ExitError{"diff --name-only " + span, res.ExitCode, res.Stderr}
	}
	var files []string
	for _, l := range strings.Split(res.Stdout, "\x00") {
		if l != "" {
			files = append(files, l)
		}
	}
	return files, nil
}

// ChangedLines counts the added and deleted lines `git diff --numstat <span>`
// reports. A non-zero exit is an error.
func ChangedLines(ctx context.Context, run Runner, dir, span string) (int, error) {
	res, err := run(ctx, dir, "diff", "--numstat", span)
	if err != nil {
		return 0, err
	}
	if res.ExitCode != 0 {
		return 0, &ExitError{"diff --numstat " + span, res.ExitCode, res.Stderr}
	}
	return CountLines(res.Stdout), nil
}

// ChangedLinesCounted is ChangedLines that says when the count is not the whole
// change: counted is false when numstat marks an entry binary (dashes), which
// a binary file or a -diff .gitattributes entry does and which counts for 0.
func ChangedLinesCounted(ctx context.Context, run Runner, dir, span string) (lines int, counted bool, err error) {
	res, err := run(ctx, dir, "diff", "--numstat", span)
	if err != nil {
		return 0, false, err
	}
	if res.ExitCode != 0 {
		return 0, false, &ExitError{"diff --numstat " + span, res.ExitCode, res.Stderr}
	}
	counted = true
	for _, l := range strings.Split(res.Stdout, "\n") {
		if f := strings.Fields(l); len(f) >= 2 && (f[0] == "-" || f[1] == "-") {
			counted = false
		}
	}
	return CountLines(res.Stdout), counted, nil
}

// CountLines sums the added and deleted lines of `git diff --numstat` output.
// Binary files, which numstat marks with dashes, count for nothing.
func CountLines(numstat string) int {
	total := 0
	for _, l := range strings.Split(numstat, "\n") {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		a, errA := strconv.Atoi(f[0])
		d, errD := strconv.Atoi(f[1])
		if errA == nil && errD == nil {
			total += a + d
		}
	}
	return total
}

// RemoteBase is the package function on r's runner in r.Dir.
func (r Repo) RemoteBase(ctx context.Context, base string) string {
	return RemoteBase(ctx, r.runner(), r.Dir, base)
}

// ChangedPaths is the package function on r's runner in r.Dir.
func (r Repo) ChangedPaths(ctx context.Context, span string) ([]string, error) {
	return ChangedPaths(ctx, r.runner(), r.Dir, span)
}

// ChangedLines is the package function on r's runner in r.Dir.
func (r Repo) ChangedLines(ctx context.Context, span string) (int, error) {
	return ChangedLines(ctx, r.runner(), r.Dir, span)
}
