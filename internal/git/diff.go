package git

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// The helpers below take a Runner, not a Repo, so a layer that fakes git (the
// gate, the round env) keeps its seam. Repo's methods run them on Exec.

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
		return nil, fmt.Errorf("git diff --name-only %s exited %d", span, res.ExitCode)
	}
	var paths []string
	for _, l := range strings.Split(res.Stdout, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			paths = append(paths, l)
		}
	}
	return paths, nil
}

// ChangedLines counts the added and deleted lines `git diff --numstat <span>`
// reports. A non-zero exit is an error.
func ChangedLines(ctx context.Context, run Runner, dir, span string) (int, error) {
	res, err := run(ctx, dir, "diff", "--numstat", span)
	if err != nil {
		return 0, err
	}
	if res.ExitCode != 0 {
		return 0, fmt.Errorf("git diff --numstat %s exited %d", span, res.ExitCode)
	}
	return CountLines(res.Stdout), nil
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

// RemoteBase is the package function on Exec in r.Dir.
func (r Repo) RemoteBase(ctx context.Context, base string) string {
	return RemoteBase(ctx, Exec, r.Dir, base)
}

// ChangedPaths is the package function on Exec in r.Dir.
func (r Repo) ChangedPaths(ctx context.Context, span string) ([]string, error) {
	return ChangedPaths(ctx, Exec, r.Dir, span)
}

// ChangedLines is the package function on Exec in r.Dir.
func (r Repo) ChangedLines(ctx context.Context, span string) (int, error) {
	return ChangedLines(ctx, Exec, r.Dir, span)
}
