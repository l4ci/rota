package round

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/tracker"
)

// prFilesForge is a Forge that can also list an open PR's changed files.
type prFilesForge struct {
	Forge
	files map[int][]string
	err   error
}

func (p prFilesForge) PRFiles(_ context.Context, n int) ([]string, error) {
	return p.files[n], p.err
}

// inFlightFor is the InFlight entry of one issue.
func (f *moveFx) inFlightFor(t *testing.T, issue string) InFlight {
	t.Helper()
	for _, x := range f.env.InFlightItems(bg, f.root, f.be, f.env.trackedFiles(bg, f.root), nil) {
		if x.Issue == issue {
			return x
		}
	}
	t.Fatalf("issue %s is not in flight", issue)
	return InFlight{}
}

// withPR gives ben's slot an open PR (number 7) that changes files.
func (f *moveFx) withPR(files []string, err error) {
	branch := f.slot("ben").Branch()
	f.forge.prs = []tracker.PR{{Number: 7, Branch: branch}}
	f.env.Forge = prFilesForge{Forge: f.forge.asForge(), files: map[int][]string{7: files}, err: err}
}

// overlapWith assesses issue 14 (names internal/cli/round.go) against what is in flight.
func (f *moveFx) overlapWith(t *testing.T) Readiness {
	t.Helper()
	tracked := f.env.trackedFiles(bg, f.root)
	inFlight := f.env.InFlightItems(bg, f.root, f.be, tracked, nil)
	r, err := Assess(f.be, "14", tracked, nil, inFlight, false)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// #1094 AC-1, AC-5: an in-flight item with an open PR is compared by the PR's
// files, not by the paths its issue text names.
func TestInFlightWithOpenPRComparesPRFiles(t *testing.T) {
	f := newMoveFx(t)
	if r := f.overlapWith(t); len(r.Overlaps) != 1 {
		t.Fatalf("without a PR the issue text overlaps: %+v", r.Overlaps)
	}
	f.withPR([]string{"internal/cli/nudge.go"}, nil)
	x := f.inFlightFor(t, "12")
	if !x.FromPR || strings.Join(x.Paths, ",") != "internal/cli/nudge.go" {
		t.Errorf("paths come from the PR: %+v", x)
	}
	if r := f.overlapWith(t); len(r.Overlaps) != 0 {
		t.Errorf("a PR touching only nudge files does not clash: %+v", r.Overlaps)
	}
	f.withPR([]string{"internal/cli/round.go"}, nil)
	if r := f.overlapWith(t); len(r.Overlaps) != 1 || r.Overlaps[0].Paths[0] != "internal/cli/round.go" {
		t.Errorf("a PR touching the same file still clashes: %+v", r.Overlaps)
	}
}

// AC-1: a subsystem or inferred scope is dropped once the PR is known; AC-3: a
// declared ## Touches entry stays.
func TestInFlightWithOpenPRKeepsOnlyDeclaredTouches(t *testing.T) {
	f := newMoveFx(t)
	f.be.items["12"].Fields.Subsystem = "CLI"
	f.withPR([]string{"internal/cli/nudge.go"}, nil)
	if x := f.inFlightFor(t, "12"); len(x.Scopes) != 0 || x.ScopeSource != SourceNone {
		t.Errorf("subsystem scope is not kept for an item with a PR: %+v", x)
	}
	f.be.details["12"] += "\n\n## Touches\n- `assign.go`\n"
	x := f.inFlightFor(t, "12")
	if x.ScopeSource != SourceTouches || len(x.Scopes) != 1 || x.Scopes[0] != "assign.go" {
		t.Errorf("declared touches stay: %+v", x)
	}
}

// AC-2: with no open PR for the branch, today's comparison stands.
func TestInFlightWithoutOpenPRKeepsFootprint(t *testing.T) {
	f := newMoveFx(t)
	f.withPR([]string{"internal/cli/nudge.go"}, nil)
	f.forge.prs = nil
	x := f.inFlightFor(t, "12")
	if x.FromPR || !hasKind(x.Paths, "internal/cli/round.go") || x.PRFilesErr != "" {
		t.Errorf("no PR, no change: %+v", x)
	}
}

// AC-4: when the PR's files cannot be read the check falls back and says so.
func TestInFlightPRFilesErrorFallsBackAndWarns(t *testing.T) {
	f := newMoveFx(t)
	f.withPR(nil, errors.New("rate limited"))
	x := f.inFlightFor(t, "12")
	if x.FromPR || !hasKind(x.Paths, "internal/cli/round.go") || !strings.Contains(x.PRFilesErr, "rate limited") {
		t.Fatalf("fallback keeps the footprint: %+v", x)
	}
	r := f.overlapWith(t)
	if len(r.Overlaps) != 1 || !strings.Contains(strings.Join(r.Checks[2].Detail, "\n"), "open PR's files unavailable") {
		t.Errorf("the warning says the fallback happened: %+v", r)
	}
}
